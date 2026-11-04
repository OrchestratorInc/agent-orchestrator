package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuggieListedNativeHistory(t *testing.T) {
	root := t.TempDir()
	id := "11111111-2222-4333-8444-555555555555"
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"sessionId":"` + id + `","created":"2026-11-03T00:00:00Z","modified":"2026-11-03T00:05:00Z","agentState":{"modelId":"native-model"},"chatHistory":[{"exchange":{"request_message":"fallback prompt","request_nodes":[{"type":4,"ide_state_node":{"workspace_folders":[{"repository_root":"/work/repo"}]}},{"type":0,"text_node":{"content":"fix widget"}},{"type":2,"content":"tool noise"}],"response_nodes":[{"type":0,"content":"fixed"},{"type":2,"content":"hidden"}]}},{"isHistorySummary":true,"exchange":{"request_message":"injected summary","response_text":"summary"}}]}`
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	listed := []byte(`[{"sessionId":"` + id + `","workspaceRoot":"/work/repo","modified":"2026-11-03T00:05:00Z"},{"sessionId":"` + id + `","workspaceRoot":"/work/repo"},{"sessionId":"../escape","workspaceRoot":"/work/repo"}]`)
	files, err := auggieListedSources(root, listed)
	if err != nil || len(files) != 1 || files[0].nativeID() != id {
		t.Fatalf("listed=%+v err=%v", files, err)
	}
	got, err := readAuggieTranscript(context.Background(), root, path)
	if err != nil || got.cwd != "/work/repo" || got.title != "fix widget" || got.model != "native-model" || len(got.messages) != 2 || got.messages[1].Text != "fixed" {
		t.Fatalf("native=%+v err=%v", got, err)
	}
	for _, marker := range []string{`"isCloudAgent":true,`, `"poseidonAgentId":"remote",`, `"parentConversationId":"parent",`} {
		cloud := strings.Replace(body, `"created":`, marker+`"created":`, 1)
		if err := os.WriteFile(path, []byte(cloud), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readAuggieTranscript(context.Background(), root, path); err == nil {
			t.Fatalf("accepted remote/subagent marker %s", marker)
		}
	}
	if _, err := auggieListedSources(root, []byte("not JSON")); err == nil {
		t.Fatal("accepted malformed provider list")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := discoverAuggie(ctx, root); err == nil {
		t.Fatal("ignored cancelled discovery")
	}
}
