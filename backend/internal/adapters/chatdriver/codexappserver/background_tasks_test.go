package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A completed launch with no exit code may leave a native process behind.
// Its next inventory read, rather than this notification, confirms that job.
func completeCodexBackgroundLaunch(c *conversation, id string) {
	c.observeBackgroundNotification(notification{Method: "item/completed", Params: []byte(fmt.Sprintf(`{"item":{"id":%q,"type":"commandExecution","exitCode":null}}`, id))})
}

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
			conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conv.Close() }()
			completeCodexBackgroundLaunch(conv.(*conversation), "cmd-1")
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
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
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
			conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conv.Close() }()
			c := conv.(*conversation)
			completeCodexBackgroundLaunch(c, "cmd-1")
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
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conv.Close() }()
	c := conv.(*conversation)
	completeCodexBackgroundLaunch(c, "cmd-1")
	completeCodexBackgroundLaunch(c, "cmd-2")
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
	for _, knownStart := range []bool{false, true} {
		t.Run(fmt.Sprint(knownStart), func(t *testing.T) {
			d, srv := newTestDriver(t)
			srv.reply("thread/backgroundTerminals/list", `{"data":[{"processId":"p","itemId":"cmd","command":"sleep 30"}]}`)
			opened, err := d.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			c := opened.(*conversation)
			before := time.Now()
			if knownStart {
				c.observeBackgroundNotification(notification{Method: "item/started", Params: []byte(`{"item":{"id":"cmd","type":"commandExecution","command":"sleep 30"}}`)})
				completeCodexBackgroundLaunch(c, "cmd")
			} else {
				tasks, _ := c.BackgroundTasks(t.Context())
				if len(tasks) != 0 {
					t.Fatal("unconfirmed attachment promoted", tasks)
				}
				srv.reply("thread/read", `{"thread":{"id":"thread-1","turns":[{"id":"previous","status":"completed","items":[{"id":"cmd","type":"commandExecution","processId":"p","exitCode":null,"status":"completed"}]}]}}`)
				if _, err := c.ReadHistory(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			after := time.Now()
			for range 2 {
				tasks, _ := c.BackgroundTasks(t.Context())
				if len(tasks) != 1 || (tasks[0].StartedAt != nil) != knownStart {
					t.Fatalf("tasks=%+v", tasks)
				}
				if knownStart && (tasks[0].StartedAt.Before(before) || tasks[0].StartedAt.After(after)) {
					t.Fatal("poll moved command start", tasks)
				}
			}
		})
	}
}

func TestBackgroundTasksCodexCompletionSurvivesInventoryOrdering(t *testing.T) {
	for _, listed := range []bool{false, true} {
		t.Run(fmt.Sprint(listed), func(t *testing.T) {
			d, srv := newTestDriver(t)
			srv.reply("thread/backgroundTerminals/list", `{"data":[{"processId":"p","itemId":"cmd","command":"friendly command"}]}`)
			opened, err := d.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			c := opened.(*conversation)
			c.observeBackgroundNotification(notification{Method: "item/started", Params: []byte(`{"item":{"id":"cmd","type":"commandExecution","command":"shell wrapper"}}`)})
			if len(c.backgroundObserved.Snapshot(time.Now())) != 0 {
				t.Fatal("foreground start became background")
			}
			if listed {
				completeCodexBackgroundLaunch(c, "cmd")
				if _, err := c.BackgroundTasks(t.Context()); !errors.Is(err, ports.ErrBackgroundTasksPartial) {
					t.Fatal(err)
				}
			}
			// The terminal leaves a complete inventory before its completion arrives.
			c.backgroundObserved.ReconcileShells(nil, time.Now())
			c.observeBackgroundNotification(notification{Method: "item/completed", Params: []byte(`{"item":{"id":"cmd","type":"commandExecution","processId":"p","command":"shell wrapper","exitCode":1}}`)})
			c.backgroundObserved.ReconcileShells(nil, time.Now())
			tasks := c.backgroundObserved.Snapshot(time.Now())
			if !listed {
				if len(tasks) != 0 {
					t.Fatalf("foreground completion became background: %+v", tasks)
				}
				return
			}
			if len(tasks) != 1 || tasks[0].State != "failed" || tasks[0].StartedAt == nil || tasks[0].Description != "friendly command" {
				t.Fatalf("tasks=%+v", tasks)
			}
		})
	}
}

func TestBackgroundTasksCodexBoundsPendingCommandStarts(t *testing.T) {
	// A real listed task must survive pruning of hundreds of missed foreground completions.
	d, srv := newTestDriver(t)
	srv.reply("thread/backgroundTerminals/list", `{"data":[{"processId":"p","itemId":"listed","command":"server"}]}`)
	opened, err := d.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	c := opened.(*conversation)
	completeCodexBackgroundLaunch(c, "listed")
	if _, err := c.BackgroundTasks(t.Context()); !errors.Is(err, ports.ErrBackgroundTasksPartial) {
		t.Fatal(err)
	}
	for i := range 600 {
		raw := fmt.Sprintf(`{"item":{"id":"fg-%d","type":"commandExecution","command":"TOKEN=secret"}}`, i)
		c.observeBackgroundNotification(notification{Method: "item/started", Params: []byte(raw)})
	}
	if len(c.backgroundCommands) > 257 {
		t.Fatalf("unbounded starts: %d", len(c.backgroundCommands))
	}
	for _, command := range c.backgroundCommands {
		if strings.Contains(command.Description, "secret") {
			t.Fatal("stored raw credential")
		}
	}
	c.observeBackgroundNotification(notification{Method: "item/completed", Params: []byte(`{"item":{"id":"listed","type":"commandExecution","processId":"p","exitCode":1}}`)})
	tasks := c.backgroundObserved.Snapshot(time.Now())
	if len(tasks) != 1 || tasks[0].State != "failed" || tasks[0].Description != "server" {
		t.Fatal(tasks)
	}
}

func TestBackgroundTasksCodexLongForegroundNeverPromoted(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("thread/backgroundTerminals/list", `{"data":[{"processId":"p","itemId":"cmd","command":"long foreground build"}]}`)
	opened, err := d.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	c := opened.(*conversation)
	c.observeBackgroundNotification(notification{Method: "item/started", Params: []byte(`{"threadId":"thread-1","turnId":"turn-1","item":{"id":"cmd","type":"commandExecution","command":"long foreground build"}}`)})
	for range 3 {
		tasks, err := c.BackgroundTasks(t.Context())
		if !errors.Is(err, ports.ErrBackgroundTasksPartial) || len(tasks) != 0 {
			t.Fatalf("foreground became background: %+v err=%v", tasks, err)
		}
		if err := c.StopBackgroundTask(t.Context(), "p"); !errors.Is(err, ports.ErrBackgroundTaskNotStoppable) {
			t.Fatalf("foreground stoppable: %v", err)
		}
	}
	c.observeBackgroundNotification(notification{Method: "item/completed", Params: []byte(`{"threadId":"thread-1","turnId":"turn-1","item":{"id":"cmd","type":"commandExecution","processId":"p","exitCode":1}}`)})
	srv.reply("thread/backgroundTerminals/list", `{"data":[]}`)
	c.observeBackgroundNotification(notification{Method: "turn/completed", Params: []byte(`{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}`)})
	tasks, _ := c.BackgroundTasks(t.Context())
	if len(tasks) != 0 {
		t.Fatalf("foreground failure became background outcome: %+v", tasks)
	}
	if srv.sentMethod("thread/backgroundTerminals/terminate") {
		t.Fatal("sent terminate for foreground")
	}
}

func TestBackgroundTasksCodexRequiresPostBoundaryInventory(t *testing.T) {
	for _, boundary := range []string{"call", "turn"} {
		t.Run(boundary, func(t *testing.T) {
			d, srv := newTestDriver(t)
			srv.reply("thread/backgroundTerminals/list", `{"data":[{"processId":"p","itemId":"cmd","command":"server"}]}`)
			opened, err := d.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			c := opened.(*conversation)
			c.observeBackgroundNotification(notification{Method: "item/started", Params: []byte(`{"turnId":"turn-1","item":{"id":"cmd","type":"commandExecution","command":"server"}}`)})
			tasks, _ := c.BackgroundTasks(t.Context())
			if len(tasks) != 0 {
				t.Fatalf("promoted before %s boundary: %+v", boundary, tasks)
			}

			for _, raw := range []string{
				`{"threadId":"child-thread","turn":{"id":"turn-1","status":"completed"}}`,
				`{"threadId":"thread-1","turn":{"id":"another-turn","status":"completed"}}`,
			} {
				c.observeBackgroundNotification(notification{Method: "turn/completed", Params: []byte(raw)})
				tasks, _ = c.BackgroundTasks(t.Context())
				if len(tasks) != 0 {
					t.Fatal("unrelated boundary promoted command", tasks)
				}
			}
			if boundary == "call" {
				c.observeBackgroundNotification(notification{Method: "item/completed", Params: []byte(`{"turnId":"turn-1","item":{"id":"cmd","type":"commandExecution","processId":"p","exitCode":null}}`)})
			} else {
				c.observeBackgroundNotification(notification{Method: "turn/completed", Params: []byte(`{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}`)})
			}
			if len(c.backgroundObserved.Snapshot(time.Now())) != 0 {
				t.Fatal("boundary alone created a job")
			}
			tasks, _ = c.BackgroundTasks(t.Context())
			if len(tasks) != 1 || tasks[0].State != "running" || !tasks[0].Stoppable {
				t.Fatalf("post-boundary job missing: %+v", tasks)
			}
		})
	}
}

func TestBackgroundTasksCodexCompletionDuringInventoryWaitsForNextRead(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			d, srv := newTestDriver(t)
			// No canned response: hold the first list RPC until after completion.
			opened, err := d.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			c := opened.(*conversation)
			c.observeBackgroundNotification(notification{Method: "item/started", Params: []byte(`{"item":{"id":"cmd","type":"commandExecution"}}`)})
			result := make(chan []ports.ChatBackgroundTask, 1)
			go func() { tasks, _ := c.BackgroundTasks(t.Context()); result <- tasks }()
			request := srv.awaitFrame(func(f frame) bool { return f.Method == "thread/backgroundTerminals/list" })
			if terminal {
				c.observeBackgroundNotification(notification{Method: "item/completed", Params: []byte(`{"item":{"id":"cmd","type":"commandExecution","processId":"p","exitCode":1}}`)})
			} else {
				completeCodexBackgroundLaunch(c, "cmd")
			}
			inventory := `{"data":[{"processId":"p","itemId":"cmd","command":"server"}]}`
			srv.push(`{"id":` + string(*request.ID) + `,"result":` + inventory + `}`)
			if tasks := <-result; len(tasks) != 0 {
				t.Fatalf("pre-boundary inventory promoted: %+v", tasks)
			}
			srv.reply("thread/backgroundTerminals/list", inventory)
			tasks, _ := c.BackgroundTasks(t.Context())
			if terminal {
				if len(tasks) != 0 {
					t.Fatal("terminal foreground resurrected", tasks)
				}
			} else if len(tasks) != 1 || tasks[0].State != "running" {
				t.Fatalf("later background inventory missing: %+v", tasks)
			}
		})
	}
}

