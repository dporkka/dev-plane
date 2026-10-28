package verifier

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"
)

// Run this opt-in test only with a read-only Woodpecker API credential in the
// local environment. Never check tokens into the repository or print them.
func TestWoodpeckerRealReadOnlyAttempt(t *testing.T) {
	keys := []string{
		"DEVPLANE_WP_TEST_URL", "DEVPLANE_WP_TEST_TOKEN", "DEVPLANE_WP_TEST_REPOSITORY",
		"DEVPLANE_WP_TEST_REPO_ID", "DEVPLANE_WP_TEST_PIPELINE_ID",
		"DEVPLANE_WP_TEST_PIPELINE_NUMBER", "DEVPLANE_WP_TEST_STEP_ID",
		"DEVPLANE_WP_TEST_RERUN", "DEVPLANE_WP_TEST_HEAD_SHA",
	}
	present := 0
	for _, key := range keys {
		if os.Getenv(key) != "" {
			present++
		}
	}
	if present == 0 {
		t.Skip("real Woodpecker requires all DEVPLANE_WP_TEST_* read-only credentials and exact attempt identity")
	}
	if present != len(keys) {
		t.Fatal("partial Woodpecker integration test configuration; refusing unauthenticated/ambiguous request")
	}
	id, err := strconv.ParseInt(os.Getenv("DEVPLANE_WP_TEST_REPO_ID"), 10, 64)
	if err != nil || id <= 0 {
		t.Fatal("invalid Woodpecker repository ID")
	}
	q := RemoteCIQuery{
		HeadSHA:    os.Getenv("DEVPLANE_WP_TEST_HEAD_SHA"),
		PipelineID: os.Getenv("DEVPLANE_WP_TEST_PIPELINE_ID"),
		RunID:      os.Getenv("DEVPLANE_WP_TEST_PIPELINE_NUMBER"),
		JobID:      os.Getenv("DEVPLANE_WP_TEST_STEP_ID"),
		AttemptID:  os.Getenv("DEVPLANE_WP_TEST_RERUN"),
	}
	reader := WoodpeckerReader{
		BaseURL:      os.Getenv("DEVPLANE_WP_TEST_URL"),
		Token:        os.Getenv("DEVPLANE_WP_TEST_TOKEN"),
		RepositoryID: id, Repository: os.Getenv("DEVPLANE_WP_TEST_REPOSITORY"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	observation, err := reader.ObserveAttempt(ctx, q)
	if err != nil {
		t.Fatalf("authenticated read-only pipeline observation failed: %v", err)
	}
	if observation.HeadSHA != q.HeadSHA || observation.ConfigurationSHA256 == "" {
		t.Fatalf("incomplete live exact-head observation, no release approval granted")
	}
	// Even on a live green pipeline the public API lacks trustworthy assignment
	// timestamps, so this operation must never return a releasable receipt.
	if _, err = reader.FetchAttempt(ctx, q); !errors.Is(err, ErrWoodpeckerAssignmentUnproven) {
		t.Fatalf("real Woodpecker unexpectedly produced release-signable CI proof: %v", err)
	}
}
