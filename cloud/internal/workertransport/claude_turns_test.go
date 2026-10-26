package workertransport

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
)

// Records written by Claude Code 2.1.287 (fields AO does not read trimmed):
// a prompt, Esc while the reply streamed, and Esc while a Bash tool ran or
// waited at its permission prompt (the rejected tool result, then the
// marker, share a timestamp).
const (
	claudePrompt          = `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"SLOW please"},"uuid":"c777b312-76d9-4292-b9d2-b81f4db98933","timestamp":"2026-10-19T04:32:29.535Z","sessionId":"511e68be-f634-4b26-b7c7-7f4d258144c6","version":"2.1.287"}` + "\n"
	claudeAssistant       = `{"parentUuid":"c777b312-76d9-4292-b9d2-b81f4db98933","isSidechain":false,"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"tick 0 tick 1 "}]},"timestamp":"2026-10-19T04:32:34.621Z"}` + "\n"
	claudeInterrupted     = `{"parentUuid":"b5d9c73e-c819-4980-a46c-334b77a7cab1","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]},"uuid":"140d42fd-27d5-4865-9f20-9ae37b260d48","timestamp":"2026-10-19T04:32:34.624Z","interruptedMessageId":"msg_1792384349623","sessionId":"511e68be-f634-4b26-b7c7-7f4d258144c6","version":"2.1.287"}` + "\n"
	claudeToolRejected    = `{"parentUuid":"77e5d0c8-074e-4243-a7fc-5869fed37198","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"The user doesn't want to proceed with this tool use.","is_error":true,"tool_use_id":"toolu_1792384368818"}]},"timestamp":"2026-10-19T04:33:02.854Z","toolUseResult":"User rejected tool use"}` + "\n"
	claudeToolInterrupted = `{"parentUuid":"4b4c6e72-bddc-48d9-9c82-3e26e0a8ff09","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]},"uuid":"65bdc24b-8d9d-4610-849b-a8ff5cc6dc9a","timestamp":"2026-10-19T04:33:02.854Z","interruptedMessageId":"msg_1792384368818","sessionId":"511e68be-f634-4b26-b7c7-7f4d258144c6","version":"2.1.287"}` + "\n"
	claudeLastPrompt      = `{"type":"last-prompt","lastPrompt":"TOOL now","sessionId":"511e68be-f634-4b26-b7c7-7f4d258144c6"}` + "\n"
)

// claudeInterruptedAt is claudeInterrupted stamped at a given time.
func claudeInterruptedAt(at time.Time) string {
	return strings.Replace(claudeInterrupted, "2026-10-19T04:32:34.624Z", at.UTC().Format(time.RFC3339Nano), 1)
}