func TestBackgroundTasksCodexKeepsConfirmedJobDuringForegroundWork(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.reply("thread/backgroundTerminals/list", `{"data":[{"processId":"server","itemId":"background","command":"dev server"},{"processId":"build","itemId":"foreground","command":"build"}]}`)
	opened, err := d.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	c := opened.(*conversation)
	completeCodexBackgroundLaunch(c, "background")
	c.observeBackgroundNotification(notification{Method: "item/started", Params: []byte(`{"turnId":"new-turn","item":{"id":"foreground","type":"commandExecution","command":"build"}}`)})
	tasks, err := c.BackgroundTasks(t.Context())
	if !errors.Is(err, ports.ErrBackgroundTasksPartial) || len(tasks) != 1 || tasks[0].ID != "server" || !tasks[0].Stoppable {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	if err := c.StopBackgroundTask(t.Context(), "build"); !errors.Is(err, ports.ErrBackgroundTaskNotStoppable) {
		t.Fatal(err)
	}
}

func TestBackgroundTasksCodexHistoryRequiresExplicitCompletion(t *testing.T) {
	for _, status := range []string{"completed", "failed", "interrupted", "inProgress", "futureStatus"} {
		t.Run(status, func(t *testing.T) {
			d, srv := newTestDriver(t)
			srv.reply("thread/backgroundTerminals/list", `{"data":[{"processId":"p","itemId":"cmd","command":"build"}]}`)
			srv.reply("thread/read", fmt.Sprintf(`{"thread":{"id":"thread-1","turns":[{"id":"turn","status":%q,"items":[{"id":"cmd","type":"commandExecution","exitCode":null}]}]}}`, status))
			opened, err := d.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			c := opened.(*conversation)
			_, err = c.ReadHistory(t.Context())
			if (status == "inProgress") != errors.Is(err, ports.ErrChatHistoryUnsettled) {
				t.Fatal(err)
			}
			tasks, _ := c.BackgroundTasks(t.Context())
			expected := 0
			if status == "completed" || status == "failed" || status == "interrupted" {
				expected = 1
			}
			if len(tasks) != expected {
				t.Fatalf("status %s tasks=%+v", status, tasks)
			}
		})
	}
}

func TestBackgroundTasksCodexLiveReconnectRecoversSettledLaunches(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.replyError("initialize", -32600, "Already initialized")
	workspace := t.TempDir()
	proc, err := d.spawn(t.Context(), "codex", workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.persistent = true
	d.connectHost = func(context.Context, persistenthost.Config) (*persistenthost.Transport, error) {
		return &persistenthost.Transport{Stdin: proc.stdin, Stdout: proc.stdout, Reconnected: true, NextRequestID: 41}, nil
	}
	srv.reply("thread/read", `{"thread":{"id":"thread-1","turns":[{"id":"previous","status":"completed","items":[{"id":"server","type":"commandExecution","exitCode":null}]},{"id":"current","status":"inProgress","items":[{"id":"build","type":"commandExecution","exitCode":null}]}]}}`)
	srv.reply("thread/backgroundTerminals/list", `{"data":[{"processId":"old-shell","itemId":"server","command":"npm run dev"},{"processId":"foreground","itemId":"build","command":"build"}]}`)
	srv.reply("thread/backgroundTerminals/terminate", `{"terminated":true}`)
	opened, err := d.Reconnect(t.Context(), ports.ChatResumeConfig{SessionID: "reconnect", ProviderConversationID: "thread-1", WorkspacePath: workspace, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	c := opened.(*conversation)
	for range 2 {
		tasks, err := c.BackgroundTasks(t.Context())
		if !errors.Is(err, ports.ErrBackgroundTasksPartial) || len(tasks) != 1 || tasks[0].ID != "old-shell" || !tasks[0].Stoppable || tasks[0].StartedAt != nil {
			t.Fatalf("reconnected tasks=%+v err=%v", tasks, err)
		}
	}
	if err := c.StopBackgroundTask(t.Context(), "foreground"); !errors.Is(err, ports.ErrBackgroundTaskNotStoppable) {
		t.Fatal(err)
	}
	if err := c.StopBackgroundTask(t.Context(), "old-shell"); err != nil {
		t.Fatal(err)
	}
	// An active launch was retained but could not qualify until a later boundary.
	c.observeBackgroundNotification(notification{Method: "turn/completed", Params: []byte(`{"threadId":"thread-1","turn":{"id":"current","status":"completed"}}`)})
	tasks, err := c.BackgroundTasks(t.Context())
	if !errors.Is(err, ports.ErrBackgroundTasksPartial) || len(tasks) != 2 {
		t.Fatalf("post-boundary tasks=%+v err=%v", tasks, err)
	}
	foundLive := false
	for _, task := range tasks {
		if task.ID == "foreground" {
			foundLive = task.State == "running" && task.Stoppable
		}
	}
	if !foundLive {
		t.Fatalf("surviving active launch missing after completion: %+v", tasks)
	}
	if srv.sentMethod("thread/resume") {
		t.Fatal("background history read resumed the thread")
	}
	srv.mu.Lock()
	historyReads := 0
	for _, request := range srv.seen {
		if request.Method == "thread/read" {
			historyReads++
		}
	}
	srv.mu.Unlock()
	if historyReads != 1 {
		t.Fatalf("background history read %d times; want once", historyReads)
	}
	select {
	case event := <-c.events:
		t.Fatalf("background history replayed transcript: %+v", event)
	default:
	}
}

func TestBackgroundTasksCodexReconnectHistoryFailureCanRetry(t *testing.T) {
	for _, bad := range []string{`{"thread":{"id":"other","turns":[]}}`, `{"thread":{"id":"thread-1"}}`} {
		t.Run(bad, func(t *testing.T) {
			d, srv := newTestDriver(t)
			opened, err := d.Start(t.Context(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			c := opened.(*conversation)
			c.mu.Lock()
			c.backgroundHistoryPending = true
			c.mu.Unlock()
			srv.reply("thread/read", bad)
			if _, err := c.BackgroundTasks(t.Context()); err == nil || errors.Is(err, ports.ErrBackgroundTasksPartial) {
				t.Fatalf("invalid history accepted: %v", err)
			}
			if srv.sentMethod("thread/backgroundTerminals/list") {
				t.Fatal("listed before history was validated")
			}
			srv.reply("thread/read", `{"thread":{"id":"thread-1","turns":[{"id":"past","status":"completed","items":[{"id":"cmd","type":"commandExecution","exitCode":null}]}]}}`)
			srv.reply("thread/backgroundTerminals/list", `{"data":[{"itemId":"cmd","processId":"p","command":"server"}]}`)
			tasks, err := c.BackgroundTasks(t.Context())
			if !errors.Is(err, ports.ErrBackgroundTasksPartial) || len(tasks) != 1 || !tasks[0].Stoppable {
				t.Fatalf("retry tasks=%+v err=%v", tasks, err)
			}
		})
	}
}
