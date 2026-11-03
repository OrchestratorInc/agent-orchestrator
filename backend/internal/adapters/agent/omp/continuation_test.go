//go:build !windows

package omp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const ompTestID = "01a17cf3-99d0-76cd-b084-4cdce17a0fec"

// TestOMPSessionDir pins the folder encoder empirically derived from omp
// 18.14.0 (the binary ships no source). Paths below do not exist, so symlink
// resolution falls back to the cleaned path and the result is deterministic.
func TestOMPSessionDir(t *testing.T) {
	const home = "/synthetic-home/user"
	cases := []struct{ cwd, want string }{
		// Under $HOME: strip the home prefix, "/"->"-", no wrapping dashes.
		{home + "/.ao/data/worktrees/proj/sess-1", "-.ao-data-worktrees-proj-sess-1"},
		// Dots, caps, underscores and dashes are preserved.
		{home + "/.ao/dot.ted/Caps_And-Dash", "-.ao-dot.ted-Caps_And-Dash"},
		// Not under $HOME: wrap "--" + path without leading "/" + "--".
		{"/private/tmp/ao/wt/proj/sess-2", "--private-tmp-ao-wt-proj-sess-2--"},
	}
	for _, c := range cases {
		if got := ompSessionDir(home, c.cwd); got != c.want {
			t.Fatalf("ompSessionDir(%q) = %q, want %q", c.cwd, got, c.want)
		}
	}
}

// TestOMPSessionDirResolvesSymlinks pins that the folder name is built from the
// symlink-resolved path (OMP names the folder from the resolved cwd, while the
// session record keeps the raw launch path, as the relocation test covers).
func TestOMPSessionDirResolvesSymlinks(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "resolved-target", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "symlink-name")
	if err := os.Symlink(filepath.Join(base, "resolved-target"), link); err != nil {
		t.Fatal(err)
	}
	// A cwd reached through the symlink must encode the real target, not the link.
	got := ompSessionDir("/synthetic-home/user", filepath.Join(link, "sub"))
	if !strings.Contains(got, "resolved-target") || strings.Contains(got, "symlink-name") {
		t.Fatalf("ompSessionDir did not resolve symlinks: %q", got)
	}
}

func TestOMPLocateAndProbe(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "-work-repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-10-27T10-41-41-840Z_"+ompTestID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New()
	ref := ports.NativeSessionRef{NativeSessionID: ompTestID, ConfigDir: root}
	got, ok, err := p.LocateTranscript(context.Background(), ref)
	if err != nil || !ok || got != path {
		t.Fatalf("LocateTranscript = %q, %v, %v; want %q", got, ok, err, path)
	}
	avail, err := p.ProbeNativeSession(context.Background(), ref)
	if err != nil || avail != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("ProbeNativeSession = %v, %v; want available", avail, err)
	}
	// An unknown id is not available.
	missing := ports.NativeSessionRef{NativeSessionID: "00000000-0000-7000-8000-000000000000", ConfigDir: root}
	if avail, _ := p.ProbeNativeSession(context.Background(), missing); avail != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("ProbeNativeSession(missing) = %v, want unavailable", avail)
	}
	// A missing config dir is Unknown, never Unavailable.
	if avail, _ := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: ompTestID}); avail != ports.NativeSessionAvailabilityUnknown {
		t.Fatalf("ProbeNativeSession(no config dir) = %v, want unknown", avail)
	}
}

func TestOMPNativeSessionConfigDir(t *testing.T) {
	p := New()
	// Explicit override wins.
	dir, err := p.NativeSessionConfigDir(context.Background(), map[string]string{ompConfigDirEnv: "/custom/omp"})
	if err != nil || dir != "/custom/omp" {
		t.Fatalf("override config dir = %q, %v", dir, err)
	}
	// Default is ~/.omp/agent, resolved from the env HOME. Neutralize any
	// PI_CODING_AGENT_DIR exported on the runner so the default path is exercised.
	t.Setenv(ompConfigDirEnv, "")
	dir, err = p.NativeSessionConfigDir(context.Background(), map[string]string{"HOME": "/synthetic-home/user"})
	if err != nil || dir != filepath.Join("/synthetic-home/user", ".omp", "agent") {
		t.Fatalf("default config dir = %q, %v", dir, err)
	}
}

func TestOMPRelocationCopiesOnceAndKeepsRawCWD(t *testing.T) {
	root := t.TempDir()
	launch := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(launch, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(launch, link); err != nil {
		t.Fatal(err)
	}
	name := "2026-10-27T10-41-41-840Z_" + ompTestID + ".jsonl"
	src := filepath.Join(root, "sessions", "original", name)
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatal(err)
	}
	title := `{"type":"title","title":"Synthetic import"}`
	header := `{"type":"session","id":"` + ompTestID + `","cwd":"/original","large":9007199254740993}`
	message := `{"type":"message","message":{"role":"user","content":"remember the code word"}}`
	body := title + "\n" + header + "\n" + message + "\n"
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r := ports.ImportedSessionRelocation{NativeSessionID: ompTestID, ConfigDir: root, TranscriptPath: src, SourceCWD: "/original", LaunchCWD: link}
	p := New()
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "sessions", ompSessionDir(home, launch), name)
	out, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 3 || lines[0] != title || lines[2] != message {
		t.Fatalf("non-session records changed: %s", out)
	}
	var rec struct {
		CWD   string          `json:"cwd"`
		ID    string          `json:"id"`
		Large json.RawMessage `json:"large"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.CWD != link || rec.ID != ompTestID || string(rec.Large) != "9007199254740993" {
		t.Fatalf("session record changed incorrectly: %s", lines[1])
	}
	info, err := os.Stat(dst)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("copy is not private: %v, %v", info, err)
	}
	original, err := os.ReadFile(src)
	if err != nil || string(original) != body {
		t.Fatalf("source changed: %s, %v", original, err)
	}
	continued := string(out) + `{"type":"message","message":{"role":"assistant","content":"AO continuation"}}` + "\n"
	if err := os.WriteFile(dst, []byte(continued), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(dst)
	if err != nil || string(kept) != continued {
		t.Fatalf("retry overwrote AO turns: %s, %v", kept, err)
	}
}

func TestOMPRelocationRejectsInvalidSessionHeaders(t *testing.T) {
	header := `{"type":"session","id":"` + ompTestID + `","cwd":"/original"}` + "\n"
	for _, tc := range []struct{ name, body string }{
		{"missing", `{"type":"title","title":"no header"}` + "\n"},
		{"duplicate", header + header},
		{"wrong identity", `{"type":"session","id":"another-session","cwd":"/original"}` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			src := filepath.Join(root, "2026-10-27_"+ompTestID+".jsonl")
			if err := os.WriteFile(src, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			launch := t.TempDir()
			r := ports.ImportedSessionRelocation{NativeSessionID: ompTestID, ConfigDir: root, TranscriptPath: src, LaunchCWD: launch}
			if err := New().RelocateImportedSession(context.Background(), r); err == nil {
				t.Fatal("invalid transcript was copied")
			}
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(root, "sessions", ompSessionDir(home, launch), filepath.Base(src))
			if _, err := os.Stat(dst); !os.IsNotExist(err) {
				t.Fatalf("failed copy left a destination: %v", err)
			}
		})
	}
}
