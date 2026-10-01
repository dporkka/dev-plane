package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/internal/capability"
	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/execution"
	"github.com/ai-dev-control-plane/gateway"
)

var ErrDeploymentEffectConflict = errors.New("deployment effect conflict")

type deploymentEffectGateway interface {
	ListDeployments(
		ctx context.Context,
		token *oauth2.Token,
		owner, name string,
		opts gateway.DeploymentListOptions,
	) ([]gateway.Deployment, error)
	CreateDeploymentWithPayload(
		ctx context.Context,
		token *oauth2.Token,
		owner, name, environment, ref string,
		payload map[string]any,
	) (*gateway.Deployment, error)
}

type deploymentEffectInput struct {
	RunID       string
	TaskID      string
	ProjectID   string
	RepoID      string
	Owner       string
	RepoName    string
	Environment string
	CommitSHA   string
}

func newDeploymentEffectIntent(input deploymentEffectInput) (execution.EffectIntent, error) {
	runID := strings.TrimSpace(input.RunID)
	if runID == "" {
		runID = strings.TrimSpace(input.TaskID)
	}
	environment := strings.TrimSpace(input.Environment)
	if environment == "" {
		return execution.EffectIntent{}, fmt.Errorf("deployment environment is required")
	}
	commitSHA := strings.TrimSpace(input.CommitSHA)
	if commitSHA == "" {
		return execution.EffectIntent{}, fmt.Errorf("deployment commit sha is required")
	}
	repoID := strings.TrimSpace(input.RepoID)
	if repoID == "" {
		return execution.EffectIntent{}, fmt.Errorf("deployment repository id is required")
	}

	activation := execution.Activation{
		RunID: runID,
		ID:    "deployment:" + repoID + ":" + environment,
		Epoch: 1,
	}
	grant := execution.Grant{
		Operation: capability.OpDeploy,
		Resource:  fmt.Sprintf("%s/%s", input.ProjectID, repoID),
		Revision:  "git-commit:" + commitSHA,
	}
	payload, err := json.Marshal(struct {
		Environment string
		CommitSHA   string
	}{
		Environment: environment,
		CommitSHA:   commitSHA,
	})
	if err != nil {
		return execution.EffectIntent{}, fmt.Errorf("encode deployment effect input: %w", err)
	}
	return execution.NewEffectIntent(activation, 0, grant, payload)
}

func newDeploymentEffectReceipt(intent execution.EffectIntent, deployment *gateway.Deployment) (execution.EffectReceipt, error) {
	if deployment == nil || deployment.ID <= 0 {
		return execution.EffectReceipt{}, fmt.Errorf("%w: deployment id is missing", ErrDeploymentEffectConflict)
	}
	output, err := json.Marshal(struct {
		ID          int64
		SHA         string
		Environment string
	}{
		ID:          deployment.ID,
		SHA:         strings.TrimSpace(deployment.SHA),
		Environment: strings.TrimSpace(deployment.Environment),
	})
	if err != nil {
		return execution.EffectReceipt{}, fmt.Errorf("encode deployment effect receipt: %w", err)
	}
	return execution.NewEffectReceipt(intent, "github", strconv.FormatInt(deployment.ID, 10), output)
}

func deploymentPayloadEffectID(deployment gateway.Deployment) string {
	if len(deployment.Payload) == 0 {
		return ""
	}
	var payload struct {
		EffectID string `json:"dev_plane_effect_id"`
	}
	if err := json.Unmarshal(deployment.Payload, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.EffectID)
}

func validateCorrelatedDeployment(input deploymentEffectInput, deployment gateway.Deployment) error {
	if deployment.ID <= 0 {
		return fmt.Errorf("%w: correlated deployment has no id", ErrDeploymentEffectConflict)
	}
	if sha := strings.TrimSpace(deployment.SHA); sha != "" && sha != strings.TrimSpace(input.CommitSHA) {
		return fmt.Errorf(
			"%w: deployment sha %s differs from authorized %s",
			ErrDeploymentEffectConflict,
			sha,
			input.CommitSHA,
		)
	}
	if environment := strings.TrimSpace(deployment.Environment); environment != "" &&
		environment != strings.TrimSpace(input.Environment) {
		return fmt.Errorf(
			"%w: deployment environment %s differs from authorized %s",
			ErrDeploymentEffectConflict,
			environment,
			input.Environment,
		)
	}
	return nil
}

