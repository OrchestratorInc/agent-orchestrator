package claudeacp

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestClaudeCanHibernateRequiresAnExplicitNativeTaskCheck(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		ready, wantErr bool
		requestErr     error
	}{
		{name: "empty", response: `{"canHibernate":true}`, ready: true},
		{name: "running", response: `{"canHibernate":false}`},
		{name: "missing", response: `{}`, wantErr: true},
		{name: "null", response: `{"canHibernate":null}`, wantErr: true},
		{name: "malformed", response: `{"canHibernate":"true"}`, wantErr: true},
		{name: "unsupported"},
		{name: "provider error", wantErr: true, requestErr: &acpsdk.RequestError{Code: -32603, Message: "Internal error"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			acpsdk.NewAgentSideConnection(hibernationAgent{t: t, response: tc.response, requestErr: tc.requestErr}, server, server)
			conn := acpsdk.NewClientSideConnection(nil, client, client)
			ready, err := claudeCanHibernate(context.Background(), conn, "native-session")
			if ready != tc.ready || (err != nil) != tc.wantErr {
				t.Fatalf("CanHibernate = %v, %v; want %v, error=%v", ready, err, tc.ready, tc.wantErr)
			}
		})
	}
}

type hibernationAgent struct {
	acpsdk.Agent
	t          *testing.T
	response   string
	requestErr error
}

func (a hibernationAgent) HandleExtensionMethod(_ context.Context, method string, raw json.RawMessage) (any, error) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || method != "_ao/session/can_hibernate" || params.SessionID != "native-session" {
		a.t.Errorf("native task check: method=%q params=%s error=%v", method, raw, err)
	}
	if a.requestErr != nil {
		return nil, a.requestErr
	}
	if a.response == "" {
		return nil, acpsdk.NewMethodNotFound(method)
	}
	return json.RawMessage(a.response), nil
}
