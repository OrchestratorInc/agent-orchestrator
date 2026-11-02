package continueagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	continueSourceID = "11111111-2222-4333-8444-555555555555"
	continueForkID   = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	continueOtherID  = "00000000-1111-4222-8333-444444444444"
	continueHistory  = `[{"message":{"id":"message-user-1","role":"user","content":"make a greeting"},"contextItems":[]},{"message":{"id":"message-assistant-1","role":"assistant","content":"Hello"},"contextItems":[]}]`
)

func writeContinueNative(t *testing.T, root, id, workspace, history string) string {
	t.Helper()
	data, err := json.Marshal(struct {
		ID        string          `json:"sessionId"`
		Workspace string          `json:"workspaceDirectory"`
		History   json.RawMessage `json:"history"`
	}{id, workspace, json.RawMessage(history)})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestContinueForkProofAdoptsOnlyNewIDWithExactNativeHistoryAndWorktree(t *testing.T) {
	root, original, worktree := t.TempDir(), t.TempDir(), t.TempDir()
	writeContinueNative(t, root, continueSourceID, original, continueHistory)
	proof, err := prepareContinueFork(context.Background(), root, continueSourceID, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if id, found, err := proof.resolve(context.Background()); err != nil || found || id != "" {
		t.Fatalf("before launch = %q, %v, %v", id, found, err)
	}
	// The provider sets editorState while persisting the fork and omits system
	// entries. Identity matching follows that native transformation exactly.
	history := strings.Replace(continueHistory, `"contextItems":[]`, `"contextItems":[],"editorState":"make a greeting"`, 1)
	writeContinueNative(t, root, continueForkID, worktree, history)
	id, found, err := proof.resolve(context.Background())
	if err != nil || !found || id != continueForkID || id == continueSourceID {
		t.Fatalf("fork = %q, %v, %v", id, found, err)
	}
	path, found, err := (&Plugin{}).LocateTranscript(context.Background(), ports.NativeSessionRef{NativeSessionID: id, ConfigDir: root})
	if err != nil || !found || path != filepath.Join(root, "sessions", continueForkID+".json") {
		t.Fatalf("adopted history = %q, %v, %v", path, found, err)
	}
}

func TestContinueForkProofNeverSelectsOldIDOrDifferentHistoryOrWorkspace(t *testing.T) {
	for _, mode := range []string{"old-id", "wrong-workspace", "wrong-history", "torn-file"} {
		t.Run(mode, func(t *testing.T) {
			root, original, worktree := t.TempDir(), t.TempDir(), t.TempDir()
			writeContinueNative(t, root, continueSourceID, original, continueHistory)
			if mode == "old-id" {
				writeContinueNative(t, root, continueForkID, original, continueHistory)
			}
			proof, err := prepareContinueFork(context.Background(), root, continueSourceID, worktree)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "old-id":
				writeContinueNative(t, root, continueForkID, worktree, continueHistory)
			case "wrong-workspace":
				writeContinueNative(t, root, continueForkID, original, continueHistory)
			case "wrong-history":
				writeContinueNative(t, root, continueForkID, worktree, strings.Replace(continueHistory, "Hello", "Unrelated answer", 1))
			case "torn-file":
				if err := os.WriteFile(filepath.Join(root, "sessions", continueForkID+".json"), []byte(`{"sessionId":`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if id, found, err := proof.resolve(context.Background()); err != nil || found || id != "" {
				t.Fatalf("accepted unrelated fork = %q, %v, %v", id, found, err)
			}
		})
	}
}

func TestContinueForkProofFailsOnAmbiguousNewIDs(t *testing.T) {
	root, worktree := t.TempDir(), t.TempDir()
	writeContinueNative(t, root, continueSourceID, t.TempDir(), continueHistory)
	proof, err := prepareContinueFork(context.Background(), root, continueSourceID, worktree)
	if err != nil {
		t.Fatal(err)
	}
	writeContinueNative(t, root, continueForkID, worktree, continueHistory)
	writeContinueNative(t, root, continueOtherID, worktree, continueHistory)
	if _, found, err := proof.resolve(context.Background()); err == nil || found {
		t.Fatalf("ambiguous fork accepted = %v, %v", found, err)
	}
}

func TestContinueForkPreparationRequiresExactReadableNativeSource(t *testing.T) {
	root := t.TempDir()
	writeContinueNative(t, root, continueSourceID, t.TempDir(), continueHistory)
	if _, err := prepareContinueFork(context.Background(), root, "11111111", t.TempDir()); err == nil {
		t.Fatal("accepted a native id prefix")
	}
	if _, err := prepareContinueFork(context.Background(), root, continueForkID, t.TempDir()); err == nil {
		t.Fatal("accepted a missing fork source")
	}
	outside := writeContinueNative(t, t.TempDir(), continueForkID, t.TempDir(), continueHistory)
	if err := os.Symlink(outside, filepath.Join(root, "sessions", continueForkID+".json")); err != nil {
		t.Skip(err)
	}
	if _, err := prepareContinueFork(context.Background(), root, continueForkID, t.TempDir()); err == nil {
		t.Fatal("accepted escaping native history")
	}
}

func TestContinueHistoryDigestUsesProviderPersistenceRules(t *testing.T) {
	var history []map[string]any
	if err := json.Unmarshal([]byte(continueHistory), &history); err != nil {
		t.Fatal(err)
	}
	want, err := continueHistoryDigest(history)
	if err != nil {
		t.Fatal(err)
	}
	history = append([]map[string]any{{"message": map[string]any{"role": "system", "content": "rebuilt runtime instructions"}}}, history...)
	history[1]["editorState"] = "stale editor state"
	got, err := continueHistoryDigest(history)
	if err != nil || got != want {
		t.Fatalf("native persistence normalization = %x, %v", got, err)
	}
}
