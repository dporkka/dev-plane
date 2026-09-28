package capability

import "testing"

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
