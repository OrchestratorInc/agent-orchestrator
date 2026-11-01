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
	c := &conversation{}
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
	c.observeBackgroundTool(&toolState{id: "launch", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusInProgress, rawInput: input})
	tasks := read(1)
	if tasks[0].Kind != "shell" || tasks[0].Description != "sleep 1800" {
		t.Fatal(tasks)
	}
	c.observeBackgroundTool(&toolState{id: "launch", status: acpsdk.ToolCallStatusCompleted})
	tasks = read(1)
	if tasks[0].State != "completed" {
		t.Fatal(tasks)
	}
	c.backgroundObserved.Finish("launch", "failed", time.Now().Add(-time.Minute))
	read(0)
}

func TestBackgroundTasksACPLaunchResultIsNotJobCompletion(t *testing.T) {
	c := &conversation{}
	c.observeBackgroundTool(&toolState{id: "launch", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusCompleted, rawInput: map[string]any{"run_in_background": true}, rawOutput: map[string]any{"task_id": "native-job"}})
	tasks, _ := c.BackgroundTasks(context.Background())
	if len(tasks) != 1 || tasks[0].ID != "native-job" || tasks[0].State != "running" {
		t.Fatal(tasks)
	}
	c.observeBackgroundTool(&toolState{id: "native-job", status: acpsdk.ToolCallStatusFailed})
	tasks, _ = c.BackgroundTasks(context.Background())
	if tasks[0].State != "failed" {
		t.Fatal(tasks)
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
	c := &conversation{}
	c.observeBackgroundTool(&toolState{id: "job", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusInProgress, rawInput: map[string]any{"background": true}})
	if ready, err := c.CanHibernate(context.Background()); ready || err != nil {
		t.Fatalf("hibernate=%v %v", ready, err)
	}
	c.observeBackgroundTool(&toolState{id: "job", status: acpsdk.ToolCallStatusCompleted})
	if ready, err := c.CanHibernate(context.Background()); !ready || err != nil {
		t.Fatalf("hibernate after completion=%v %v", ready, err)
	}
}

func TestBackgroundTasksCopilotAsyncShell(t *testing.T) {
	c := &conversation{}
	c.observeBackgroundTool(&toolState{id: "tool", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusCompleted, rawInput: map[string]any{"command": "sleep 1800", "mode": "async"}, rawOutput: map[string]any{"shellId": "native-shell"}})
	tasks, _ := c.BackgroundTasks(context.Background())
	if len(tasks) != 1 || tasks[0].ID != "native-shell" || tasks[0].State != "running" || tasks[0].Kind != "shell" {
		t.Fatal(tasks)
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
