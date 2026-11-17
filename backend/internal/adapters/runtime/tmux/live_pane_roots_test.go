package tmux

import "testing"

// A pane kept after its process exited (remain-on-exit) may report a pid that
// another process now holds; only live panes are process-tree roots.
func TestParseLivePaneRootsSkipsDeadPanes(t *testing.T) {
	roots := parseLivePaneRoots("4242 0\n4343 1\nbad\n1 0\n")
	if len(roots) != 1 || roots[0].PID != 4242 || !roots[0].StartedBy.IsZero() {
		t.Fatalf("roots = %+v, want only the live pane 4242", roots)
	}
}
