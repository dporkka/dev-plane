package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/internal/capability"
	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/execution"
	"github.com/ai-dev-control-plane/gateway"
)

var ErrMergeEffectConflict = errors.New("pull request merge effect conflict")

type mergeEffectInput struct {
	RunID         string
	TaskID        string
	PullRequestID string
	Owner         string
	RepoName      string
	Number        int
	Method        string
	Revision      string
}

func newMergeEffectIntent(input mergeEffectInput) (execution.EffectIntent, error) {
	runID := strings.TrimSpace(input.RunID)
	if runID == "" {
		runID = strings.TrimSpace(input.TaskID)
	}
	method := strings.TrimSpace(input.Method)
	if method == "" {
		method = "merge"
	}
	revision := strings.TrimSpace(input.Revision)
	if revision == "" {
		return execution.EffectIntent{}, fmt.Errorf("merge revision is required")
	}

	activation := execution.Activation{
		RunID: runID,
		ID:    "pull-request-merge:" + strings.TrimSpace(input.PullRequestID),
		Epoch: 1,
	}
	grant := execution.Grant{
		Operation: capability.OpMergePR,
		Resource:  fmt.Sprintf("%s/%s#%d", input.Owner, input.RepoName, input.Number),
		Revision:  revision,
	}
	payload, err := json.Marshal(struct {
		Method string
		SHA    string
	}{
		Method: method,
		SHA:    revision,
	})
	if err != nil {
		return execution.EffectIntent{}, fmt.Errorf("encode merge effect input: %w", err)
	}
	return execution.NewEffectIntent(activation, 0, grant, payload)
}

func newMergeEffectReceipt(intent execution.EffectIntent, mergeSHA string) (execution.EffectReceipt, error) {
	mergeSHA = strings.TrimSpace(mergeSHA)
	if mergeSHA == "" {
		return execution.EffectReceipt{}, fmt.Errorf("%w: merge result sha is empty", ErrMergeEffectConflict)
	}
	output, err := json.Marshal(struct {
		Merged bool
		SHA    string
	}{
		Merged: true,
		SHA:    mergeSHA,
	})
	if err != nil {
		return execution.EffectReceipt{}, fmt.Errorf("encode merge effect receipt: %w", err)
	}
	return execution.NewEffectReceipt(intent, "github", mergeSHA, output)
}

// executeMergeEffect runs or reconciles one logical GitHub merge effect.
//
// The intent is durable before the external call. If the process dies after
// GitHub merges but before a receipt is written, the next attempt can observe
// the already-merged remote PR and record the missing receipt without issuing a
// second merge.
func (h *Handler) executeMergeEffect(
	ctx context.Context,
	gh githubGateway,
	token *oauth2.Token,
	input mergeEffectInput,
	remote *gateway.GitHubPR,
) (string, error) {
	if h == nil || h.db == nil {
		return "", errors.New("database is required")
	}
	if gh == nil {
		return "", errors.New("github gateway is required")
	}
	if remote == nil {
		return "", errors.New("remote pull request is required")
	}

	intent, err := newMergeEffectIntent(input)
	if err != nil {
		return "", err
	}
	if err := dbpkg.EnsureEffectIntent(ctx, h.db, intent); err != nil {
		return "", fmt.Errorf("persist merge effect intent: %w", err)
	}
	record, err := dbpkg.LoadEffect(ctx, h.db, intent.ID)
	if err != nil {
		return "", fmt.Errorf("load merge effect: %w", err)
	}

	if strings.TrimSpace(remote.Head.SHA) != strings.TrimSpace(input.Revision) {
		return "", fmt.Errorf(
			"%w: authorized head %s became %s",
			ErrMergeEffectConflict,
			input.Revision,
			remote.Head.SHA,
		)
	}

	action, err := execution.RecoverEffect(intent, record.Receipt)
	if err != nil {
		return "", fmt.Errorf("recover merge effect: %w", err)
	}
	if action == execution.EffectReplayRecordedResult {
		if !remote.Merged {
			return "", fmt.Errorf("%w: receipt exists but remote pull request is not merged", ErrMergeEffectConflict)
		}
		if remote.MergeCommitSHA != "" && remote.MergeCommitSHA != record.Receipt.Reference {
			return "", fmt.Errorf(
				"%w: recorded merge sha %s differs from remote %s",
				ErrMergeEffectConflict,
				record.Receipt.Reference,
				remote.MergeCommitSHA,
			)
		}
		return record.Receipt.Reference, nil
	}

	if remote.Merged {
		receipt, err := newMergeEffectReceipt(intent, remote.MergeCommitSHA)
		if err != nil {
			return "", err
		}
		if err := dbpkg.RecordEffectReceipt(ctx, h.db, receipt); err != nil {
			return "", fmt.Errorf("record reconciled merge receipt: %w", err)
		}
		return receipt.Reference, nil
	}
	if remote.State != "open" {
		return "", fmt.Errorf("%w: remote pull request is %s without merge", ErrMergeEffectConflict, remote.State)
	}

	method := strings.TrimSpace(input.Method)
	if method == "" {
		method = "merge"
	}
	result, err := gh.MergePR(ctx, token, input.Owner, input.RepoName, input.Number, gateway.MergePRRequest{
		Method: method,
		SHA:    input.Revision,
	})
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", errors.New("github merge returned no result")
	}
	if !result.Merged {
		return "", fmt.Errorf("%w: %s", ErrMergeEffectConflict, result.Message)
	}

	receipt, err := newMergeEffectReceipt(intent, result.SHA)
	if err != nil {
		return "", err
	}
	if err := dbpkg.RecordEffectReceipt(ctx, h.db, receipt); err != nil {
		return "", fmt.Errorf("record merge effect receipt: %w", err)
	}
	return receipt.Reference, nil
}
