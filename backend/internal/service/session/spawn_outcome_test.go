package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

// asyncCommander behaves like an asynchronous Chat start: Spawn writes
// "provisioning" through the observer on the request goroutine and answers;
// the test then settles the start the way the background goroutine would.
type asyncCommander struct {
	*fakeCommander
	observer sessionmanager.ProvisionObserver
	clock    *fakeClock
	answerIn time.Duration
	// afterAsync is work between the provisioning write and the answer
	// (attachments, the queued prompt).
	afterAsync time.Duration
	// settleDuringSpawn settles the start before Spawn answers (a fast start).
	settleDuringSpawn  domain.SessionProvisionState
	spawnErrAfterAsync error
}

func (a *asyncCommander) SetProvisionObserver(fn sessionmanager.ProvisionObserver) { a.observer = fn }

func (a *asyncCommander) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	rec := domain.SessionRecord{ID: "chat-1", ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: cfg.Harness, ProvisionState: domain.SessionProvisionProvisioning}
	a.clock.advance(a.answerIn)
	a.observer(ctx, rec, domain.SessionProvisionProvisioning, nil)
	if a.settleDuringSpawn != "" {
		a.observer(context.Background(), rec, a.settleDuringSpawn, nil)
	}
	a.clock.advance(a.afterAsync)
	if a.spawnErrAfterAsync != nil {
		return domain.SessionRecord{}, 0, 0, a.spawnErrAfterAsync
	}
	return rec, 0, 0, nil
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

type countingIdentity struct{ calls int }

func (c *countingIdentity) AuthenticatedIdentityForProvider(context.Context, string, string) (ports.SCMIdentity, error) {
	c.calls++
	return ports.SCMIdentity{Login: "octocat", Human: true}, nil
}

func newAsyncSpawnService(t *testing.T) (*Service, *asyncCommander, *fakeTelemetrySink, *fakeStore, memMilestones) {
	t.Helper()
	svc, cmd, sink, st, milestones, _ := newAsyncSpawnServiceWithIdentity(t)
	return svc, cmd, sink, st, milestones
}

func newAsyncSpawnServiceWithIdentity(t *testing.T) (*Service, *asyncCommander, *fakeTelemetrySink, *fakeStore, memMilestones, *countingIdentity) {
	t.Helper()
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	cmd := &asyncCommander{fakeCommander: &fakeCommander{}, clock: clock, answerIn: 40 * time.Millisecond, afterAsync: 25 * time.Millisecond}
	identity := &countingIdentity{}
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	sink := &fakeTelemetrySink{}
	milestones := memMilestones{}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st, Telemetry: sink, Milestones: milestones, GithubIdentity: identity, Clock: func() time.Time { return clock.now }})
	return svc, cmd, sink, st, milestones, identity
}

func named(sink *fakeTelemetrySink, name string) []ports.TelemetryEvent {
	var out []ports.TelemetryEvent
	for _, ev := range sink.events {
		if ev.Name == name {
			out = append(out, ev)
		}
	}
	return out
}

