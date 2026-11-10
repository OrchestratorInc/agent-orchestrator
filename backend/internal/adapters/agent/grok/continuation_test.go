package grok

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestImportRelocationKeepsSourceAndExistingCopy(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	id := "00000000-0000-7000-8000-000000000001"
	src := filepath.Join(root, "sessions", "source", id)
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{"info":{"id":"` + id + `","cwd":"/source"},"generated_title":"Synthetic"}`
	for name, data := range map[string]string{"summary.json": original, "updates.jsonl": "{}\n", "chat_history.jsonl": "{}\n"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	checkpointDir := filepath.Join(src, "compaction_checkpoints")
	if err := os.MkdirAll(checkpointDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkpointDir, "checkpoint.json"), []byte(`{"synthetic":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "workspace-link")
	launch := workspace
	if err := os.Symlink(workspace, link); err == nil {
		launch = link
	}
	cfg := ports.ImportedSessionRelocation{ConfigDir: root, NativeSessionID: id, TranscriptPath: filepath.Join(src, "updates.jsonl"), LaunchCWD: launch}
	if err := New().RelocateImportedSession(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "sessions", grokImportBucket(resolved), id)
	if _, err := os.Stat(filepath.Join(dest, "compaction_checkpoints", "checkpoint.json")); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dest, "summary.json"))
	if err != nil || !strings.Contains(string(body), resolved) {
		t.Fatalf("copy=%s err=%v", body, err)
	}
	body, err = os.ReadFile(filepath.Join(src, "summary.json"))
	if err != nil || string(body) != original {
		t.Fatal("source changed")
	}
	path := filepath.Join(dest, "updates.jsonl")
	if err := os.WriteFile(path, []byte("AO turn"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New().RelocateImportedSession(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil || string(body) != "AO turn" {
		t.Fatal("existing copy overwritten")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private copy mode: %v %v", info, err)
	}
}

func TestImportBucketAndMissingProbe(t *testing.T) {
	if got := grokImportBucket("/work/a b"); got != "%2Fwork%2Fa%20b" {
		t.Fatal(got)
	}
	got, err := New().ProbeNativeSession(context.Background(), ports.NativeSessionRef{ConfigDir: t.TempDir(), NativeSessionID: "00000000-0000-7000-8000-000000000001"})
	if err != nil || got != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("probe=%s err=%v", got, err)
	}
}
