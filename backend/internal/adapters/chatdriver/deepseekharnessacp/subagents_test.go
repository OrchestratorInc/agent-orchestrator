package deepseekharnessacp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// The records below follow DeepSeek Harness's session store as of
// @deepseek-ai/dsh 0.2.3-alpha.2 (commit 4b399c39): the event payloads in
// packages/core/session/src/types.ts, the catalog in
// packages/subagent/subagent/src/catalog.ts, the settlement notice in
// continuation-messages.ts, and the launch tool in tool-subagent/src/index.ts.

const (
	parentSession = "11111111-1111-4111-8111-111111111111"
	childSession  = "22222222-2222-4222-8222-222222222222"
	otherChild    = "33333333-3333-4333-8333-333333333333"
)

type dshLog struct {
	t    *testing.T
	path string
	seq  int
}

func newDSHLog(t *testing.T, home, id string, header map[string]any) *dshLog {
	t.Helper()
	dir := filepath.Join(home, "sessions", "--work--", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	h := map[string]any{"type": "session", "version": 4, "id": id, "cwd": "/work", "createdAt": 1, "isSeeded": false, "delegationDepth": 0}
	for key, value := range header {
		h[key] = value
	}
	log := &dshLog{t: t, path: filepath.Join(dir, "session.v4.jsonl")}
	log.write(h)
	return log
}

func (l *dshLog) write(line map[string]any) {
	l.t.Helper()
	encoded, err := json.Marshal(line)
	if err != nil {
		l.t.Fatal(err)
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		l.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(encoded, '\n')); err != nil {
		l.t.Fatal(err)
	}
}

func (l *dshLog) add(typ string, data map[string]any) {
	l.t.Helper()
	l.write(map[string]any{"type": typ, "seq": l.seq, "time": 1000 + l.seq, "data": data})
	l.seq++
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

// launch writes the parent's side of a launch: the call, the catalog entry the
// activation appends, and the tool's result.
func (l *dshLog) launch(callID, tool, name, child string) {
	l.add("tool/call", map[string]any{"turn": 1, "step": 1, "callId": callID, "name": tool,
		"arguments": `{"description":"Audit the cart","prompt":"Read-only. Audit the cart."}`})
	l.add("subagent/catalog", map[string]any{"version": 3, "childId": child, "childCreatedAt": 5,
		"mode": "continuable", "label": "Audit the cart", "name": name})
	l.add("tool/result", map[string]any{"turn": 1, "step": 1, "message": map[string]any{
		"id": "result-" + callID, "role": "tool", "toolCallId": callID, "content": []any{text("started subagent " + name)}}})
}

func (l *dshLog) settled(child, name, stopReason string, content ...string) {
	blocks := make([]any, 0, len(content))
	for _, c := range content {
		blocks = append(blocks, text(c))
	}
	l.add("user/message", map[string]any{"id": fmt.Sprintf("settled-%d", l.seq), "role": "user", "content": blocks,
		"source": map[string]any{"kind": "subagent-settled", "form": "notice", "summary": content[0],
			"stopReason": stopReason, "senderSessionId": child}})
}

func childHeader() map[string]any {
	return map[string]any{"parentSession": parentSession, "origin": "subagent", "delegationDepth": 1}
}

// runStart and runWork write one ordinary run of a child: its task, what it
// said, a command, and its closing message.
func (l *dshLog) runStart(turn int) {
	l.add("turn/start", map[string]any{"turn": turn})
	l.add("user/message", map[string]any{"id": fmt.Sprintf("task-%d", turn), "role": "user",
		"content": []any{text("Read-only. Audit the cart.")}, "source": map[string]any{"kind": "user"}})
}

func (l *dshLog) runWork(turn int, closing string) {
	call := fmt.Sprintf("call-%d", turn)
	l.add("assistant/message", map[string]any{"turn": turn, "step": 1, "stream": []any{}, "message": map[string]any{
		"id": fmt.Sprintf("said-%d", turn), "role": "assistant", "content": []any{
			map[string]any{"type": "reasoning", "text": "Where is the cart?"},
			text("Looking at the cart."),
			map[string]any{"type": "tool-call", "id": call, "name": "bash", "arguments": `{"command":"ls src"}`},
		}}})
	l.add("tool/call", map[string]any{"turn": turn, "step": 1, "callId": call, "name": "bash", "arguments": `{"command":"ls src"}`})
	l.add("tool/result", map[string]any{"turn": turn, "step": 1, "message": map[string]any{
		"id": "result-" + call, "role": "tool", "toolCallId": call, "content": []any{text("cart.ts")}}})
	l.add("assistant/message", map[string]any{"turn": turn, "step": 2, "stream": []any{}, "message": map[string]any{
		"id": fmt.Sprintf("closing-%d", turn), "role": "assistant", "content": []any{text(closing)}}})
}

func (l *dshLog) runEnd(turn int, reason string) {
	l.add("turn/end", map[string]any{"turn": turn, "reason": map[string]any{"kind": reason}})
}

func openReader(home string) *recordReader {
	return subagentRecords().Open(map[string]string{"DSH_HOME": home}, "/work", parentSession).(*recordReader)
}

// describe renders updates compactly: "launch/child tool_call bash", etc.
func describe(updates []acpdriver.RecordedUpdate) []string {
	out := make([]string, 0, len(updates))
	for _, u := range updates {
		prefix := u.Launch + " "
		switch {
		case u.End != nil:
			out = append(out, prefix+fmt.Sprintf("end %s %q", u.End.Status, u.End.Report))
		case u.Update.ToolCall != nil:
			out = append(out, prefix+fmt.Sprintf("tool_call %s %v", u.Update.ToolCall.Title, u.Update.ToolCall.RawInput))
		case u.Update.ToolCallUpdate != nil:
			out = append(out, prefix+fmt.Sprintf("tool_update %s %s", u.Update.ToolCallUpdate.ToolCallId, *u.Update.ToolCallUpdate.Status))
		case u.Update.AgentMessageChunk != nil:
			out = append(out, prefix+fmt.Sprintf("said %s %q", *u.Update.AgentMessageChunk.MessageId, u.Update.AgentMessageChunk.Content.Text.Text))
		}
	}
	return out
}

func read(t *testing.T, r *recordReader, launched ...string) []string {
	t.Helper()
	set := map[string]bool{}
	for _, id := range launched {
		set[id] = true
	}
	updates, err := r.Read(context.Background(), set)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return describe(updates)
}

func expectLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestSubagentLaunchIsTheLaunchToolWithATask(t *testing.T) {
	input := map[string]any{"description": "Audit the cart", "prompt": "Read-only. Audit the cart.", "name": "cart"}
	launch, ok := subagentLaunch(acpdriver.RecordedCall{Title: "subagent", RawInput: input})
	if !ok || launch != (acpdriver.RecordedLaunch{ToolName: "subagent", Title: "Audit the cart", Prompt: "Read-only. Audit the cart."}) {
		t.Fatalf("subagent = %+v, %v", launch, ok)
	}
	fork, ok := subagentLaunch(acpdriver.RecordedCall{Title: "subagent_fork", RawInput: map[string]any{"prompt": "Go on.", "name": "cart"}})
	if !ok || fork.AgentType != "fork" || fork.Title != "cart" {
		t.Fatalf("subagent_fork = %+v, %v; want the fork type and the name as its title", fork, ok)
	}
	preset, _ := subagentLaunch(acpdriver.RecordedCall{Title: "subagent", RawInput: map[string]any{"prompt": "Go.", "agent_preset": "reviewer"}})
	if preset.AgentType != "reviewer" {
		t.Fatalf("agent_preset = %+v", preset)
	}
	for _, call := range []acpdriver.RecordedCall{
		{Title: "bash", RawInput: input},
		{Title: "subagent", RawInput: map[string]any{"description": "no task"}},
		{Title: "subagent", RawInput: "not an object"},
	} {
		if _, ok := subagentLaunch(call); ok {
			t.Fatalf("%+v must not be a launch", call)
		}
	}
}

func TestReaderFilesALiveChildsWorkAndReportUnderItsLaunch(t *testing.T) {
	home := t.TempDir()
	r := openReader(home)
	if got := read(t, r, "call-launch"); len(got) != 0 {
		t.Fatalf("before the parent log exists: %v", got)
	}
	parent := newDSHLog(t, home, parentSession, nil)
	parent.launch("call-launch", "subagent", "agent-1", childSession)
	// The child's session has not written its log yet.
	if got := read(t, r, "call-launch"); len(got) != 0 {
		t.Fatalf("before the child log exists: %v", got)
	}
	child := newDSHLog(t, home, childSession, childHeader())
	child.runStart(1)
	child.runWork(1, "The cart is fine.")
	expectLines(t, read(t, r, "call-launch"),
		`call-launch said said-1 "Looking at the cart."`,
		`call-launch tool_call bash map[command:ls src]`,
		`call-launch tool_update call-1 completed`,
		`call-launch said closing-1 "The cart is fine."`,
	)
	if !r.Busy() {
		t.Fatal("a child mid-run is busy")
	}
	// The parent records the end before the child's last batch lands: the end
	// waits for the child's log to close the run.
	parent.settled(childSession, "agent-1", "completed",
		"Background subagent agent-1 finished and will do no further work unless you send it more.",
		"Its closing message:", "The cart is fine.")
	if got := read(t, r, "call-launch"); len(got) != 0 {
		t.Fatalf("the end must wait for the run to close, got %v", got)
	}
	child.runEnd(1, "completed")
	expectLines(t, read(t, r, "call-launch"), `call-launch end completed "The cart is fine."`)
	if r.Busy() {
		t.Fatal("a child whose run ended is not busy")
	}

	// A follow-up message starts a second run under the same launch.
	child.runStart(2)
	child.runWork(2, "Still fine.")
	child.runEnd(2, "completed")
	parent.settled(childSession, "agent-1", "completed", "Background subagent agent-1 finished.", "Its closing message:", "Still fine.")
	got := read(t, r, "call-launch")
	if last := got[len(got)-1]; last != `call-launch end completed "Still fine."` {
		t.Fatalf("second run: %v", got)
	}
}

func TestReaderEndsARunWhoseCloseNeverLands(t *testing.T) {
	home := t.TempDir()
	now := time.Unix(100, 0)
	r := openReader(home)
	r.now = func() time.Time { return now }
	parent := newDSHLog(t, home, parentSession, nil)
	parent.launch("call-launch", "subagent", "agent-1", childSession)
	child := newDSHLog(t, home, childSession, childHeader())
	child.runStart(1)
	read(t, r, "call-launch")
	parent.settled(childSession, "agent-1", "error", "Background subagent agent-1 failed before it finished.", "It left no closing message.", "rate limited")
	if got := read(t, r, "call-launch"); len(got) != 0 {
		t.Fatalf("too soon: %v", got)
	}
	now = now.Add(endWaitFor)
	expectLines(t, read(t, r, "call-launch"),
		`call-launch end failed "Background subagent agent-1 failed before it finished.\n\nIt left no closing message.\n\nrate limited"`)
	if r.Busy() {
		t.Fatal("an ended run is not busy")
	}
}

func TestSettledRunSaysHowTheRunEnded(t *testing.T) {
	for _, test := range []struct {
		stop   string
		texts  []string
		status acpdriver.RecordedEndStatus
		report string
	}{
		{"completed", []string{"finished.", "Its closing message:", "Done.", "More."}, acpdriver.RecordedCompleted, "Done.\n\nMore."},
		{"completed", []string{"finished.", "It left no closing message."}, acpdriver.RecordedCompleted, ""},
		{"completed", []string{"finished.", "It sent you a message just before finishing, so its closing message is omitted."}, acpdriver.RecordedCompleted, ""},
		{"max-tokens", []string{"ran out of room.", "Its closing message:", "Partial."}, acpdriver.RecordedCompleted, "Partial."},
		{"aborted", []string{"was stopped before it finished.", "It left no closing message."}, acpdriver.RecordedCancelled, "was stopped before it finished.\n\nIt left no closing message."},
		{"refusal", []string{"declined the task."}, acpdriver.RecordedFailed, "declined the task."},
		{"something-new", []string{"ended abnormally."}, acpdriver.RecordedFailed, "ended abnormally."},
	} {
		status, report := settledRun(test.stop, test.texts)
		if status != test.status || report != test.report {
			t.Errorf("%s %v = %s %q; want %s %q", test.stop, test.texts, status, report, test.status, test.report)
		}
	}
}

func TestReaderSkipsAForkedChildsInheritedHistory(t *testing.T) {
	home := t.TempDir()
	parent := newDSHLog(t, home, parentSession, nil)
	parent.launch("call-fork", "subagent_fork", "agent-1", childSession)
	child := newDSHLog(t, home, childSession, map[string]any{"parentSession": parentSession, "origin": "subagent", "delegationDepth": 1, "isSeeded": true})
	// The parent's conversation, copied into the child.
	child.runStart(1)
	child.runWork(1, "The parent said this.")
	child.runEnd(1, "forked")
	child.add("session/end-seed", map[string]any{"inherited": true})
	child.runStart(2)
	child.add("assistant/message", map[string]any{"turn": 2, "step": 1, "stream": []any{}, "message": map[string]any{
		"id": "own", "role": "assistant", "content": []any{text("My own work.")}}})
	expectLines(t, read(t, openReader(home), "call-fork"), `call-fork said own "My own work."`)
}

func TestReaderOnReattachFilesOnlyRunsStillGoing(t *testing.T) {
	home := t.TempDir()
	parent := newDSHLog(t, home, parentSession, nil)
	// A child that finished before this connection: an earlier one filed it.
	parent.launch("call-old", "subagent", "agent-1", childSession)
	done := newDSHLog(t, home, childSession, childHeader())
	done.runStart(1)
	done.runWork(1, "Old report.")
	done.runEnd(1, "completed")
	parent.settled(childSession, "agent-1", "completed", "finished.", "Its closing message:", "Old report.")
	// A child still working when this connection attached.
	parent.launch("call-running", "subagent", "agent-2", otherChild)
	running := newDSHLog(t, home, otherChild, childHeader())
	running.runStart(1)
	running.add("tool/call", map[string]any{"turn": 1, "step": 1, "callId": "grep", "name": "bash", "arguments": `{"command":"grep -r cart"}`})

	r := openReader(home)
	expectLines(t, read(t, r), `call-running tool_call bash map[command:grep -r cart]`)
	if !r.Busy() {
		t.Fatal("the running child is busy")
	}
	running.add("tool/result", map[string]any{"turn": 1, "step": 1, "message": map[string]any{
		"id": "r", "role": "tool", "toolCallId": "grep", "content": []any{text("src/cart.ts")}}})
	running.runEnd(1, "completed")
	parent.settled(otherChild, "agent-2", "completed", "finished.", "Its closing message:", "Found it.")
	expectLines(t, read(t, r),
		`call-running tool_update grep completed`,
		`call-running end completed "Found it."`)

	// The finished child takes more work: that run is new, and is filed.
	done.runStart(2)
	done.runWork(2, "New report.")
	done.runEnd(2, "completed")
	parent.settled(childSession, "agent-1", "completed", "finished.", "Its closing message:", "New report.")
	got := read(t, r)
	if len(got) != 5 || got[0] != `call-old said said-2 "Looking at the cart."` || got[4] != `call-old end completed "New report."` {
		t.Fatalf("the old child's new run: %v", got)
	}
}

func TestReaderLeavesAChildWithoutAnUnreadableLog(t *testing.T) {
	home := t.TempDir()
	parent := newDSHLog(t, home, parentSession, nil)
	parent.launch("call-launch", "subagent", "agent-1", childSession)
	broken := newDSHLog(t, home, childSession, map[string]any{"version": 9})
	_ = broken
	r := openReader(home)
	if got := read(t, r, "call-launch"); len(got) != 0 {
		t.Fatalf("an unreadable child log files nothing, got %v", got)
	}
	if r.Busy() {
		t.Fatal("a child that cannot be followed is not busy")
	}
}
