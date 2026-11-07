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
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
	rec.Activity = domain.Activity{State: state, LastActivityAt: at}
	return rec
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
	snapshot.Prompt = ports.NativeMessage{Text: "do it", At: nativeSpawnedAt.Add(2 * time.Second)}
	snapshot.Reply = ports.NativeMessage{Text: "done", At: nativeSpawnedAt.Add(3 * time.Second)}

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
	if prompt.LatestUserPrompt != "do it" || prompt.TurnUserRequest != "do it" || !prompt.Timestamp.Equal(snapshot.Prompt.At) ||
		prompt.ConversationCheckpointOrigin != domain.ConversationCheckpointOriginHuman {
		t.Fatalf("prompt signal = %#v", prompt)
	}
	if stop.FinalAssistantMessage != "done" || !stop.Timestamp.Equal(snapshot.Reply.At) {
		t.Fatalf("stop signal = %#v", stop)
	}
}

func TestNativeSessionDoesNotRepeatATurnAOAlreadyHas(t *testing.T) {
	snapshot := nativeSnapshot("/work")
	snapshot.Prompt = ports.NativeMessage{Text: "do it", At: nativeSpawnedAt.Add(2 * time.Second)}
	snapshot.Reply = ports.NativeMessage{Text: "done", At: nativeSpawnedAt.Add(3 * time.Second)}
	rec := signaled(nativeRecord("/work"), snapshot, snapshot.Reply.At, domain.ActivityIdle)

	if signals := pollNative(t, &nativeAgent{snapshots: []ports.NativeSessionSnapshot{snapshot}, ok: true}, rec); len(signals) != 0 {
		t.Fatalf("signals = %q, want none", events(signals))
	}
}

func TestNativeSessionKeepsResumedHistoryOutOfTheNewLaunch(t *testing.T) {
	snapshot := nativeSnapshot("/work")
	snapshot.StartedAt = nativeSpawnedAt.Add(-time.Hour)
	snapshot.Prompt = ports.NativeMessage{Text: "old", At: nativeSpawnedAt.Add(-time.Hour)}
	snapshot.Reply = ports.NativeMessage{Text: "old reply", At: nativeSpawnedAt.Add(-time.Hour)}

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
			snapshot.Working, snapshot.Prompt, snapshot.Reply = tt.working, tt.prompt, tt.reply
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
	snapshot.Prompt = ports.NativeMessage{Text: domain.WrapReportDelivery("report-1", "worker finished"), At: nativeSpawnedAt.Add(2 * time.Second)}
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
	snapshot.Reply = ports.NativeMessage{Text: strings.Repeat("é", maxNativeMessageText), At: nativeSpawnedAt.Add(time.Second)}
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
	snapshot.Prompt = ports.NativeMessage{Text: "do it", At: nativeSpawnedAt.Add(2 * time.Second)}
	snapshot.Reply = ports.NativeMessage{Text: "done", At: nativeSpawnedAt.Add(3 * time.Second)}
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
	t.Run("session without a workspace", func(t *testing.T) {
		if got, ok := matchNativeSession(nativeRecord(""), []ports.NativeSessionSnapshot{session("a", "", older)}); ok {
			t.Fatalf("match = %#v, want none", got)
		}
	})
}
