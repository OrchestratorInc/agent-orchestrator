package terminal

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestClientMsgRoundTrip(t *testing.T) {
	in := clientMsg{
		Ch:   chTerminal,
		ID:   "sess-1",
		Type: msgData,
		Data: base64.StdEncoding.EncodeToString([]byte("ls -la\n")),
		Cols: 80,
		Rows: 24,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out clientMsg
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

// TestClientMsgDecodesMobileFrames decodes the exact frames the mobile client
// sends (packages/mobile/lib/mux.test.ts pins the same literals). The fakeConn
// tests hand the manager ready-made clientMsg values, so they cannot notice a
// client frame that decodes to something else, which is how a subscribe frame
// without "type" went unnoticed. "projectId" and "topics" are not clientMsg
// fields; decoding ignores them.
func TestClientMsgDecodesMobileFrames(t *testing.T) {
	tests := []struct {
		name  string
		frame string
		want  clientMsg
	}{
		{"subscribe", `{"ch":"subscribe","type":"subscribe","topics":["sessions","notifications"]}`,
			clientMsg{Ch: chSubscribe, Type: msgSubscribe}},
		{"terminal open", `{"ch":"terminal","id":"s1","type":"open","projectId":"p1","role":"secondary"}`,
			clientMsg{Ch: chTerminal, ID: "s1", Type: msgOpen, Role: roleSecondary}},
		{"terminal data", `{"ch":"terminal","id":"s1","type":"data","data":"bHMK","projectId":"p1"}`,
			clientMsg{Ch: chTerminal, ID: "s1", Type: msgData, Data: "bHMK"}},
		{"terminal resize", `{"ch":"terminal","id":"s1","type":"resize","cols":80,"rows":24,"projectId":"p1"}`,
			clientMsg{Ch: chTerminal, ID: "s1", Type: msgResize, Cols: 80, Rows: 24}},
		{"terminal close", `{"ch":"terminal","id":"s1","type":"close","projectId":"p1"}`,
			clientMsg{Ch: chTerminal, ID: "s1", Type: msgClose}},
		{"system ping", `{"ch":"system","type":"ping"}`,
			clientMsg{Ch: chSystem, Type: msgPing}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got clientMsg
			if err := json.Unmarshal([]byte(tt.frame), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got != tt.want {
				t.Fatalf("decoded:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestServerMsgSessionFrameWireShape(t *testing.T) {
	msg := serverMsg{
		Ch:   chSessions,
		Type: msgSnapshot,
		Session: &sessionUpdate{
			Seq: 7, ProjectID: "p1", SessionID: "s1", EventType: "session_updated",
		},
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Golden wire shape the client depends on.
	want := `{"ch":"sessions","type":"snapshot","session":{"seq":7,"projectId":"p1","sessionId":"s1","eventType":"session_updated"}}`
	if string(raw) != want {
		t.Fatalf("wire shape:\n got %s\nwant %s", raw, want)
	}
}

// A project-level change (a PR event, say) has no session: the daemon omits
// sessionId rather than sending an empty string. packages/mobile/lib/mux.test.ts
// pins this literal and the one above.
func TestServerMsgProjectEventFrameWireShape(t *testing.T) {
	msg := serverMsg{
		Ch:      chSessions,
		Type:    msgSnapshot,
		Session: &sessionUpdate{Seq: 8, ProjectID: "p1", EventType: "pr_updated"},
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"ch":"sessions","type":"snapshot","session":{"seq":8,"projectId":"p1","eventType":"pr_updated"}}`
	if string(raw) != want {
		t.Fatalf("wire shape:\n got %s\nwant %s", raw, want)
	}
}

func TestServerMsgOmitsEmptyOptionalFields(t *testing.T) {
	raw, err := json.Marshal(serverMsg{Ch: chTerminal, ID: "t1", Type: msgOpened})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"ch":"terminal","id":"t1","type":"opened"}`
	if string(raw) != want {
		t.Fatalf("wire shape:\n got %s\nwant %s", raw, want)
	}
}
