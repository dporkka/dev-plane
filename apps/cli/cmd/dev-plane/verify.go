package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/verifier"
)

type verifyOptions struct {
	Provider     string
	SourcePath   string
	Repository   string
	CandidateSHA string
	BaseSHA      string
	CloneURL     string
	Gates        []string
	ChangedPaths []string
	EvidenceOut  string
	RuntimeURL   string
	CPUMillis    int
	MemoryMB     int
	DiskMB       int
	WallTimeSecs int
}

type verifyOutput struct {
	Status       string                      `json:"status"`
	SessionID    string                      `json:"session_id,omitempty"`
	Evidence     repoprotocol.EvidenceBundle `json:"evidence"`
	PendingGates []string                    `json:"pending_gates,omitempty"`
	NextState    repoprotocol.WorkState      `json:"next_state,omitempty"`
}

type verifyRuntimeFactory func(verifyOptions) (verifier.WorkspaceRuntime, func() error, error)

type repeatedStringFlag []string

func (values *repeatedStringFlag) String() string {
	return strings.Join(*values, ",")
}

func (values *repeatedStringFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("value cannot be empty")
	}
	*values = append(*values, value)
	return nil
}

type cliEvidenceDocument struct {
	Evidence *repoprotocol.EvidenceBundle `json:"evidence,omitempty"`
	WorkItem *repoprotocol.WorkItem       `json:"work_item,omitempty"`
}

type cliEvidenceStore struct {
	path string
	doc  cliEvidenceDocument
}

func (s *cliEvidenceStore) PutEvidenceBundle(_ context.Context, bundle repoprotocol.EvidenceBundle) error {
	copyBundle := bundle
	s.doc.Evidence = &copyBundle
	return s.persist()
}

func (s *cliEvidenceStore) PutWorkItem(_ context.Context, item repoprotocol.WorkItem) error {
	copyItem := item
	s.doc.WorkItem = &copyItem
	return s.persist()
}

func (s *cliEvidenceStore) persist() error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return errors.New("verification evidence path is required")
	}
	parent := filepath.Dir(s.path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create verification evidence directory: %w", err)
	}
	payload, err := json.MarshalIndent(s.doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode verification evidence: %w", err)
	}
	payload = append(payload, '\n')
	temp, err := os.CreateTemp(parent, ".verification-evidence-*")
	if err != nil {
		return fmt.Errorf("create verification evidence temp file: %w", err)
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o644); err != nil {
		return fmt.Errorf("chmod verification evidence temp file: %w", err)
	}
	if _, err := temp.Write(payload); err != nil {
		return fmt.Errorf("write verification evidence: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync verification evidence: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close verification evidence: %w", err)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("commit verification evidence: %w", err)
	}
	committed = true
	return nil
}

func runVerify(args []string) error {
	return runVerifyWith(context.Background(), args, os.Stdout, newVerifyRuntime)
}

func runVerifyWith(ctx context.Context, args []string, out io.Writer, runtimeFactory verifyRuntimeFactory) error {
	opts, err := parseVerifyOptions(args)
	if err != nil {
		return err
	}
	if runtimeFactory == nil {
		return errors.New("verification runtime factory is required")
	}

	head, err := gitHead(ctx, opts.SourcePath)
	if err != nil {
		return fmt.Errorf("resolve verification source HEAD: %w", err)
	}
	if head != opts.CandidateSHA {
		return fmt.Errorf("source HEAD mismatch: expected %s got %s", opts.CandidateSHA, head)
	}

	runtime, cleanup, err := runtimeFactory(opts)
	if err != nil {
		return err
	}
	if runtime == nil {
		return errors.New("verification runtime factory returned nil runtime")
	}
	if cleanup == nil {
		cleanup = func() error { return nil }
	}

	store := &cliEvidenceStore{path: opts.EvidenceOut}
	runner := verifier.NewRunner(runtime, store)
	runResult, runErr := runner.Run(ctx, buildVerifyRunRequest(opts))
	if cleanupErr := cleanup(); cleanupErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("runtime cleanup: %w", cleanupErr))
	}

	result := verifyOutput{
		Status:       "passed",
		SessionID:    runResult.SessionID,
		Evidence:     runResult.Verification.Evidence,
		PendingGates: runResult.Verification.PendingGates,
		NextState:    runResult.Verification.NextState,
	}
	if runErr != nil {
		result.Status = "failed"
		if store.doc.Evidence != nil && strings.TrimSpace(result.Evidence.HeadSHA) == "" {
			result.Evidence = *store.doc.Evidence
		}
	}
	if encodeErr := json.NewEncoder(out).Encode(result); encodeErr != nil {
		if runErr != nil {
			return errors.Join(runErr, fmt.Errorf("encode verification result: %w", encodeErr))
		}
		return fmt.Errorf("encode verification result: %w", encodeErr)
	}
	return runErr
}

