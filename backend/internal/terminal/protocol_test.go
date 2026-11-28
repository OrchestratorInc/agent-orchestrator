package terminal

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
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
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", out, in)
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
