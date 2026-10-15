// Package github provides a read-only observer for GitHub pull-request integration candidates.
package github

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	promotion "github.com/ai-dev-control-plane/promotion"
	protocol "github.com/ai-dev-control-plane/repoprotocol"
)

var (
	namePattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	branchPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	shaPattern    = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
)

const githubAPI = "https://api.github.com"

// Observation captures read-only, point-in-time GitHub evidence; it does not grant merge authority.
type Observation struct {
	Snapshot          promotion.Snapshot
	Policy            promotion.Policy
	Approvals         []promotion.Approval
	AgentActorID      string
	PolicyRevisionSHA string
}

type Reader struct {
	client   *http.Client
	endpoint string
	token    string
}

// NewReader uses the official GitHub API endpoint and never performs mutations.
func NewReader(token string) *Reader {
	return newReader(&http.Client{Timeout: 12 * time.Second}, githubAPI, token)
}

func newReader(client *http.Client, endpoint, token string) *Reader {
	return &Reader{client: client, endpoint: strings.TrimSuffix(endpoint, "/"), token: strings.TrimSpace(token)}
}

type pull struct {
	State          string  `json:"state"`
	Draft          bool    `json:"draft"`
	Mergeable      *bool   `json:"mergeable"`
	MergeCommitSHA *string `json:"merge_commit_sha"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"base"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}
type gitRef struct {
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
}
type branch struct {
	Protected bool `json:"protected"`
	Commit    struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}
type gitCommit struct {
	SHA  string `json:"sha"`
	Tree struct {
		SHA string `json:"sha"`
	} `json:"tree"`
	Parents []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
}
type policyFile struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}
type review struct {
	ID          int64  `json:"id"`
	State       string `json:"state"`
	CommitID    string `json:"commit_id"`
	SubmittedAt string `json:"submitted_at"`
	User        struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

// Observe fetches the PR, current protected target, test merge commit, protected policy,
// and current human approvals. Its output is only a snapshot: callers must never merge
// based solely on this non-atomic read sequence.
func (r *Reader) Observe(ctx context.Context, repository string, number int) (Observation, error) {
	if r == nil || r.client == nil || r.token == "" {
		return Observation{}, fmt.Errorf("GitHub observer requires authenticated HTTP client")
	}
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || !validName(parts[0]) || !validName(parts[1]) || number <= 0 {
		return Observation{}, fmt.Errorf("invalid repository or PR number")
	}
	prefix := "/repos/" + parts[0] + "/" + parts[1]
	pullPath := fmt.Sprintf("%s/pulls/%d", prefix, number)
	var p pull
	if err := r.get(ctx, pullPath, &p); err != nil {
		return Observation{}, fmt.Errorf("read pull request: %w", err)
	}
	if p.State != "open" || p.Draft || p.Mergeable == nil || !*p.Mergeable ||
		p.MergeCommitSHA == nil || !validSHA(*p.MergeCommitSHA) ||
		!validSHA(p.Head.SHA) || !validSHA(p.Base.SHA) ||
		!validBranch(p.Base.Ref) || strings.TrimSpace(p.User.Login) == "" {
		return Observation{}, fmt.Errorf("pull request is not a current, mergeable review candidate")
	}
	branchRefPath := prefix + "/git/ref/heads/" + url.PathEscape(p.Base.Ref)
	var ref gitRef
	if err := r.get(ctx, branchRefPath, &ref); err != nil {
		return Observation{}, fmt.Errorf("read target ref: %w", err)
	}
	if ref.Object.Type != "commit" || !validSHA(ref.Object.SHA) || ref.Object.SHA != p.Base.SHA {
		return Observation{}, fmt.Errorf("target branch moved or is not a commit")
	}
	var b branch
	if err := r.get(ctx, prefix+"/branches/"+url.PathEscape(p.Base.Ref), &b); err != nil {
		return Observation{}, fmt.Errorf("read target protection: %w", err)
	}
	if !b.Protected || b.Commit.SHA != ref.Object.SHA {
		return Observation{}, fmt.Errorf("target branch is unprotected or changed")
	}

	mergeSHA := *p.MergeCommitSHA
	var merge gitCommit
	if err := r.get(ctx, prefix+"/git/commits/"+mergeSHA, &merge); err != nil {
		return Observation{}, fmt.Errorf("read integration commit: %w", err)
	}
	if merge.SHA != mergeSHA || !validSHA(merge.Tree.SHA) || len(merge.Parents) != 2 ||
		merge.Parents[0].SHA != ref.Object.SHA || merge.Parents[1].SHA != p.Head.SHA {
		return Observation{}, fmt.Errorf("integration candidate is stale, invalid, or not an exact two-parent merge")
	}

	trustedURL := prefix + "/contents/devplane.yaml?ref=" + url.QueryEscape(ref.Object.SHA)
	var file policyFile
	if err := r.get(ctx, trustedURL, &file); err != nil {
		return Observation{}, fmt.Errorf("read protected policy: %w", err)
	}
	if file.Type != "file" || file.Encoding != "base64" {
		return Observation{}, fmt.Errorf("protected policy content must be a base64 encoded file")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil || len(raw) == 0 || len(raw) > 1024*1024 {
		return Observation{}, fmt.Errorf("protected policy is empty, oversized, or undecodable")
	}
	cfg, err := protocol.ParseConfigYAML(raw)
	if err != nil {
		return Observation{}, fmt.Errorf("invalid protected policy: %w", err)
	}
	gates := make([]string, 0, len(cfg.Verification))
	for name := range cfg.Verification {
		gates = append(gates, name)
	}
	sort.Strings(gates)
	if len(gates) == 0 {
		return Observation{}, fmt.Errorf("no mandatory verification profiles")
	}
	digest := sha256.Sum256(raw)
	policyDigest := fmt.Sprintf("sha256:%x", digest)

	var reviews []review
	if err := r.get(ctx, fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", prefix, number), &reviews); err != nil {
		return Observation{}, fmt.Errorf("read GitHub reviews: %w", err)
	}
	if len(reviews) >= 100 {
		return Observation{}, fmt.Errorf("review pagination exceeds verified limit")
	}
	latest := make(map[string]review)
	for _, item := range reviews {
		login := strings.ToLower(strings.TrimSpace(item.User.Login))
		if login == "" {
			continue
		}
		previous, exists := latest[login]
		if !exists || item.SubmittedAt > previous.SubmittedAt ||
			(item.SubmittedAt == previous.SubmittedAt && item.ID > previous.ID) {
			latest[login] = item
		}
	}
	approvals := make([]promotion.Approval, 0)
	for login, item := range latest {
		if item.State != "APPROVED" || item.User.Type != "User" || item.CommitID != p.Head.SHA ||
			strings.EqualFold(login, p.User.Login) || item.SubmittedAt == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, item.SubmittedAt); err != nil {
			continue
		}
		approvals = append(approvals, promotion.Approval{
			ReviewerID: login, Human: true, Approved: true,
			CandidateCommitSHA: mergeSHA, PolicyDigest: policyDigest,
		})
	}
	sort.Slice(approvals, func(i, j int) bool { return approvals[i].ReviewerID < approvals[j].ReviewerID })

	// Read current branch and PR once more: a mid-observation change fails closed.
	var afterPull pull
	if err := r.get(ctx, pullPath, &afterPull); err != nil {
		return Observation{}, fmt.Errorf("recheck pull request: %w", err)
	}
	var afterRef gitRef
	if err := r.get(ctx, branchRefPath, &afterRef); err != nil {
		return Observation{}, fmt.Errorf("recheck target ref: %w", err)
	}
	if afterPull.State != "open" || afterPull.Draft || afterPull.MergeCommitSHA == nil ||
		*afterPull.MergeCommitSHA != mergeSHA || afterPull.Head.SHA != p.Head.SHA ||
		afterPull.Base.SHA != p.Base.SHA || afterPull.Base.Ref != p.Base.Ref ||
		afterRef.Object.SHA != ref.Object.SHA {
		return Observation{}, fmt.Errorf("pull request or target moved during observation")
	}

	return Observation{
		Snapshot: promotion.Snapshot{
			Repository: repository, TargetBranch: p.Base.Ref,
			CurrentSourceHeadSHA: p.Head.SHA, CurrentTargetHeadSHA: ref.Object.SHA,
			CandidateCommitSHA: mergeSHA, CandidateTreeSHA: merge.Tree.SHA,
		},
		Policy: promotion.Policy{
			Digest: policyDigest, RequiredGates: gates, RequiredHumanApprovals: 1,
		},
		Approvals: approvals, AgentActorID: strings.ToLower(p.User.Login),
		PolicyRevisionSHA: ref.Object.SHA,
	}, nil
}

func validName(s string) bool {
	return s != "" && s != "." && s != ".." && namePattern.MatchString(s)
}
func validBranch(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.Contains(s, "..") &&
		!strings.HasPrefix(s, "/") && !strings.HasSuffix(s, "/") && branchPattern.MatchString(s)
}
func validSHA(s string) bool { return shaPattern.MatchString(s) }

func (r *Reader) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
}
