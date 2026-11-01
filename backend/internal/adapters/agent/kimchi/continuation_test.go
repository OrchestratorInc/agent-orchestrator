package kimchi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const kimchiTestSession = "019fe4d5-24c5-77ac-a0e3-d3c18b37dcef"

// TestEncodeKimchiCwdDir pins the folder encoder against kimchi 1.24.3's
// encodeCwdDir: strip one leading slash/backslash, map '/', '\\' and ':' to
// '-', wrap in '--'. Kimchi does NOT strip the home directory (unlike OMP), so
// a home-based AO worktree keeps its full path form.
func TestEncodeKimchiCwdDir(t *testing.T) {
	cases := map[string]string{
		"/Users/me/.ao/data/worktrees/proj/x": "--Users-me-.ao-data-worktrees-proj-x--",
		"/private/tmp/a/b":                     "--private-tmp-a-b--",
		"/work/repo":                           "--work-repo--",
		`C:\Users\me\proj`:                     "--C--Users-me-proj--",
	}
	for in, want := range cases {
		if got := encodeKimchiCwdDir(in); got != want {
			t.Errorf("encodeKimchiCwdDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func writeKimchiFixture(t *testing.T, path string, cwd string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session","version":3,"id":"` + kimchiTestSession + `","timestamp":"2026-08-09T04:43:21.669Z","cwd":"` + cwd + `"}
{"type":"message","id":"u1","parentId":"` + kimchiTestSession + `","timestamp":"2026-08-09T04:43:30.000Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-08-09T04:43:40.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hi there"}]}}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sourcePath(root, folder string) string {
	return filepath.Join(root, "sessions", folder, "2026-08-09T04-43-21-669Z_"+kimchiTestSession+".jsonl")
}

// TestRelocateKimchiFirstOpenCopiesAndRewritesCwd checks the first-open copy:
// the transcript is placed at the launch cwd's folder, its header cwd rewritten
// to the exact launch cwd, and the original is left untouched.
func TestRelocateKimchiFirstOpenCopiesAndRewritesCwd(t *testing.T) {
	configDir := t.TempDir()
	src := sourcePath(configDir, "--work-repo--")
	writeKimchiFixture(t, src, "/work/repo")

	launch := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(launch, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := relocateKimchiImportedSession(context.Background(), configDir, kimchiTestSession, src, launch)
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(configDir, "sessions", encodeKimchiCwdDir(launch))
	if filepath.Dir(got) != wantDir {
		t.Fatalf("copy landed in %q, want dir %q", got, wantDir)
	}
	header, err := readKimchiHeader(got)
	if err != nil || header == nil {
		t.Fatalf("read copy header: %v", err)
	}
	if header.ID != kimchiTestSession {
		t.Fatalf("copy header id = %q", header.ID)
	}
	if header.CWD != launch {
		t.Fatalf("copy header cwd = %q, want exact launch cwd %q", header.CWD, launch)
	}
	if !kimchiHasConversation(got) {
		t.Fatal("copy has no conversation")
	}
	// The original must be byte-for-byte unchanged.
	original, err := readKimchiHeader(src)
	if err != nil || original == nil {
		t.Fatalf("read original header: %v", err)
	}
	if original.CWD != "/work/repo" {
		t.Fatalf("original cwd changed to %q", original.CWD)
	}
}

// TestRelocateKimchiNeverOverwritesExistingCopy checks the idempotent, first-
// open-only contract: a copy already at the launch cwd (which may hold turns AO
// appended after the import) is returned as-is, never overwritten.
func TestRelocateKimchiNeverOverwritesExistingCopy(t *testing.T) {
	configDir := t.TempDir()
	src := sourcePath(configDir, "--work-repo--")
	writeKimchiFixture(t, src, "/work/repo")
	launch := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(launch, 0o755); err != nil {
		t.Fatal(err)
	}

	first, err := relocateKimchiImportedSession(context.Background(), configDir, kimchiTestSession, src, launch)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate AO appending a turn to the copy after the first resume.
	appended := "\n" + `{"type":"message","id":"a2","parentId":"a1","timestamp":"2026-08-09T05:00:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"a turn added in AO"}]}}` + "\n"
	f, err := os.OpenFile(first, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(appended); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	before, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}

	second, err := relocateKimchiImportedSession(context.Background(), configDir, kimchiTestSession, src, launch)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("second relocate returned %q, want existing %q", second, first)
	}
	after, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("existing copy was overwritten; AO's appended turn would be lost")
	}
	if !strings.Contains(string(after), "a turn added in AO") {
		t.Fatal("appended turn missing from copy")
	}
}

