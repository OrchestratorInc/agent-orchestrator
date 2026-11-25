package acp

import (
	"context"
	"errors"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/backgroundtasks"
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
	if tasks[0].Kind != "shell" || tasks[0].Description != "sleep 1800" || tasks[0].Command != "sleep 1800" {
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

func TestBackgroundTasksAsyncReplaysDoNotBlockHibernation(t *testing.T) {
	c := &conversation{sessionID: "native"}
	spawn := []byte(`{"sessionId":"native","update":{"sessionUpdate":"async_task_spawned","asyncTaskId":"job","taskType":"shell"}}`)
	c.observeAsyncTaskNotification(spawn)
	c.observeAsyncTaskNotification([]byte(`{"sessionId":"native","update":{"sessionUpdate":"async_task_state_update","asyncTaskId":"job","state":"completed"}}`))
	for _, state := range []string{"running", "paused"} {
		c.observeAsyncTaskNotification([]byte(`{"sessionId":"native","update":{"sessionUpdate":"async_task_state_update","asyncTaskId":"job","state":"` + state + `"}}`))
		if ready, err := c.CanHibernate(context.Background()); !ready || err != nil {
			t.Fatalf("replayed %s blocked hibernation: %v %v", state, ready, err)
		}
	}
	c.backgroundObserved.Snapshot(time.Now().Add(time.Minute))
	c.observeAsyncTaskNotification(spawn)
	if ready, err := c.CanHibernate(context.Background()); !ready || err != nil {
		t.Fatalf("expired spawn replay blocked hibernation: %v %v", ready, err)
	}
}

func TestBackgroundTasksCopilotAsyncShell(t *testing.T) {
	for _, key := range []string{"task_id", "taskId", "backgroundTaskId", "background_id", "processId", "process_id", "shellId", "shell_id"} {
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
	if len(tasks) != 1 || tasks[0].ID != "launch" || tasks[0].State != "running" || tasks[0].Kind != "agent" {
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
	if len(tasks) != 1 || tasks[0].ID != "launch" || tasks[0].Kind != "agent" {
		t.Fatal(tasks)
	}
}

func TestBackgroundTasksDoNotUseSharedSessionIdentity(t *testing.T) {
	for _, key := range []string{"sessionId", "sessionID", "session_id"} {
		t.Run(key, func(t *testing.T) {
			c := &conversation{}
			for _, id := range []string{"one", "two"} {
				c.observeBackgroundTool(&toolState{id: id, kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusInProgress, rawInput: map[string]any{"background": true}, rawOutput: map[string]any{key: "shared"}})
			}
			tasks, _ := c.BackgroundTasks(context.Background())
			if len(tasks) != 2 {
				t.Fatal(tasks)
			}
			c.observeBackgroundTool(&toolState{id: "three", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusCompleted, rawInput: map[string]any{"background": true}, rawOutput: map[string]any{key: "shared", "processId": "native"}})
			tasks, _ = c.BackgroundTasks(context.Background())
			if len(tasks) != 3 {
				t.Fatal(tasks)
			}
			c.backgroundObserved.Finish("native", "completed", time.Now())
			c.observeBackgroundTool(&toolState{id: "four", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusCompleted, rawInput: map[string]any{"background": true}, rawOutput: map[string]any{key: "shared", "processId": "native"}})
			tasks, _ = c.BackgroundTasks(context.Background())
			for _, task := range tasks {
				if task.ID == "native" && (task.RunID != "four" || task.State != "running") {
					t.Fatal(tasks)
				}
			}
		})
	}
}

func TestBackgroundTasksAgentSessionReferencesDoNotBecomeIdentity(t *testing.T) {
	for _, key := range []string{"sessionId", "sessionID", "session_id"} {
		for _, terminal := range []string{"completed", "failed", "stopped"} {
			t.Run(key+terminal, func(t *testing.T) {
				c := &conversation{}
				launch := &toolState{id: "launch", kind: acpsdk.ToolKindThink, status: acpsdk.ToolCallStatusCompleted, rawInput: map[string]any{"agent": "general", "background": true}, rawOutput: map[string]any{key: "child", "status": "running"}}
				c.observeBackgroundTool(launch)
				c.observeBackgroundTool(&toolState{id: "status", status: acpsdk.ToolCallStatusCompleted, rawInput: map[string]any{key: "child"}, rawOutput: map[string]any{"status": terminal}})
				tasks, _ := c.BackgroundTasks(t.Context())
				if len(tasks) != 1 || tasks[0].ID != "launch" || tasks[0].State != terminal {
					t.Fatal(tasks)
				}
			})
		}
	}
	c := &conversation{}
	for _, id := range []string{"one", "two"} {
		c.observeBackgroundTool(&toolState{id: id, kind: acpsdk.ToolKindThink, status: acpsdk.ToolCallStatusCompleted, rawInput: map[string]any{"agent": "general", "background": true}, rawOutput: map[string]any{"sessionID": "shared", "status": "running"}})
	}
	c.observeBackgroundTool(&toolState{id: "status", status: acpsdk.ToolCallStatusCompleted, rawInput: map[string]any{"sessionID": "shared"}, rawOutput: map[string]any{"status": "completed"}})
	tasks, _ := c.BackgroundTasks(t.Context())
	if len(tasks) != 2 {
		t.Fatal(tasks)
	}
	for _, task := range tasks {
		if task.State != "running" {
			t.Fatal("ambiguous session reference settled a job", tasks)
		}
	}
}

func TestBackgroundTaskOutputNeedsAProviderSource(t *testing.T) {
	c := &conversation{tools: make(map[string]*toolState)}
	if _, err := c.BackgroundTaskOutput(context.Background(), "launch"); !errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) {
		t.Fatalf("event-tracked task err=%v", err)
	}
	c.backgroundTaskOutput = func(_ context.Context, _ *acpsdk.ClientSideConnection, id acpsdk.SessionId, task string) (ports.ChatBackgroundTaskOutput, error) {
		return ports.ChatBackgroundTaskOutput{Text: string(id) + ":" + task}, nil
	}
	c.sessionID = "native"
	if got, err := c.BackgroundTaskOutput(context.Background(), "launch"); err != nil || got.Text != "native:launch" {
		t.Fatalf("output=%+v err=%v", got, err)
	}
	// Agents and other work have no command line.
	c.observeBackgroundTool(&toolState{id: "agent", status: acpsdk.ToolCallStatusInProgress, rawInput: map[string]any{"prompt": "review", "command": "x", "run_in_background": true, "subagent_type": "reviewer"}})
	tasks, _ := c.BackgroundTasks(context.Background())
	if len(tasks) != 1 || tasks[0].Kind != "agent" || tasks[0].Command != "" {
		t.Fatal(tasks)
	}
}

func TestBackgroundTasksACPRestoresStartTimeAcrossRestartAndReconnect(t *testing.T) {
	dataDir := t.TempDir()
	sessionID := "ses-acp-test"
	store := backgroundtasks.NewFileStore(dataDir)

	c1 := &conversation{
		tools:              make(map[string]*toolState),
		backgroundObserved: backgroundtasks.Tracker{SessionID: sessionID, Store: store},
	}

	launchTime := time.Now().Add(-10 * time.Minute).Truncate(time.Millisecond).UTC()
	tool1 := &toolState{
		id:        "tool-launch-1",
		kind:      acpsdk.ToolKindExecute,
		status:    acpsdk.ToolCallStatusInProgress,
		rawInput:  map[string]any{"command": "make test", "run_in_background": true},
		startedAt: launchTime,
	}
	c1.tools[tool1.id] = tool1
	c1.observeBackgroundTool(tool1)

	// Tool completes and returns native task handle.
	completed := acpsdk.ToolCallStatusCompleted
	tool1.status = completed
	tool1.rawOutput = map[string]any{"task_id": "native-job-1"}
	c1.observeBackgroundTool(tool1)

	tasks1, err := c1.BackgroundTasks(context.Background())
	if err != nil && !errors.Is(err, ports.ErrBackgroundTasksPartial) {
		t.Fatal(err)
	}
	if len(tasks1) != 1 || tasks1[0].StartedAt == nil || !tasks1[0].StartedAt.Equal(launchTime) {
		t.Fatalf("first ACP task start time = %+v, want %v", tasks1, launchTime)
	}

	// Simulate restart / reconnect: new conversation with fresh tools and tracker connecting to same store.
	c2 := &conversation{
		tools:              make(map[string]*toolState),
		backgroundObserved: backgroundtasks.Tracker{SessionID: sessionID, Store: backgroundtasks.NewFileStore(dataDir)},
	}

	// Provider replays the tool call after reconnect with a newer timestamp.
	replayTime := time.Now().Truncate(time.Millisecond).UTC()
	toolReplay := &toolState{
		id:        "tool-launch-1",
		kind:      acpsdk.ToolKindExecute,
		status:    acpsdk.ToolCallStatusCompleted,
		rawInput:  map[string]any{"command": "make test", "run_in_background": true},
		rawOutput: map[string]any{"task_id": "native-job-1"},
		startedAt: replayTime,
	}
	c2.tools[toolReplay.id] = toolReplay
	c2.observeBackgroundTool(toolReplay)

	tasks2, err := c2.BackgroundTasks(context.Background())
	if err != nil && !errors.Is(err, ports.ErrBackgroundTasksPartial) {
		t.Fatal(err)
	}
	if len(tasks2) != 1 || tasks2[0].StartedAt == nil || !tasks2[0].StartedAt.Equal(launchTime) {
		t.Fatalf("reconnected ACP task did not restore launch start time: %+v, want %v", tasks2, launchTime)
	}

	// Settle the old run.
	c2.backgroundObserved.Finish("native-job-1", "completed", time.Now())

	// A new run of the same task ID starts from zero (its own new start time).
	newLaunchTime := time.Now().Truncate(time.Millisecond).UTC()
	tool2 := &toolState{
		id:        "tool-launch-2",
		kind:      acpsdk.ToolKindExecute,
		status:    acpsdk.ToolCallStatusCompleted,
		rawInput:  map[string]any{"command": "make test", "run_in_background": true},
		rawOutput: map[string]any{"task_id": "native-job-1"},
		startedAt: newLaunchTime,
	}
	c2.tools[tool2.id] = tool2
	c2.observeBackgroundTool(tool2)

	tasks3, err := c2.BackgroundTasks(context.Background())
	if err != nil && !errors.Is(err, ports.ErrBackgroundTasksPartial) {
		t.Fatal(err)
	}
	if len(tasks3) != 1 || tasks3[0].StartedAt == nil || !tasks3[0].StartedAt.Equal(newLaunchTime) {
		t.Fatalf("new run did not reset start time: got %+v, want %v", tasks3, newLaunchTime)
	}
}

func TestBackgroundTasksACPNativeReadPersistsStartTime(t *testing.T) {
	store := backgroundtasks.NewMemoryStore()
	start := time.Now().Add(-20 * time.Minute).Truncate(time.Millisecond).UTC()
	c := &conversation{
		sessionID: "native-session",
		backgroundObserved: backgroundtasks.Tracker{
			SessionID: "native-session",
			Store:     store,
		},
		backgroundTasks: func(_ context.Context, _ *acpsdk.ClientSideConnection, _ acpsdk.SessionId) ([]ports.ChatBackgroundTask, error) {
			return []ports.ChatBackgroundTask{
				{ID: "native-task-1", RunID: "run-1", StartedAt: &start},
			}, nil
		},
	}

	tasks, err := c.BackgroundTasks(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].StartedAt == nil || !tasks[0].StartedAt.Equal(start) {
		t.Fatalf("first native read failed: tasks=%+v err=%v", tasks, err)
	}

	// Stored time is persisted in store.
	stored, ok := store.GetStartedAt("native-session", "native-task-1", "run-1")
	if !ok || !stored.Equal(start) {
		t.Fatalf("stored start time = %v, want %v", stored, start)
	}

	// Later native read where provider omits optional StartedAt restores stored time.
	c.backgroundTasks = func(_ context.Context, _ *acpsdk.ClientSideConnection, _ acpsdk.SessionId) ([]ports.ChatBackgroundTask, error) {
		return []ports.ChatBackgroundTask{
			{ID: "native-task-1", RunID: "run-1", StartedAt: nil},
		}, nil
	}

	tasks2, err := c.BackgroundTasks(context.Background())
	if err != nil || len(tasks2) != 1 || tasks2[0].StartedAt == nil || !tasks2[0].StartedAt.Equal(start) {
		t.Fatalf("re-read did not restore stored start time: %+v, err=%v", tasks2, err)
	}
}

func TestBackgroundTasksACPNativeReadRunIDMismatch(t *testing.T) {
	dataDir := t.TempDir()
	sessionID := "session-mismatch"
	store := backgroundtasks.NewFileStore(dataDir)

	c := &conversation{
		sessionID: sessionID,
		tools:     make(map[string]*toolState),
		backgroundObserved: backgroundtasks.Tracker{
			SessionID: sessionID,
			Store:     store,
		},
	}

	launchTime := time.Now().Add(-15 * time.Minute).Truncate(time.Millisecond).UTC()
	tool := &toolState{
		id:        "tool-call-1",
		kind:      acpsdk.ToolKindExecute,
		status:    acpsdk.ToolCallStatusCompleted,
		rawInput:  map[string]any{"command": "pytest", "run_in_background": true},
		rawOutput: map[string]any{"task_id": "native-job-mismatch"},
		startedAt: launchTime,
	}
	c.tools[tool.id] = tool
	c.observeBackgroundTool(tool)

	// Case 1: Provider returns mismatched RunID with nil StartedAt.
	c.backgroundTasks = func(_ context.Context, _ *acpsdk.ClientSideConnection, _ acpsdk.SessionId) ([]ports.ChatBackgroundTask, error) {
		return []ports.ChatBackgroundTask{
			{ID: "native-job-mismatch", RunID: "provider-different-run", StartedAt: nil},
		}, nil
	}

	tasks, err := c.BackgroundTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].StartedAt == nil || !tasks[0].StartedAt.Equal(launchTime) {
		t.Fatalf("mismatched RunID did not restore start time: %+v, want %v", tasks, launchTime)
	}

	// Case 2: Provider returns empty RunID with nil StartedAt.
	c.backgroundTasks = func(_ context.Context, _ *acpsdk.ClientSideConnection, _ acpsdk.SessionId) ([]ports.ChatBackgroundTask, error) {
		return []ports.ChatBackgroundTask{
			{ID: "native-job-mismatch", RunID: "", StartedAt: nil},
		}, nil
	}

	tasks2, err := c.BackgroundTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks2) != 1 || tasks2[0].StartedAt == nil || !tasks2[0].StartedAt.Equal(launchTime) {
		t.Fatalf("empty RunID did not restore start time: %+v, want %v", tasks2, launchTime)
	}
	if tasks2[0].RunID != "tool-call-1" {
		t.Fatalf("empty RunID was not resolved to observed RunID: got %q, want %q", tasks2[0].RunID, "tool-call-1")
	}
}

func TestBackgroundTasksACPReplayWithoutStoredTimeDoesNotSetNow(t *testing.T) {
	dataDir := t.TempDir()
	sessionID := "session-no-fallback"
	store := backgroundtasks.NewFileStore(dataDir)

	c := &conversation{
		sessionID: sessionID,
		tools:     make(map[string]*toolState),
		backgroundObserved: backgroundtasks.Tracker{
			SessionID: sessionID,
			Store:     store,
		},
	}

	// Tool call replayed with zero startedAt and no prior stored time.
	tool := &toolState{
		id:        "tool-replayed-1",
		kind:      acpsdk.ToolKindExecute,
		status:    acpsdk.ToolCallStatusCompleted,
		rawInput:  map[string]any{"command": "sleep 100", "run_in_background": true},
		rawOutput: map[string]any{"task_id": "native-sleep-1"},
		startedAt: time.Time{},
	}
	c.tools[tool.id] = tool
	c.observeBackgroundTool(tool)

	tasks, err := c.BackgroundTasks(context.Background())
	if err != nil && !errors.Is(err, ports.ErrBackgroundTasksPartial) {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].StartedAt != nil {
		t.Fatalf("expected StartedAt to remain nil when no start time was known, got %v", tasks[0].StartedAt)
	}
}
