package activity

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type nativeAgent struct {
	ports.Agent
	snapshots []ports.NativeSessionSnapshot
	ok        bool
	err       error
	calls     int
}

func (a *nativeAgent) ObserveNativeSessions(context.Context, string) ([]ports.NativeSessionSnapshot, bool, error) {
	a.calls++
	return a.snapshots, a.ok, a.err
}

var (
	nativeSpawnedAt = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	nativeNow       = nativeSpawnedAt.Add(time.Minute)
)

func nativeRecord(workspace string) domain.SessionRecord {
	return domain.SessionRecord{
		ID:      "work-1",
		Harness: domain.HarnessPrimeAgent,
		Activity: domain.Activity{
			State:          domain.ActivityIdle,
			LastActivityAt: nativeSpawnedAt,
		},
		Metadata: domain.SessionMetadata{
			WorkspacePath:   workspace,
			RuntimeHandleID: "ptyhost-v1:work-1",
			RuntimeLaunchID: "launch-1",
		},
	}
}

func nativeSnapshot(workspace string) ports.NativeSessionSnapshot {
	return ports.NativeSessionSnapshot{
		WorkspacePath:  workspace,
		AgentSessionID: "native-1",
		TranscriptPath: "/data/sessions/native-1.jsonl",
		StartedAt:      nativeSpawnedAt.Add(time.Second),
	}
}

func signaled(rec domain.SessionRecord, snapshot ports.NativeSessionSnapshot, at time.Time, state domain.ActivityState) domain.SessionRecord {
	rec.FirstSignalAt = nativeSpawnedAt.Add(time.Second)
	rec.Metadata.AgentSessionID = snapshot.AgentSessionID
	rec.Metadata.NativeIdentityObservedAt = at
	rec.Activity = domain.Activity{State: state, LastActivityAt: at}
	return rec
}

// turn lists a prompt and its reply as a transcript would, leaving out a zero one.
func turn(prompt, reply ports.NativeMessage) []ports.NativeMessage {
	var messages []ports.NativeMessage
	if !prompt.At.IsZero() {
		messages = append(messages, prompt)
	}
	if !reply.At.IsZero() {
		reply.Assistant = true
		messages = append(messages, reply)
	}
	return messages
}

