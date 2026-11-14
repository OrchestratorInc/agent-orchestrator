package claudeacp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestClaudeBackgroundTasksRequiresNativeSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, response       string
		wantErr, unsupported bool
		count                int
	}{
		{name: "empty", response: `{"tasks":[]}`},
		{name: "shell", response: `{"tasks":[{"id":"s","kind":"shell","description":"dev server","state":"running"}]}`, count: 1},
		{name: "unknown kind", response: `{"tasks":[{"id":"s","kind":"future","state":"running"}]}`, count: 1},
		{name: "missing", response: `{}`, wantErr: true},
		{name: "null", response: `{"tasks":null}`, wantErr: true},
		{name: "missing id", response: `{"tasks":[{"state":"running"}]}`, wantErr: true},
		{name: "unknown state", response: `{"tasks":[{"id":"s","state":"unknown"}]}`, wantErr: true},
		{name: "unsupported", unsupported: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			acpsdk.NewAgentSideConnection(backgroundTaskAgent{t: t, response: tc.response}, server, server)
			conn := acpsdk.NewClientSideConnection(nil, client, client)
			tasks, err := claudeBackgroundTasks(context.Background(), conn, "native-session")
			if (err != nil) != tc.wantErr || errors.Is(err, ports.ErrBackgroundTasksUnsupported) != tc.unsupported || len(tasks) != tc.count {
				t.Fatalf("tasks=%+v err=%v", tasks, err)
			}
		})
	}
}

type backgroundTaskAgent struct {
	acpsdk.Agent
	t        *testing.T
	response string
}

func (a backgroundTaskAgent) HandleExtensionMethod(_ context.Context, method string, raw json.RawMessage) (any, error) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || method != "_ao/session/background_tasks" || params.SessionID != "native-session" {
		a.t.Errorf("unexpected request %s %s", method, raw)
	}
	if a.response == "" {
		return nil, acpsdk.NewMethodNotFound(method)
	}
	return json.RawMessage(a.response), nil
}

func TestClaudeBackgroundTasksCarryStartTimeAndStopOnlyLiveTasks(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	acpsdk.NewAgentSideConnection(backgroundTaskAgent{t: t, response: `{"tasks":[
		{"id":"live","kind":"shell","description":"dev server","state":"running","startedAt":"2026-11-13T10:00:00Z","stoppable":true},
		{"id":"done","kind":"shell","description":"lint","state":"failed","startedAt":"2026-11-13T09:58:00Z","endedAt":"2026-11-13T10:00:00Z","stoppable":true}
	]}`}, server, server)
	conn := acpsdk.NewClientSideConnection(nil, client, client)
	tasks, err := claudeBackgroundTasks(context.Background(), conn, "native-session")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	if tasks[0].StartedAt == nil || tasks[0].StartedAt.UTC().Hour() != 10 || !tasks[0].Stoppable {
		t.Fatalf("live task lost its start time or stop control: %+v", tasks[0])
	}
	if tasks[1].Stoppable {
		t.Fatalf("a finished task offered a stop: %+v", tasks[1])
	}
}

func TestClaudeStopBackgroundTaskMapsMissingControlAndSettledTasks(t *testing.T) {
	for _, tc := range []struct {
		name        string
		reply       error
		notStopable bool
		wantErr     bool
	}{
		{name: "stopped"},
		{name: "old runtime", reply: acpsdk.NewMethodNotFound("_ao/session/stop_background_task"), notStopable: true, wantErr: true},
		{name: "already settled", reply: acpsdk.NewInvalidParams(nil), notStopable: true, wantErr: true},
		{name: "provider failure", reply: acpsdk.NewInternalError(nil), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			acpsdk.NewAgentSideConnection(stopTaskAgent{t: t, reply: tc.reply}, server, server)
			conn := acpsdk.NewClientSideConnection(nil, client, client)
			err := claudeStopBackgroundTask(context.Background(), conn, "native-session", "task-1")
			if (err != nil) != tc.wantErr || errors.Is(err, ports.ErrBackgroundTaskNotStoppable) != tc.notStopable {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

type stopTaskAgent struct {
	acpsdk.Agent
	t     *testing.T
	reply error
}

func (a stopTaskAgent) HandleExtensionMethod(_ context.Context, method string, raw json.RawMessage) (any, error) {
	var params struct {
		SessionID string `json:"sessionId"`
		TaskID    string `json:"taskId"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || method != "_ao/session/stop_background_task" || params.SessionID != "native-session" || params.TaskID != "task-1" {
		a.t.Errorf("unexpected request %s %s", method, raw)
	}
	if a.reply != nil {
		return nil, a.reply
	}
	return map[string]any{}, nil
}

func TestClaudeBackgroundTasksReportTheCommand(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	acpsdk.NewAgentSideConnection(backgroundTaskAgent{t: t, response: `{"tasks":[
		{"id":"live","kind":"shell","description":"Start the dev server","command":"npm run dev\u001b","state":"running"},
		{"id":"agent","kind":"agent","description":"Review","state":"running"}
	]}`}, server, server)
	conn := acpsdk.NewClientSideConnection(nil, client, client)
	tasks, err := claudeBackgroundTasks(context.Background(), conn, "native-session")
	if err != nil || len(tasks) != 2 || tasks[0].Command != "npm run dev" || tasks[0].Description != "Start the dev server" || tasks[1].Command != "" {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
}

func TestClaudeBackgroundTaskOutputTailsTheFileAndMapsMissingOutput(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		reply          error
		want           string
		truncated      bool
		unavailable    bool
		wantErr        bool
	}{
		{name: "tail", response: `{"output":"\u001b[32mready\u001b[0m\n","truncated":true}`, want: "ready", truncated: true},
		{name: "old runtime", reply: acpsdk.NewMethodNotFound("_ao/session/background_task_output"), unavailable: true, wantErr: true},
		{name: "no output file", reply: acpsdk.NewInvalidParams(nil), unavailable: true, wantErr: true},
		{name: "missing output", response: `{}`, wantErr: true},
		{name: "provider failure", reply: acpsdk.NewInternalError(nil), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			acpsdk.NewAgentSideConnection(outputTaskAgent{t: t, response: tc.response, reply: tc.reply}, server, server)
			conn := acpsdk.NewClientSideConnection(nil, client, client)
			got, err := claudeBackgroundTaskOutput(context.Background(), conn, "native-session", "task-1")
			if (err != nil) != tc.wantErr || errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) != tc.unavailable || got.Text != tc.want || got.Truncated != tc.truncated {
				t.Fatalf("output=%+v err=%v", got, err)
			}
		})
	}
}

type outputTaskAgent struct {
	acpsdk.Agent
	t        *testing.T
	response string
	reply    error
}

func (a outputTaskAgent) HandleExtensionMethod(_ context.Context, method string, raw json.RawMessage) (any, error) {
	var params struct {
		SessionID string `json:"sessionId"`
		TaskID    string `json:"taskId"`
		MaxBytes  int    `json:"maxBytes"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || method != "_ao/session/background_task_output" ||
		params.SessionID != "native-session" || params.TaskID != "task-1" || params.MaxBytes != 64<<10 {
		a.t.Errorf("unexpected request %s %s", method, raw)
	}
	if a.reply != nil {
		return nil, a.reply
	}
	return json.RawMessage(a.response), nil
}
