package changegraph

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type State string

const (
	StateOpen   State = "open"
	StateMerged State = "merged"
	StateClosed State = "closed"
)

type Node struct {
	ID        string   `json:"id"`
	DependsOn []string `json:"depends_on,omitempty"`
}

type Graph struct {
	Nodes []Node `json:"nodes"`
}

func Validate(graph Graph) error {
	if len(graph.Nodes) == 0 {
		return errors.New("change graph requires at least one candidate")
	}

	nodes := make(map[string]Node, len(graph.Nodes))
	for i, node := range graph.Nodes {
		node.ID = strings.TrimSpace(node.ID)
		if node.ID == "" {
			return fmt.Errorf("candidate at index %d requires an id", i)
		}
		if _, exists := nodes[node.ID]; exists {
			return fmt.Errorf("duplicate candidate id %q", node.ID)
		}
		nodes[node.ID] = node
	}

	for _, node := range graph.Nodes {
		seen := make(map[string]struct{}, len(node.DependsOn))
		for _, rawDependency := range node.DependsOn {
			dependency := strings.TrimSpace(rawDependency)
			if dependency == node.ID {
				return fmt.Errorf("dependency cycle detected at candidate %s", node.ID)
			}
			if _, exists := nodes[dependency]; !exists {
				return fmt.Errorf("candidate %s depends on unknown candidate %s", node.ID, dependency)
			}
			if _, exists := seen[dependency]; exists {
				return fmt.Errorf("candidate %s has duplicate dependency %s", node.ID, dependency)
			}
			seen[dependency] = struct{}{}
		}
	}

	visiting := make(map[string]bool, len(nodes))
	visited := make(map[string]bool, len(nodes))
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("dependency cycle detected at candidate %s", id)
		}
		visiting[id] = true
		for _, dependency := range nodes[id].DependsOn {
			if err := visit(strings.TrimSpace(dependency)); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for _, node := range graph.Nodes {
		if err := visit(node.ID); err != nil {
			return err
		}
	}
	return nil
}

func Blockers(graph Graph, targetID string, states map[string]State) ([]string, error) {
	if err := Validate(graph); err != nil {
		return nil, err
	}
	targetID = strings.TrimSpace(targetID)
	nodes := make(map[string]Node, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodes[node.ID] = node
	}
	if _, exists := nodes[targetID]; !exists {
		return nil, fmt.Errorf("unknown target candidate %s", targetID)
	}

	blocked := make(map[string]struct{})
	visited := make(map[string]bool)
	var walk func(string)
	walk = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		for _, dependency := range nodes[id].DependsOn {
			dependency = strings.TrimSpace(dependency)
			if states[dependency] != StateMerged {
				blocked[dependency] = struct{}{}
			}
			walk(dependency)
		}
	}
	walk(targetID)

	result := make([]string, 0, len(blocked))
	for id := range blocked {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}
