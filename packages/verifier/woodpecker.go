package verifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

// ErrWoodpeckerAssignmentUnproven is a blocking trust-boundary error. The
// public Woodpecker pipeline API gives workflow agent_id and step start, but
// not necessarily an independently established agent assignment timestamp.
var ErrWoodpeckerAssignmentUnproven = errors.New("Woodpecker lacks authoritative job assignment timestamp for signed release proof")

// WoodpeckerReader is a read-only, authenticated diagnostic adapter for the
// Woodpecker API. It does not supply signed release evidence.
type WoodpeckerReader struct {
	BaseURL      string // scheme+host only; API prefix is added by the reader
	Token        string
	RepositoryID int64
	Repository   string
	Client       *http.Client
}

type WoodpeckerObservation struct {
	HeadSHA             string
	PipelineID          string
	RunID               string
	JobID               string
	AttemptID           string
	WorkerID            string
	JobName             string
	PipelineStatus      string
	StepStatus          string
	ConfigurationSHA256 string
	StartedAt           time.Time
	FinishedAt          time.Time
}

type wpRepository struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
}
type wpPipeline struct {
	ID         int64        `json:"id"`
	Number     int64        `json:"number"`
	Commit     string       `json:"commit"`
	Status     string       `json:"status"`
	RerunCount int64        `json:"rerun_count"`
	Workflows  []wpWorkflow `json:"workflows"`
}
type wpWorkflow struct {
	ID       int64    `json:"id"`
	AgentID  int64    `json:"agent_id"`
	Children []wpStep `json:"children"`
}
type wpStep struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	Started  int64  `json:"started"`
	Finished int64  `json:"finished"`
}
type wpConfig struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

func (w WoodpeckerReader) client() (*http.Client, string, error) {
	if strings.TrimSpace(w.Token) == "" || strings.TrimSpace(w.Repository) == "" || w.RepositoryID <= 0 {
		return nil, "", errors.New("Woodpecker API token, repository name and numeric repository ID required")
	}
	u, err := url.Parse(w.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return nil, "", errors.New("invalid Woodpecker API base URL")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, "", errors.New("Woodpecker API requires TLS except loopback development")
	}
	c := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	if w.Client != nil {
		copy := *w.Client
		if copy.Timeout == 0 {
			copy.Timeout = 10 * time.Second
		}
		copy.CheckRedirect = c.CheckRedirect
		c = &copy
	}
	return c, strings.TrimRight(w.BaseURL, "/"), nil
}

func (w WoodpeckerReader) get(ctx context.Context, c *http.Client, base, resource string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api"+resource, nil)
	if err != nil {
		return fmt.Errorf("construct Woodpecker GET: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("Woodpecker GET failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Woodpecker GET returned HTTP %d", resp.StatusCode)
	}
	// Prevent a buggy server response from consuming unbounded memory.
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return fmt.Errorf("read Woodpecker response: %w", err)
	}
	if len(data) > 2<<20 {
		return errors.New("Woodpecker response too large")
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("decode Woodpecker response: %w", err)
	}
	return nil
}

