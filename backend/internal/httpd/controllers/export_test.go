package controllers

import (
	"context"
	"testing"
	"time"
)

func ForcePartialWatches(t *testing.T, recheck time.Duration) {
	t.Helper()
	workspace, artifacts, interval := watchWorkspace, watchArtifacts, partialWatchRecheck
	t.Cleanup(func() { watchWorkspace, watchArtifacts, partialWatchRecheck = workspace, artifacts, interval })
	partialWatchRecheck = recheck
	always := func() bool { return true }
	watchWorkspace = func(ctx context.Context, roots ...string) (<-chan struct{}, func() bool, error) {
		changes, _, err := workspace(ctx, roots...)
		return changes, always, err
	}
	watchArtifacts = func(ctx context.Context, root string, skip func(string) bool) (<-chan struct{}, func() bool, error) {
		changes, _, err := artifacts(ctx, root, skip)
		return changes, always, err
	}
}
