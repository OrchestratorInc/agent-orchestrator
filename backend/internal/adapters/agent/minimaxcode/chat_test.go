package minimaxcode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestChatProfileRefreshPreservesNativeHistoryAndSource(t *testing.T) {
	source, data := t.TempDir(), t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { t.Fatal(err) }
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil { t.Fatal(err) }
	}
	write(filepath.Join(source, "config.yaml"), "defaultModel: p/first\npermissionMode: auto\n")
	write(filepath.Join(source, "auth", "token"), "first-credential")
	plugin := New()
	env := map[string]string{"MINIMAX_DATA_DIR": source}
	profile, err := plugin.PrepareChatProfile(context.Background(), data, "chat-1", env)
	if err != nil { t.Fatal(err) }
	if profile == filepath.Join(data, "agents", adapterID, "chat-1") { t.Fatal("Chat adopted TUI history") }
	write(filepath.Join(profile, "v2", "history-proof"), "retained-native-history")
	write(filepath.Join(source, "config.yaml"), "defaultModel: p/second\n")
	write(filepath.Join(source, "auth", "token"), "rotated-credential")
	if _, err := plugin.PrepareChatProfile(context.Background(), data, "chat-1", env); err != nil { t.Fatal(err) }
	for _, tc := range []struct{ path, want string }{
		{filepath.Join(profile, "config.yaml"), "defaultModel: p/second\n"},
		{filepath.Join(profile, "auth", "token"), "rotated-credential"},
		{filepath.Join(profile, "v2", "history-proof"), "retained-native-history"},
		{filepath.Join(source, "config.yaml"), "defaultModel: p/second\n"},
	} {
		b, err := os.ReadFile(tc.path)
		if err != nil || string(b) != tc.want { t.Fatalf("private profile/source mismatch: %v", err) }
	}
	if _, err := os.Stat(filepath.Join(profile, "plugins", "ao-activity")); !os.IsNotExist(err) { t.Fatal("Chat installed terminal hooks") }
}

func TestChatProfileRejectsUnownedAndSymlinkedCredentials(t *testing.T) {
	source, data := t.TempDir(), t.TempDir()
	env := map[string]string{"MINIMAX_DATA_DIR": source}
	if err := os.Symlink(t.TempDir(), filepath.Join(source, "auth")); err != nil { t.Fatal(err) }
	if _, err := New().PrepareChatProfile(context.Background(), data, "chat-1", env); err == nil { t.Fatal("accepted symlinked credentials") }
	if err := os.MkdirAll(filepath.Join(data, "agents", adapterID, "chat", "chat-2"), 0o700); err != nil { t.Fatal(err) }
	if _, err := New().PrepareChatProfile(context.Background(), data, "chat-2", env); err == nil { t.Fatal("adopted unowned profile") }
}
