package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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
			if tc.count > 0 && (tasks[0].ID != "63962" || tasks[0].Kind != "shell" || tasks[0].Description != "sleep 1800") {
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

func TestBackgroundTasksCodexKeepsTerminalOutputAfterTheTurn(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("thread/backgroundTerminals/list", `{"data":[{"processId":"93667","itemId":"exec-1","command":"for i in $(seq 1 40); do echo tick-$i; sleep 1; done"}],"nextCursor":null}`)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	c := conv.(*conversation)
	delta := func(thread, item, text string) {
		params, _ := json.Marshal(map[string]string{"threadId": thread, "itemId": item, "turnId": "turn-1", "delta": text})
		c.observeBackgroundNotification(notification{Method: "item/commandExecution/outputDelta", Params: params})
	}
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
	if err != nil || got.Text != "tick-1\ntick-2" {
		t.Fatalf("output=%+v err=%v", got, err)
	}
	if _, err := reader.BackgroundTaskOutput(context.Background(), "unknown"); !errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) {
		t.Fatalf("unknown task err=%v", err)
	}
}