func writeClaudeTranscript(t *testing.T, configDir, name, content string) string {
	t.Helper()
	path := filepath.Join(configDir, "projects", "-home-ao-work", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeTurnRecord(t *testing.T) {
	for name, line := range map[string]string{"streaming": claudeInterrupted, "tool": claudeToolInterrupted} {
		end, kind := claudeTurnRecord([]byte(line))
		if kind != turnRecordEnd || !end.interrupted {
			t.Fatalf("%s interrupt = %+v/%v, want an interrupted turn end", name, end, kind)
		}
	}
	end, _ := claudeTurnRecord([]byte(claudeInterrupted))
	if want := time.Date(2026, 10, 19, 4, 32, 34, 624_000_000, time.UTC); !end.at.Equal(want) {
		t.Fatalf("interrupt at %v, want its record's timestamp %v", end.at, want)
	}
	for name, line := range map[string]string{
		"prompt":      claudePrompt,
		"tool result": claudeToolRejected,
		// Claude ends a turn to deliver a queued message; the message follows.
		"queued delivery":           `{"isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Tool call did not complete: the turn was ended to deliver the message that follows. Nothing refused it; re-run it if still needed.]"}]},"timestamp":"2026-10-19T04:33:02.854Z"}`,
		"marker quoted in a prompt": strings.Replace(claudePrompt, `"SLOW please"`, `"why did I get [Request interrupted by user]?"`, 1),
		// A typed prompt is recorded as a string, however it reads.
		"prompt that is the marker":          strings.Replace(claudePrompt, `"SLOW please"`, `"[Request interrupted by user]"`, 1),
		"prompt that is the tool use marker": strings.Replace(claudePrompt, `"SLOW please"`, `"[Request interrupted by user for tool use]"`, 1),
		"marker beside another block": strings.Replace(claudeInterrupted, `"text":"[Request interrupted by user]"}]`,
			`"text":"[Request interrupted by user]"},{"type":"text","text":"more"}]`, 1),
	} {
		if _, kind := claudeTurnRecord([]byte(line)); kind != turnRecordStart {
			t.Fatalf("%s classified %v, want a turn start", name, kind)
		}
	}
	for name, line := range map[string]string{
		"assistant":   claudeAssistant,
		"bookkeeping": claudeLastPrompt,
		// A subagent's interrupt is not the conversation's turn end.
		"sidechain": strings.Replace(claudeInterrupted, `"isSidechain":false`, `"isSidechain":true`, 1),
		"not json":  `not json "user"`,
	} {
		if _, kind := claudeTurnRecord([]byte(line)); kind != turnRecordOther {
			t.Fatalf("%s classified %v, want ignored", name, kind)
		}
	}
}

func TestClaudeTranscriptTailReportsOnlyNewInterrupts(t *testing.T) {
	configDir := t.TempDir()
	// An interrupt from before the terminal started is history.
	path := writeClaudeTranscript(t, configDir, "511e68be-f634-4b26-b7c7-7f4d258144c6.jsonl", claudePrompt+claudeAssistant+claudeInterrupted)
	tail := newClaudeTranscriptTail(configDir, "511e68be-f634-4b26-b7c7-7f4d258144c6", "", time.Now().Add(-time.Second))
	// The terminal resumed that conversation and started a new turn.
	appendRollout(t, path, claudePrompt)
	if ends := tail.poll(); len(ends) != 0 {
		t.Fatalf("history reported mid-turn: %v", ends)
	}
	appendRollout(t, path, claudeAssistant+claudeToolRejected+claudeToolInterrupted+claudeLastPrompt)
	ends := tail.poll()
	if len(ends) != 1 || !ends[0].interrupted {
		t.Fatalf("ends = %+v, want the interrupt", ends)
	}
	// Esc then an immediate new prompt in one read: the new turn is running.
	appendRollout(t, path, claudeInterrupted+claudePrompt)
	if ends := tail.poll(); len(ends) != 0 {
		t.Fatalf("ends = %+v, want none: a later prompt superseded the interrupt", ends)
	}
}

// Subagent transcripts (nested, or agent-*.jsonl in older releases) are never
// the terminal's own turn.
func TestClaudeTranscriptTailIgnoresSubagentTranscripts(t *testing.T) {
	configDir := t.TempDir()
	tail := newClaudeTranscriptTail(configDir, "", "", time.Now().Add(-time.Second))
	main := writeClaudeTranscript(t, configDir, "s1.jsonl", claudePrompt)
	if ends := tail.poll(); len(ends) != 0 {
		t.Fatalf("ends = %v", ends)
	}
	time.Sleep(20 * time.Millisecond)
	writeClaudeTranscript(t, configDir, filepath.Join("s1", "subagents", "agent-a1.jsonl"), claudePrompt+claudeInterrupted)
	writeClaudeTranscript(t, configDir, "agent-a2.jsonl", claudePrompt+claudeInterrupted)
	tail.scans = 0
	if ends := tail.poll(); len(ends) != 0 {
		t.Fatalf("a subagent transcript was reported: %v", ends)
	}
	appendRollout(t, main, claudeInterrupted)
	tail.scans = 0
	if ends := tail.poll(); len(ends) != 1 {
		t.Fatalf("ends = %v, want the conversation's interrupt", ends)
	}
}

// A headless claude the agent runs from a tool writes its own transcript to
// the same config directory, often the newest one. With the terminal's
// conversation id known, only its transcript is followed.
func TestClaudeTranscriptTailFollowsTheTerminalConversation(t *testing.T) {
	configDir := t.TempDir()
	const id = "511e68be-f634-4b26-b7c7-7f4d258144c6"
	tail := newClaudeTranscriptTail(configDir, id, "", time.Now().Add(-time.Second))
	main := writeClaudeTranscript(t, configDir, id+".jsonl", claudePrompt)
	if ends := tail.poll(); len(ends) != 0 {
		t.Fatalf("ends = %v", ends)
	}
	time.Sleep(20 * time.Millisecond)
	writeClaudeTranscript(t, configDir, "0f1e2d3c-4b5a-4968-8776-655443322110.jsonl", claudePrompt+claudeInterrupted)
	tail.scans = 0
	if ends := tail.poll(); len(ends) != 0 {
		t.Fatalf("another conversation's interrupt was reported: %v", ends)
	}
	appendRollout(t, main, claudeInterrupted)
	tail.scans = 0
	if ends := tail.poll(); len(ends) != 1 || !ends[0].interrupted {
		t.Fatalf("ends = %v, want the terminal conversation's interrupt", ends)
	}
}

// /clear, /resume or a conversation switch inside the TUI moves the
// terminal's Claude to another transcript. Its hooks record the new
// conversation for its process (CLAUDE_PID), and the tail follows it; the
// conversation left behind, and a headless claude's, no longer count.
func TestClaudeTranscriptTailFollowsAConversationSwitch(t *testing.T) {
	configDir, dataDir := t.TempDir(), t.TempDir()
	const launch, cleared, headless = "511e68be-f634-4b26-b7c7-7f4d258144c6", "7d0c1b2a-3948-4576-8a9b-0c1d2e3f4a5b", "0f1e2d3c-4b5a-4968-8776-655443322110"
	start := time.Now().Add(-time.Minute)
	tail := newClaudeTranscriptTail(configDir, launch, dataDir, start.Add(-time.Second))
	// Live processes, as hook helpers record only running Claude processes'
	// conversations and prune the others.
	terminalPID, headlessPID := strconv.Itoa(os.Getpid()), strconv.Itoa(os.Getppid())
	record := func(pid, session string, at time.Time) {
		t.Helper()
		if err := worker.RecordClaudeConversation(dataDir, pid, session, at); err != nil {
			t.Fatal(err)
		}
	}
	poll := func() []turnEnd {
		tail.scans = 0
		return tail.poll()
	}
	record(terminalPID, launch, start)
	main := writeClaudeTranscript(t, configDir, launch+".jsonl", claudePrompt)
	if ends := poll(); len(ends) != 0 {
		t.Fatalf("ends = %v", ends)
	}

	// A headless claude the agent runs is its own process and conversation.
	record(headlessPID, headless, start.Add(time.Second))
	writeClaudeTranscript(t, configDir, headless+".jsonl", claudePrompt+claudeInterruptedAt(start.Add(2*time.Second)))
	if ends := poll(); len(ends) != 0 {
		t.Fatalf("a headless claude's interrupt was reported: %v", ends)
	}

	// /clear, then a prompt in the new conversation.
	clearedAt := start.Add(10 * time.Second)
	record(terminalPID, cleared, clearedAt)
	next := writeClaudeTranscript(t, configDir, cleared+".jsonl", claudePrompt)
	if ends := poll(); len(ends) != 0 {
		t.Fatalf("ends = %v", ends)
	}
	// The conversation left behind no longer counts.
	appendRollout(t, main, claudeInterruptedAt(start.Add(11*time.Second)))
	if ends := poll(); len(ends) != 0 {
		t.Fatalf("the left conversation's interrupt was reported: %v", ends)
	}
	appendRollout(t, next, claudeInterruptedAt(start.Add(12*time.Second)))
	if ends := poll(); len(ends) != 1 || !ends[0].interrupted {
		t.Fatalf("ends = %v, want the new conversation's interrupt", ends)
	}

	// /resume back to the launch conversation: what it holds from before the
	// switch is history, an interrupt after it is the turn's end.
	resumedAt := start.Add(20 * time.Second)
	record(terminalPID, launch, resumedAt)
	if ends := poll(); len(ends) != 0 {
		t.Fatalf("history of the resumed conversation was reported: %v", ends)
	}
	appendRollout(t, main, claudePrompt+claudeInterruptedAt(start.Add(21*time.Second)))
	if ends := poll(); len(ends) != 1 || !ends[0].at.Equal(start.Add(21*time.Second).Truncate(0)) {
		t.Fatalf("ends = %v, want the resumed conversation's new interrupt", ends)
	}
}

// The tail keeps the conversation records bounded and never drops the
// terminal's.
func TestClaudeTranscriptTailPrunesConversationRecords(t *testing.T) {
	configDir, dataDir := t.TempDir(), t.TempDir()
	const launch = "511e68be-f634-4b26-b7c7-7f4d258144c6"
	tail := newClaudeTranscriptTail(configDir, launch, dataDir, time.Now().Add(-time.Second))
	dir := filepath.Join(dataDir, "claude-conversations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Records of exited processes (pids above any pid_max), the terminal's
	// among them as the oldest.
	for i := 0; i <= worker.MaxClaudeConversations+5; i++ {
		first := fmt.Sprintf("headless-%d", i)
		if i == 0 {
			first = launch
		}
		record := fmt.Sprintf(`{"first":%q,"current":%q,"since":%d,"started":%d}`, first, first, i+1, i+1)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", 90_000_000+i)), []byte(record), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tail.poll()
	records := worker.ReadClaudeConversations(dataDir)
	if _, ok := records["90000000"]; !ok || len(records) != 1 {
		t.Fatalf("records = %d (terminal's kept %v), want only the terminal's", len(records), ok)
	}
}

func TestClaudeTerminalConversationID(t *testing.T) {
	for name, tc := range map[string]struct {
		command workerexec.Command
		want    string
	}{
		"resumed":          {workerexec.Command{ConversationID: "c1", Args: []string{"--resume", "c1"}}, "c1"},
		"new":              {workerexec.Command{Args: []string{"--session-id", "n1", "--model", "opus"}}, "n1"},
		"new, joined flag": {workerexec.Command{Args: []string{"--session-id=n2"}}, "n2"},
		// The prompt after -- is not a flag.
		"flag text in the prompt": {workerexec.Command{Args: []string{"--model", "opus", "--", "--session-id", "x"}}, ""},
		"unknown":                 {workerexec.Command{Args: []string{"-c", "cat"}}, ""},
	} {
		if got := claudeTerminalConversationID(tc.command); got != tc.want {
			t.Errorf("%s: claudeTerminalConversationID = %q, want %q", name, got, tc.want)
		}
	}
}

// Esc in a cloud Claude terminal fires no hook. The worker must report the
// agent idle from the transcript and write the terminal stop proof, or queued
// messages wait out the control plane's five minute staleness valve.
func TestClaudeTerminalInterruptMarksAgentIdle(t *testing.T) {
	previous := terminalTurnPollInterval
	terminalTurnPollInterval = 20 * time.Millisecond
	defer func() { terminalTurnPollInterval = previous }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	configDir, dataDir := t.TempDir(), t.TempDir()
	terminalID := "00000000-0000-0000-0000-000000000071"
	control := &codexActivityControl{got: make(chan struct{}, 1)}
	supervisor := &Supervisor{
		Control: control, Workspace: t.TempDir(), DataDir: dataDir, Harness: "claude-code",
		AgentTerminalID: terminalID,
		AgentCommand: workerexec.Command{
			Path: "/bin/sh", Args: []string{"-c", "cat"},
			Env: map[string]string{"CLAUDE_CONFIG_DIR": configDir},
		},
		terminals: make(map[string]*terminalProcess),
	}
	supervisor.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	defer supervisor.closeAllTerminals()
	if err := supervisor.openTerminal(ctx, worker.TerminalCommand{TerminalID: terminalID, Kind: "agent"}); err != nil {
		t.Fatal(err)
	}
	supervisor.mu.Lock()
	supervisor.noteAgentTerminalInput([]byte("fix CI\r"))
	supervisor.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	path := writeClaudeTranscript(t, configDir, "e92256a1-81f1-58ff-9b18-277dc6f2026b.jsonl", claudePrompt+claudeAssistant)
	// Esc, then a second Esc after the turn ended: neither starts a turn.
	supervisor.mu.Lock()
	supervisor.noteAgentTerminalInput([]byte{0x1b})
	supervisor.mu.Unlock()
	appendRollout(t, path, claudeInterruptedAt(time.Now()))
	time.Sleep(5 * time.Millisecond)
	supervisor.mu.Lock()
	supervisor.noteAgentTerminalInput([]byte{0x1b})
	supervisor.mu.Unlock()
	select {
	case <-control.got:
	case <-time.After(5 * time.Second):
		t.Fatal("the interrupted Claude turn was not reported")
	}
	control.mu.Lock()
	event := control.activity[0]
	control.mu.Unlock()
	if event.Harness != "claude-code" || event.Event != "interrupt" ||
		event.State != contract.ActivityIdle || event.SourceInterface != "tui" {
		t.Fatalf("activity = %+v, want claude-code interrupt idle from the terminal", event)
	}
	if !worker.ValidActivityEvent(event) {
		t.Fatalf("the control plane would reject %+v", event)
	}
	if _, err := worker.ReadTUIStop(dataDir); err != nil {
		t.Fatalf("no terminal stop proof: %v", err)
	}
}

// A prompt submitted after the interrupt, before the poll saw it, started a
// turn the idle must not end. Typing without Enter does not.
func TestTerminalInterruptDoesNotOverrideALaterSubmit(t *testing.T) {
	terminalID := "00000000-0000-0000-0000-000000000072"
	control := &codexActivityControl{got: make(chan struct{}, 4)}
	interruptedAt := time.Now().Add(-time.Second).Truncate(time.Millisecond)
	supervisor := &Supervisor{
		Control: control, DataDir: t.TempDir(), Harness: "claude-code", AgentTerminalID: terminalID,
		terminals:    map[string]*terminalProcess{terminalID: {}},
		tuiStartedAt: interruptedAt.Add(-time.Minute),
	}
	supervisor.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	end := turnEnd{at: interruptedAt, interrupted: true}

	supervisor.lastTUIInputAt, supervisor.lastTUISubmitAt = interruptedAt.Add(300*time.Millisecond), interruptedAt.Add(-time.Second)
	supervisor.reportTerminalTurnEnd(context.Background(), "claude-code", end)
	if len(control.activity) != 1 {
		t.Fatalf("activity = %+v, want idle despite typing after the interrupt", control.activity)
	}

	// The Enter that rejected a permission prompt lands in the marker's
	// millisecond, just before it.
	supervisor.lastTUISubmitAt = interruptedAt.Add(500 * time.Microsecond)
	supervisor.reportTerminalTurnEnd(context.Background(), "claude-code", end)
	if len(control.activity) != 2 {
		t.Fatalf("activity = %+v, want idle for input inside the marker's millisecond", control.activity)
	}

	supervisor.lastTUISubmitAt = interruptedAt.Add(400 * time.Millisecond)
	supervisor.reportTerminalTurnEnd(context.Background(), "claude-code", end)
	if len(control.activity) != 2 {
		t.Fatalf("published %+v for an interrupt older than the latest submitted prompt", control.activity)
	}
}

func TestMayStartTurn(t *testing.T) {
	for input, want := range map[string]bool{
		"\x1b": false, "\x1b\x1b": false, "hello": false, "\x1b[A": false,
		"\r": true, "hi\n": true, "\x1b[200~line1\nline2\x1b[201~": true,
		// Keypad Enter in application keypad mode.
		"\x1bOM": true, "\x1bOA": false,
		// The kitty keyboard protocol's Enter: plain, with modifiers, with an
		// event type or alternate key codes.
		"\x1b[13u": true, "\x1b[13;2u": true, "\x1b[13;1:1u": true, "\x1b[13:13u": true, "go\x1b[13u": true,
		// Not Enter: a cursor position report on row 13, F3, a kitty key
		// whose code starts with 13 and kitty Esc.
		"\x1b[13;1R": false, "\x1b[13;80R": false, "\x1b[13~": false, "\x1b[13;5~": false,
		"\x1b[130u": false, "\x1b[27u": false, "\x1b[13": false,
		// Terminal replies: kitty flags, a color report.
		"\x1b[?13u": false, "\x1b]11;rgb:1313/1313/1313\x07": false, "\x1b]11;rgb:0d0d/0d0d/0d0d\x1b\\": false,
		// Alt+Enter still carries CR.
		"\x1b\r": true,
	} {
		if got := mayStartTurn([]byte(input)); got != want {
			t.Fatalf("mayStartTurn(%q) = %v, want %v", input, got, want)
		}
	}
}

// A Chat switch drains a terminal only on a stop proof newer than its latest
// input. Esc while Claude runs a tool (the turn's tool_use may get a rejected
// result) must leave that proof, so the switch is not held by the turn Esc
// ended; a tool use still running must not.
func TestClaudeTerminalToolInterruptLetsAChatSwitchDrain(t *testing.T) {
	previous := terminalTurnPollInterval
	terminalTurnPollInterval = 20 * time.Millisecond
	defer func() { terminalTurnPollInterval = previous }()
	const toolUse = `{"parentUuid":"c777b312-76d9-4292-b9d2-b81f4db98933","isSidechain":false,"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1792384368818","name":"Bash","input":{"command":"sleep 60"}}],"stop_reason":"tool_use"},"uuid":"4b4c6e72-bddc-48d9-9c82-3e26e0a8ff09","timestamp":"2026-10-19T04:33:01.000Z"}` + "\n"
	for _, test := range []struct {
		name      string
		escape    bool
		after     string
		wantDrain bool
	}{
		{name: "interrupted with a rejected tool result", escape: true, after: claudeToolRejected + claudeToolInterrupted, wantDrain: true},
		{name: "interrupted with no tool result", escape: true, after: claudeToolInterrupted, wantDrain: true},
		{name: "tool still running", after: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			configDir, dataDir := t.TempDir(), t.TempDir()
			terminalID := "00000000-0000-0000-0000-000000000073"
			control := &codexActivityControl{got: make(chan struct{}, 1)}
			supervisor := &Supervisor{
				Control: control, Workspace: t.TempDir(), DataDir: dataDir, Harness: "claude-code",
				AgentTerminalID: terminalID,
				AgentCommand: workerexec.Command{
					Path: "/bin/sh", Args: []string{"-c", "cat"},
					Env: map[string]string{"CLAUDE_CONFIG_DIR": configDir},
				},
				terminals: make(map[string]*terminalProcess),
			}
			supervisor.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
			defer supervisor.closeAllTerminals()
			if err := supervisor.openTerminal(ctx, worker.TerminalCommand{TerminalID: terminalID, Kind: "agent"}); err != nil {
				t.Fatal(err)
			}
			supervisor.iface.current = InterfaceTUI
			supervisor.mu.Lock()
			supervisor.noteAgentTerminalInput([]byte("run it\r"))
			supervisor.mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			path := writeClaudeTranscript(t, configDir, "e92256a1-81f1-58ff-9b18-277dc6f2026b.jsonl", claudePrompt+toolUse)
			if test.escape {
				supervisor.mu.Lock()
				supervisor.noteAgentTerminalInput([]byte{0x1b})
				supervisor.mu.Unlock()
				now := time.Now().UTC().Format(time.RFC3339Nano)
				appendRollout(t, path, strings.ReplaceAll(test.after, "2026-10-19T04:33:02.854Z", now))
				// A second Esc after the turn ended does not start one.
				time.Sleep(5 * time.Millisecond)
				supervisor.mu.Lock()
				supervisor.noteAgentTerminalInput([]byte{0x1b})
				supervisor.mu.Unlock()
				select {
				case <-control.got:
				case <-time.After(5 * time.Second):
					t.Fatal("the interrupted tool use was not reported")
				}
			} else {
				time.Sleep(10 * terminalTurnPollInterval)
			}
			result, err := supervisor.inspectInterface()
			if err != nil {
				t.Fatal(err)
			}
			inspection := result.(interfaceInspectResult)
			if inspection.Idle != test.wantDrain || inspection.QuiescenceUnverified == test.wantDrain {
				t.Fatalf("inspection = %+v, want drainable %v", inspection, test.wantDrain)
			}
		})
	}
}