func configDigest(files []wpConfig) (string, error) {
	if len(files) == 0 {
		return "", errors.New("Woodpecker executed configuration unavailable")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	h := sha256.New()
	h.Write([]byte("dev-plane/woodpecker-executed-config/v1\x00"))
	var last string
	for _, f := range files {
		if strings.TrimSpace(f.Name) == "" || f.Name == last || len(f.Data) == 0 ||
			strings.ContainsRune(f.Name, '\x00') {
			return "", errors.New("invalid or duplicated executed Woodpecker config")
		}
		h.Write([]byte(strconv.Itoa(len(f.Name))))
		h.Write([]byte{0})
		h.Write([]byte(f.Name))
		h.Write([]byte{0})
		h.Write([]byte(strconv.Itoa(len(f.Data))))
		h.Write([]byte{0})
		h.Write(f.Data)
		h.Write([]byte{0})
		last = f.Name
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ObserveAttempt fetches authoritative read-only pipeline, repository and
// pipeline-scoped configuration data. The result is diagnostic, not release
// eligibility evidence. No webhook claims are accepted.
func (w WoodpeckerReader) ObserveAttempt(ctx context.Context, q RemoteCIQuery) (WoodpeckerObservation, error) {
	c, base, err := w.client()
	if err != nil {
		return WoodpeckerObservation{}, err
	}
	pid, err := strconv.ParseInt(q.PipelineID, 10, 64)
	if err != nil || pid <= 0 {
		return WoodpeckerObservation{}, errors.New("numeric Woodpecker pipeline ID required")
	}
	run, err := strconv.ParseInt(q.RunID, 10, 64)
	if err != nil || run <= 0 {
		return WoodpeckerObservation{}, errors.New("numeric Woodpecker pipeline number required")
	}
	job, err := strconv.ParseInt(q.JobID, 10, 64)
	if err != nil || job <= 0 {
		return WoodpeckerObservation{}, errors.New("numeric Woodpecker step ID required")
	}
	attempt, err := strconv.ParseInt(q.AttemptID, 10, 64)
	if err != nil || attempt < 0 {
		return WoodpeckerObservation{}, errors.New("numeric Woodpecker rerun attempt required")
	}
	if q.HeadSHA == "" {
		return WoodpeckerObservation{}, errors.New("exact Woodpecker source SHA required")
	}
	repoRoot := fmt.Sprintf("/repos/%d", w.RepositoryID)
	var repo wpRepository
	if err = w.get(ctx, c, base, repoRoot, &repo); err != nil {
		return WoodpeckerObservation{}, err
	}
	if repo.ID != w.RepositoryID || repo.FullName != w.Repository {
		return WoodpeckerObservation{}, errors.New("Woodpecker repository identity mismatch")
	}
	pipelineRoot := fmt.Sprintf("%s/pipelines/%d", repoRoot, run)
	var pipeline wpPipeline
	if err = w.get(ctx, c, base, pipelineRoot, &pipeline); err != nil {
		return WoodpeckerObservation{}, err
	}
	if pipeline.ID != pid || pipeline.Number != run || pipeline.RerunCount != attempt ||
		pipeline.Commit != q.HeadSHA || !(len(pipeline.Commit) == 40 || len(pipeline.Commit) == 64) {
		return WoodpeckerObservation{}, errors.New("Woodpecker exact pipeline/head/attempt mismatch")
	}
	var observed *wpStep
	var worker int64
	for _, workflow := range pipeline.Workflows {
		for _, step := range workflow.Children {
			if step.ID != job {
				continue
			}
			if observed != nil {
				return WoodpeckerObservation{}, errors.New("ambiguous Woodpecker step identity")
			}
			stepCopy := step
			observed = &stepCopy
			worker = workflow.AgentID
		}
	}
	if observed == nil {
		return WoodpeckerObservation{}, errors.New("requested Woodpecker job missing")
	}
	var files []wpConfig
	if err = w.get(ctx, c, base, pipelineRoot+"/config", &files); err != nil {
		return WoodpeckerObservation{}, err
	}
	digest, err := configDigest(files)
	if err != nil {
		return WoodpeckerObservation{}, err
	}
	out := WoodpeckerObservation{
		HeadSHA: pipeline.Commit, PipelineID: q.PipelineID, RunID: q.RunID,
		JobID: q.JobID, AttemptID: q.AttemptID, JobName: observed.Name,
		PipelineStatus: pipeline.Status, StepStatus: observed.State,
		ConfigurationSHA256: digest,
	}
	if worker > 0 {
		out.WorkerID = strconv.FormatInt(worker, 10)
	}
	if observed.Started > 0 {
		out.StartedAt = time.Unix(observed.Started, 0).UTC()
	}
	if observed.Finished > 0 {
		out.FinishedAt = time.Unix(observed.Finished, 0).UTC()
	}
	return out, nil
}

// FetchAttempt intentionally never returns a release-signable record.
// Without authoritative assignment transitions and proof of the exact job
// attempt, a pipeline status + agent_id is not sufficient for attestation.
func (w WoodpeckerReader) FetchAttempt(ctx context.Context, q RemoteCIQuery) (repoprotocol.RemoteCIRecord, error) {
	if _, err := w.ObserveAttempt(ctx, q); err != nil {
		return repoprotocol.RemoteCIRecord{}, err
	}
	return repoprotocol.RemoteCIRecord{}, ErrWoodpeckerAssignmentUnproven
}
