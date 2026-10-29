package cline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeConversationRequiresSavedClineRootHistory(t *testing.T) {
	probe, ok := any(New()).(ports.AgentInterfaceHandoffHistoryProbe)
	if !ok {
		t.Fatal("Cline must verify saved CLI root history before an unattended resume")
	}
	for _, tt := range []struct {
		name, id, manifest, messages string
		want                         bool
	}{
		{"saved root", "1792275014518_yyjbm", `{"session_id":"1792275014518_yyjbm","cwd":"WORKSPACE"}`, `{"sessionId":"1792275014518_yyjbm","messages":[{"role":"user","content":"remember"}]}`, true},
		{"empty native ID", "", "", "", false},
		{"parent traversal", "../outside", "", "", false},
		{"windows traversal", `..\outside`, "", "", false},
		{"hook internal ID absent", "conv_internal", "", "", false},
		{"wrong manifest identity", "1792275014518_yyjbm", `{"session_id":"other","cwd":"WORKSPACE"}`, `{"sessionId":"1792275014518_yyjbm","messages":[{}]}`, false},
		{"wrong workspace", "1792275014518_yyjbm", `{"session_id":"1792275014518_yyjbm","cwd":"OTHER"}`, `{"sessionId":"1792275014518_yyjbm","messages":[{}]}`, false},
		{"missing messages", "1792275014518_yyjbm", `{"session_id":"1792275014518_yyjbm","cwd":"WORKSPACE"}`, "", false},
		{"wrong message identity", "1792275014518_yyjbm", `{"session_id":"1792275014518_yyjbm","cwd":"WORKSPACE"}`, `{"sessionId":"other","messages":[{}]}`, false},
		{"empty history", "1792275014518_yyjbm", `{"session_id":"1792275014518_yyjbm","cwd":"WORKSPACE"}`, `{"sessionId":"1792275014518_yyjbm","messages":[]}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			dir := filepath.Join(home, ".cline", "data", "sessions", tt.id)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if tt.manifest != "" {
				writeClineHistoryFixture(t, filepath.Join(dir, tt.id+".json"), tt.manifest, workspace)
			}
			if tt.messages != "" {
				writeClineHistoryFixture(t, filepath.Join(dir, tt.id+".messages.json"), tt.messages, workspace)
			}
			got, err := probe.NativeConversationExists(context.Background(), ports.SessionRef{WorkspacePath: workspace}, tt.id, map[string]string{"HOME": home, "USERPROFILE": home})
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("history exists = %v, want %v", got, tt.want)
			}
		})
	}
}

func writeClineHistoryFixture(t *testing.T, path, text, workspace string) {
	t.Helper()
	encoded, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	text = strings.ReplaceAll(text, `"WORKSPACE"`, string(encoded))
	other, err := json.Marshal(filepath.Dir(workspace))
	if err != nil {
		t.Fatal(err)
	}
	text = strings.ReplaceAll(text, `"OTHER"`, string(other))
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNativeConversationRejectsUnreadableClineHistory(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	id := "saved-root"
	dir := filepath.Join(home, ".cline", "data", "sessions", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(`{"session_id":`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := New().NativeConversationExists(context.Background(), ports.SessionRef{WorkspacePath: workspace}, id, map[string]string{"HOME": home, "USERPROFILE": home})
	if got || err == nil {
		t.Fatalf("malformed history accepted or hidden: exists=%v error=%v", got, err)
	}
}
