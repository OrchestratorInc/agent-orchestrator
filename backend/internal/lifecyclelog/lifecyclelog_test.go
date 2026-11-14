package lifecyclelog

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	var out []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("line is not JSON: %q", scanner.Text())
		}
		out = append(out, record)
	}
	return out
}

func TestScrubRemovesSecretsAndPromptLikeText(t *testing.T) {
	home := "/home/alice"
	cases := map[string]struct{ in, mustNotContain, mustContain string }{
		"url":                     {"dial https://api.example.com/v1?key=abc failed", "api.example.com", "[url]"},
		"bearer":                  {"header Bearer sk-live-123456 rejected", "sk-live-123456", "[credential]"},
		"assignment":              {"config token=ghp_abcdefghijklmnop invalid", "ghp_abcdefghijklmnop", "token=[redacted]"},
		"quoted multi-word value": {`login password="correct horse battery staple" rejected`, "horse", "password=[redacted] rejected"},
		"single-quoted value":     {"api_key: 'two secret words' then ok", "secret words", "api_key: [redacted] then ok"},
		"opaque":                  {"id 0123456789abcdef0123456789abcdef01 unknown", "0123456789abcdef0123456789abcdef01", "[opaque]"},
		"quoted text":             {`parse "please refactor the billing module and add tests" failed`, "billing module", `"[text]"`},
		"home":                    {"open /home/alice/.ao/data/state.db: denied", "/home/alice", "~/.ao/data/state.db"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := Scrub(tc.in, home, 400)
			if strings.Contains(got, tc.mustNotContain) || !strings.Contains(got, tc.mustContain) {
				t.Fatalf("Scrub(%q) = %q", tc.in, got)
			}
		})
	}
	if got := Scrub(strings.Repeat("a ", 300), home, 20); len(got) > 24 {
		t.Fatalf("not truncated: %d bytes", len(got))
	}
}

func TestRecordWritesScalarFieldsOnly(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir, "1.2.3")
	l.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	l.Record("daemon_start", map[string]any{
		"port":     3001,
		"owner":    "app",
		"nested":   map[string]any{"prompt": "secret"},
		"bad key!": "x",
		"event":    "overwrite attempt",
	})
	l.RecordError("daemon_exit", errors.New("open store: Bearer abc.def"), nil)
	l.Record("Not A Valid Event", nil)

	records := readRecords(t, filepath.Join(dir, "logs", FileName))
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2: %v", len(records), records)
	}
	start := records[0]
	if start["event"] != "daemon_start" || start["port"] != float64(3001) || start["owner"] != "app" || start["version"] != "1.2.3" {
		t.Fatalf("unexpected start record: %v", start)
	}
	if _, ok := start["nested"]; ok {
		t.Fatal("non-scalar field was persisted")
	}
	if _, ok := start["bad key!"]; ok {
		t.Fatal("invalid field name was persisted")
	}
	if records[1]["error"] != "open store: [credential]" {
		t.Fatalf("error not scrubbed: %v", records[1]["error"])
	}
	info, err := os.Stat(filepath.Join(dir, "logs", FileName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode = %v, err %v; want 0600", info.Mode().Perm(), err)
	}
}

func TestRecordRotatesAndCapsGenerations(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir, "v")
	l.maxBytes = 400
	for i := 0; i < 40; i++ {
		l.Record("tick", map[string]any{"n": i})
	}
	base := filepath.Join(dir, "logs", FileName)
	for _, suffix := range []string{"", ".1", ".2"} {
		info, err := os.Stat(base + suffix)
		if err != nil {
			t.Fatalf("missing %s: %v", base+suffix, err)
		}
		if info.Size() > l.maxBytes {
			t.Fatalf("%s is %d bytes, over the %d cap", suffix, info.Size(), l.maxBytes)
		}
	}
	if _, err := os.Stat(base + ".3"); !os.IsNotExist(err) {
		t.Fatalf("kept more than %d generations", Keep)
	}
	records := readRecords(t, base)
	if last := records[len(records)-1]["n"]; last != float64(39) {
		t.Fatalf("newest record not in live file: %v", last)
	}
}

func TestNilLogIsNoop(t *testing.T) {
	var l *Log
	l.Record("x", nil)
	l.RecordError("x", errors.New("y"), nil)
	if err := l.InstallCrashOutput(); err == nil {
		t.Fatal("nil log reported crash output installed")
	}
}

func TestRecordRefusesSymlinkedLog(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "logs", FileName)); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	Open(dir, "v").Record("daemon_start", nil)
	if data, _ := os.ReadFile(target); len(data) != 0 {
		t.Fatal("record followed a symlink")
	}
}

// A fatal panic in a child process must leave its traceback in the crash file.
func TestCrashOutputCapturesFatalPanic(t *testing.T) {
	if dir := os.Getenv("AO_LIFECYCLELOG_CRASH_CHILD"); dir != "" {
		if err := Open(dir, "v").InstallCrashOutput(); err != nil {
			t.Fatalf("install: %v", err)
		}
		go func() { panic("lifecyclelog-test-panic") }()
		time.Sleep(5 * time.Second)
		return
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashOutputCapturesFatalPanic$")
	cmd.Env = append(os.Environ(), "AO_LIFECYCLELOG_CRASH_CHILD="+dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("child did not crash")
	}
	data, err := os.ReadFile(filepath.Join(dir, "logs", CrashFileName))
	if err != nil {
		t.Fatalf("read crash file: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "=== daemon pid") || !strings.Contains(text, "panic: lifecyclelog-test-panic") || !strings.Contains(text, "goroutine") {
		t.Fatalf("crash file lacks the panic:\n%s", text)
	}
}
