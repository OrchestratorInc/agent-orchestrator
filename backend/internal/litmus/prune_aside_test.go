package litmus

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A checkout moved aside by a pass that never deleted it (<sha>.pruning) is deleted by the next pass without
// another move, even when its commit is pinned, and never shows in a dry run as something to move again.
func TestPruneDeletesCheckoutsLeftAsideByAnInterruptedPass(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	repo := filepath.Join(root, "repos", "aoagents__agent-orchestrator")
	stale := filepath.Join(repo, strings.Repeat("1", 40)+".pruning")
	pinned := filepath.Join(repo, strings.Repeat("2", 40)+".pruning.pruning") // its commit is still pinned by run_recent
	for _, d := range []string{stale, pinned} {
		writeFile(t, filepath.Join(d, "node_modules", "big.bin"), strings.Repeat("x", 1000))
	}
	rep, err := svc.Prune(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{stale, pinned, filepath.Join(repo, strings.Repeat("1", 40))} {
		if pathExists(d) {
			t.Errorf("%s still exists", strings.TrimPrefix(d, root))
		}
	}
	if !pathExists(filepath.Join(repo, strings.Repeat("2", 40))) {
		t.Fatal("the pinned checkout itself was deleted")
	}
	if len(rep.Checkouts) != 3 {
		t.Fatalf("checkouts = %v", rep.Checkouts)
	}
	if left, _ := filepath.Glob(filepath.Join(repo, "*.pruning*")); len(left) != 0 {
		t.Fatalf("dirs left aside: %v", left)
	}
}

// A pass whose context is already cancelled moves nothing aside: a half-done pass must never leave a dir that
// no run can use and no pass deleted.
func TestPruneMovesNothingAsideOnACancelledContext(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := svc.Prune(cctx, 1, 1); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repos", "aoagents__agent-orchestrator")
	if !pathExists(filepath.Join(repo, strings.Repeat("1", 40))) {
		t.Fatal("the stale checkout was deleted or moved on a cancelled context")
	}
	if left, _ := filepath.Glob(filepath.Join(repo, "*.pruning*")); len(left) != 0 {
		t.Fatalf("dirs moved aside: %v", left)
	}
}
