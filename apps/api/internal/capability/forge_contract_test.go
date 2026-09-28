package capability

import (
	"context"
	"errors"
	"testing"

	"github.com/ai-dev-control-plane/policies"
)

func TestForgeOperationMapping(t *testing.T) {
	tests := []struct {
		forgeOp string
		wantOp  string
	}{
		{"repo.read", OpReadRepository},
		{"branch.create", OpCreateBranch},
		{"commit.write", OpCreateCommit},
		{"change.create", OpOpenPR},
		{"change.review", OpReviewPR},
		{"change.merge", OpMergePR},
		{"check.read", OpReadChecks},
	}

	for _, tt := range tests {
		t.Run(tt.forgeOp, func(t *testing.T) {
			got, ok := OperationForForge(tt.forgeOp)
			if !ok {
				t.Fatalf("OperationForForge(%q) not found", tt.forgeOp)
			}
			if got != tt.wantOp {
				t.Fatalf("OperationForForge(%q) = %q, want %q", tt.forgeOp, got, tt.wantOp)
			}
		})
	}

	if _, ok := OperationForForge("unknown"); ok {
		t.Fatal("unknown forge operation should not map")
	}
}

func TestKernelEvaluateForgeUsesDevPlanePolicy(t *testing.T) {
	ctx := context.Background()
	k := NewKernel(nil, nil, nil, nil)

	tests := []struct {
		name         string
		forgeOp      string
		wantEffect   policies.Effect
		wantApproval bool
	}{
		{"repo read allowed", ForgeOperationRepoRead, policies.EffectAllow, false},
		{"check read allowed", ForgeOperationCheckRead, policies.EffectAllow, false},
		{"branch create asks", ForgeOperationBranchCreate, policies.EffectAsk, true},
		{"commit write asks", ForgeOperationCommitWrite, policies.EffectAsk, true},
		{"change create asks", ForgeOperationChangeCreate, policies.EffectAsk, true},
		{"change review asks", ForgeOperationChangeReview, policies.EffectAsk, true},
		{"change merge admin only", ForgeOperationChangeMerge, policies.EffectAdminOnly, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := k.EvaluateForge(ctx, tt.forgeOp, Request{
				ActorType: "agent",
				Resource:  "nulang-org/nulang",
			})
			if err != nil {
				t.Fatalf("EvaluateForge() error: %v", err)
			}
			if result.Effect != tt.wantEffect {
				t.Fatalf("effect = %q, want %q", result.Effect, tt.wantEffect)
			}
			if result.RequiredApproval != tt.wantApproval {
				t.Fatalf("RequiredApproval = %v, want %v", result.RequiredApproval, tt.wantApproval)
			}
		})
	}
}

func TestKernelEvaluateForgeUnknownFailsClosed(t *testing.T) {
	k := NewKernel(nil, nil, nil, nil)
	result, err := k.EvaluateForge(context.Background(), "forge.superuser", Request{})
	if !errors.Is(err, ErrCapabilityUnknown) {
		t.Fatalf("error = %v, want ErrCapabilityUnknown", err)
	}
	if result == nil || result.Effect != policies.EffectDeny {
		t.Fatalf("result = %+v, want deny", result)
	}
}
