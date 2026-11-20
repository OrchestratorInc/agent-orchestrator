package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/backgroundtasks"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestBackgroundTasksReadsNativeCodexInventory(t *testing.T) {
	for _, tc := range []struct {
		name, response                string
		count                         int
		unsupported, wantErr, partial bool
	}{
		{name: "empty with unobserved agents", response: `{"data":[],"nextCursor":null}`, wantErr: true, partial: true},
		{name: "shell with unobserved agents", response: `{"data":[{"processId":"63962","itemId":"cmd-1","command":"sleep 1800","osPid":20205}],"nextCursor":null}`, count: 1, wantErr: true, partial: true},
		{name: "missing data", response: `{}`, wantErr: true},
		{name: "missing identity", response: `{"data":[{"command":"sleep"}]}`, wantErr: true},
		{name: "duplicate identity", response: `{"data":[{"processId":"1"},{"processId":"1"}]}`, wantErr: true},
		{name: "repeated cursor", response: `{"data":[],"nextCursor":"again"}`, wantErr: true},
		{name: "unsupported", unsupported: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, srv := newTestDriver(t)
			if tc.unsupported {
				srv.replyError("thread/backgroundTerminals/list", -32601, "method not found")
			} else {
				srv.reply("thread/backgroundTerminals/list", tc.response)
			}
			conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conv.Close() }()
			tasks, err := conv.(ports.ChatBackgroundTaskInspector).BackgroundTasks(context.Background())
			if (err != nil) != tc.wantErr || errors.Is(err, ports.ErrBackgroundTasksUnsupported) != tc.unsupported || errors.Is(err, ports.ErrBackgroundTasksPartial) != tc.partial || len(tasks) != tc.count {
				t.Fatalf("tasks=%+v err=%v", tasks, err)
			}
			if tc.count > 0 && (tasks[0].ID != "63962" || tasks[0].Kind != "shell" || tasks[0].Description != "sleep 1800" || !tasks[0].Stoppable) {
				t.Fatalf("task=%+v", tasks[0])
			}
		})
	}
}

func TestBackgroundTasksCodexSubagentScopeAndSettlement(t *testing.T) {
	c := &conversation{threadID: "root"}
	c.observeBackgroundNotification(notification{Method: "item/completed", Params: []byte(`{"threadId":"other","item":{"type":"collabAgentToolCall","agentsStates":{"child":{"status":"running"}}}}`)})
	if len(c.backgroundObserved.Snapshot(time.Now())) != 0 {
		t.Fatal("accepted cross-thread task")
	}
	c.observeBackgroundNotification(notification{Method: "item/completed", Params: []byte(`{"threadId":"root","item":{"type":"collabAgentToolCall","agentsStates":{"child":{"status":"running"}}}}`)})
	c.observeBackgroundNotification(notification{Method: "item/completed", Params: []byte(`{"threadId":"root","item":{"type":"collabAgentToolCall","agentsStates":{"child":{"status":"completed"}}}}`)})
	tasks := c.backgroundObserved.Snapshot(time.Now())
	if len(tasks) != 1 || tasks[0].Kind != "agent" || tasks[0].State != "completed" {
		t.Fatal(tasks)
	}
}

