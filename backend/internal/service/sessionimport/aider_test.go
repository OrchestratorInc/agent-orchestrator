package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/aider"
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
