package codexappserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestBackgroundTasksReadsNativeCodexInventory(t *testing.T) {
	for _, tc := range []struct {
		name, response       string
		count                int
		unsupported, wantErr bool
	}{
		{name: "empty", response: `{"data":[],"nextCursor":null}`},
		{name: "shell", response: `{"data":[{"processId":"63962","itemId":"cmd-1","command":"sleep 1800","osPid":20205}],"nextCursor":null}`, count: 1},
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
			if (err != nil) != tc.wantErr || errors.Is(err, ports.ErrBackgroundTasksUnsupported) != tc.unsupported || len(tasks) != tc.count {
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
