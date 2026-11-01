package importcopy

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func setCWD(path string) RewriteLine {
	return func(line []byte) ([]byte, error) {
		if kind, _ := StringField(line, "type"); kind != "session" {
			return line, nil
		}
		return SetStringField(line, "cwd", path)
	}
}

func TestCopyOnceRewritesRecordsAndLeavesSourceUntouched(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source", "chat.jsonl")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "{\"type\":\"title\",\"title\":\"a <b> & c\"}\n" +
		"{\"type\":\"session\",\"id\":\"native\",\"cwd\":\"/work\"}\n" +
		"\n" +
		"{\"type\":\"message\",\"text\":\"keep <me> & cwd /work\"}"
	if err := os.WriteFile(src, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "target", "nested", "chat.jsonl")

	wrote, err := CopyOnce(src, dst, setCWD("/ao/worktree"), nil)
	if err != nil || !wrote {
		t.Fatalf("CopyOnce = %v, %v", wrote, err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"type\":\"title\",\"title\":\"a <b> & c\"}\n" +
		"{\"cwd\":\"/ao/worktree\",\"id\":\"native\",\"type\":\"session\"}\n" +
		"\n" +
		"{\"type\":\"message\",\"text\":\"keep <me> & cwd /work\"}"
	if string(got) != want {
		t.Fatalf("copy =\n%s\nwant\n%s", got, want)
	}
	if source, _ := os.ReadFile(src); string(source) != original {
		t.Fatal("the source session changed")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("copy mode = %v, want 0600", perm)
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dst), ".importcopy-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

// After the first resume the copy holds the provider's later turns, so a
// second open must never replace it with the original.
func TestCopyOnceKeepsAnExistingCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "chat.jsonl")
	dst := filepath.Join(dir, "copy.jsonl")
	if err := os.WriteFile(src, []byte("{\"type\":\"session\",\"cwd\":\"/work\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := "{\"type\":\"session\",\"cwd\":\"/ao\"}\n{\"type\":\"message\",\"text\":\"AO turn\"}\n"
	if err := os.WriteFile(dst, []byte(later), 0o600); err != nil {
		t.Fatal(err)
	}
	wrote, err := CopyOnce(src, dst, setCWD("/ao"), nil)
	if err != nil || wrote {
		t.Fatalf("CopyOnce = %v, %v; want kept copy", wrote, err)
	}
	if got, _ := os.ReadFile(dst); string(got) != later {
		t.Fatalf("existing copy replaced: %s", got)
	}
}

func TestCopyOnceWritesNothingWhenARecordCannotBeRewritten(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "chat.jsonl")
	dst := filepath.Join(dir, "out", "copy.jsonl")
	if err := os.WriteFile(src, []byte("{\"type\":\"session\",\"cwd\":\"/work\"}\nnot json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := CopyOnce(src, dst, func(line []byte) ([]byte, error) { return SetStringField(line, "cwd", "/ao") }, nil)
	if err == nil {
		t.Fatal("a torn record was copied")
	}
	if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a partial copy was left at the destination: %v", statErr)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dst), ".importcopy-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestCopyOnceMissingSource(t *testing.T) {
	dir := t.TempDir()
	if _, err := CopyOnce(filepath.Join(dir, "missing.jsonl"), filepath.Join(dir, "copy.jsonl"), setCWD("/ao"), nil); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want not exist", err)
	}
}

func TestStringFieldAndSetStringField(t *testing.T) {
	if value, ok := StringField([]byte(`{"cwd":"/work","n":1}`), "cwd"); !ok || value != "/work" {
		t.Fatalf("StringField = %q, %v", value, ok)
	}
	if _, ok := StringField([]byte(`{"n":1}`), "cwd"); ok {
		t.Fatal("missing field reported present")
	}
	if _, ok := StringField([]byte(`{"cwd":1}`), "cwd"); ok {
		t.Fatal("non-string field reported as a string")
	}
	if _, err := SetStringField([]byte(`[1]`), "cwd", "/ao"); err == nil {
		t.Fatal("non-object record accepted")
	}
	if _, err := SetStringField([]byte(`null`), "cwd", "/ao"); err == nil {
		t.Fatal("null record accepted")
	}
}

// An adapter refuses a transcript without the record its provider needs, and
// nothing is left behind for the provider to load.
func TestCopyOnceCheckRefusesTheCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "chat.jsonl")
	dst := filepath.Join(dir, "out", "copy.jsonl")
	if err := os.WriteFile(src, []byte("{\"type\":\"title\"}\n{\"type\":\"message\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := 0
	rewrite := func(line []byte) ([]byte, error) {
		if kind, _ := StringField(line, "type"); kind != "session" {
			return line, nil
		}
		sessions++
		return SetStringField(line, "cwd", "/ao")
	}
	missing := errors.New("no session record")
	check := func() error {
		if sessions != 1 {
			return missing
		}
		return nil
	}
	if _, err := CopyOnce(src, dst, rewrite, check); !errors.Is(err, missing) {
		t.Fatalf("err = %v, want the check's error", err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused copy was left at the destination: %v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dst), ".importcopy-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}
