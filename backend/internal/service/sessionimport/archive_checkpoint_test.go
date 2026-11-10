package sessionimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestRefreshArchiveRetainsPreparedNativeSnapshotOnRetry(t *testing.T) {
	for _, checkpoint := range []string{"native id", "launch cwd", "both"} {
		t.Run(checkpoint, func(t *testing.T) {
			ctx := context.Background()
			root, private := t.TempDir(), t.TempDir()
			id := "66666666-6666-4666-8666-666666666666"
			path := writeClaude(t, root, id, "", "copied native turn", time.Now().Add(-time.Hour))
			store := &importStore{}
			service := New(store, "", chatSupported)
			source := &domain.SessionImportSource{NativeID: id, ConfigDir: root, Prepared: true}
			record := domain.SessionRecord{ID: "import-1", Harness: domain.HarnessClaudeCode, Metadata: domain.SessionMetadata{NativeTranscriptPath: path, ImportSource: source}}
			// Workspace transfer alone must still allow the initial refresh.
			if err := service.RefreshArchive(ctx, record); err != nil || len(store.messages) != 1 {
				t.Fatalf("initial archive = %#v, %v", store.messages, err)
			}
			copied, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			privatePath := filepath.Join(private, "prepared.jsonl")
			if err := os.WriteFile(privatePath, copied, 0o600); err != nil {
				t.Fatal(err)
			}
			source.ResumeConfigDir, source.ResumeTranscriptPath = private, privatePath
			if checkpoint != "launch cwd" {
				source.ResumeNativeID = "private-native-id"
			}
			if checkpoint != "native id" {
				source.ResumeCwd = t.TempDir()
			}
			record.Metadata.NativeTranscriptPath = privatePath
			later, err := json.Marshal(map[string]any{"type": "user", "sessionId": id, "timestamp": time.Now(), "message": map[string]any{"role": "user", "content": "original-only turn after preparation"}})
			if err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			_, writeErr := file.Write(append(later, '\n'))
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				t.Fatalf("append synthetic source: %v, close: %v", writeErr, closeErr)
			}
			// A failed runtime start leaves the checkpoint pending. Retry
			// must display the archive that belongs to its pinned native copy.
			if err := service.RefreshArchive(ctx, record); err != nil {
				t.Fatal(err)
			}
			wantTexts(t, store.messages, "user: copied native turn")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := service.RefreshArchive(ctx, record); err != nil {
				t.Fatalf("prepared retry reread removed original source: %v", err)
			}
			wantTexts(t, store.messages, "user: copied native turn")
			after, err := os.ReadFile(privatePath)
			if err != nil || string(after) != string(copied) {
				t.Fatalf("prepared native copy changed: %v", err)
			}
		})
	}
}