// executeDeploymentEffect runs or reconciles one logical GitHub deployment.
//
// GitHub receives the deterministic Dev Plane effect ID in deployment payload.
// A retry lists deployments for the exact immutable commit/environment and only
// accepts a remote deployment carrying that exact effect ID.
func (h *Handler) executeDeploymentEffect(
	ctx context.Context,
	gh deploymentEffectGateway,
	token *oauth2.Token,
	input deploymentEffectInput,
) (*gateway.Deployment, error) {
	if h == nil || h.db == nil {
		return nil, errors.New("database is required")
	}
	if gh == nil {
		return nil, errors.New("deployment gateway is required")
	}

	intent, err := newDeploymentEffectIntent(input)
	if err != nil {
		return nil, err
	}
	if err := dbpkg.EnsureEffectIntent(ctx, h.db, intent); err != nil {
		return nil, fmt.Errorf("persist deployment effect intent: %w", err)
	}
	record, err := dbpkg.LoadEffect(ctx, h.db, intent.ID)
	if err != nil {
		return nil, fmt.Errorf("load deployment effect: %w", err)
	}

	deployments, err := gh.ListDeployments(ctx, token, input.Owner, input.RepoName, gateway.DeploymentListOptions{
		SHA:         strings.TrimSpace(input.CommitSHA),
		Environment: strings.TrimSpace(input.Environment),
	})
	if err != nil {
		return nil, fmt.Errorf("reconcile github deployments: %w", err)
	}

	var matches []gateway.Deployment
	for _, deployment := range deployments {
		if deploymentPayloadEffectID(deployment) != string(intent.ID) {
			continue
		}
		if err := validateCorrelatedDeployment(input, deployment); err != nil {
			return nil, err
		}
		matches = append(matches, deployment)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf(
			"%w: found %d remote deployments for effect %s",
			ErrDeploymentEffectConflict,
			len(matches),
			intent.ID,
		)
	}

	if len(matches) == 1 {
		deployment := matches[0]
		receipt, err := newDeploymentEffectReceipt(intent, &deployment)
		if err != nil {
			return nil, err
		}
		if record.Receipt != nil {
			if _, err := execution.MergeReceipt(*record.Receipt, receipt); err != nil {
				return nil, fmt.Errorf("replay deployment receipt: %w", err)
			}
			return &deployment, nil
		}
		if err := dbpkg.RecordEffectReceipt(ctx, h.db, receipt); err != nil {
			return nil, fmt.Errorf("record reconciled deployment receipt: %w", err)
		}
		return &deployment, nil
	}

	if record.Receipt != nil {
		return nil, fmt.Errorf(
			"%w: recorded deployment %s is no longer discoverable by its effect id",
			ErrDeploymentEffectConflict,
			record.Receipt.Reference,
		)
	}

	deployment, err := gh.CreateDeploymentWithPayload(
		ctx,
		token,
		input.Owner,
		input.RepoName,
		strings.TrimSpace(input.Environment),
		strings.TrimSpace(input.CommitSHA),
		map[string]any{"dev_plane_effect_id": string(intent.ID)},
	)
	if err != nil {
		return nil, err
	}
	if deployment == nil {
		return nil, errors.New("github deployment creation returned no result")
	}
	if err := validateCorrelatedDeployment(input, *deployment); err != nil {
		return nil, err
	}

	receipt, err := newDeploymentEffectReceipt(intent, deployment)
	if err != nil {
		return nil, err
	}
	if err := dbpkg.RecordEffectReceipt(ctx, h.db, receipt); err != nil {
		return nil, fmt.Errorf("record deployment effect receipt: %w", err)
	}
	return deployment, nil
}

func isDeploymentEffectConflict(err error) bool {
	return errors.Is(err, ErrDeploymentEffectConflict) ||
		errors.Is(err, execution.ErrIntentConflict) ||
		errors.Is(err, execution.ErrReceiptConflict)
}
