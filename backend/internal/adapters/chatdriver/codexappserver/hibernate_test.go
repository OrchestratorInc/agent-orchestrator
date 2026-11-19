package codexappserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCanHibernateRequiresAnEmptyBackgroundTerminalInventory(t *testing.T) {
	const method = "thread/backgroundTerminals/list"
	for _, tc := range []struct {
		name, response string
		ready, wantErr bool
	}{
		{"empty", `{"data":[],"nextCursor":null}`, true, false},
		{"running", `{"data":[{"processId":"63962","osPid":20205}],"nextCursor":null}`, false, false},
		{"more_pages", `{"data":[],"nextCursor":"next"}`, false, false},
		{"missing_data", `{}`, false, true},
		{"null_data", `{"data":null}`, false, true},
		{"malformed", `{"data":{}}`, false, true},
		{"unsupported", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, srv := newTestDriver(t)
			if tc.response == "" {
				srv.replyError(method, -32601, "method not found")
			} else {
				srv.reply(method, tc.response)
			}
			conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conv.Close() }()
			ready, err := conv.(ports.ChatProviderHibernator).CanHibernate(context.Background())
			if ready != tc.ready || (err != nil) != tc.wantErr {
				t.Fatalf("CanHibernate = %v, %v; want %v, error=%v", ready, err, tc.ready, tc.wantErr)
			}
			request := srv.awaitFrame(func(f frame) bool { return f.Method == method })
			var params struct {
				ThreadID string `json:"threadId"`
				Limit    int    `json:"limit"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil || params.ThreadID != conv.ProviderConversationID() || params.Limit != 1 {
				t.Fatalf("background inventory params = %s, error=%v", request.Params, err)
			}
		})
	}
}
