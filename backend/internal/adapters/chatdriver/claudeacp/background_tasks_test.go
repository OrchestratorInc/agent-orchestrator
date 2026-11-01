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
