package reconcile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// cleanupLookupStore answers ProjectCleanupCommands with fixed commands or an
// error.
type cleanupLookupStore struct {
	lifecycleStore
	commands []string
	err      error
}

func (s *cleanupLookupStore) ProjectCleanupCommands(context.Context, string, string) ([]string, error) {
	return s.commands, s.err
}

// Delete receives the project's current Cleanup commands, including an empty
// list for a project that no longer has Cleanup; when they can't be read, or
// the store can't read them, Delete gets no list and the provider falls back
// to the commands the worker installed.
func TestDeletionCarriesTheProjectsCurrentCleanup(t *testing.T) {
	record := domain.Sandbox{OrgID: "org-1", SessionID: "session-1"}
	reconciler := func(store Store) *Reconciler {
		return New(store, lifecycleResolver{}, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	}

	ctx := reconciler(&cleanupLookupStore{commands: []string{"docker compose down"}}).withCleanupCommands(context.Background(), record)
	if commands, ok := sandbox.CleanupCommands(ctx); !ok || strings.Join(commands, "|") != "docker compose down" {
		t.Fatalf("current Cleanup = %v (%v), want the project's commands", commands, ok)
	}

	ctx = reconciler(&cleanupLookupStore{}).withCleanupCommands(context.Background(), record)
	if commands, ok := sandbox.CleanupCommands(ctx); !ok || len(commands) != 0 {
		t.Fatalf("a project without Cleanup = %v (%v), want a known empty list", commands, ok)
	}

	ctx = reconciler(&cleanupLookupStore{err: errors.New("project deleted")}).withCleanupCommands(context.Background(), record)
	if _, ok := sandbox.CleanupCommands(ctx); ok {
		t.Fatal("a failed lookup must leave the provider's installed commands in charge")
	}

	ctx = reconciler(&lifecycleStore{}).withCleanupCommands(context.Background(), record)
	if _, ok := sandbox.CleanupCommands(ctx); ok {
		t.Fatal("a store without the lookup must leave the provider's installed commands in charge")
	}
}

// deadlineDeleteProvider records the Deletes it receives and whether each
// asked to skip the project's Cleanup.
type deadlineDeleteProvider struct {
	lifecycleProvider
	deletes []bool
}

func (p *deadlineDeleteProvider) Delete(ctx context.Context, _ sandbox.ID) error {
	p.deletes = append(p.deletes, sandbox.CleanupSkipped(ctx))
	return nil
}

// Past the deletion deadline the reconciler gives up on the row, but first
// sends one Delete that skips Cleanup, so a Cleanup that outlived the deadline
// never leaves the workspace running.
func TestDeletionDeadlineDeletesWithoutCleanupBeforeGivingUp(t *testing.T) {
	requested := time.Now().Add(-time.Hour)
	record := domain.Sandbox{
		OrgID: "org-1", SessionID: "session-1", Provider: "coder",
		ProviderEnvironmentID: "workspace-1", DeletionRequestedAt: &requested,
	}
	provider := &deadlineDeleteProvider{lifecycleProvider: lifecycleProvider{
		environment: sandbox.Environment{ID: "workspace-1", State: sandbox.StateRunning},
	}}
	reconciler := New(&lifecycleStore{}, lifecycleResolver{provider: provider}, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := reconciler.reconcileDeletion(context.Background(), record, provider); err != nil {
		t.Fatal(err)
	}
	if len(provider.deletes) != 1 || !provider.deletes[0] {
		t.Fatalf("deletes = %v, want one that skips Cleanup", provider.deletes)
	}

	// Before the deadline Delete keeps running Cleanup.
	fresh := time.Now()
	record.DeletionRequestedAt = &fresh
	provider.deletes = nil
	if err := reconciler.reconcileDeletion(context.Background(), record, provider); err != nil {
		t.Fatal(err)
	}
	if len(provider.deletes) != 1 || provider.deletes[0] {
		t.Fatalf("deletes = %v, want one with Cleanup", provider.deletes)
	}
}
