package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/aider"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAiderProjectHistoryDiscoveryAndNativeMessages(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, aiderImportHistory), "# aider chat started\n> model configuration\n\n#### make a greeting\n#### with two lines\n\nHello\n> secret tool output\n#### next turn\n\nDone\n")
	groups, err := discoverAiderProject(context.Background(), project)
	if err != nil || len(groups) != 1 {
		t.Fatalf("discover = %+v, %v", groups, err)
	}
	if groups[0].key != aider.NativeHistoryID(project) || groups[0].root != canonical(project) {
		t.Fatalf("project identity = %+v", groups[0])
	}
	got, err := readAiderTranscript(context.Background(), groups[0].root, groups[0].paths...)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: make a greeting\nwith two lines", "assistant: Hello", "user: next turn", "assistant: Done")
	if got.nativeID != groups[0].key || got.cwd != canonical(project) || got.title != "make a greeting with two lines" {
		t.Fatalf("transcript = %+v", got)
	}
}

func TestAiderRegisteredProjectScanImportsNativeTerminalHistory(t *testing.T) {
	project, unrelated := t.TempDir(), t.TempDir()
	path := writeFile(t, filepath.Join(project, aiderImportHistory), "#### make a greeting\n\nHello\n")
	writeFile(t, filepath.Join(unrelated, aiderImportHistory), "#### never scan this repository\n")
	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
	svc := New(st, "", chatSupported)
	svc.roots = map[domain.AgentHarness]string{}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Harness != domain.HarnessAider || preview.Candidates[0].ProjectID != "repo" {
		t.Fatalf("registered project scan = %+v, %v", preview, err)
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %+v, %v", results, err)
	}
	rec := st.sessions[0]
	if rec.Mode != domain.SessionModeTUI || rec.Metadata.AgentSessionID != aider.NativeHistoryID(project) || rec.Metadata.ImportSource.ConfigDir != canonical(project) || !rec.NeedsImportResume() {
		t.Fatalf("native Terminal record = %+v", rec)
	}
	writeFile(t, path, "#### make a greeting\n\nHello\n#### a later turn\n\nDone\n")
	if err := svc.RefreshArchive(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	wantTexts(t, st.messages, "user: make a greeting", "assistant: Hello", "user: a later turn", "assistant: Done")
}

func TestAiderDiscoveryDoesNotSearchNestedOrEscapingFiles(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "nested", aiderImportHistory), "#### nested private chat\n")
	groups, err := discoverAiderProject(context.Background(), project)
	if err != nil || len(groups) != 0 {
		t.Fatalf("searched nested history: %+v, %v", groups, err)
	}
	outside := writeFile(t, filepath.Join(t.TempDir(), aiderImportHistory), "#### outside private chat\n")
	if err := os.Symlink(outside, filepath.Join(project, aiderImportHistory)); err != nil {
		t.Skip(err)
	}
	groups, err = discoverAiderProject(context.Background(), project)
	if err != nil || len(groups) != 0 {
		t.Fatalf("offered escaping history: %+v, %v", groups, err)
	}
	if _, err := readAiderTranscript(context.Background(), project, filepath.Join(project, aiderImportHistory)); err == nil {
		t.Fatal("read escaping history")
	}
}

func TestAiderToolOnlyHistoryIsNotResumable(t *testing.T) {
	project := t.TempDir()
	path := writeFile(t, filepath.Join(project, aiderImportHistory), "# aider chat started\n> tool-only output\n")
	if _, err := readAiderTranscript(context.Background(), project, path); err == nil {
		t.Fatal("accepted history without a native user message")
	}
}
