package workertransport

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
)

const (
	codexCLIMeta     = `{"timestamp":"2026-10-18T06:00:00Z","type":"session_meta","payload":{"id":"t1","originator":"codex-tui","source":"cli"}}` + "\n"
	codexExecMeta    = `{"timestamp":"2026-10-18T06:00:00Z","type":"session_meta","payload":{"id":"t3","originator":"codex_exec","source":"exec"}}` + "\n"
	codexChatMeta    = `{"timestamp":"2026-10-18T06:00:00Z","type":"session_meta","payload":{"id":"t2","originator":"agent-orchestrator","source":"vscode"}}` + "\n"
	codexTurnStarted = `{"timestamp":"2026-10-18T06:00:01Z","type":"event_msg","payload":{"type":"task_started","turn_id":"u1"}}` + "\n"
	// Recorded from Codex 0.150 when its sign-in expired mid-session.
	codexTurnFailed = `{"timestamp":"2026-10-18T06:00:02Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"u1","last_agent_message":null,"error":{"message":"Your access token could not be refreshed because your refresh token was already used. Please log out and sign in again.","codex_error_info":"unauthorized"}}}` + "\n"
	codexTurnDone   = `{"timestamp":"2026-10-18T06:00:02Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"u1","last_agent_message":"done"}}` + "\n"
	// Recorded from Codex 0.147 when Esc interrupted a turn.
	codexTurnInterrupted = `{"timestamp":"2026-10-19T04:39:23.327Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"01a15274-f5ac-7b32-bd2e-6ff4528b4f0f","reason":"interrupted","started_at":1792384759,"completed_at":1792384763,"duration_ms":4109}}` + "\n"
)

// codexTurnFailedAt is codexTurnFailed stamped at a given time.
func codexTurnFailedAt(at time.Time) string {
	return strings.Replace(codexTurnFailed, "2026-10-18T06:00:02Z", at.UTC().Format(time.RFC3339Nano), 1)
}

func writeRollout(t *testing.T, codexHome, name, content string) string {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "10", "18")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendRollout(t *testing.T, path, content string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func TestCodexTurnRecord(t *testing.T) {
	end, kind := codexTurnRecord([]byte(codexTurnFailed))
	if kind != turnRecordEnd || end.message == "" || end.interrupted {
		t.Fatalf("failed turn = %+v/%v, want reported with its message", end, kind)
	}
	if want := time.Date(2026, 10, 18, 6, 0, 2, 0, time.UTC); !end.at.Equal(want) {
		t.Fatalf("failed turn ended at %v, want its record's timestamp %v", end.at, want)
	}
	before := time.Now()
	end, kind = codexTurnRecord([]byte(strings.Replace(codexTurnFailed, `"timestamp":"2026-10-18T06:00:02Z",`, "", 1)))
	if kind != turnRecordEnd || end.at.Before(before) {
		t.Fatalf("failed turn without a timestamp = %+v/%v, want it ended now", end, kind)
	}
	end, kind = codexTurnRecord([]byte(codexTurnInterrupted))
	if kind != turnRecordEnd || !end.interrupted {
		t.Fatalf("interrupted turn = %+v/%v, want an interrupt", end, kind)
	}
	if want := time.Date(2026, 10, 19, 4, 39, 23, 327_000_000, time.UTC); !end.at.Equal(want) {
		t.Fatalf("interrupted turn ended at %v, want its record's timestamp %v", end.at, want)
	}
	if _, kind := codexTurnRecord([]byte(codexTurnStarted)); kind != turnRecordStart {
		t.Fatalf("task_started kind = %v, want a turn start", kind)
	}
	for _, line := range []string{
		codexTurnDone,
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"u1","error":null}}`,
		`{"type":"response_item","payload":{"type":"task_complete","error":{"message":"x"}}}`,
		// A turn replaced by a newer one leaves the agent working.
		`{"timestamp":"2026-10-19T04:39:23.327Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"u1","reason":"replaced"}}`,
		`not json`,
	} {
		if _, kind := codexTurnRecord([]byte(line)); kind != turnRecordOther {
			t.Fatalf("record %s classified %v, want ignored", line, kind)
		}
	}
}