func pollNative(t *testing.T, agent *nativeAgent, records ...domain.SessionRecord) []ports.ActivitySignal {
	t.Helper()
	sink := &fakeSink{}
	observer := NewNativeSessionObserver(fakeSessions{rows: records}, sink, fakeAgents{domain.HarnessPrimeAgent: agent}, NativeSessionConfig{
		DataDir: "/data",
		Clock:   func() time.Time { return nativeNow },
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := observer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	return sink.signals
}

func events(signals []ports.ActivitySignal) string {
	names := make([]string, 0, len(signals))
	for _, signal := range signals {
		names = append(names, signal.Event+":"+string(signal.State))
	}
	return strings.Join(names, " ")
}

func TestNativeSessionReportsATurnFinishedBetweenPolls(t *testing.T) {
	snapshot := nativeSnapshot("/work")
	snapshot.Messages = turn(ports.NativeMessage{Text: "do it", At: nativeSpawnedAt.Add(2 * time.Second)}, ports.NativeMessage{Text: "done", At: nativeSpawnedAt.Add(3 * time.Second)})

	signals := pollNative(t, &nativeAgent{snapshots: []ports.NativeSessionSnapshot{snapshot}, ok: true}, nativeRecord("/work"))

	if got, want := events(signals), "session-start:idle user-prompt-submit:active stop:idle"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	for _, signal := range signals {
		if !signal.Valid || signal.AgentSessionID != "native-1" || signal.TranscriptPath != snapshot.TranscriptPath ||
			signal.LaunchID != "launch-1" || signal.ExpectedHarness != domain.HarnessPrimeAgent {
			t.Fatalf("signal lost its identity: %#v", signal)
		}
	}
	prompt, stop := signals[1], signals[2]
	if prompt.LatestUserPrompt != "do it" || prompt.TurnUserRequest != "do it" || !prompt.Timestamp.Equal(snapshot.Messages[0].At) ||
		prompt.ConversationCheckpointOrigin != domain.ConversationCheckpointOriginHuman {
		t.Fatalf("prompt signal = %#v", prompt)
	}
	if stop.FinalAssistantMessage != "done" || !stop.Timestamp.Equal(snapshot.Messages[1].At) {
		t.Fatalf("stop signal = %#v", stop)
	}
}

func TestNativeSessionDoesNotRepeatATurnAOAlreadyHas(t *testing.T) {
	snapshot := nativeSnapshot("/work")
	snapshot.Messages = turn(ports.NativeMessage{Text: "do it", At: nativeSpawnedAt.Add(2 * time.Second)}, ports.NativeMessage{Text: "done", At: nativeSpawnedAt.Add(3 * time.Second)})
	rec := signaled(nativeRecord("/work"), snapshot, snapshot.Messages[1].At, domain.ActivityIdle)

	if signals := pollNative(t, &nativeAgent{snapshots: []ports.NativeSessionSnapshot{snapshot}, ok: true}, rec); len(signals) != 0 {
		t.Fatalf("signals = %q, want none", events(signals))
	}
}

func TestNativeSessionKeepsResumedHistoryOutOfTheNewLaunch(t *testing.T) {
	snapshot := nativeSnapshot("/work")
	snapshot.StartedAt = nativeSpawnedAt.Add(-time.Hour)
	snapshot.Messages = turn(ports.NativeMessage{Text: "old", At: nativeSpawnedAt.Add(-time.Hour)}, ports.NativeMessage{Text: "old reply", At: nativeSpawnedAt.Add(-time.Hour)})

	signals := pollNative(t, &nativeAgent{snapshots: []ports.NativeSessionSnapshot{snapshot}, ok: true}, nativeRecord("/work"))

	if got, want := events(signals), "session-start:idle"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	if !signals[0].Timestamp.Equal(nativeSpawnedAt) {
		t.Fatalf("session-start at %v moved activity back from %v", signals[0].Timestamp, nativeSpawnedAt)
	}
}

func TestNativeSessionTurnBoundaries(t *testing.T) {
	prompt := ports.NativeMessage{Text: "go", At: nativeSpawnedAt.Add(2 * time.Second)}
	tests := []struct {
		name    string
		working bool
		prompt  ports.NativeMessage
		reply   ports.NativeMessage
		state   domain.ActivityState
		at      time.Time
		want    string
	}{
		{name: "new prompt while working", working: true, prompt: prompt, state: domain.ActivityIdle, at: nativeSpawnedAt, want: "user-prompt-submit:active"},
		{name: "reply stays open while tools run", working: true, prompt: prompt, reply: ports.NativeMessage{At: nativeNow}, state: domain.ActivityActive, at: prompt.At, want: ""},
		{name: "working with no prompt AO saw", working: true, state: domain.ActivityIdle, at: nativeSpawnedAt, want: "terminal-active:active"},
		{name: "interrupted turn has no reply", prompt: prompt, state: domain.ActivityActive, at: prompt.At, want: "stop:idle"},
		{name: "idle report just after a prompt waits", prompt: prompt, state: domain.ActivityActive, at: nativeNow.Add(-nativeTurnEndGrace + time.Second), want: ""},
		{name: "idle session stays quiet", prompt: prompt, state: domain.ActivityIdle, at: nativeNow, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := nativeSnapshot("/work")
			snapshot.Working, snapshot.Messages = tt.working, turn(tt.prompt, tt.reply)
			rec := signaled(nativeRecord("/work"), snapshot, tt.at, tt.state)

			signals := pollNative(t, &nativeAgent{snapshots: []ports.NativeSessionSnapshot{snapshot}, ok: true}, rec)

			if got := events(signals); got != tt.want {
				t.Fatalf("events = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNativeSessionKeepsCoordinationOutOfTheUserPrompt(t *testing.T) {
	snapshot := nativeSnapshot("/work")
	snapshot.Working = true
	snapshot.Messages = turn(ports.NativeMessage{Text: domain.WrapReportDelivery("report-1", "worker finished"), At: nativeSpawnedAt.Add(2 * time.Second)}, ports.NativeMessage{})
	rec := signaled(nativeRecord("/work"), snapshot, nativeSpawnedAt, domain.ActivityIdle)

	signals := pollNative(t, &nativeAgent{snapshots: []ports.NativeSessionSnapshot{snapshot}, ok: true}, rec)

	if len(signals) != 1 {
		t.Fatalf("events = %q, want one prompt", events(signals))
	}
	got := signals[0]
	if got.CoordinationID != "report-1" || got.ConversationCheckpointOrigin != domain.ConversationCheckpointOriginCoordination ||
		got.LatestUserPrompt != "" || got.TurnUserRequest != "" {
		t.Fatalf("coordination signal = %#v", got)
	}
}

func TestNativeSessionCapsMessageText(t *testing.T) {
	snapshot := nativeSnapshot("/work")
	snapshot.Messages = turn(ports.NativeMessage{}, ports.NativeMessage{Text: strings.Repeat("é", maxNativeMessageText), At: nativeSpawnedAt.Add(time.Second)})
	rec := signaled(nativeRecord("/work"), snapshot, nativeSpawnedAt, domain.ActivityActive)

	signals := pollNative(t, &nativeAgent{snapshots: []ports.NativeSessionSnapshot{snapshot}, ok: true}, rec)

	if len(signals) != 1 {
		t.Fatalf("events = %q, want one stop", events(signals))
	}
	got := signals[0].FinalAssistantMessage
	if len(got) > maxNativeMessageText || !strings.HasSuffix(got, "é") {
		t.Fatalf("capped text has %d bytes and ends %q", len(got), got[len(got)-3:])
	}
}

func TestNativeSessionStopsAfterARejectedSignal(t *testing.T) {
	snapshot := nativeSnapshot("/work")
	snapshot.Messages = turn(ports.NativeMessage{Text: "do it", At: nativeSpawnedAt.Add(2 * time.Second)}, ports.NativeMessage{Text: "done", At: nativeSpawnedAt.Add(3 * time.Second)})
	sink := &fakeSink{err: errors.New("rejected")}
	agent := &nativeAgent{snapshots: []ports.NativeSessionSnapshot{snapshot}, ok: true}
	observer := NewNativeSessionObserver(fakeSessions{rows: []domain.SessionRecord{nativeRecord("/work")}}, sink, fakeAgents{domain.HarnessPrimeAgent: agent}, NativeSessionConfig{
		DataDir: "/data",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	if err := observer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(sink.signals) != 1 {
		t.Fatalf("signals = %q, want the rejected one only", events(sink.signals))
	}
}

func TestNativeSessionOnlyAsksAboutRunningSessions(t *testing.T) {
	terminated := nativeRecord("/work")
	terminated.IsTerminated = true
	exited := nativeRecord("/work")
	exited.Activity.State = domain.ActivityExited
	noRuntime := nativeRecord("/work")
	noRuntime.Metadata.RuntimeHandleID = ""
	for name, rec := range map[string]domain.SessionRecord{"terminated": terminated, "exited": exited, "no runtime": noRuntime} {
		t.Run(name, func(t *testing.T) {
			agent := &nativeAgent{ok: true}
			if signals := pollNative(t, agent, rec); len(signals) != 0 || agent.calls != 0 {
				t.Fatalf("signals = %q, calls = %d, want none", events(signals), agent.calls)
			}
		})
	}
}

func TestNativeSessionLeavesHookSessionsAlone(t *testing.T) {
	snapshot := nativeSnapshot("/work")
	tests := []struct {
		name  string
		agent *nativeAgent
	}{
		{name: "version with hooks", agent: &nativeAgent{snapshots: []ports.NativeSessionSnapshot{snapshot}}},
		{name: "list failed", agent: &nativeAgent{snapshots: []ports.NativeSessionSnapshot{snapshot}, ok: true, err: errors.New("no daemon")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if signals := pollNative(t, tt.agent, nativeRecord("/work")); len(signals) != 0 {
				t.Fatalf("signals = %q, want none", events(signals))
			}
		})
	}
}

func TestMatchNativeSession(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	session := func(id, workspace string, started time.Time) ports.NativeSessionSnapshot {
		return ports.NativeSessionSnapshot{AgentSessionID: id, WorkspacePath: workspace, StartedAt: started}
	}
	older, newer := nativeSpawnedAt, nativeSpawnedAt.Add(time.Hour)

	t.Run("symlinked workspace", func(t *testing.T) {
		got, ok := matchNativeSession(nativeRecord(link), []ports.NativeSessionSnapshot{session("a", target, older)})
		if !ok || got.AgentSessionID != "a" {
			t.Fatalf("match = %#v, %v", got, ok)
		}
	})
	t.Run("another workspace", func(t *testing.T) {
		if got, ok := matchNativeSession(nativeRecord(target), []ports.NativeSessionSnapshot{session("a", "/elsewhere", older)}); ok {
			t.Fatalf("match = %#v, want none", got)
		}
	})
	t.Run("newest session wins", func(t *testing.T) {
		got, _ := matchNativeSession(nativeRecord(target), []ports.NativeSessionSnapshot{session("old", target, older), session("new", target, newer), session("mid", target, older)})
		if got.AgentSessionID != "new" {
			t.Fatalf("match = %q, want new", got.AgentSessionID)
		}
	})
	t.Run("known identity beats a newer session", func(t *testing.T) {
		rec := nativeRecord(target)
		rec.Metadata.AgentSessionID = "old"
		got, _ := matchNativeSession(rec, []ports.NativeSessionSnapshot{session("new", target, newer), session("old", target, older)})
		if got.AgentSessionID != "old" {
			t.Fatalf("match = %q, want old", got.AgentSessionID)
		}
	})
	t.Run("bound identity missing takes no other session", func(t *testing.T) {
		rec := nativeRecord(target)
		rec.Metadata.AgentSessionID = "gone"
		if got, ok := matchNativeSession(rec, []ports.NativeSessionSnapshot{session("other", target, newer)}); ok {
			t.Fatalf("match = %#v, want none", got)
		}
	})
	t.Run("session without a workspace", func(t *testing.T) {
		if got, ok := matchNativeSession(nativeRecord(""), []ports.NativeSessionSnapshot{session("a", "", older)}); ok {
			t.Fatalf("match = %#v, want none", got)
		}
	})
}

// nativeLab runs the native observer against the real lifecycle and store, so a
// test sees what AO durably keeps between polls.
type nativeLab struct {
	t        *testing.T
	store    *sqlite.Store
	id       domain.SessionID
	agent    *nativeAgent
	observer *NativeSessionObserver
	now      time.Time
}

func newNativeLab(t *testing.T, workspace string) *nativeLab {
	t.Helper()
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "prime", Path: workspace, RegisteredAt: start}); err != nil {
		t.Fatal(err)
	}
	rec := nativeRecord(workspace)
	rec.ID = ""
	rec.ProjectID = "prime"
	rec.Kind = domain.KindWorker
	rec.Mode = domain.SessionModeTUI
	rec.Activity.LastActivityAt = start
	rec.CreatedAt, rec.UpdatedAt = start, start
	created, err := store.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	lab := &nativeLab{t: t, store: store, id: created.ID, agent: &nativeAgent{ok: true}, now: start}
	lab.observer = NewNativeSessionObserver(store, lifecycle.New(store, nil), fakeAgents{domain.HarnessPrimeAgent: lab.agent}, NativeSessionConfig{
		DataDir: "/data",
		Clock:   func() time.Time { return lab.now },
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return lab
}

// poll advances the clock by step, then polls with the given sessions.
func (l *nativeLab) poll(step time.Duration, snapshots ...ports.NativeSessionSnapshot) domain.SessionRecord {
	l.t.Helper()
	l.now = l.now.Add(step)
	l.agent.snapshots = snapshots
	if err := l.observer.Poll(context.Background()); err != nil {
		l.t.Fatalf("Poll: %v", err)
	}
	rec, found, err := l.store.GetSession(context.Background(), l.id)
	if err != nil || !found {
		l.t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	return rec
}

func (l *nativeLab) accepted(deliveryID string) bool {
	l.t.Helper()
	ok, err := l.store.SemanticDeliveryAccepted(context.Background(), l.id, "launch-1", deliveryID)
	if err != nil {
		l.t.Fatal(err)
	}
	return ok
}

func TestNativeSessionReplaysEveryTurnFinishedWhileAOWasDown(t *testing.T) {
	lab := newNativeLab(t, "/work")
	snapshot := nativeSnapshot("/work")
	snapshot.StartedAt = lab.now.Add(time.Second)
	lab.poll(2*time.Second, snapshot)

	// Two turns finish before the next poll; the first is an AO delivery.
	at := lab.now.Add(time.Second)
	snapshot.Messages = []ports.NativeMessage{
		{Text: domain.WrapReportDelivery("report-1", "worker finished"), At: at},
		{Assistant: true, Text: "noted", At: at.Add(time.Second)},
		{Text: "second task", At: at.Add(2 * time.Second)},
		{Assistant: true, Text: "second done", At: at.Add(3 * time.Second)},
	}
	rec := lab.poll(time.Minute, snapshot)

	if !lab.accepted("report-1") {
		t.Fatal("the coordination delivery in the earlier turn was not accepted")
	}
	if rec.Activity.State != domain.ActivityIdle || rec.Metadata.LatestUserPrompt != "second task" ||
		rec.Metadata.ConversationCheckpointState != domain.ConversationCheckpointComplete ||
		!rec.Metadata.LatestAssistantUpdateAt.Equal(at.Add(3*time.Second)) {
		t.Fatalf("session = %+v, %+v", rec.Activity, rec.Metadata)
	}
	if signals := pollNative(t, &nativeAgent{snapshots: []ports.NativeSessionSnapshot{snapshot}, ok: true}, rec); len(signals) != 0 {
		t.Fatalf("replayed again: %q", events(signals))
	}
}

func TestNativeSessionAcceptsAPromptReadAfterPrimeReportedWorking(t *testing.T) {
	lab := newNativeLab(t, "/work")
	snapshot := nativeSnapshot("/work")
	snapshot.StartedAt = lab.now.Add(time.Second)
	lab.poll(2*time.Second, snapshot)

	// Prime reports working while the transcript can't be read yet.
	promptAt := lab.now.Add(time.Second)
	snapshot.Working = true
	if rec := lab.poll(3*time.Second, snapshot); rec.Activity.State != domain.ActivityActive {
		t.Fatalf("state = %q, want active", rec.Activity.State)
	}

	// The prompt line, older than that poll, becomes readable.
	snapshot.Messages = []ports.NativeMessage{{Text: domain.WrapReportDelivery("report-1", "worker finished"), At: promptAt}}
	lab.poll(3*time.Second, snapshot)

	if !lab.accepted("report-1") {
		t.Fatal("the delivery read after synthetic activity was not accepted")
	}
}

func TestNativeSessionKeepsItsIdentityWhenItsSessionIsGone(t *testing.T) {
	lab := newNativeLab(t, "/work")
	snapshot := nativeSnapshot("/work")
	snapshot.StartedAt = lab.now.Add(time.Second)
	lab.poll(2*time.Second, snapshot)

	// AO's session left Prime's list; another worker remains in the workspace.
	other := nativeSnapshot("/work")
	other.AgentSessionID, other.TranscriptPath = "native-2", "/data/sessions/native-2.jsonl"
	other.StartedAt = lab.now.Add(time.Second)
	other.Working = true
	rec := lab.poll(time.Minute, other)

	if rec.Metadata.AgentSessionID != "native-1" || rec.Metadata.NativeTranscriptPath != snapshot.TranscriptPath {
		t.Fatalf("identity = %q, %q, want native-1 kept", rec.Metadata.AgentSessionID, rec.Metadata.NativeTranscriptPath)
	}
	if rec.Activity.State != domain.ActivityIdle {
		t.Fatalf("state = %q, another session's activity leaked in", rec.Activity.State)
	}
}
