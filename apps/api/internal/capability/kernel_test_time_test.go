package capability

import (
	"testing"
	"time"
)

// CI and local developers must interpret the same supplied timestamp.
// Previously the helper replaced every timestamp with the same fixed value
// whenever GITHUB_ACTIONS was set, hiding elapsed time and breaking budgets.
func TestTimeHelperIsDeterministicAcrossEnvironments(t *testing.T) {
	for _, env := range []string{"", "true"} {
		t.Run("GITHUB_ACTIONS="+env, func(t *testing.T) {
			t.Setenv("GITHUB_ACTIONS", env)
			start := testTime(t, "2024-01-01T10:00:00Z")
			end := testTime(t, "2024-01-01T10:05:00Z")
			if got := end.Sub(start); got != 5*time.Minute {
				t.Fatalf("elapsed time = %s, want 5m", got)
			}
		})
	}
}