// Esc in the terminal: Codex 0.147 (the version AO installs) has no Interrupt
// hook and fires no Stop for an aborted turn, but writes turn_aborted.
func TestCodexRolloutTailReportsAnInterruptedTurn(t *testing.T) {
	home := t.TempDir()
	tail := newCodexRolloutTail(home, "", time.Now().Add(-time.Second))
	path := writeRollout(t, home, "rollout-tui.jsonl", codexCLIMeta+codexTurnStarted)
	if ends := tail.poll(); len(ends) != 0 {
		t.Fatalf("a running turn was reported: %v", ends)
	}
	appendRollout(t, path, codexTurnInterrupted)
	ends := tail.poll()
	if len(ends) != 1 || !ends[0].interrupted {
		t.Fatalf("ends = %+v, want the interrupt", ends)
	}
	// A prompt sent right after, in the same read, supersedes it.
	appendRollout(t, path, codexTurnStarted+codexTurnInterrupted+codexTurnStarted)
	if ends := tail.poll(); len(ends) != 0 {
		t.Fatalf("ends = %+v, want none: a later turn is running", ends)
	}
}

func TestCodexRolloutTailReportsOnlyNewFailedTurns(t *testing.T) {
	home := t.TempDir()
	// A failure from before the terminal started is history, not this turn.
	path := writeRollout(t, home, "rollout-a.jsonl", codexCLIMeta+codexTurnStarted+codexTurnFailed)
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	tail := newCodexRolloutTail(home, "", time.Now())
	if failures := tail.poll(); len(failures) != 0 {
		t.Fatalf("history reported as failures: %v", failures)
	}
	appendRollout(t, path, codexTurnStarted+codexTurnDone)
	if failures := tail.poll(); len(failures) != 0 {
		t.Fatalf("a successful turn was reported: %v", failures)
	}
	// A record split across polls is read once it is complete.
	appendRollout(t, path, codexTurnFailed[:40])
	if failures := tail.poll(); len(failures) != 0 {
		t.Fatalf("a partial record was reported: %v", failures)
	}
	appendRollout(t, path, codexTurnFailed[40:])
	if failures := tail.poll(); len(failures) != 1 {
		t.Fatalf("failures = %v, want the failed turn", failures)
	}
}

// A resumed conversation (codex resume, or a Codex adopted across a worker
// restart) appends to the rollout that already holds its earlier turns. Its
// old failure is history even though the file changes after the watcher
// starts.
func TestCodexRolloutTailSkipsAResumedRolloutsHistory(t *testing.T) {
	home := t.TempDir()
	path := writeRollout(t, home, "rollout-a.jsonl", codexCLIMeta+codexTurnStarted+codexTurnFailed)
	tail := newCodexRolloutTail(home, "", time.Now().Add(-time.Second))
	appendRollout(t, path, codexTurnStarted)
	if failures := tail.poll(); len(failures) != 0 {
		t.Fatalf("a resumed rollout's old failure was reported mid-turn: %v", failures)
	}
	appendRollout(t, path, codexTurnFailed)
	if failures := tail.poll(); len(failures) != 1 {
		t.Fatalf("failures = %v, want the resumed turn's failure", failures)
	}
}

func TestCodexRolloutTailIgnoresExecRollouts(t *testing.T) {
	home := t.TempDir()
	tail := newCodexRolloutTail(home, "", time.Now().Add(-time.Second))
	writeRollout(t, home, "rollout-exec.jsonl", codexExecMeta+codexTurnStarted+codexTurnFailed)
	if failures := tail.poll(); len(failures) != 0 {
		t.Fatalf("a codex exec rollout was reported: %v", failures)
	}
	writeRollout(t, home, "rollout-tui.jsonl", codexCLIMeta+codexTurnStarted+codexTurnFailed)
	tail.scans = 0
	if failures := tail.poll(); len(failures) != 1 {
		t.Fatalf("failures = %v, want the terminal rollout's failed turn", failures)
	}
}

