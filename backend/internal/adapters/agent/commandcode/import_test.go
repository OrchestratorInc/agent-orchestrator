package commandcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const commandImportUser = `{"type":"message","id":"u","parentId":null,"timestamp":"2026-11-03T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"synthetic request"}]}}` + "\n"

func commandImportHeader(t *testing.T, id string) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"type": "session", "version": 3, "id": id, "cwd": t.TempDir(), "timestamp": "2026-11-03T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return string(header) + "\n"
}

func TestImportedCommandCodeCopyAndStrictResume(t *testing.T) {
	home := isolatedHome(t)
	root := filepath.Join(home, ".commandcode")
	id := "11111111-2222-4333-8444-555555555555"
	source := filepath.Join(root, "projects", "old-repo", id+".jsonl")
	body := []byte(commandImportHeader(t, id) + commandImportUser)
	writeHomeFile(t, source, body)
	writeHomeFile(t, filepath.Join(filepath.Dir(source), id+".meta.json"), []byte(`{"title":"synthetic"}`))
	cwd := filepath.Join(t.TempDir(), "AORepo")
	makeHomeDir(t, cwd)
	r := ports.ImportedSessionRelocation{ConfigDir: root, NativeSessionID: id, TranscriptPath: source, SourceCWD: "/old/repo", LaunchCWD: cwd}
	p := New()
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	bucket, err := commandNativeProjectBucket(cwd)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(root, "projects", bucket, id+".jsonl")
	if got := readHomeFile(t, source); string(got) != string(body) {
		t.Fatal("source changed")
	}
	if got := readHomeFile(t, copyPath); string(got) != string(body) {
		t.Fatal("native copy changed history or saved cwd")
	}
	if info, err := os.Stat(copyPath); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("private copy mode: info=%v err=%v", info, err)
	}
	data := t.TempDir()
	prepareHome(t, data, "imported", "AO instructions")
	if err := verifyImportedCommandResume(context.Background(), data, "imported", cwd, id); err == nil {
		t.Fatal("missing isolated history allowed fresh resume")
	}
	MigrateNativeHistory(data, "imported", id)
	if err := verifyImportedCommandResume(context.Background(), data, "imported", cwd, id); err != nil {
		t.Fatal(err)
	}
	updated := append(append([]byte(nil), body...), []byte(`{"type":"message","id":"a","parentId":"u","timestamp":"2026-11-03T00:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"AO turn"}]}}`+"\n")...)
	writeHomeFile(t, copyPath, updated)
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if string(readHomeFile(t, copyPath)) != string(updated) {
		t.Fatal("retry overwrote existing AO turns")
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatalf("valid prepared copy depended on removed source: %v", err)
	}
	if string(readHomeFile(t, copyPath)) != string(updated) {
		t.Fatal("source removal changed the prepared conversation")
	}
	writeHomeFile(t, copyPath, []byte("corrupt prepared history"))
	if err := p.RelocateImportedSession(context.Background(), r); err == nil {
		t.Fatal("accepted corrupt prepared history after source removal")
	}
	r.NativeSessionID = "../escape"
	if err := p.RelocateImportedSession(context.Background(), r); err == nil {
		t.Fatal("accepted invalid native id")
	}
}

func TestCommandCodeBucketMatchesProviderASCII(t *testing.T) {
	for cwd, want := range map[string]string{"/work/AORepo": "work-ao-repo", "/work/HTTP2/APIs": "work-http-2-apis", "/work/my_project": "work-my-project"} {
		if runtime.GOOS == "windows" {
			cwd = "C:" + cwd
			want = "c-" + want
		}
		got, err := commandProjectBucket(cwd)
		if err != nil || got != want {
			t.Fatalf("bucket(%q)=%q err=%v want=%q", cwd, got, err, want)
		}
	}
	if _, err := commandProjectBucket(filepath.Join(t.TempDir(), "café")); err == nil {
		t.Fatal("guessed unsupported transliteration")
	}
}

