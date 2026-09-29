package forgeexec

import (
	"errors"
	"fmt"
	"strings"
)

type RepositoryRef struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

func (r RepositoryRef) FullName() string {
	return r.Owner + "/" + r.Name
}

func (r RepositoryRef) Validate() error {
	if !validRepositoryComponent(r.Owner) || !validRepositoryComponent(r.Name) {
		return errors.New("invalid repository reference")
	}
	return nil
}

type Command struct {
	Type            string        `json:"type"`
	Repository      RepositoryRef `json:"repository"`
	Path            string        `json:"path,omitempty"`
	Reference       *string       `json:"reference,omitempty"`
	Name            string        `json:"name,omitempty"`
	From            string        `json:"from,omitempty"`
	Branch          string        `json:"branch,omitempty"`
	Content         []int         `json:"content,omitempty"`
	Message         string        `json:"message,omitempty"`
	ExpectedBlobSHA *string       `json:"expected_blob_sha,omitempty"`
	Head            string        `json:"head,omitempty"`
	Base            string        `json:"base,omitempty"`
	Title           string        `json:"title,omitempty"`
	Body            string        `json:"body,omitempty"`
	Number          uint64        `json:"number,omitempty"`
	Event           string        `json:"event,omitempty"`
	CommitID        *string       `json:"commit_id,omitempty"`
	Method          string        `json:"method,omitempty"`
	ExpectedHeadSHA *string       `json:"expected_head_sha,omitempty"`
}

func (c Command) Operation() (string, error) {
	switch c.Type {
	case "read_file":
		return "repo.read", nil
	case "create_branch":
		return "branch.create", nil
	case "write_file":
		return "commit.write", nil
	case "create_change":
		return "change.create", nil
	case "review_change":
		return "change.review", nil
	case "merge_change":
		return "change.merge", nil
	case "list_checks":
		return "check.read", nil
	default:
		return "", fmt.Errorf("unknown forge command type %q", c.Type)
	}
}

func (c Command) ReadOnly() bool {
	return c.Type == "read_file" || c.Type == "list_checks"
}

type Response struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

type FileContent struct {
	Path    string `json:"path"`
	Content []int  `json:"content"`
	SHA     string `json:"sha"`
}

type BranchRef struct {
	Name     string `json:"name"`
	CommitID string `json:"commit_id"`
}

type CommitRef struct {
	ID string `json:"id"`
}

type ChangeRef struct {
	Number uint64  `json:"number"`
	URL    *string `json:"url"`
	Head   string  `json:"head"`
	Base   string  `json:"base"`
	State  string  `json:"state"`
}

type ReviewRef struct {
	ID *uint64 `json:"id"`
}

type MergeResult struct {
	Merged bool `json:"merged"`
}

type CheckRun struct {
	ID        string  `json:"id"`
	Context   string  `json:"context"`
	State     string  `json:"state"`
	TargetURL *string `json:"target_url"`
}

func bytesFromInts(values []int) ([]byte, error) {
	out := make([]byte, len(values))
	for i, value := range values {
		if value < 0 || value > 255 {
			return nil, fmt.Errorf("content byte %d is outside 0..255", value)
		}
		out[i] = byte(value)
	}
	return out, nil
}

func intsFromBytes(values []byte) []int {
	out := make([]int, len(values))
	for i, value := range values {
		out[i] = int(value)
	}
	return out
}

func validRepositoryComponent(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			strings.ContainsRune("._-", r) {
			continue
		}
		return false
	}
	return true
}

func validateFilePath(path string) error {
	if path == "" || strings.HasPrefix(path, "/") {
		return errors.New("repository file path must be relative and non-empty")
	}
	for _, component := range strings.Split(path, "/") {
		if component == "" || component == "." || component == ".." {
			return errors.New("repository file path contains an unsafe segment")
		}
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return errors.New("repository file path contains a control character")
		}
	}
	return nil
}

func requireText(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must be non-empty", label)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains a control character", label)
		}
	}
	return nil
}
