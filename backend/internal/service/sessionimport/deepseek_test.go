package sessionimport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDeepSeekImportsEligibleNativeHistoryAsChat(t *testing.T) {
	for _, id := range []string{"00000000-0000-4000-8000-000000000522", "session-00000000-0000-4000-8000-000000000522"} {
		for _, compressed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/compressed=%v", id, compressed), func(t *testing.T) {
				root, cwd := canonical(t.TempDir()), canonical(t.TempDir())
				path := filepath.Join(root, "sessions", "project", id, "session.v4.jsonl")
				header, err := json.Marshal(map[string]any{"type": "session", "version": 4, "id": id, "cwd": cwd, "createdAt": 1790000000000, "isSeeded": false, "delegationDepth": 0})
				if err != nil {
					t.Fatal(err)
				}
				body := `{"type":"user/message","seq":0,"time":1790000000000,"data":{"role":"user","source":{"kind":"user"},"content":[{"type":"text","text":"native marker"}]},"surfaceOp":"append"}` + "\n"
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				original := append(append(header, '\n'), []byte(body)...)
				if compressed {
					path += ".zstd"
					enc, err := zstd.NewWriter(nil, zstd.WithEncoderCRC(true))
					if err != nil {
						t.Fatal(err)
					}
					original = enc.EncodeAll(original, nil)
					enc.Close()
				}
				if err := os.WriteFile(path, original, 0o600); err != nil {
					t.Fatal(err)
				}
				st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: cwd}}}
				svc := New(st, "", chatSupported)
				svc.home = canonical(t.TempDir())
				svc.now = func() time.Time { return time.UnixMilli(1790000001000) }
				svc.roots = map[domain.AgentHarness]string{domain.HarnessDeepSeek: root}
				preview, err := svc.Scan(context.Background())
				if err != nil || len(preview.Candidates) != 1 {
					t.Fatalf("scan=%+v,%v", preview, err)
				}
				results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
				if err != nil || len(results) != 1 || results[0].Status != "created" {
					t.Fatalf("import=%+v,%v", results, err)
				}
				if st.sessions[0].Mode != domain.SessionModeChat || st.sessions[0].Metadata.ProviderConversationID != id || !st.sessions[0].NeedsImportResume() {
					t.Fatalf("imported record=%+v", st.sessions[0])
				}
				wantTexts(t, st.messages, "user: native marker")
				if st.sessions[0].Metadata.ImportSource.NativeID != id {
					t.Fatal("source provenance rewrote the native ID")
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(original, after) {
					t.Fatalf("source changed after scan/import: %v", err)
				}

				// A future generation must invalidate discovery before cached text is reused.
				writeFile(t, filepath.Join(filepath.Dir(path), "session.v5.jsonl"), "unknown")
				preview, err = svc.Scan(context.Background())
				if err != nil || len(preview.Candidates) != 0 {
					t.Fatalf("offered unknown generation=%+v,%v", preview, err)
				}
			})
		}
	}
}

func TestDeepSeekDiscoverySkipsNonDirectorySessions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sessions"), "regular file")
	files, err := discoverDeepSeek(context.Background(), root)
	if err != nil || len(files) != 0 {
		t.Fatalf("special sessions entry=%v,%v", files, err)
	}
}
