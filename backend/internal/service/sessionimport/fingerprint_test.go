package sessionimport

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestNativeFingerprintPinsSelection(t *testing.T) {
	for _, change := range []string{"changed", "stable", "unset"} {
		t.Run(change, func(t *testing.T) {
			old := harnessImporters[domain.HarnessGemini]
			t.Cleanup(func() { harnessImporters[domain.HarnessGemini] = old })
			root, cwd := canonical(t.TempDir()), canonical(t.TempDir())
			path := filepath.Join(root, "synthetic-native.jsonl")
			fingerprint := "synthetic-content-and-wal-v1"
			if change == "unset" {
				fingerprint = ""
			}
			harnessImporters[domain.HarnessGemini] = harnessImporter{
				root: func(string) string { return root },
				discover: func(context.Context, string) ([]sourceFiles, error) {
					return []sourceFiles{{harness: domain.HarnessGemini, root: root, paths: []string{path}, key: "native"}}, nil
				},
				read: func(context.Context, string, ...string) (transcript, error) {
					return transcript{harness: domain.HarnessGemini, root: root, path: path, paths: []string{path}, nativeID: "native", cwd: cwd, last: time.Now(), fingerprint: fingerprint, messages: []Message{{Role: domain.MessageRoleUser, Text: "synthetic native history"}}}, nil
				},
			}
			st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: cwd}}}
			svc := New(st, "", nil)
			svc.roots = map[domain.AgentHarness]string{domain.HarnessGemini: root}
			scanAndImport := func() Result {
				t.Helper()
				preview, err := svc.Scan(context.Background())
				if err != nil || len(preview.Candidates) != 1 {
					t.Fatalf("preview = %#v, %v", preview, err)
				}
				if change == "changed" {
					fingerprint = "synthetic-content-and-wal-v2"
				}
				results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
				if err != nil || len(results) != 1 {
					t.Fatalf("import = %#v, %v", results, err)
				}
				return results[0]
			}
			result := scanAndImport()
			if change == "changed" {
				if result.Status != "failed" || len(st.sessions) != 0 {
					t.Fatalf("changed native input was registered: %#v", result)
				}
				result = scanAndImport()
			}
			if result.Status != "created" || len(st.sessions) != 1 {
				t.Fatalf("import = %#v", result)
			}
			if got := st.sessions[0].Metadata.ImportSource.Fingerprint; got != fingerprint {
				t.Fatalf("persisted fingerprint %q, want %q", got, fingerprint)
			}
		})
	}
}