// A conversation started in Chat records source vscode in its rollout. Resumed
// in the terminal, its failed turns are the terminal's, whether or not the
// worker knows which conversation it resumed.
func TestCodexRolloutTailFollowsAChatConversationResumedInTheTerminal(t *testing.T) {
	for _, conversationID := range []string{"t2", ""} {
		home := t.TempDir()
		path := writeRollout(t, home, "rollout-2026-10-18T06-00-00-t2.jsonl", codexChatMeta+codexTurnStarted+codexTurnDone)
		// Written by Chat before the terminal took over.
		old := time.Now().Add(-time.Minute)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		tail := newCodexRolloutTail(home, conversationID, time.Now().Add(-time.Second))
		if failures := tail.poll(); len(failures) != 0 {
			t.Fatalf("conversation %q: Chat's history was reported: %v", conversationID, failures)
		}
		appendRollout(t, path, codexTurnStarted+codexTurnFailed)
		tail.scans = 0
		if failures := tail.poll(); len(failures) != 1 {
			t.Fatalf("conversation %q: failures = %v, want the terminal's failed turn", conversationID, failures)
		}
	}
}

type codexActivityControl struct {
	supervisorControlStub
	mu       sync.Mutex
	activity []worker.ActivityEvent
	got      chan struct{}
}

func (c *codexActivityControl) PublishActivity(_ context.Context, event worker.ActivityEvent) error {
	c.mu.Lock()
	c.activity = append(c.activity, event)
	c.mu.Unlock()
	select {
	case c.got <- struct{}{}:
	default:
	}
	return nil
}

// A Codex terminal turn that ends on an error fires no Stop hook. The worker
// must still report the agent idle and write the terminal stop proof, or a
// switch to Chat waits forever for the turn to finish.
func TestCodexTerminalTurnFailureMarksAgentIdle(t *testing.T) {
	previous := terminalTurnPollInterval
	terminalTurnPollInterval = 20 * time.Millisecond
	defer func() { terminalTurnPollInterval = previous }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	home, dataDir := t.TempDir(), t.TempDir()
	// The failed turn's edits are checkpointed, as a Stop hook would ask.
	socketDir, err := os.MkdirTemp("", "aocp")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDir)
	checkpointSocket := filepath.Join(socketDir, "c.sock")
	listener, err := net.Listen("unix", checkpointSocket)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := make(chan struct{}, 4)
	go func() {
		_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/checkpoint" {
				checkpoints <- struct{}{}
			}
			w.WriteHeader(http.StatusAccepted)
		}))
	}()
	defer listener.Close()
	terminalID := "00000000-0000-0000-0000-000000000061"
	control := &codexActivityControl{got: make(chan struct{}, 1)}
	supervisor := &Supervisor{
		Control: control, Workspace: t.TempDir(), DataDir: dataDir, Harness: "codex",
		AgentTerminalID: terminalID,
		AgentCommand: workerexec.Command{
			Path: "/bin/sh", Args: []string{"-c", "cat"},
			Env: map[string]string{"CODEX_HOME": home, "AO_CHECKPOINT_SOCKET": checkpointSocket},
		},
		terminals: make(map[string]*terminalProcess),
	}
	supervisor.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	defer supervisor.closeAllTerminals()
	if err := supervisor.openTerminal(ctx, worker.TerminalCommand{TerminalID: terminalID, Kind: "agent"}); err != nil {
		t.Fatal(err)
	}
	// The user typed the prompt that failed.
	supervisor.mu.Lock()
	supervisor.noteAgentTerminalInput([]byte("fix it\r"))
	supervisor.mu.Unlock()
	if idle, _ := supervisor.tuiSourceIdle(); idle {
		t.Fatal("terminal reported idle before the turn ended")
	}
	time.Sleep(10 * time.Millisecond)
	writeRollout(t, home, "rollout-tui.jsonl", codexCLIMeta+codexTurnStarted+codexTurnFailedAt(time.Now()))
	select {
	case <-control.got:
	case <-time.After(5 * time.Second):
		t.Fatal("the failed Codex turn was not reported")
	}
	control.mu.Lock()
	event := control.activity[0]
	control.mu.Unlock()
	if event.Harness != "codex" || event.Event != "stop-failure" ||
		event.State != contract.ActivityIdle || event.SourceInterface != "tui" {
		t.Fatalf("activity = %+v, want codex stop-failure idle from the terminal", event)
	}
	if !worker.ValidActivityEvent(event) {
		t.Fatalf("the control plane would reject %+v", event)
	}
	if idle, unverified := supervisor.tuiSourceIdle(); !idle || unverified {
		t.Fatalf("terminal idle = %v (unverified %v), want verified idle after the failed turn", idle, unverified)
	}
	select {
	case <-checkpoints:
	case <-time.After(5 * time.Second):
		t.Fatal("the failed Codex turn was not checkpointed")
	}
}

