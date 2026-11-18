package sessionimport

import (
	"context"
	"encoding/json"
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
	if err != nil || got.cwd != "/work/repo" || got.title != "fix widget" || len(got.messages) != 2 || got.messages[1].Text != "fixed" {
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

func TestAuggieHistoryRetainsOpeningAndNewestWithinBudget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		exchanges int
		textBytes int
		wantCount int
		shortened bool
	}{
		{name: "many-small-exchanges", exchanges: 1000, textBytes: 1, wantCount: 2002},
		{name: "overflow", exchanges: 2, textBytes: maxVisibleBytes / 2, wantCount: 4, shortened: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, cwd := t.TempDir(), t.TempDir()
			id := "11111111-2222-4333-8444-555555555555"
			history := []any{map[string]any{"exchange": map[string]any{
				"request_message": "opening", "response_text": "first",
				"request_nodes": []any{map[string]any{"type": 4, "ide_state_node": map[string]any{"workspace_folders": []any{map[string]any{"repository_root": cwd}}}}},
			}}}
			for range tc.exchanges {
				history = append(history, map[string]any{"exchange": map[string]any{"request_message": strings.Repeat("u", tc.textBytes), "response_text": "newest"}})
			}
			data, err := json.Marshal(map[string]any{"sessionId": id, "created": "2026-11-18T00:00:00Z", "modified": "2026-11-18T00:01:00Z", "chatHistory": history})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "sessions", id+".json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := readAuggieTranscript(context.Background(), root, path)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.messages) != tc.wantCount || got.shortened != tc.shortened || got.messages[0].Text != "opening" || got.messages[len(got.messages)-1].Text != "newest" {
				t.Fatalf("retention: count=%d shortened=%v", len(got.messages), got.shortened)
			}
			total := 0
			for _, message := range got.messages {
				total += len(message.Text)
			}
			if total > maxVisibleBytes {
				t.Fatalf("retained %d bytes exceeds budget", total)
			}
		})
	}
}
