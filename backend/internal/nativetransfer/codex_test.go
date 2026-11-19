package nativetransfer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

const codexID = "01a1f2eb-1c10-7462-9afc-a0787b4e965c"

func codexRolloutText(cwd string) string {
	return `{"timestamp":"2026-11-19T08:27:37.890Z","type":"session_meta","payload":{"id":"` + codexID + `","timestamp":"2026-11-19T08:27:36.895Z","cwd":"` + cwd + `"}}` + "\n" +
		`{"type":"turn_context","payload":{"cwd":"` + cwd + `","model":"gpt"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","content":[{"type":"input_text","text":"edit ` + cwd + `/a.go"}]}}` + "\n"
}

func TestWriteCodexRolloutForCloudUsesThePlaceholder(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		home := t.TempDir()
		text := codexRolloutText("/Users/me/work/app")
		path := filepath.Join(home, "sessions", "2026", "11", "19", "rollout-2026-11-19T13-57-36-"+codexID+".jsonl")
		data := []byte(text)
		if compressed {
			enc, _ := zstd.NewWriter(nil)
			data, path = enc.EncodeAll(data, nil), path+".zst"
		}
		writeFile(t, path, string(data))
		var out bytes.Buffer
		id, err := WriteCodexRolloutForCloud(context.Background(), &out, CodexOptions{Home: home, NativeID: codexID}, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if id != codexID {
			t.Fatalf("id = %q", id)
		}
		if got := out.String(); strings.Contains(got, "/Users/me") || strings.Count(got, CloudCwdPlaceholder) != 3 {
			t.Errorf("compressed=%v: rollout for Cloud:\n%s", compressed, got)
		}
		if _, err := WriteCodexRolloutForCloud(context.Background(), &out, CodexOptions{Home: home, NativeID: codexID}, 100); err == nil {
			t.Error("a rollout over the limit was written")
		}
	}
}

func TestImportCodexPlacesTheRolloutWhereCodexResumesIt(t *testing.T) {
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	from := filepath.Join(t.TempDir(), "in.jsonl")
	writeFile(t, from, codexRolloutText("/workspace/repository"))
	placed := func(got []byte) bool {
		return strings.Count(string(got), cwd) == 3 && !strings.Contains(string(got), "/workspace")
	}

	t.Run("new", func(t *testing.T) {
		home := t.TempDir()
		id, err := ImportCodex(context.Background(), from, CodexImportOptions{Home: home, Cwd: cwd})
		if err != nil || id != codexID {
			t.Fatalf("ImportCodex = %q, %v", id, err)
		}
		got, err := os.ReadFile(filepath.Join(home, "sessions", "2026", "11", "19", "rollout-2026-11-19T08-27-36-"+codexID+".jsonl"))
		if err != nil || !placed(got) {
			t.Fatalf("placed rollout = %s, %v", got, err)
		}
	})
	for _, ext := range []string{".jsonl", ".jsonl.zst"} {
		t.Run("replaces "+ext, func(t *testing.T) {
			home := t.TempDir()
			old := filepath.Join(home, "sessions", "2026", "10", "02", "rollout-2026-10-02T09-00-00-"+codexID)
			writeFile(t, old+ext, "old")
			if _, err := ImportCodex(context.Background(), from, CodexImportOptions{Home: home, Cwd: cwd}); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(old + ".jsonl")
			if err != nil || !placed(got) {
				t.Fatalf("placed rollout = %s, %v", got, err)
			}
			if _, err := os.Stat(old + ".jsonl.zst"); !os.IsNotExist(err) {
				t.Errorf("the compressed copy stayed: %v", err)
			}
		})
	}
}