func parseVerifyOptions(args []string) (verifyOptions, error) {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var gates repeatedStringFlag
	var changed repeatedStringFlag

	opts := verifyOptions{}
	flags.StringVar(&opts.Provider, "provider", "local", "workspace provider: local or nulang")
	flags.StringVar(&opts.SourcePath, "source", ".", "already-checked-out repository source")
	flags.StringVar(&opts.Repository, "repository", "", "repository identity, for example owner/name")
	flags.StringVar(&opts.CandidateSHA, "candidate-sha", "", "immutable candidate commit SHA")
	flags.StringVar(&opts.BaseSHA, "base-sha", "", "immutable base commit SHA")
	flags.StringVar(&opts.CloneURL, "clone-url", "", "optional credential-bearing provenance URL; credentials are stripped before guest transfer")
	flags.Var(&gates, "gate", "required repository verification gate; repeat for multiple gates")
	flags.Var(&changed, "changed-path", "changed repository path; repeat for risk-based gates")
	flags.StringVar(&opts.EvidenceOut, "evidence-out", ".devplane/verification-evidence.json", "atomic JSON evidence output path")
	flags.StringVar(&opts.RuntimeURL, "runtime-url", "", "Nulang workspace API URL; defaults to DEV_PLANE_NULANG_URL")
	flags.IntVar(&opts.CPUMillis, "cpu-millis", 2000, "verification workspace CPU allocation")
	flags.IntVar(&opts.MemoryMB, "memory-mb", 4096, "verification workspace memory allocation")
	flags.IntVar(&opts.DiskMB, "disk-mb", 10240, "verification workspace disk allocation")
	flags.IntVar(&opts.WallTimeSecs, "wall-time-seconds", 1800, "verification workspace wall-time limit")

	if err := flags.Parse(args); err != nil {
		return verifyOptions{}, fmt.Errorf("usage: dev-plane verify --repository=<owner/name> --candidate-sha=<sha> --base-sha=<sha> --gate=<name> [options]: %w", err)
	}
	if flags.NArg() != 0 {
		return verifyOptions{}, errors.New("usage: dev-plane verify --repository=<owner/name> --candidate-sha=<sha> --base-sha=<sha> --gate=<name> [options]")
	}
	opts.Provider = strings.ToLower(strings.TrimSpace(opts.Provider))
	if opts.Provider != "local" && opts.Provider != "nulang" {
		return verifyOptions{}, fmt.Errorf("unsupported verification provider %q", opts.Provider)
	}
	opts.Repository = strings.TrimSpace(opts.Repository)
	opts.CandidateSHA = strings.TrimSpace(opts.CandidateSHA)
	opts.BaseSHA = strings.TrimSpace(opts.BaseSHA)
	if opts.Repository == "" || opts.CandidateSHA == "" || opts.BaseSHA == "" || len(gates) == 0 {
		return verifyOptions{}, errors.New("verify requires --repository, --candidate-sha, --base-sha, and at least one --gate")
	}
	absoluteSource, err := filepath.Abs(opts.SourcePath)
	if err != nil {
		return verifyOptions{}, fmt.Errorf("resolve verification source: %w", err)
	}
	opts.SourcePath = absoluteSource
	opts.Gates = append([]string(nil), gates...)
	opts.ChangedPaths = append([]string(nil), changed...)
	if !filepath.IsAbs(opts.EvidenceOut) {
		opts.EvidenceOut = filepath.Join(opts.SourcePath, opts.EvidenceOut)
	}
	if opts.CPUMillis <= 0 || opts.MemoryMB <= 0 || opts.DiskMB <= 0 || opts.WallTimeSecs <= 0 {
		return verifyOptions{}, errors.New("verification resource limits must be positive")
	}
	return opts, nil
}

