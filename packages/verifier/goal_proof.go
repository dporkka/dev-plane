package verifier

import (
	"errors"
	"fmt"
	"strings"
	"time"

	models "github.com/ai-dev-control-plane/models"
	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

// GoalProofFromEvidenceBundle converts terminal repository verification evidence
// into a proof reference for one current goal criterion. The adapter preserves
// the existing EvidenceBundle authority boundary: incomplete/pending evidence
// does not become proof, while explicit terminal gate failure becomes failed
// proof rather than being mistaken for an adapter error.
func GoalProofFromEvidenceBundle(goal models.Goal, criterionID string, bundle repoprotocol.EvidenceBundle, observedAt time.Time) (models.GoalProof, error) {
	if err := goal.Validate(); err != nil {
		return models.GoalProof{}, fmt.Errorf("validate goal: %w", err)
	}
	goal.ProofEpoch = strings.TrimSpace(goal.ProofEpoch)
	if goal.ProofEpoch == "" {
		return models.GoalProof{}, errors.New("goal proof_epoch is required")
	}
	criterionID = strings.TrimSpace(criterionID)
	criterion, ok := verifierGoalCriterion(goal, criterionID)
	if !ok {
		return models.GoalProof{}, fmt.Errorf("goal criterion %q is not part of goal %s", criterionID, goal.ID)
	}
	if observedAt.IsZero() {
		return models.GoalProof{}, errors.New("evidence observed_at is required")
	}

	bundle.WorkItemID = strings.TrimSpace(bundle.WorkItemID)
	bundle.BaseSHA = strings.TrimSpace(bundle.BaseSHA)
	bundle.HeadSHA = strings.TrimSpace(bundle.HeadSHA)
	if bundle.WorkItemID == "" {
		return models.GoalProof{}, errors.New("evidence work_item_id is required")
	}
	if bundle.BaseSHA == "" {
		return models.GoalProof{}, errors.New("evidence base_sha is required")
	}
	if bundle.HeadSHA == "" {
		return models.GoalProof{}, errors.New("evidence head_sha is required")
	}
	if len(bundle.Gates) == 0 {
		return models.GoalProof{}, errors.New("evidence requires at least one gate")
	}

	hasFailed := false
	for _, gate := range bundle.Gates {
		if strings.TrimSpace(gate.Name) == "" {
			return models.GoalProof{}, errors.New("evidence gate name is required")
		}
		switch gate.Status {
		case repoprotocol.GatePassed:
		case repoprotocol.GateFailed:
			hasFailed = true
		case repoprotocol.GatePending, repoprotocol.GateSkipped:
			return models.GoalProof{}, fmt.Errorf("evidence gate %q is not terminal proof: %s", gate.Name, gate.Status)
		default:
			return models.GoalProof{}, fmt.Errorf("evidence gate %q has invalid status %q", gate.Name, gate.Status)
		}
	}

	status := models.GoalProofPassed
	if hasFailed {
		status = models.GoalProofFailed
	} else if err := bundle.ValidateHead(bundle.HeadSHA); err != nil {
		return models.GoalProof{}, fmt.Errorf("validate passing evidence: %w", err)
	}

	return models.GoalProof{
		CriterionID:     criterion.ID,
		CriterionDigest: criterion.Digest(),
		EvidenceID:      "evidence-bundle:" + bundle.WorkItemID + ":" + bundle.HeadSHA,
		SubjectRevision: "git-commit:" + bundle.HeadSHA,
		ProofEpoch:      goal.ProofEpoch,
		Status:          status,
		ObservedAt:      observedAt,
	}, nil
}

func verifierGoalCriterion(goal models.Goal, criterionID string) (models.GoalCriterion, bool) {
	for _, criterion := range goal.SuccessCriteria {
		if strings.TrimSpace(criterion.ID) == criterionID {
			return criterion, true
		}
	}
	return models.GoalCriterion{}, false
}
