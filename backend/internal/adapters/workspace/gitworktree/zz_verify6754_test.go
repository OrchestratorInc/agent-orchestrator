package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Verification-only (not committed): issue #6754 — is the live default-branch lookup bounded?
func TestVerify6754StalledLsRemote(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	shim := filepath.Join(tmp, "git-stall-shim")
	script := "#!/bin/sh\nfor a in \"$@\"; do\n  if [ \"$a\" = \"ls-remote\" ]; then sleep 20 >/dev/null 2>&1; fi\ndone\nexec " + git + " \"$@\"\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ws, err := New(Options{Binary: shim, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	target, err := ws.ResolveDefaultBranch(context.Background(), repo, "")
	elapsed := time.Since(start)
	t.Logf("ResolveDefaultBranch with stalled ls-remote (20s): elapsed=%s err=%v baseRef=%q (defaultBranchResolutionBudget=%s)", elapsed.Round(time.Millisecond), err, target.BaseRef, defaultBranchResolutionBudget)
	if elapsed > 9*time.Second {
		t.Fatalf("UNBOUNDED: lookup waited %s for the stalled remote instead of its own %s budget", elapsed.Round(time.Millisecond), defaultBranchResolutionBudget)
	}
}
