package commandcode

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestImportedCommandCodeCopyAndStrictResume(t *testing.T) {
	home := isolatedHome(t)
	root := filepath.Join(home, ".commandcode")
	id := "11111111-2222-4333-8444-555555555555"
	source := filepath.Join(root, "projects", "old-repo", id+".jsonl")
	body := []byte(`{"type":"session","version":3,"id":"` + id + `","cwd":"/old/repo"}` + "\n" + `{"type":"message","id":"u","parentId":null,"message":{"role":"user","content":"synthetic request"}}` + "\n")
	writeHomeFile(t, source, body)
	writeHomeFile(t, filepath.Join(filepath.Dir(source), id+".meta.json"), []byte(`{"title":"synthetic"}`))
	cwd := filepath.Join(t.TempDir(), "AORepo")
	r := ports.ImportedSessionRelocation{ConfigDir: root, NativeSessionID: id, TranscriptPath: source, SourceCWD: "/old/repo", LaunchCWD: cwd}
	p := New()
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	bucket, err := commandProjectBucket(cwd)
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
	if err := PrepareNativeResume(context.Background(), data, "imported", cwd, id); err == nil {
		t.Fatal("missing isolated history allowed fresh resume")
	}
	MigrateNativeHistory(data, "imported", id)
	if err := PrepareNativeResume(context.Background(), data, "imported", cwd, id); err != nil {
		t.Fatal(err)
	}
	updated := append(append([]byte(nil), body...), []byte(`{"type":"message","id":"a","parentId":"u","message":{"role":"assistant","content":"AO turn"}}`+"\n")...)
	writeHomeFile(t, copyPath, updated)
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if string(readHomeFile(t, copyPath)) != string(updated) {
		t.Fatal("retry overwrote existing AO turns")
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
