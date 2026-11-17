package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProcessConversationID(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	sessions := filepath.Join(config, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "0f8c2b9e-3c1a-4d5e-9f00-1a2b3c4d5e6f"
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(sessions, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("4242.json", `{"pid":4242,"sessionId":"`+id+`","kind":"interactive"}`)
	write("4343.json", `{"pid":9999,"sessionId":"`+id+`"}`)
	write("4444.json", `{"pid":4444,"sessionId":"not-a-uuid"}`)

	p := &Plugin{}
	cases := []struct {
		pid    int
		wantID string
		wantOK bool
	}{
		{4242, id, true},
		{4343, "", false},
		{4444, "", false},
		{4545, "", false},
	}
	for _, tc := range cases {
		got, ok, err := p.ProcessConversationID(context.Background(), tc.pid)
		if err != nil || got != tc.wantID || ok != tc.wantOK {
			t.Errorf("pid %d: got (%q, %v, %v), want (%q, %v, nil)", tc.pid, got, ok, err, tc.wantID, tc.wantOK)
		}
	}
}