// TestRelocateKimchiEncodesRawSymlinkedLaunchCWD pins the behavior 496 proved
// live over ACP: Kimchi names the session folder (and compares header.cwd) from
// the RAW launch cwd AO's driver sends, with no EvalSymlinks. Encoding the
// resolved path instead places the copy where the loader never looks and
// reintroduces the silent-empty load. The launch cwd here is a symlink whose
// target differs from the link path; the copy must land under the link-named
// folder, not the target-named one.
func TestRelocateKimchiEncodesRawSymlinkedLaunchCWD(t *testing.T) {
	configDir := t.TempDir()
	src := sourcePath(configDir, "--work-repo--")
	writeKimchiFixture(t, src, "/work/repo")

	target := filepath.Join(t.TempDir(), "real-worktree")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link-worktree")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == link {
		t.Skip("launch path did not resolve to a different physical path; cannot distinguish raw from resolved")
	}

	got, err := relocateKimchiImportedSession(context.Background(), configDir, kimchiTestSession, src, link)
	if err != nil {
		t.Fatal(err)
	}
	rawDir := filepath.Join(configDir, "sessions", encodeKimchiCwdDir(link))
	resolvedDir := filepath.Join(configDir, "sessions", encodeKimchiCwdDir(resolved))
	if filepath.Dir(got) != rawDir {
		t.Fatalf("copy landed in %q, want raw-encoded dir %q (not resolved %q)", filepath.Dir(got), rawDir, resolvedDir)
	}
	header, err := readKimchiHeader(got)
	if err != nil || header == nil {
		t.Fatalf("read copy header: %v", err)
	}
	if header.CWD != link {
		t.Fatalf("copy header cwd = %q, want the raw launch path %q", header.CWD, link)
	}
}

func TestRelocateKimchiRejectsMissingTranscript(t *testing.T) {
	configDir := t.TempDir()
	launch := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(launch, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := sourcePath(configDir, "--work-repo--")
	if _, err := relocateKimchiImportedSession(context.Background(), configDir, kimchiTestSession, missing, launch); err == nil {
		t.Fatal("expected an error when the source transcript is missing")
	}
}

func TestRelocateKimchiRejectsRelativeLaunchCWD(t *testing.T) {
	configDir := t.TempDir()
	src := sourcePath(configDir, "--work-repo--")
	writeKimchiFixture(t, src, "/work/repo")
	if _, err := relocateKimchiImportedSession(context.Background(), configDir, kimchiTestSession, src, "relative/dir"); err == nil {
		t.Fatal("expected an error for a relative launch directory")
	}
}

func TestLocateTranscriptFindsByNativeID(t *testing.T) {
	configDir := t.TempDir()
	src := sourcePath(configDir, "--work-repo--")
	writeKimchiFixture(t, src, "/work/repo")
	p := New()
	got, ok, err := p.LocateTranscript(context.Background(), ports.NativeSessionRef{NativeSessionID: kimchiTestSession, ConfigDir: configDir})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != src {
		t.Fatalf("LocateTranscript = %q, %v; want %q", got, ok, src)
	}
	avail, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: kimchiTestSession, ConfigDir: configDir})
	if err != nil {
		t.Fatal(err)
	}
	if avail != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("availability = %q", avail)
	}
}

func TestProbeNativeSessionUnavailableWhenMissing(t *testing.T) {
	p := New()
	avail, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: kimchiTestSession, ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if avail != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("availability = %q, want unavailable", avail)
	}
}

func TestNativeSessionConfigDirHonorsEnv(t *testing.T) {
	p := New()
	dir := filepath.Join(t.TempDir(), "custom-kimchi")
	got, err := p.NativeSessionConfigDir(context.Background(), map[string]string{kimchiCodingAgentDirEnv: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("config dir = %q, want %q", got, dir)
	}
}

func TestCanonicalKimchiSessionIDRejectsPathEscape(t *testing.T) {
	for _, bad := range []string{"", "../escape", "a/b", `a\b`, ".leading"} {
		if _, err := canonicalKimchiSessionID(bad); err == nil {
			t.Errorf("canonicalKimchiSessionID(%q) = nil error, want rejection", bad)
		}
	}
	if _, err := canonicalKimchiSessionID(kimchiTestSession); err != nil {
		t.Errorf("canonicalKimchiSessionID(valid uuid) errored: %v", err)
	}
}
