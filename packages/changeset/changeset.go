package changeset

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ai-dev-control-plane/changegraph"
)

const ManifestVersion = 1

type Member struct {
	ID             string `json:"id"`
	CommitSHA      string `json:"commit_sha"`
	TreeHash       string `json:"tree_hash"`
	DecisionDigest string `json:"decision_digest"`
	Approvable     bool   `json:"approvable"`
}

type Input struct {
	Members []Member
	Graph   changegraph.Graph
	States  map[string]changegraph.State
}

type Blocker struct {
	CandidateID string `json:"candidate_id"`
	Reason      string `json:"reason"`
}

type Result struct {
	Ready            bool      `json:"ready"`
	Blockers         []Blocker `json:"blockers,omitempty"`
	PublicationOrder []string  `json:"publication_order"`
}

type ManifestInput struct {
	ChangeSetID string
	ProjectID   string
	Members     []Member
	Graph       changegraph.Graph
}

type ManifestMember struct {
	ID             string `json:"id"`
	CommitSHA      string `json:"commit_sha"`
	TreeHash       string `json:"tree_hash"`
	DecisionDigest string `json:"decision_digest"`
}

type Edge struct {
	CandidateID          string `json:"candidate_id"`
	DependsOnCandidateID string `json:"depends_on_candidate_id"`
}

type Manifest struct {
	Version     int              `json:"version"`
	ChangeSetID string           `json:"change_set_id"`
	ProjectID   string           `json:"project_id"`
	Members     []ManifestMember `json:"members"`
	Edges       []Edge           `json:"edges,omitempty"`
}

func Evaluate(input Input) (Result, error) {
	members, nodes, err := validateInputs(input.Members, input.Graph)
	if err != nil {
		return Result{}, err
	}

	blockers := make([]Blocker, 0)
	for _, member := range members {
		if strings.TrimSpace(member.CommitSHA) == "" ||
			strings.TrimSpace(member.TreeHash) == "" ||
			strings.TrimSpace(member.DecisionDigest) == "" {
			blockers = append(blockers, Blocker{CandidateID: member.ID, Reason: "missing-authority"})
			continue
		}
		if !member.Approvable {
			blockers = append(blockers, Blocker{CandidateID: member.ID, Reason: "review-not-approvable"})
		}
	}

	memberSet := make(map[string]struct{}, len(members))
	for _, member := range members {
		memberSet[member.ID] = struct{}{}
	}

	external := make(map[string]struct{})
	visited := make(map[string]bool)
	var walk func(string)
	walk = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		for _, dependency := range nodes[id].DependsOn {
			dependency = strings.TrimSpace(dependency)
			if _, internal := memberSet[dependency]; !internal && input.States[dependency] != changegraph.StateMerged {
				external[dependency] = struct{}{}
			}
			walk(dependency)
		}
	}
	for _, member := range members {
		walk(member.ID)
	}
	externalIDs := make([]string, 0, len(external))
	for id := range external {
		externalIDs = append(externalIDs, id)
	}
	sort.Strings(externalIDs)
	for _, id := range externalIDs {
		blockers = append(blockers, Blocker{CandidateID: id, Reason: "external-dependency-not-merged"})
	}

	sort.SliceStable(blockers, func(i, j int) bool {
		if blockers[i].CandidateID == blockers[j].CandidateID {
			return blockers[i].Reason < blockers[j].Reason
		}
		return blockers[i].CandidateID < blockers[j].CandidateID
	})

	order, err := publicationOrder(memberSet, nodes)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Ready:            len(blockers) == 0,
		Blockers:         blockers,
		PublicationOrder: order,
	}, nil
}

func NewManifest(input ManifestInput) (Manifest, error) {
	if strings.TrimSpace(input.ChangeSetID) == "" {
		return Manifest{}, errors.New("change_set_id is required")
	}
	if strings.TrimSpace(input.ProjectID) == "" {
		return Manifest{}, errors.New("project_id is required")
	}
	members, nodes, err := validateInputs(input.Members, input.Graph)
	if err != nil {
		return Manifest{}, err
	}

	manifestMembers := make([]ManifestMember, 0, len(members))
	memberSet := make(map[string]struct{}, len(members))
	for _, member := range members {
		if strings.TrimSpace(member.CommitSHA) == "" ||
			strings.TrimSpace(member.TreeHash) == "" ||
			strings.TrimSpace(member.DecisionDigest) == "" {
			return Manifest{}, fmt.Errorf("candidate %s is missing authority identity", member.ID)
		}
		memberSet[member.ID] = struct{}{}
		manifestMembers = append(manifestMembers, ManifestMember{
			ID:             member.ID,
			CommitSHA:      member.CommitSHA,
			TreeHash:       member.TreeHash,
			DecisionDigest: member.DecisionDigest,
		})
	}
	sort.Slice(manifestMembers, func(i, j int) bool { return manifestMembers[i].ID < manifestMembers[j].ID })

	closure := ancestorClosure(memberSet, nodes)
	edges := make([]Edge, 0)
	for candidateID := range closure {
		for _, dependency := range nodes[candidateID].DependsOn {
			dependency = strings.TrimSpace(dependency)
			edges = append(edges, Edge{CandidateID: candidateID, DependsOnCandidateID: dependency})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].CandidateID == edges[j].CandidateID {
			return edges[i].DependsOnCandidateID < edges[j].DependsOnCandidateID
		}
		return edges[i].CandidateID < edges[j].CandidateID
	})

	return Manifest{
		Version:     ManifestVersion,
		ChangeSetID: strings.TrimSpace(input.ChangeSetID),
		ProjectID:   strings.TrimSpace(input.ProjectID),
		Members:     manifestMembers,
		Edges:       edges,
	}, nil
}