func TestBackgroundTasksCodexSubagentCanRunAgainAfterCompletion(t *testing.T) {
	for _, tool := range []string{"sendInput", "resumeAgent"} {
		t.Run(tool, func(t *testing.T) {
			c := &conversation{threadID: "root"}
			observe := func(method, itemID, tool, state string) {
				t.Helper()
				params, err := json.Marshal(map[string]any{
					"threadId": "root",
					"item": map[string]any{
						"id": itemID, "type": "collabAgentToolCall", "tool": tool,
						"agentsStates": map[string]any{"child": map[string]string{"status": state}},
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				c.observeBackgroundNotification(notification{Method: method, Params: params})
			}
			assertTask := func(runID, state string, at time.Time) {
				t.Helper()
				tasks := c.backgroundObserved.Snapshot(at)
				if len(tasks) != 1 || tasks[0].ID != "agent:child" || tasks[0].RunID != runID || tasks[0].State != state || (tasks[0].EndedAt != nil) != (state == "completed") {
					t.Fatalf("tasks = %+v, want child run %s in state %s", tasks, runID, state)
				}
			}
			observe("item/completed", "spawn", "spawnAgent", "running")
			observe("item/started", "wait-1", "wait", "running")
			assertTask("spawn", "running", time.Now())
			observe("item/completed", "wait-1", "wait", "completed")
			// Replaying the old dispatch or a wait cannot restart a settled run.
			observe("item/completed", "spawn", "spawnAgent", "running")
			observe("item/started", "wait-1", "wait", "running")
			assertTask("spawn", "completed", time.Now())

			observe("item/started", "next-run", tool, "running")
			observe("item/completed", "next-run", tool, "running")
			observe("item/started", "wait-2", "wait", "running")
			// The second run must survive expiry of the first run's recent outcome.
			assertTask("next-run", "running", time.Now().Add(time.Minute))
			// A terminal dispatch replay from the previous run cannot settle
			// this run or poison its subsequent running updates.
			for _, state := range []string{"completed", "errored", "shutdown", "interrupted"} {
				observe("item/completed", "spawn", "spawnAgent", state)
				assertTask("next-run", "running", time.Now())
				observe("item/completed", "next-run", tool, "running")
				assertTask("next-run", "running", time.Now())
			}
			observe("item/completed", "next-run", tool, "completed")
			assertTask("next-run", "completed", time.Now())
			observe("item/completed", "wait-2", "wait", "completed")
			observe("item/completed", "next-run", tool, "running")
			assertTask("next-run", "completed", time.Now())
		})
	}
}

func TestBackgroundTasksPreservesObservedAgentsWhenShellInventoryUnsupported(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.replyError("thread/backgroundTerminals/list", -32601, "method not found")
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	c := conv.(*conversation)
	c.backgroundObserved.Update(ports.ChatBackgroundTask{ID: "agent:child", Kind: "agent", State: "running"}, time.Now())
	tasks, err := c.BackgroundTasks(context.Background())
	if !errors.Is(err, ports.ErrBackgroundTasksPartial) || len(tasks) != 1 || tasks[0].ID != "agent:child" {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
}

func TestStopBackgroundTaskTerminatesOneCodexTerminal(t *testing.T) {
	for _, tc := range []struct {
		name, taskID string
		reply        string
		code         int
		wantErr      error
		wantSent     bool
	}{
		{name: "terminated", taskID: "63962", reply: `{"terminated":true}`, wantSent: true},
		{name: "already exited", taskID: "63962", reply: `{"terminated":false}`, wantErr: ports.ErrBackgroundTaskNotStoppable, wantSent: true},
		{name: "unsupported", taskID: "63962", code: -32601, wantErr: ports.ErrBackgroundTaskNotStoppable, wantSent: true},
		{name: "not a listed terminal", taskID: "99", wantErr: ports.ErrBackgroundTaskNotStoppable},
		{name: "a subagent", taskID: "agent:child", wantErr: ports.ErrBackgroundTaskNotStoppable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, srv := newTestDriver(t)
			srv.reply("thread/backgroundTerminals/list", `{"data":[{"processId":"63962","itemId":"cmd-1","command":"npm run dev"}],"nextCursor":null}`)
			if tc.code != 0 {
				srv.replyError("thread/backgroundTerminals/terminate", tc.code, "refused")
			} else if tc.reply != "" {
				srv.reply("thread/backgroundTerminals/terminate", tc.reply)
			}
			conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conv.Close() }()
			c := conv.(*conversation)
			c.backgroundObserved.Update(ports.ChatBackgroundTask{ID: "agent:child", Kind: "agent", State: "running"}, time.Now())
			tasks, _ := c.BackgroundTasks(context.Background())
			for _, task := range tasks {
				if task.Stoppable != (task.ID == "63962") {
					t.Fatalf("only the listed terminal is stoppable: %+v", tasks)
				}
			}
			err = c.StopBackgroundTask(context.Background(), tc.taskID)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
			if srv.sentMethod("thread/backgroundTerminals/terminate") != tc.wantSent {
				t.Fatalf("terminate sent=%v, want %v", !tc.wantSent, tc.wantSent)
			}
			if tc.wantSent {
				sent := srv.awaitFrame(func(f frame) bool { return f.Method == "thread/backgroundTerminals/terminate" })
				var params struct{ ThreadID, ProcessID string }
				if err := json.Unmarshal(sent.Params, &params); err != nil || params.ThreadID != "thread-1" || params.ProcessID != "63962" {
					t.Fatalf("params=%s err=%v", sent.Params, err)
				}
			}
		})
	}
}

// Codex reports a terminated terminal with the signal's exit code; one AO
// stopped must not read as failed.
func TestStoppedCodexTerminalSettlesAsStopped(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("thread/backgroundTerminals/list", `{"data":[{"processId":"63962","itemId":"cmd-1","command":"npm run dev"},{"processId":"70001","itemId":"cmd-2","command":"npm run build"}],"nextCursor":null}`)
	srv.reply("thread/backgroundTerminals/terminate", `{"terminated":true}`)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	c := conv.(*conversation)
	if _, err := c.BackgroundTasks(context.Background()); !errors.Is(err, ports.ErrBackgroundTasksPartial) {
		t.Fatal(err)
	}
	if err := c.StopBackgroundTask(context.Background(), "63962"); err != nil {
		t.Fatal(err)
	}
	// Both exit with a signal's code; only the one AO stopped is not a failure.
	srv.push(`{"method":"item/completed","params":{"threadId":"thread-1","item":{"id":"cmd-1","type":"commandExecution","processId":"63962","exitCode":143}}}`)
	srv.push(`{"method":"item/completed","params":{"threadId":"thread-1","item":{"id":"cmd-2","type":"commandExecution","processId":"70001","exitCode":1}}}`)
	deadline := time.Now().Add(3 * time.Second)
	for {
		states := map[string]string{}
		for _, task := range c.backgroundObserved.Snapshot(time.Now()) {
			states[task.ID] = task.State
		}
		if states["70001"] == "failed" {
			if states["63962"] != "stopped" {
				t.Fatalf("states=%v", states)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("completions never settled: %v", states)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBackgroundTasksCodexUsesCommandStartAndHidesUnknownAge(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("thread/backgroundTerminals/list", `{"data":[{"processId":"p","itemId":"cmd","command":"sleep 30"}]}`)
	opened, err := d.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	c := opened.(*conversation)
	tasks, _ := c.BackgroundTasks(t.Context())
	if len(tasks) != 1 || tasks[0].StartedAt != nil {
		t.Fatalf("late attachment invented start: %+v", tasks)
	}
	before := time.Now()
	c.observeBackgroundNotification(notification{Method: "item/started", Params: []byte(`{"item":{"id":"cmd","type":"commandExecution","command":"sleep 30"}}`)})
	after := time.Now()
	tasks, _ = c.BackgroundTasks(t.Context())
	if len(tasks) != 1 || tasks[0].StartedAt == nil || tasks[0].StartedAt.Before(before) || tasks[0].StartedAt.After(after) {
		t.Fatalf("lost command start: %+v", tasks)
	}
	first := *tasks[0].StartedAt
	tasks, _ = c.BackgroundTasks(t.Context())
	if !tasks[0].StartedAt.Equal(first) {
		t.Fatal("poll moved command start")
	}
}

func TestBackgroundTasksCodexCompletionSurvivesInventoryOrdering(t *testing.T) {
	for _, listed := range []bool{false, true} {
		c := &conversation{threadID: "root"}
		c.observeBackgroundNotification(notification{Method: "item/started", Params: []byte(`{"item":{"id":"cmd","type":"commandExecution","command":"false"}}`)})
		if len(c.backgroundObserved.Snapshot(time.Now())) != 0 {
			t.Fatal("foreground start became background")
		}
		if listed {
			c.backgroundObserved.ReconcileShells([]ports.ChatBackgroundTask{{ID: "p", RunID: "cmd", Kind: "shell", State: "running"}}, time.Now())
		}
		c.backgroundObserved.ReconcileShells(nil, time.Now())
		c.observeBackgroundNotification(notification{Method: "item/completed", Params: []byte(`{"item":{"id":"cmd","type":"commandExecution","processId":"p","command":"false","exitCode":1}}`)})
		c.backgroundObserved.ReconcileShells(nil, time.Now())
		tasks := c.backgroundObserved.Snapshot(time.Now())
		if len(tasks) != 1 || tasks[0].State != "failed" || tasks[0].StartedAt == nil || tasks[0].Description != "false" {
			t.Fatalf("listed=%v tasks=%+v", listed, tasks)
		}
	}
	c := &conversation{}
	c.observeBackgroundNotification(notification{Method: "item/completed", Params: []byte(`{"item":{"id":"fg","type":"commandExecution","exitCode":1}}`)})
	if len(c.backgroundObserved.Snapshot(time.Now())) != 0 {
		t.Fatal("foreground completion became background")
	}
}

func TestBackgroundTasksCodexKeepsTerminalOutputAfterTheTurn(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("thread/backgroundTerminals/list", `{"data":[{"processId":"93667","itemId":"exec-1","command":"for i in $(seq 1 40); do echo tick-$i; sleep 1; done"}],"nextCursor":null}`)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	c := conv.(*conversation)
	delta := func(thread, item, text string) {
		params, _ := json.Marshal(map[string]string{"threadId": thread, "itemId": item, "turnId": "turn-1", "delta": text})
		c.observeBackgroundNotification(notification{Method: "item/commandExecution/outputDelta", Params: params})
	}
	commandStarted(c, "exec-1")
	commandStarted(c, "exec-2")
	// Measured on codex-cli 0.158.0: deltas keep coming on the exec item after turn/completed.
	delta(c.threadID, "exec-1", "tick-1\n")
	delta(c.threadID, "exec-1", "tick-2\n")
	delta("child-thread", "exec-1", "not ours\n")
	delta(c.threadID, "exec-2", "a foreground command\n")

	reader := conv.(ports.ChatBackgroundTaskOutputReader)
	if _, err := reader.BackgroundTaskOutput(context.Background(), "93667"); !errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) {
		t.Fatalf("read output before the terminal was listed: %v", err)
	}
	tasks, _ := conv.(ports.ChatBackgroundTaskInspector).BackgroundTasks(context.Background())
	if len(tasks) != 1 || tasks[0].Command != "for i in $(seq 1 40); do echo tick-$i; sleep 1; done" || tasks[0].RunID != "exec-1" {
		t.Fatalf("tasks=%+v", tasks)
	}
	got, err := reader.BackgroundTaskOutput(context.Background(), "93667")
	if err != nil || got.Text != "tick-1\ntick-2" || got.Truncated {
		t.Fatalf("output=%+v err=%v", got, err)
	}
	if _, err := reader.BackgroundTaskOutput(context.Background(), "unknown"); !errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) {
		t.Fatalf("unknown task err=%v", err)
	}
	if c.backgroundOutput.MaxDeltas != codexMaxOutputDeltas {
		t.Fatalf("delta cap=%d", c.backgroundOutput.MaxDeltas)
	}
}

func commandStarted(c *conversation, item string) {
	c.observeBackgroundNotification(notification{Method: "item/started", Params: []byte(`{"item":{"id":"` + item + `","type":"commandExecution","command":"x"}}`)})
}

func commandOutput(c *conversation, item, text string) {
	params, _ := json.Marshal(map[string]string{"itemId": item, "delta": text})
	c.observeBackgroundNotification(notification{Method: "item/commandExecution/outputDelta", Params: params})
}

// Reproduced with real Codex on Windows: a quiet dev server, then 18 pings.
func TestBackgroundTaskOutputCodexKeepsAListedTerminalPastForegroundCommands(t *testing.T) {
	c := &conversation{}
	commandStarted(c, "server")
	commandOutput(c, "server", "QUIET-START server on http://localhost:4000\n")
	// What a listing records for a terminal it reports.
	c.backgroundOutput.Pin([]string{"server"})
	c.rememberBackgroundRun("4000", "server")
	c.backgroundObserved.ReconcileShells([]ports.ChatBackgroundTask{{ID: "4000", RunID: "server", Kind: "shell", State: "running"}}, time.Now())
	for i := range 18 {
		item := fmt.Sprintf("ping-%d", i)
		commandStarted(c, item)
		commandOutput(c, item, "Reply from 127.0.0.1\n")
	}
	got, err := c.BackgroundTaskOutput(context.Background(), "4000")
	if err != nil || got.Text != "QUIET-START server on http://localhost:4000" || got.Truncated {
		t.Fatalf("output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputCodexStaysReadableAfterTheTaskEnds(t *testing.T) {
	c := &conversation{}
	commandStarted(c, "late")
	commandOutput(c, "late", "LATE2 starting\n")
	// It ends before any listing saw it, and the stream missed its last line.
	c.observeBackgroundNotification(notification{Method: "item/completed", Params: []byte(`{"item":{"id":"late","type":"commandExecution","processId":"70001","exitCode":5,"aggregatedOutput":"LATE2 starting\nLATE2 FATAL: exit 5\n"}}`)})
	// Once settled tasks leave the reading (30 s), the output is still buffered.
	c.backgroundObserved = backgroundtasks.Tracker{}
	got, err := c.BackgroundTaskOutput(context.Background(), "70001")
	if err != nil || got.Text != "LATE2 starting\nLATE2 FATAL: exit 5" {
		t.Fatalf("output=%+v err=%v", got, err)
	}
	// Evicted by later commands: unavailable is honest, empty-and-ready is not.
	for i := range 3 * 16 {
		commandOutput(c, fmt.Sprintf("fg-%d", i), "x\n")
	}
	if got, err := c.BackgroundTaskOutput(context.Background(), "70001"); err != nil || got.Text != "" || !got.Truncated {
		t.Fatalf("evicted output=%+v err=%v", got, err)
	}
}

func TestBackgroundTaskOutputCodexMarksAMissedStart(t *testing.T) {
	c := &conversation{}
	// Attached part way through: no item/started for this command.
	commandOutput(c, "running", "tick-9\n")
	c.rememberBackgroundRun("9", "running")
	got, err := c.BackgroundTaskOutput(context.Background(), "9")
	if err != nil || got.Text != "tick-9" || !got.Truncated {
		t.Fatalf("output=%+v err=%v", got, err)
	}
}