// The poll sees a failed turn up to two seconds after it ended. A prompt the
// user sent in between is newer than the failure, so the stop proof (stamped
// when the turn ended) must not mark that new turn idle.
func TestCodexTurnFailureDoesNotOverrideALaterPrompt(t *testing.T) {
	dataDir := t.TempDir()
	terminalID := "00000000-0000-0000-0000-000000000062"
	control := &codexActivityControl{got: make(chan struct{}, 1)}
	failedAt := time.Now().Add(-time.Second)
	supervisor := &Supervisor{
		Control: control, DataDir: dataDir, Harness: "codex", AgentTerminalID: terminalID,
		terminals:    map[string]*terminalProcess{terminalID: {}},
		tuiStartedAt: failedAt.Add(-time.Minute),
	}
	supervisor.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	supervisor.lastTUIInputAt = failedAt.Add(500 * time.Millisecond)
	supervisor.lastTUISubmitAt = supervisor.lastTUIInputAt
	supervisor.reportTerminalTurnEnd(context.Background(), "codex", turnEnd{message: "usage limit", at: failedAt})
	if idle, _ := supervisor.tuiSourceIdle(); idle {
		t.Fatal("a failure from before the latest prompt marked the terminal idle")
	}
	if len(control.activity) != 0 {
		t.Fatalf("published %+v for a failure older than the latest prompt", control.activity)
	}

	supervisor.lastTUIInputAt = failedAt.Add(-500 * time.Millisecond)
	supervisor.lastTUISubmitAt = supervisor.lastTUIInputAt
	supervisor.reportTerminalTurnEnd(context.Background(), "codex", turnEnd{message: "usage limit", at: failedAt})
	if idle, unverified := supervisor.tuiSourceIdle(); !idle || unverified {
		t.Fatalf("terminal idle = %v (unverified %v), want verified idle after the failed turn", idle, unverified)
	}
	if len(control.activity) != 1 {
		t.Fatalf("activity = %+v, want the idle report", control.activity)
	}
}

// A record past the read limit (a large tool output) is skipped, and the turn
// end written after it in the same batch is still read.
func TestCodexRolloutTailSkipsAnOversizedRecord(t *testing.T) {
	home := t.TempDir()
	tail := newCodexRolloutTail(home, "", time.Now().Add(-time.Second))
	huge := `{"type":"response_item","payload":{"output":"` + strings.Repeat("x", 2*maxTranscriptReadSize+1) + `"}}` + "\n"
	writeRollout(t, home, "rollout-tui.jsonl", codexCLIMeta+codexTurnStarted+huge+codexTurnFailed)
	var failures []turnEnd
	for range 6 {
		failures = append(failures, tail.poll()...)
	}
	if len(failures) != 1 {
		t.Fatalf("failures = %v, want the failed turn after the oversized record", failures)
	}
}

// A rollout whose session_meta line is still being written is looked at again
// on the next scan rather than written off.
func TestCodexRolloutTailRechecksAnUnfinishedRollout(t *testing.T) {
	home := t.TempDir()
	tail := newCodexRolloutTail(home, "", time.Now().Add(-time.Second))
	path := writeRollout(t, home, "rollout-tui.jsonl", codexCLIMeta[:20])
	if failures := tail.poll(); len(failures) != 0 {
		t.Fatalf("failures = %v from an unfinished rollout", failures)
	}
	appendRollout(t, path, codexCLIMeta[20:]+codexTurnStarted+codexTurnFailed)
	if failures := tail.poll(); len(failures) != 1 {
		t.Fatalf("failures = %v, want the failed turn once the rollout is readable", failures)
	}
}
