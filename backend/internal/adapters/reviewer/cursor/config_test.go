package cursor

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestReviewerProfileIsActuallyReadAndNarrow pins the #6842 Cursor fixes found
// live on Cursor CLI 2026.10.22: Cursor reads its rules from CURSOR_CONFIG_DIR
// (CURSOR_DATA_DIR alone left the reviewer on the user's own rules), and a
// whole-tool allow such as Shell(git) or Shell(printf) let `git push` and
// `printf x > file` run without a prompt.
func TestReviewerProfileIsActuallyReadAndNarrow(t *testing.T) {
	env := reviewerEnv(ports.ReviewInvocation{ReviewerID: "review-w1", DataDir: t.TempDir()})
	if env[cursorConfigDirEnv] == "" || env[cursorConfigDirEnv] != env[cursorDataDirEnv] {
		t.Fatalf("reviewer env = %#v; Cursor must read its rules from the AO profile", env)
	}
	for _, rule := range reviewerAllowedPermissions {
		switch rule {
		case "Shell(git)", "Shell(gh)", "Shell(ao)", "Shell(printf)":
			t.Fatalf("allowlist admits a whole tool: %q", rule)
		}
	}
	for _, want := range []string{"Shell(git push)", "Shell(git commit)", "Shell(ao spawn)"} {
		found := false
		for _, rule := range reviewerDeniedPermissions {
			found = found || rule == want
		}
		if !found {
			t.Fatalf("deny list missing %q: %#v", want, reviewerDeniedPermissions)
		}
	}
}
