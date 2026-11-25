package nativetransfer

import (
	"bytes"
	"context"
	"errors"
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

// A thread continued in segment files is refused both ways rather than moved
// without its later turns.
func TestCodexSegmentedThreadIsRefused(t *testing.T) {
	enc, _ := zstd.NewWriter(nil)
	day := func(home, d string) string { return filepath.Join(home, "sessions", "2026", "10", d) }
	segment := "rollout-2026-10-08T04-09-28-" + codexID + "_01a11885-8f9f-76d1-a6d0-3b60c45dc3a5.jsonl"
	layouts := map[string]func(home string){
		"compressed base and a later segment": func(home string) {
			writeFile(t, filepath.Join(day(home, "04"), "rollout-2026-10-04T01-41-32-"+codexID+".jsonl.zst"),
				string(enc.EncodeAll([]byte(codexRolloutText("/Users/me/work/app")), nil)))
			writeFile(t, filepath.Join(day(home, "08"), segment), codexRolloutText("/Users/me/work/app"))
		},
		"segment only": func(home string) {
			writeFile(t, filepath.Join(day(home, "08"), segment+".zst"), "x")
		},
	}
	cwd := t.TempDir()
	from := filepath.Join(t.TempDir(), "in.jsonl")
	writeFile(t, from, codexRolloutText("/workspace/repository"))
	for name, layout := range layouts {
		home := t.TempDir()
		layout(home)
		var out bytes.Buffer
		if _, err := WriteCodexRolloutForCloud(context.Background(), &out, CodexOptions{Home: home, NativeID: codexID}, 1<<20); !errors.Is(err, ErrSegmented) || out.Len() > 0 {
			t.Errorf("%s: export = %v with %d bytes, want ErrSegmented", name, err, out.Len())
		}
		if _, err := ImportCodex(context.Background(), from, CodexImportOptions{Home: home, Cwd: cwd}); !errors.Is(err, ErrSegmented) {
			t.Errorf("%s: import = %v, want ErrSegmented", name, err)
		}
		if _, err := os.Stat(filepath.Join(home, "sessions", "2026", "11")); !os.IsNotExist(err) {
			t.Errorf("%s: import placed a rollout beside the segments", name)
		}
	}
}

func TestRewriteOpenCodeExport(t *testing.T) {
	doc := `{"info":{"id":"ses_1","location":{"directory":"/Users/me/app"}},"messages":[{"text":"edit /Users/me/app/a.go, not /Users/me/app2/b.go <ok>"}]}`
	got, err := RewriteOpenCodeExport([]byte(doc), CloudCwdPlaceholder)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"directory":"/ao-moved/cwd"`, `edit /ao-moved/cwd/a.go, not /Users/me/app2/b.go <ok>`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("rewritten document lacks %s:\n%s", want, got)
		}
	}
	if _, err := RewriteOpenCodeExport([]byte(`{"info":{"id":"ses_1"}}`), CloudCwdPlaceholder); err == nil {
		t.Error("a document naming no directory was rewritten")
	}
}

// A rollout from AO Cloud that cannot be placed never costs the copy here:
// the earlier rollout, plain or compressed, stays exactly as it was.
func TestImportCodexKeepsTheEarlierCopyWhenItFails(t *testing.T) {
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	from := filepath.Join(t.TempDir(), "in.jsonl")
	// The second record names the sandbox's folder but is not JSON, so it cannot be rewritten.
	writeFile(t, from, codexRolloutText("/workspace/repository")+`{"type":"response_item","payload":"/workspace/repository/a.go`+"\n")
	for _, ext := range []string{".jsonl", ".jsonl.zst"} {
		t.Run(ext, func(t *testing.T) {
			home := t.TempDir()
			old := filepath.Join(home, "sessions", "2026", "10", "02", "rollout-2026-10-02T09-00-00-"+codexID)
			writeFile(t, old+ext, "the earlier conversation")
			if _, err := ImportCodex(context.Background(), from, CodexImportOptions{Home: home, Cwd: cwd}); err == nil {
				t.Fatal("an unreadable rollout was placed")
			}
			if got, err := os.ReadFile(old + ext); err != nil || string(got) != "the earlier conversation" {
				t.Fatalf("earlier copy = %q, %v", got, err)
			}
			entries, _ := os.ReadDir(filepath.Dir(old))
			if len(entries) != 1 {
				t.Fatalf("left %d files beside the earlier copy, want none", len(entries)-1)
			}
		})
	}
}