func TestCommandCodeRefusesUnreadableNativeBundle(t *testing.T) {
	root := t.TempDir()
	id := "11111111-2222-4333-8444-555555555555"
	dir := filepath.Join(root, "projects", "source")
	path := filepath.Join(dir, id+".jsonl")
	cwd := t.TempDir()
	r := ports.ImportedSessionRelocation{ConfigDir: root, NativeSessionID: id, TranscriptPath: path, LaunchCWD: cwd}
	header := commandImportHeader(t, id)
	for _, body := range []string{
		commandImportHeader(t, "other") + commandImportUser,
		`{"type":"session","version":3,"id":"` + id + `"}` + "\n" + commandImportUser,
		header,
		header + `{"type":"message","id":"u","parentId":null,"message":{"role":"user","content":"synthetic"}}` + "\n",
		header + commandImportUser + `{"torn":`,
		header + commandImportUser + `{"type":"message","id":"orphan","parentId":null,"timestamp":"2026-11-03T00:00:00Z","message":{"role":"assistant","content":"unrelated"}}` + "\n",
	} {
		writeHomeFile(t, path, []byte(body))
		if err := New().RelocateImportedSession(context.Background(), r); err == nil {
			t.Fatalf("accepted unreadable native history: %s", body)
		}
	}
	writeHomeFile(t, path, []byte(header+commandImportUser))
	bucket, err := commandNativeProjectBucket(cwd)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "projects", bucket, "foreign.jsonl")
	writeHomeFile(t, foreign, []byte("other native conversation"))
	if err := New().RelocateImportedSession(context.Background(), r); err == nil {
		t.Fatal("replaced an existing destination project")
	}
	if string(readHomeFile(t, foreign)) != "other native conversation" {
		t.Fatal("destination changed after refusal")
	}
}

func TestCommandCodeRelocationUsesPhysicalWorkspace(t *testing.T) {
	root, physicalWorkspace := t.TempDir(), t.TempDir()
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(physicalWorkspace, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	id := "11111111-2222-4333-8444-555555555555"
	source := filepath.Join(root, "projects", "source", id+".jsonl")
	writeHomeFile(t, source, []byte(commandImportHeader(t, id)+commandImportUser))
	r := ports.ImportedSessionRelocation{ConfigDir: root, NativeSessionID: id, TranscriptPath: source, LaunchCWD: alias}
	if err := New().RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	bucket, err := commandNativeProjectBucket(physicalWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "projects", bucket, id+".jsonl")); err != nil {
		t.Fatalf("native physical-cwd bucket missing: %v", err)
	}
}

func TestCommandCodeAdoptedImportCannotReseedMissingHistory(t *testing.T) {
	home := isolatedHome(t)
	root, cwd, data := filepath.Join(home, ".commandcode"), t.TempDir(), t.TempDir()
	id := "11111111-2222-4333-8444-555555555555"
	source := filepath.Join(root, "projects", "source", id+".jsonl")
	writeHomeFile(t, source, []byte(commandImportHeader(t, id)+commandImportUser))
	p := New()
	if err := p.RelocateImportedSession(context.Background(), ports.ImportedSessionRelocation{ConfigDir: root, NativeSessionID: id, TranscriptPath: source, LaunchCWD: cwd}); err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceHookConfig{DataDir: data, SessionID: "imported", WorkspacePath: cwd, NativeSessionID: id, RestoreMode: ports.WorkspaceRestoreImported, ImportConfigDir: root}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	cfg.RestoreMode = ports.WorkspaceRestoreNative
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	bucket, err := commandNativeProjectBucket(cwd)
	if err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(SessionHomeDir(data, "imported"), ".commandcode", "projects", bucket, id+".jsonl")
	if err := os.Remove(private); err != nil {
		t.Fatal(err)
	}
	if err := p.GetAgentHooks(context.Background(), cfg); err == nil {
		t.Fatal("adopted import accepted missing native history")
	}
	if _, err := os.Stat(private); !os.IsNotExist(err) {
		t.Fatalf("adopted import reseeded old history: %v", err)
	}
}