func buildVerifyRunRequest(opts verifyOptions) verifier.RunRequest {
	digestInput := strings.Join([]string{opts.Repository, opts.CandidateSHA, strings.Join(opts.Gates, ",")}, "\x00")
	digest := sha256.Sum256([]byte(digestInput))
	identity := hex.EncodeToString(digest[:10])
	workspaceID := "verify-" + identity
	ownershipPaths := append([]string(nil), opts.ChangedPaths...)
	if len(ownershipPaths) == 0 {
		ownershipPaths = []string{"devplane.yaml"}
	}
	cloneURL := opts.CloneURL
	if opts.Provider == "local" {
		cloneURL = opts.SourcePath
	}

	return verifier.RunRequest{
		Workspace: runtimes.CreateRequest{
			RepositoryID: opts.Repository,
			CloneURL:     cloneURL,
			Branch:       workspaceID,
			BaseBranch:   opts.CandidateSHA,
			WorktreeName: workspaceID,
			Limits: runtimes.ResourceLimits{
				CPUMillis:       opts.CPUMillis,
				MemoryMB:        opts.MemoryMB,
				DiskMB:          opts.DiskMB,
				WallTimeSeconds: opts.WallTimeSecs,
			},
			Capabilities: runtimes.RuntimeCapabilities{Network: false},
			Metadata: map[string]string{
				"dev_plane_run_id":    workspaceID,
				"verification_sha":    opts.CandidateSHA,
				"verification_source": "ci",
			},
			IdempotencyKey: "verification:" + identity,
		},
		WorkItem: repoprotocol.WorkItem{
			ID:             workspaceID,
			Repository:     opts.Repository,
			Objective:      "Verify immutable CI candidate " + opts.CandidateSHA,
			OwnershipPaths: ownershipPaths,
			Risk:           repoprotocol.RiskMedium,
			Cost:           1,
			RequiredGates:  append([]string(nil), opts.Gates...),
			BaseSHA:        opts.BaseSHA,
			State:          repoprotocol.WorkVerifying,
		},
		CandidateHead: opts.CandidateSHA,
		ChangedPaths:  append([]string(nil), opts.ChangedPaths...),
	}
}

func newVerifyRuntime(opts verifyOptions) (verifier.WorkspaceRuntime, func() error, error) {
	switch opts.Provider {
	case "local":
		baseDir, err := os.MkdirTemp("", "devplane-verify-runtime-*")
		if err != nil {
			return nil, nil, fmt.Errorf("create local verification runtime directory: %w", err)
		}
		provider := runtimes.NewLocalProvider(baseDir)
		cleanup := func() error {
			unmountErr := runtimes.UnmountTmpfsBaseDir(baseDir)
			removeErr := os.RemoveAll(baseDir)
			return errors.Join(unmountErr, removeErr)
		}
		return provider, cleanup, nil
	case "nulang":
		runtimeURL := strings.TrimSpace(opts.RuntimeURL)
		if runtimeURL == "" {
			runtimeURL = strings.TrimSpace(os.Getenv("DEV_PLANE_NULANG_URL"))
		}
		if runtimeURL == "" {
			return nil, nil, errors.New("nulang verification requires --runtime-url or DEV_PLANE_NULANG_URL")
		}
		token := strings.TrimSpace(os.Getenv("DEV_PLANE_NULANG_INTERNAL_TOKEN"))
		if token == "" {
			return nil, nil, errors.New("nulang verification requires DEV_PLANE_NULANG_INTERNAL_TOKEN")
		}
		provider := runtimes.NewNulangCloudProvider(runtimeURL, token).
			WithRepositorySeeder(runtimes.NewLocalCheckoutNulangSeeder(opts.SourcePath, opts.CandidateSHA))
		return provider, func() error { return nil }, nil
	default:
		return nil, nil, fmt.Errorf("unsupported verification provider %q", opts.Provider)
	}
}

func gitHead(ctx context.Context, sourcePath string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", sourcePath, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
