package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
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

// Delete also receives the Cleanup commands the worker was last given, stored
// on the sandbox, whether or not the current ones can be read; a sandbox
// without them gives none.
func TestDeletionCarriesTheWorkersInstalledCleanup(t *testing.T) {
	reconciler := New(&cleanupLookupStore{err: errors.New("project deleted")}, lifecycleResolver{},
		Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	record := domain.Sandbox{OrgID: "org-1", SessionID: "session-1",
		BootstrapContext: []byte(`{"providerFailureId":"build-1","installedCleanup":["export DEPLOY_TOKEN=x1y2z3w4","make down"]}`)}
	ctx := reconciler.withCleanupCommands(context.Background(), record)
	if commands, ok := sandbox.InstalledCleanupCommands(ctx); !ok || strings.Join(commands, "|") != "export DEPLOY_TOKEN=x1y2z3w4|make down" {
		t.Fatalf("installed Cleanup = %v (%v)", commands, ok)
	}
	record.BootstrapContext = []byte(`{"providerFailureId":"build-1"}`)
	if _, ok := sandbox.InstalledCleanupCommands(reconciler.withCleanupCommands(context.Background(), record)); ok {
		t.Fatal("a sandbox without stored commands gave some")
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

// sessionEventStore records the events appended to a session.
type sessionEventStore struct {
	lifecycleStore
	mu     sync.Mutex
	events []string
}

func (s *sessionEventStore) AppendSessionEvent(ctx context.Context, orgID, sessionID, eventType string, payload json.RawMessage) (domain.ClientEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.ClientEvent{}, err
	}
	s.events = append(s.events, orgID+"/"+sessionID+" "+eventType+" "+string(payload))
	return domain.ClientEvent{}, nil
}

// cleanupStepsProvider appends a Cleanup step from its Delete, after the
// reconcile pass that called it has ended, as the Coder provider does.
type cleanupStepsProvider struct {
	lifecycleProvider
	done chan struct{}
}

func (p *cleanupStepsProvider) Delete(ctx context.Context, _ sandbox.ID) error {
	go func() {
		defer close(p.done)
		background := context.WithoutCancel(ctx)
		time.Sleep(20 * time.Millisecond)
		sandbox.RecordSessionEvent(background, "workspace.script.step", map[string]any{"phase": "cleanup", "step": 1})
	}()
	return nil
}

// A provider's Delete appends events to the session whose sandbox it deletes,
// even once the reconcile pass that asked for the deletion is over.
func TestDeletionLetsTheProviderAppendSessionEvents(t *testing.T) {
	record := domain.Sandbox{OrgID: "org-1", SessionID: "session-1", Provider: "coder", ProviderEnvironmentID: "workspace-1"}
	provider := &cleanupStepsProvider{
		lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{ID: "workspace-1", State: sandbox.StateRunning}},
		done:              make(chan struct{}),
	}
	store := &sessionEventStore{}
	reconciler := New(store, lifecycleResolver{provider: provider}, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ctx, cancel := context.WithCancel(context.Background())
	if err := reconciler.reconcileDeletion(ctx, record, provider); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-provider.done
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.events) != 1 || store.events[0] != `org-1/session-1 workspace.script.step {"phase":"cleanup","step":1}` {
		t.Fatalf("events = %q", store.events)
	}
}

// cleanupResumeLookupStore answers CleanupResumeAllowed with err.
type cleanupResumeLookupStore struct {
	lifecycleStore
	err error
}

func (s *cleanupResumeLookupStore) CleanupResumeAllowed(context.Context, string, string) error {
	return s.err
}

// Delete learns the sandbox's organization, for the per-organization bound on
// Cleanup runs, and is told to keep a paused sandbox paused only when the
// organization is over its plan.
func TestDeletionCarriesTheCleanupScope(t *testing.T) {
	record := domain.Sandbox{OrgID: "org-1", SessionID: "session-1"}
	scope := func(store Store) context.Context {
		reconciler := New(store, lifecycleResolver{}, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		return reconciler.withCleanupScope(context.Background(), record)
	}
	for name, test := range map[string]struct {
		store   Store
		refused bool
	}{
		"within plan": {&cleanupResumeLookupStore{}, false},
		"over plan":   {&cleanupResumeLookupStore{err: &postgres.PlanLimitError{Code: postgres.LimitWeekly}}, true},
		"check fails": {&cleanupResumeLookupStore{err: errors.New("database down")}, false},
		"no check":    {&lifecycleStore{}, false},
	} {
		ctx := scope(test.store)
		if sandbox.CleanupOrg(ctx) != "org-1" {
			t.Fatalf("%s: organization = %q", name, sandbox.CleanupOrg(ctx))
		}
		if reason, refused := sandbox.CleanupResumeRefused(ctx); refused != test.refused || (refused && reason != CleanupResumeRefusedReason) {
			t.Fatalf("%s: resume refused = %v (%q)", name, refused, reason)
		}
	}
}

// A Cleanup whose steps set long time limits holds the deletion longer before
// the reconciler gives up on it: by how far its budget exceeds the default,
// and as much again for the longer wait for a run slot it gets
// (sandbox.CleanupQueueWait), never past the two-hour cap of each.
func TestDeletionDeadlineGrowsWithTheCleanupsTimeLimits(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	record := domain.Sandbox{OrgID: "org-1", SessionID: "session-1"}
	for name, test := range map[string]struct {
		current   []string
		installed string
		want      time.Duration
	}{
		"no cleanup":       {nil, "", DefaultDeletionDeadline},
		"one default step": {[]string{"make down"}, "", DefaultDeletionDeadline + 2*10*time.Second},
		"a long step":      {[]string{"# ao-timeout: 60m\nmake down"}, "", DefaultDeletionDeadline + 2*(50*time.Minute+10*time.Second)},
		"installed longer": {[]string{"make down"}, `{"installedCleanup":["# ao-timeout: 90m\nmake down"]}`, DefaultDeletionDeadline + 2*(80*time.Minute+10*time.Second)},
		"capped":           {[]string{"# ao-timeout: 120m\na", "# ao-timeout: 120m\nb"}, "", DefaultDeletionDeadline + 2*110*time.Minute},
	} {
		record.BootstrapContext = []byte(test.installed)
		reconciler := New(&cleanupLookupStore{commands: test.current}, lifecycleResolver{}, Options{Logger: logger})
		if got := reconciler.deletionDeadline(context.Background(), record); got != test.want {
			t.Errorf("%s: deadline = %s, want %s", name, got, test.want)
		}
	}

	// Within its extended deadline a deletion keeps running Cleanup.
	requested := time.Now().Add(-30 * time.Minute)
	record = domain.Sandbox{
		OrgID: "org-1", SessionID: "session-1", Provider: "coder",
		ProviderEnvironmentID: "workspace-1", DeletionRequestedAt: &requested,
	}
	provider := &deadlineDeleteProvider{lifecycleProvider: lifecycleProvider{
		environment: sandbox.Environment{ID: "workspace-1", State: sandbox.StateRunning},
	}}
	reconciler := New(&cleanupLookupStore{commands: []string{"# ao-timeout: 60m\nmake down"}}, lifecycleResolver{provider: provider}, Options{Logger: logger})
	if err := reconciler.reconcileDeletion(context.Background(), record, provider); err != nil {
		t.Fatal(err)
	}
	if len(provider.deletes) != 1 || provider.deletes[0] {
		t.Fatalf("deletes = %v, want one with Cleanup inside the extended deadline", provider.deletes)
	}
	// Past it, the last Delete skips Cleanup.
	long := time.Now().Add(-3 * time.Hour)
	record.DeletionRequestedAt = &long
	provider.deletes = nil
	if err := reconciler.reconcileDeletion(context.Background(), record, provider); err != nil {
		t.Fatal(err)
	}
	if len(provider.deletes) != 1 || !provider.deletes[0] {
		t.Fatalf("deletes = %v, want one that skips Cleanup", provider.deletes)
	}
}

// queueHoldProvider's Delete reports that the deletion's Cleanup waits hold
// behind its organization's runs.
type queueHoldProvider struct {
	deadlineDeleteProvider
	hold time.Duration
}

func (p *queueHoldProvider) Delete(ctx context.Context, id sandbox.ID) error {
	if !sandbox.CleanupSkipped(ctx) {
		sandbox.ReportCleanupQueueHold(ctx, p.hold)
	}
	return p.deadlineDeleteProvider.Delete(ctx, id)
}

// A Cleanup queued behind its organization's long Cleanups holds the deletion
// as much longer as the provider said it may wait, so it is not deleted
// without its Cleanup while it still waits within that time; the hold is at
// most sandbox.MaxCleanupBudget and ends with the deletion.
func TestDeletionDeadlineGrowsWhileTheCleanupWaitsForItsOrganization(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	requested := time.Now()
	record := domain.Sandbox{
		OrgID: "org-1", SessionID: "session-1", Provider: "coder",
		ProviderEnvironmentID: "workspace-1", DeletionRequestedAt: &requested,
	}
	provider := &queueHoldProvider{hold: 100 * time.Minute, deadlineDeleteProvider: deadlineDeleteProvider{lifecycleProvider: lifecycleProvider{
		environment: sandbox.Environment{ID: "workspace-1", State: sandbox.StateRunning},
	}}}
	reconciler := New(&cleanupLookupStore{commands: []string{"make down"}}, lifecycleResolver{provider: provider}, Options{Logger: logger})
	if err := reconciler.reconcileDeletion(context.Background(), record, provider); err != nil {
		t.Fatal(err)
	}
	base := DefaultDeletionDeadline + 2*10*time.Second
	if got := reconciler.deletionDeadline(context.Background(), record); got != base+100*time.Minute {
		t.Fatalf("deadline = %s, want %s", got, base+100*time.Minute)
	}
	// An hour in, past the deadline the run's own budget gives, Delete still
	// runs Cleanup.
	waited := time.Now().Add(-time.Hour)
	record.DeletionRequestedAt = &waited
	provider.deletes = nil
	if err := reconciler.reconcileDeletion(context.Background(), record, provider); err != nil {
		t.Fatal(err)
	}
	if len(provider.deletes) != 1 || provider.deletes[0] {
		t.Fatalf("deletes = %v, want one with Cleanup while it waits for its organization", provider.deletes)
	}
	provider.hold = 10 * time.Hour
	if err := reconciler.reconcileDeletion(context.Background(), record, provider); err != nil {
		t.Fatal(err)
	}
	if got := reconciler.deletionDeadline(context.Background(), record); got != base+sandbox.MaxCleanupBudget {
		t.Fatalf("deadline = %s, want the hold capped at %s", got, sandbox.MaxCleanupBudget)
	}
	// Once the sandbox is gone the hold is forgotten.
	provider.environment.State = sandbox.StateDeleted
	if err := reconciler.reconcileDeletion(context.Background(), record, provider); err != nil {
		t.Fatal(err)
	}
	if got := reconciler.deletionDeadline(context.Background(), record); got != base {
		t.Fatalf("deadline after deletion = %s, want %s", got, base)
	}
}