func (m Manifest) Digest() (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal change set manifest: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (m Manifest) Marshal() (json.RawMessage, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal change set manifest: %w", err)
	}
	return data, nil
}

func (m Manifest) Validate() error {
	if m.Version != ManifestVersion {
		return fmt.Errorf("unsupported change set manifest version %d", m.Version)
	}
	if strings.TrimSpace(m.ChangeSetID) == "" || strings.TrimSpace(m.ProjectID) == "" {
		return errors.New("change set manifest identity is required")
	}
	if len(m.Members) == 0 {
		return errors.New("change set manifest requires at least one member")
	}
	seen := make(map[string]struct{}, len(m.Members))
	for _, member := range m.Members {
		if strings.TrimSpace(member.ID) == "" ||
			strings.TrimSpace(member.CommitSHA) == "" ||
			strings.TrimSpace(member.TreeHash) == "" ||
			strings.TrimSpace(member.DecisionDigest) == "" {
			return errors.New("change set manifest member identity is incomplete")
		}
		if _, exists := seen[member.ID]; exists {
			return fmt.Errorf("duplicate manifest member %s", member.ID)
		}
		seen[member.ID] = struct{}{}
	}
	return nil
}

func validateInputs(rawMembers []Member, graph changegraph.Graph) ([]Member, map[string]changegraph.Node, error) {
	if len(rawMembers) == 0 {
		return nil, nil, errors.New("change set requires at least one member")
	}
	if err := changegraph.Validate(graph); err != nil {
		return nil, nil, err
	}
	nodes := make(map[string]changegraph.Node, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodes[strings.TrimSpace(node.ID)] = node
	}

	members := make([]Member, 0, len(rawMembers))
	seen := make(map[string]struct{}, len(rawMembers))
	for _, raw := range rawMembers {
		member := raw
		member.ID = strings.TrimSpace(member.ID)
		if member.ID == "" {
			return nil, nil, errors.New("change set member id is required")
		}
		if _, exists := seen[member.ID]; exists {
			return nil, nil, fmt.Errorf("duplicate change set member %s", member.ID)
		}
		if _, exists := nodes[member.ID]; !exists {
			return nil, nil, fmt.Errorf("change set member %s is not present in change graph", member.ID)
		}
		seen[member.ID] = struct{}{}
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	return members, nodes, nil
}

func publicationOrder(memberSet map[string]struct{}, nodes map[string]changegraph.Node) ([]string, error) {
	indegree := make(map[string]int, len(memberSet))
	dependents := make(map[string][]string, len(memberSet))
	for id := range memberSet {
		indegree[id] = 0
	}
	for id := range memberSet {
		for _, dependency := range nodes[id].DependsOn {
			dependency = strings.TrimSpace(dependency)
			if _, internal := memberSet[dependency]; !internal {
				continue
			}
			indegree[id]++
			dependents[dependency] = append(dependents[dependency], id)
		}
	}
	ready := make([]string, 0)
	for id, degree := range indegree {
		if degree == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	order := make([]string, 0, len(memberSet))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, dependent := range dependents[id] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
				sort.Strings(ready)
			}
		}
	}
	if len(order) != len(memberSet) {
		return nil, errors.New("change set dependency graph contains a cycle")
	}
	return order, nil
}

func ancestorClosure(memberSet map[string]struct{}, nodes map[string]changegraph.Node) map[string]struct{} {
	closure := make(map[string]struct{})
	var walk func(string)
	walk = func(id string) {
		if _, exists := closure[id]; exists {
			return
		}
		closure[id] = struct{}{}
		for _, dependency := range nodes[id].DependsOn {
			walk(strings.TrimSpace(dependency))
		}
	}
	for id := range memberSet {
		walk(id)
	}
	return closure
}