func settle(cmd *asyncCommander, state domain.SessionProvisionState, cause error) {
	cmd.observer(context.Background(), domain.SessionRecord{ID: "chat-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex}, state, cause)
}

func TestAsyncChatSpawnReportsSpawnedOnlyWhenUsable(t *testing.T) {
	svc, cmd, sink, _, _ := newAsyncSpawnService(t)
	ctx := telemetrymeta.WithSurface(context.Background(), domain.RequestSurfaceDesktop)
	if _, _, _, err := svc.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := named(sink, "ao.sessions.spawned"); len(got) != 0 {
		t.Fatalf("spawned before the session was usable: %#v", got)
	}
	cmd.clock.advance(9 * time.Second)
	settle(cmd, domain.SessionProvisionReady, nil)
	settle(cmd, domain.SessionProvisionReady, nil) // a repeated write settles nothing more
	spawned := named(sink, "ao.sessions.spawned")
	if len(spawned) != 1 {
		t.Fatalf("spawned = %d, want exactly one", len(spawned))
	}
	p := spawned[0].Payload
	if p["spawn_duration_ms"] != int64(9065) || p["duration_ms"] != int64(65) {
		t.Fatalf("durations = %#v / %#v, want 9065 to ready and 65 to answer", p["spawn_duration_ms"], p["duration_ms"])
	}
	if p["harness"] != "codex" || p["surface"] != "desktop" || p["created_by"] != "user" || p["first_for_install"] != true {
		t.Fatalf("acceptance context lost: %#v", p)
	}
	if len(named(sink, "ao.sessions.first_spawned")) != 1 || len(named(sink, "ao.sessions.spawn_failed")) != 0 {
		t.Fatalf("events = %#v", sink.events)
	}
}

func TestAsyncChatSpawnFailureKeepsFirstSessionClaimForTheRetry(t *testing.T) {
	svc, cmd, sink, st, milestones := newAsyncSpawnService(t)
	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	st.sessions["chat-1"] = domain.SessionRecord{ID: "chat-1", ProjectID: "mer"}
	cmd.clock.advance(2 * time.Second)
	settle(cmd, domain.SessionProvisionFailed, sessionmanager.ErrWorkspaceCreate)
	failed := named(sink, "ao.sessions.spawn_failed")
	if len(failed) != 1 || failed[0].Payload["spawn_duration_ms"] != int64(2065) || failed[0].Payload["duration_ms"] != int64(65) || failed[0].Payload["harness"] != "codex" {
		t.Fatalf("spawn_failed = %#v", failed)
	}
	if len(named(sink, "ao.sessions.spawned")) != 0 || milestones["session_spawned"] {
		t.Fatal("a failed start must not report spawned or spend the first_for_install claim")
	}

	// The user retries the failed start: a new attempt, timed from the retry.
	cmd.observer(context.Background(), domain.SessionRecord{ID: "chat-1", ProjectID: "mer"}, domain.SessionProvisionProvisioning, nil)
	cmd.clock.advance(3 * time.Second)
	settle(cmd, domain.SessionProvisionReady, nil)
	spawned := named(sink, "ao.sessions.spawned")
	if len(spawned) != 1 {
		t.Fatalf("spawned = %d, want one for the successful retry", len(spawned))
	}
	p := spawned[0].Payload
	if p["spawn_duration_ms"] != int64(3000) || p["first_for_install"] != true {
		t.Fatalf("retry payload = %#v", p)
	}
	if _, ok := p["duration_ms"]; ok {
		t.Fatalf("a retry has no request answer time: %#v", p)
	}
}

func TestAsyncChatSpawnSettledBeforeAnswerReportsOnceWithTheAnswerTime(t *testing.T) {
	svc, cmd, sink, _, _ := newAsyncSpawnService(t)
	cmd.settleDuringSpawn = domain.SessionProvisionReady
	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	spawned := named(sink, "ao.sessions.spawned")
	if len(spawned) != 1 {
		t.Fatalf("spawned = %d, want 1", len(spawned))
	}
	// Settled at 40ms; the request was answered at 65ms.
	if p := spawned[0].Payload; p["duration_ms"] != int64(65) || p["spawn_duration_ms"] != int64(40) {
		t.Fatalf("durations = %#v", p)
	}
}

// Resolving the GitHub login can be a network call; an asynchronous start
// must not wait for it before the agent launches, nor afterwards.
func TestAsyncChatSpawnDoesNotLookUpGitHubIdentity(t *testing.T) {
	svc, cmd, sink, _, _, identity := newAsyncSpawnServiceWithIdentity(t)
	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	settle(cmd, domain.SessionProvisionReady, nil)
	if identity.calls != 0 {
		t.Fatalf("GitHub identity lookups = %d, want 0", identity.calls)
	}
	if _, ok := named(sink, "ao.sessions.spawned")[0].Payload["github_actor"]; ok {
		t.Fatal("github_actor comes from the exporter's resolved identity, not a launch-time lookup")
	}
}

func TestAsyncChatSpawnErrorAfterGoingAsyncReportsOneFailure(t *testing.T) {
	svc, cmd, sink, _, _ := newAsyncSpawnService(t)
	cmd.spawnErrAfterAsync = errors.New("commit failed")
	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex}); err == nil {
		t.Fatal("Spawn succeeded")
	}
	settle(cmd, domain.SessionProvisionFailed, nil)
	if got := len(named(sink, "ao.sessions.spawn_failed")); got != 1 || len(named(sink, "ao.sessions.spawned")) != 0 {
		t.Fatalf("events = %#v, want exactly one spawn_failed", sink.events)
	}
}

