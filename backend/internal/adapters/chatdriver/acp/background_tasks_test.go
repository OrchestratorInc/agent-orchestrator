package acp

import (
	"context"
	"errors"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestBackgroundTasksSharedACPTracking(t *testing.T) {
	c := &conversation{tools: make(map[string]*toolState)}
	read := func(count int) []ports.ChatBackgroundTask {
		t.Helper()
		tasks, err := c.BackgroundTasks(context.Background())
		if !errors.Is(err, ports.ErrBackgroundTasksPartial) || len(tasks) != count {
			t.Fatalf("tasks=%+v err=%v", tasks, err)
		}
		return tasks
	}
	read(0)
	c.observeBackgroundTool(&toolState{id: "foreground", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusInProgress})
	read(0)
	input := map[string]any{"command": "sleep 1800", "run_in_background": true}
	kind, status := acpsdk.ToolKindExecute, acpsdk.ToolCallStatusInProgress
	c.observeBackgroundTool(c.mergeToolUpdate(&acpsdk.SessionToolCallUpdate{
		ToolCallId: "launch", Kind: &kind, Status: &status, RawInput: input,
	}))
	tasks := read(1)
	if tasks[0].Kind != "shell" || tasks[0].Description != "sleep 1800" {
		t.Fatal(tasks)
	}
	status = acpsdk.ToolCallStatusCompleted
	c.observeBackgroundTool(c.mergeToolUpdate(&acpsdk.SessionToolCallUpdate{
		ToolCallId: "launch", Status: &status, RawOutput: "Background command launched",
	}))
	tasks = read(1)
	if tasks[0].State != "completed" || tasks[0].EndedAt == nil {
		t.Fatal(tasks)
	}
	c.backgroundObserved.Finish("launch", "failed", time.Now().Add(-time.Minute))
	read(0)
}

func TestBackgroundTasksACPLaunchResultIsNotJobCompletion(t *testing.T) {
	tool := &toolState{id: "launch", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusInProgress, rawInput: map[string]any{"run_in_background": true}}
	c := &conversation{tools: map[string]*toolState{"launch": tool}}
	c.observeBackgroundTool(tool)
	completed := acpsdk.ToolCallStatusCompleted
	c.observeBackgroundTool(c.mergeToolUpdate(&acpsdk.SessionToolCallUpdate{
		ToolCallId: "launch", Status: &completed, RawOutput: map[string]any{"task_id": "native-job"},
	}))
	tasks, _ := c.BackgroundTasks(context.Background())
	if len(tasks) != 1 || tasks[0].ID != "native-job" || tasks[0].State != "running" {
		t.Fatal(tasks)
	}
	if ready, err := c.CanHibernate(context.Background()); ready || err != nil {
		t.Fatalf("hibernate with native job=%v %v", ready, err)
	}
	c.observeBackgroundTool(c.mergeToolUpdate(&acpsdk.SessionToolCallUpdate{
		ToolCallId: "launch", RawOutput: map[string]any{"task_id": "native-job", "status": "completed"},
	}))
	tasks, _ = c.BackgroundTasks(context.Background())
	if len(tasks) != 1 || tasks[0].ID != "native-job" || tasks[0].State != "completed" || tasks[0].EndedAt == nil {
		t.Fatal(tasks)
	}
}

func TestBackgroundTasksACPExplicitTerminalStateAfterMergedUpdate(t *testing.T) {
	for _, field := range []string{"state", "status"} {
		for _, state := range []string{"completed", "failed", "stopped"} {
			t.Run(field+"/"+state, func(t *testing.T) {
				tool := &toolState{id: "job", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusInProgress, rawInput: map[string]any{"background": true}}
				c := &conversation{tools: map[string]*toolState{"job": tool}}
				c.observeBackgroundTool(tool)
				completed := acpsdk.ToolCallStatusCompleted
				// The real merge preserves the original background input flag.
				c.observeBackgroundTool(c.mergeToolUpdate(&acpsdk.SessionToolCallUpdate{
					ToolCallId: "job", Status: &completed, RawOutput: map[string]any{field: state},
				}))
				tasks, _ := c.BackgroundTasks(context.Background())
				if len(tasks) != 1 || tasks[0].ID != "job" || tasks[0].State != state || tasks[0].EndedAt == nil {
					t.Fatalf("tasks=%+v, want settled tool-ID task in state %s", tasks, state)
				}
				if ready, err := c.CanHibernate(context.Background()); !ready || err != nil {
					t.Fatalf("hibernate after explicit %s=%v %v", state, ready, err)
				}
			})
		}
	}
}

func TestBackgroundTasksACPNativeReadFailureDoesNotPretendEmpty(t *testing.T) {
	want := errors.New("native read failed")
	c := &conversation{backgroundTasks: func(context.Context, *acpsdk.ClientSideConnection, acpsdk.SessionId) ([]ports.ChatBackgroundTask, error) {
		return nil, want
	}}
	if _, err := c.BackgroundTasks(context.Background()); !errors.Is(err, want) {
		t.Fatal(err)
	}
	c.closed = true
	if _, err := c.BackgroundTasks(context.Background()); !errors.Is(err, errConversationClosed) {
		t.Fatal(err)
	}
}

func TestBackgroundTasksNativeAsyncLifecycleAcrossTurns(t *testing.T) {
	c := &conversation{sessionID: "native"}
	for _, raw := range []string{
		`{"sessionId":"native","update":{"sessionUpdate":"async_task_spawned","asyncTaskId":"job","taskType":"shell","name":"npm run dev","toolCallId":"launch"}}`,
		`{"sessionId":"native","update":{"sessionUpdate":"async_task_state_update","asyncTaskId":"job","state":"paused"}}`,
	} {
		if !c.observeAsyncTaskNotification([]byte(raw)) {
			t.Fatal("async update rejected")
		}
	}
	// Turn reset must not erase an independent background job.
	c.tools = make(map[string]*toolState)
	tasks, _ := c.BackgroundTasks(context.Background())
	if len(tasks) != 1 || tasks[0].State != "paused" || tasks[0].Description != "npm run dev" {
		t.Fatal(tasks)
	}
	c.observeAsyncTaskNotification([]byte(`{"sessionId":"other","update":{"sessionUpdate":"async_task_state_update","asyncTaskId":"job","state":"failed"}}`))
	tasks, _ = c.BackgroundTasks(context.Background())
	if tasks[0].State != "paused" {
		t.Fatal("cross-session update accepted")
	}
	c.observeAsyncTaskNotification([]byte(`{"sessionId":"native","update":{"sessionUpdate":"async_task_state_update","asyncTaskId":"job","state":"completed"}}`))
	tasks, _ = c.BackgroundTasks(context.Background())
	if tasks[0].State != "completed" || tasks[0].EndedAt == nil {
		t.Fatal(tasks)
	}
}

func TestBackgroundTasksDeferACPHibernation(t *testing.T) {
	for _, status := range []acpsdk.ToolCallStatus{acpsdk.ToolCallStatusCompleted, acpsdk.ToolCallStatusFailed} {
		for _, tc := range []struct {
			name   string
			output any
		}{
			{name: "no output"},
			{name: "prose output", output: "Background command launched"},
			{name: "unstructured result", output: map[string]any{"output": "Background command launched"}},
		} {
			t.Run(string(status)+"/"+tc.name, func(t *testing.T) {
				c := &conversation{tools: make(map[string]*toolState)}
				kind, started := acpsdk.ToolKindExecute, acpsdk.ToolCallStatusInProgress
				c.observeBackgroundTool(c.mergeToolUpdate(&acpsdk.SessionToolCallUpdate{
					ToolCallId: "job", Kind: &kind, Status: &started,
					RawInput: map[string]any{"command": "sleep 5", "run_in_background": true},
				}))
				if ready, err := c.CanHibernate(context.Background()); ready || err != nil {
					t.Fatalf("hibernate=%v %v", ready, err)
				}
				c.observeBackgroundTool(c.mergeToolUpdate(&acpsdk.SessionToolCallUpdate{
					ToolCallId: "job", Status: &status, RawOutput: tc.output,
				}))
				tasks, _ := c.BackgroundTasks(context.Background())
				if len(tasks) != 1 || tasks[0].State != string(status) || tasks[0].EndedAt == nil {
					t.Fatalf("tasks=%+v, want settled tool-ID task", tasks)
				}
				if ready, err := c.CanHibernate(context.Background()); !ready || err != nil {
					t.Fatalf("hibernate after %s=%v %v", status, ready, err)
				}
			})
		}
	}
}

func TestBackgroundTasksPausedAsyncTaskDefersACPHibernation(t *testing.T) {
	c := &conversation{sessionID: "native"}
	c.observeAsyncTaskNotification([]byte(`{"sessionId":"native","update":{"sessionUpdate":"async_task_spawned","asyncTaskId":"job","taskType":"shell"}}`))
	for _, state := range []string{"running", "paused"} {
		c.observeAsyncTaskNotification([]byte(`{"sessionId":"native","update":{"sessionUpdate":"async_task_state_update","asyncTaskId":"job","state":"` + state + `"}}`))
		if ready, err := c.CanHibernate(context.Background()); ready || err != nil {
			t.Fatalf("hibernate with %s task=%v %v", state, ready, err)
		}
	}
	c.observeAsyncTaskNotification([]byte(`{"sessionId":"native","update":{"sessionUpdate":"async_task_state_update","asyncTaskId":"job","state":"completed"}}`))
	if ready, err := c.CanHibernate(context.Background()); !ready || err != nil {
		t.Fatalf("hibernate after completion=%v %v", ready, err)
	}
}

func TestBackgroundTasksCopilotAsyncShell(t *testing.T) {
	for _, key := range []string{"task_id", "taskId", "backgroundTaskId", "background_id", "sessionID", "sessionId", "session_id", "processId", "process_id", "shellId", "shell_id"} {
		t.Run(key, func(t *testing.T) {
			c := &conversation{}
			c.observeBackgroundTool(&toolState{id: "tool", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusCompleted, rawInput: map[string]any{"command": "sleep 1800", "mode": "async"}, rawOutput: map[string]any{key: "native-shell"}})
			tasks, _ := c.BackgroundTasks(context.Background())
			if len(tasks) != 1 || tasks[0].ID != "native-shell" || tasks[0].State != "running" || tasks[0].Kind != "shell" {
				t.Fatal(tasks)
			}
			c.observeBackgroundTool(&toolState{id: "stop", status: acpsdk.ToolCallStatusCompleted, rawInput: map[string]any{key: "native-shell"}, rawOutput: map[string]any{"status": "stopped"}})
			tasks, _ = c.BackgroundTasks(context.Background())
			if len(tasks) != 1 || tasks[0].ID != "native-shell" || tasks[0].State != "stopped" || tasks[0].EndedAt == nil {
				t.Fatal(tasks)
			}
			if ready, err := c.CanHibernate(context.Background()); !ready || err != nil {
				t.Fatalf("hibernate after shell stopped=%v %v", ready, err)
			}
		})
	}
}

func TestBackgroundTasksOpenCodeMachineOutputAndCompletion(t *testing.T) {
	c := &conversation{}
	tool := &toolState{id: "launch", kind: acpsdk.ToolKindThink, status: acpsdk.ToolCallStatusCompleted, rawInput: map[string]any{"agent": "general", "background": true, "description": "Run demo"}, rawOutput: `{"sessionID":"ses_native","status":"running","truncated":false}`}
	c.observeBackgroundTool(tool)
	tasks, _ := c.BackgroundTasks(context.Background())
	if len(tasks) != 1 || tasks[0].ID != "ses_native" || tasks[0].State != "running" || tasks[0].Kind != "agent" {
		t.Fatal(tasks)
	}
	tool.rawOutput = `{"sessionID":"ses_native","status":"completed"}`
	c.observeBackgroundTool(tool)
	tasks, _ = c.BackgroundTasks(context.Background())
	if len(tasks) != 1 || tasks[0].State != "completed" || tasks[0].EndedAt == nil {
		t.Fatal(tasks)
	}
}

func TestBackgroundTasksWrappedMachineResult(t *testing.T) {
	c := &conversation{}
	c.observeBackgroundTool(&toolState{id: "launch", kind: acpsdk.ToolKindThink, status: acpsdk.ToolCallStatusCompleted, rawInput: map[string]any{"agent": "general", "background": true}, rawOutput: map[string]any{"output": `{"sessionID":"native-child","status":"running"}`}})
	tasks, _ := c.BackgroundTasks(context.Background())
	if len(tasks) != 1 || tasks[0].ID != "native-child" || tasks[0].Kind != "agent" {
		t.Fatal(tasks)
	}
}
