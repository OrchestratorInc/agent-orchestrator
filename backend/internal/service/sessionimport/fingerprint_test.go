package sessionimport

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestNativeFingerprintPinsSelectionAndFirstOpen(t *testing.T) {
	for _, change := range []string{"before import", "during import", "stable", "unset"} {
		t.Run(change, func(t *testing.T) {
			old := harnessImporters[domain.HarnessGemini]
			t.Cleanup(func() { harnessImporters[domain.HarnessGemini] = old })
			root, cwd := canonical(t.TempDir()), canonical(t.TempDir())
			currentCWD := cwd
			path := filepath.Join(root, "synthetic-native.jsonl")
			fingerprint := "synthetic-content-and-wal-v1"
			if change == "unset" {
				fingerprint = ""
			}
			reads := 0
			harnessImporters[domain.HarnessGemini] = harnessImporter{
				root: func(string) string { return root },
				discover: func(context.Context, string) ([]sourceFiles, error) {
					return []sourceFiles{{harness: domain.HarnessGemini, root: root, paths: []string{path}, key: "native", fingerprint: fingerprint}}, nil
				},
				read: func(context.Context, string, ...string) (transcript, error) {
					reads++
					if reads == 2 && (change == "during import" || change == "unset") {
						fingerprint = "synthetic-content-and-wal-v2"
					}
					return transcript{harness: domain.HarnessGemini, root: root, path: path, paths: []string{path}, nativeID: "native", cwd: currentCWD, last: time.Now(), messages: []Message{{Role: domain.MessageRoleUser, Text: "synthetic native history"}}}, nil
				},
			}
			st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: cwd}}}
			svc := New(st, "", nil)
			svc.roots = map[domain.AgentHarness]string{domain.HarnessGemini: root}
			preview, err := svc.Scan(context.Background())
			if err != nil || len(preview.Candidates) != 1 {
				t.Fatalf("preview = %#v, %v", preview, err)
			}
			if change == "before import" {
				fingerprint = "synthetic-content-and-wal-v2"
			}
			results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
			if err != nil || len(results) != 1 {
				t.Fatalf("import = %#v, %v", results, err)
			}
			if change == "before import" || change == "during import" {
				if results[0].Status != "failed" || len(st.sessions) != 0 {
					t.Fatalf("changed native input was registered: %#v", results)
				}
				return
			}
			if results[0].Status != "created" || len(st.sessions) != 1 {
				t.Fatalf("stable native import = %#v", results)
			}
			rec := st.sessions[0]
			if change == "unset" {
				if rec.Metadata.ImportSource.Fingerprint != "" {
					t.Fatal("unset importer acquired a fingerprint contract")
				}
				return
			}
			if rec.Metadata.ImportSource.Fingerprint != "synthetic-content-and-wal-v1" {
				t.Fatalf("selection evidence was not persisted: %#v", rec.Metadata.ImportSource)
			}
		})
	}
}