// A start interrupted by a daemon restart is marked failed by the new process,
// which never opened that attempt: nothing is reported, and no duration is invented.
func TestUnopenedAttemptReportsNothing(t *testing.T) {
	_, cmd, sink, _, _ := newAsyncSpawnService(t)
	settle(cmd, domain.SessionProvisionFailed, nil)
	settle(cmd, domain.SessionProvisionReady, nil)
	if len(sink.events) != 0 {
		t.Fatalf("events = %#v, want none", sink.events)
	}
}

func TestSyncSpawnReportsSpawnDuration(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	sink := &fakeTelemetrySink{}
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	cmd := &fakeCommander{spawnHook: func(context.Context) { clock.advance(1200 * time.Millisecond) }}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st, Telemetry: sink, Clock: func() time.Time { return clock.now }})
	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	p := named(sink, "ao.sessions.spawned")[0].Payload
	if p["spawn_duration_ms"] != int64(1200) || p["duration_ms"] != int64(1200) || p["harness"] != "claude-code" {
		t.Fatalf("payload = %#v", p)
	}
}

// Workspace reads are gated on the daemon's recorded workspace, not on the
// start state: a failed start can keep its workspace, and an agent-only retry
// reuses it without a worktree step.
func TestSessionReportsWhetherItsWorkspaceIsPublished(t *testing.T) {
	st := newFakeStore()
	st.sessions["starting"] = domain.SessionRecord{ID: "starting", ProjectID: "mer", ProvisionState: domain.SessionProvisionProvisioning}
	failed := domain.SessionRecord{ID: "failed-kept", ProjectID: "mer", ProvisionState: domain.SessionProvisionFailed}
	failed.Metadata.WorkspacePath = "/tmp/worktrees/failed-kept"
	st.sessions[failed.ID] = failed
	retry := domain.SessionRecord{ID: "agent-retry", ProjectID: "mer", ProvisionState: domain.SessionProvisionProvisioning,
		ProvisionSteps: []domain.SessionProvisionStep{{ID: domain.SessionProvisionStepAgent, Status: domain.SessionProvisionStepRunning}}}
	retry.Metadata.WorkspacePath = "/tmp/worktrees/agent-retry"
	st.sessions[retry.ID] = retry
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})
	for id, want := range map[domain.SessionID]bool{"starting": false, "failed-kept": true, "agent-retry": true} {
		got, err := svc.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if got.WorkspacePublished != want {
			t.Errorf("%s workspacePublished = %t, want %t", id, got.WorkspacePublished, want)
		}
	}
}

// A start cancelled on purpose (killed mid-start, daemon shutdown) is not a
// launch failure; a start that ran out of its time budget is.
func TestCancelledAsyncChatStartIsNotASpawnFailure(t *testing.T) {
	svc, cmd, sink, _, milestones := newAsyncSpawnService(t)
	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	settle(cmd, domain.SessionProvisionFailed, fmt.Errorf("spawn chat-1: workspace: %w", context.Canceled))
	if len(named(sink, "ao.sessions.spawn_failed")) != 0 || len(named(sink, "ao.sessions.spawned")) != 0 {
		t.Fatalf("events = %#v, want none for a cancelled start", sink.events)
	}
	if milestones["session_spawned"] {
		t.Fatal("a cancelled start must not spend the first_for_install claim")
	}

	cmd.observer(context.Background(), domain.SessionRecord{ID: "chat-1", ProjectID: "mer"}, domain.SessionProvisionProvisioning, nil)
	settle(cmd, domain.SessionProvisionFailed, context.DeadlineExceeded)
	if got := named(sink, "ao.sessions.spawn_failed"); len(got) != 1 {
		t.Fatalf("spawn_failed = %d, want 1 for a start that timed out", len(got))
	}
}

// Spawn durations compare two readings from this process; converting to UTC
// would drop Go's monotonic reading and let a wall-clock adjustment skew them.
func TestSpawnDurationClockKeepsTheMonotonicReading(t *testing.T) {
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: newFakeStore()})
	if got := svc.monoNow().String(); !strings.Contains(got, "m=") {
		t.Fatalf("spawn duration clock %q has no monotonic reading", got)
	}
}
