package minimaxcode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestChatProfileRefreshPreservesNativeHistoryAndSource(t *testing.T) {
	source, data := t.TempDir(), t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(source, "config.yaml"), "defaultModel: p/first\npermissionMode: auto\n")
	write(filepath.Join(source, "auth", "token"), "first-credential")
	globalPolicy := `{"version":2,"ask":[{"tool_name":"bash","matcher":{"kind":"tool"}}]}`
	write(filepath.Join(source, "permission.json"), globalPolicy)
	plugin := New()
	env := map[string]string{"MINIMAX_DATA_DIR": source}
	profile, err := plugin.PrepareChatProfile(context.Background(), data, "chat-1", env)
	if err != nil {
		t.Fatal(err)
	}
	if profile == filepath.Join(data, "agents", adapterID, "chat-1") {
		t.Fatal("Chat adopted TUI history")
	}
	policy, err := os.ReadFile(filepath.Join(profile, "permission.json"))
	if err != nil || string(policy) != globalPolicy {
		t.Fatal("Chat omitted native global permission policy")
	}
	privatePolicy := `{"version":2,"ask":[{"tool_name":"bash","matcher":{"kind":"tool"}}],"allow":[{"tool_name":"read","matcher":{"kind":"tool"}}]}`
	write(filepath.Join(profile, "permission.json"), privatePolicy)
	write(filepath.Join(profile, "v2", "history-proof"), "retained-native-history")
	write(filepath.Join(source, "config.yaml"), "defaultModel: p/second\n")
	write(filepath.Join(source, "auth", "token"), "rotated-credential")
	if _, err := plugin.PrepareChatProfile(context.Background(), data, "chat-1", env); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, want string }{
		{filepath.Join(profile, "config.yaml"), "defaultModel: p/second\n"},
		{filepath.Join(profile, "auth", "token"), "rotated-credential"},
		{filepath.Join(profile, "v2", "history-proof"), "retained-native-history"},
		{filepath.Join(profile, "permission.json"), privatePolicy},
		{filepath.Join(source, "permission.json"), globalPolicy},
		{filepath.Join(source, "config.yaml"), "defaultModel: p/second\n"},
	} {
		b, err := os.ReadFile(tc.path)
		if err != nil || string(b) != tc.want {
			t.Fatalf("private profile/source mismatch: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(profile, "plugins", "ao-activity")); !os.IsNotExist(err) {
		t.Fatal("Chat installed terminal hooks")
	}
}

func TestChatProfileRejectsUnownedAndSymlinkedCredentials(t *testing.T) {
	source, data := t.TempDir(), t.TempDir()
	env := map[string]string{"MINIMAX_DATA_DIR": source}
	if err := os.Symlink(t.TempDir(), filepath.Join(source, "auth")); err != nil {
		t.Fatal(err)
	}
	if _, err := New().PrepareChatProfile(context.Background(), data, "chat-1", env); err == nil {
		t.Fatal("accepted symlinked credentials")
	}
	if err := os.MkdirAll(filepath.Join(data, "agents", adapterID, "chat", "chat-2"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := New().PrepareChatProfile(context.Background(), data, "chat-2", env); err == nil {
		t.Fatal("adopted unowned profile")
	}
}

func TestChatProfileRejectsSymlinkedGlobalPermissionPolicy(t *testing.T) {
	source, data := t.TempDir(), t.TempDir()
	policy := filepath.Join(t.TempDir(), "permission.json")
	if err := os.WriteFile(policy, []byte(`{"deny":["bash"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(policy, filepath.Join(source, "permission.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := New().PrepareChatProfile(context.Background(), data, "chat-policy", map[string]string{"MINIMAX_DATA_DIR": source}); err == nil {
		t.Fatal("Chat accepted a symlinked global permission policy")
	}
}

func TestChatHistoryPreflightPreservesExactHistory(t *testing.T) {
	for _, kind := range []string{"valid", "missing", "corrupt", "workspace", "unowned", "empty-new", "empty-started"} {
		t.Run(kind, func(t *testing.T) {
			fixture, workspace, id := historyFixture(t)
			data := t.TempDir()
			parent := filepath.Join(data, "agents", adapterID, "chat")
			if err := os.MkdirAll(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			profile := filepath.Join(parent, "chat-history")
			if err := os.Rename(fixture, profile); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(profile, ".ao-managed"), []byte(chatProfileMarker), 0o600); err != nil {
				t.Fatal(err)
			}
			messages := filepath.Join(profile, "v2", "sessions", "exact", "messages.jsonl")
			switch kind {
			case "empty-new", "empty-started":
				if err := os.WriteFile(messages, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(messages); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(messages, []byte("broken\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "workspace":
				workspace = t.TempDir()
			case "unowned":
				if err := os.Remove(filepath.Join(profile, ".ao-managed")); err != nil {
					t.Fatal(err)
				}
			}
			before, beforeErr := os.ReadFile(messages)
			err := New().ValidateChatHistory(context.Background(), ports.ChatResumeConfig{DataDir: data, SessionID: "chat-history", ProviderConversationID: id, WorkspacePath: workspace, HasPriorTurns: kind != "empty-new"})
			if (err == nil) != (kind == "valid" || kind == "empty-new") {
				t.Fatalf("history preflight %s: %v", kind, err)
			}
			after, afterErr := os.ReadFile(messages)
			if string(before) != string(after) || (beforeErr == nil) != (afterErr == nil) {
				t.Fatal("preflight changed native history")
			}
		})
	}
}
