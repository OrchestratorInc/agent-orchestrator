package kimchi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
		"/private/tmp/a/b":                    "--private-tmp-a-b--",
		"/work/repo":                          "--work-repo--",
		`C:\Users\me\proj`:                    "--C--Users-me-proj--",
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
	configDir := kimchiTestRoot(t)
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
	info, err := os.Stat(got)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("copy permissions: %v, %v", info, err)
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
	configDir := kimchiTestRoot(t)
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
	configDir := kimchiTestRoot(t)
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
	configDir := kimchiTestRoot(t)
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
	configDir := kimchiTestRoot(t)
	src := sourcePath(configDir, "--work-repo--")
	writeKimchiFixture(t, src, "/work/repo")
	if _, err := relocateKimchiImportedSession(context.Background(), configDir, kimchiTestSession, src, "relative/dir"); err == nil {
		t.Fatal("expected an error for a relative launch directory")
	}
}

func TestLocateTranscriptFindsByNativeID(t *testing.T) {
	configDir := kimchiTestRoot(t)
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

func kimchiTestRoot(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(kimchiCodingAgentDirEnv, "")
	root := filepath.Join(home, ".config", "kimchi", "harness")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestNativeSessionConfigDirModelsActualHomeAndRejectsOverrides(t *testing.T) {
	root := kimchiTestRoot(t)
	p := New()
	got, err := p.NativeSessionConfigDir(context.Background(), nil)
	if err != nil || got != root {
		t.Fatalf("native root = %q, %v; want %q", got, err, root)
	}
	t.Setenv(kimchiCodingAgentDirEnv, t.TempDir())
	if _, err := p.NativeSessionConfigDir(context.Background(), nil); err == nil {
		t.Fatal("daemon config override was presented as runtime-supported")
	}
	if got, err := p.NativeSessionConfigDir(context.Background(), map[string]string{kimchiCodingAgentDirEnv: ""}); err != nil || got != root {
		t.Fatalf("empty child override did not mask daemon override: %q, %v", got, err)
	}
	alias := filepath.Join(t.TempDir(), "native-root")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if got, err := p.NativeSessionConfigDir(context.Background(), map[string]string{kimchiCodingAgentDirEnv: alias}); err != nil || got != root {
		t.Fatalf("equivalent root alias rejected: %q, %v", got, err)
	}
}

func TestRelocateKimchiRejectsUnsupportedRootBeforeCopy(t *testing.T) {
	_ = kimchiTestRoot(t)
	unsupported := t.TempDir()
	src := sourcePath(unsupported, "--source--")
	writeKimchiFixture(t, src, "/source")
	launch := t.TempDir()
	if _, err := relocateKimchiImportedSession(context.Background(), unsupported, kimchiTestSession, src, launch); err == nil || !strings.Contains(err.Error(), "native home root") {
		t.Fatalf("unsupported source root was not clearly rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unsupported, "sessions", encodeKimchiCwdDir(launch))); !os.IsNotExist(err) {
		t.Fatalf("unsupported root was copied before rejection: %v", err)
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

func TestRelocateKimchiRejectsInvalidCopiesWithoutPublishing(t *testing.T) {
	for _, kind := range []string{"missing header", "duplicate header", "wrong id", "empty history", "torn record"} {
		t.Run(kind, func(t *testing.T) {
			root := kimchiTestRoot(t)
			src := sourcePath(root, "--source--")
			writeKimchiFixture(t, src, "/source")
			body, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			header, rest, _ := bytes.Cut(body, []byte("\n"))
			switch kind {
			case "missing header":
				body = rest
			case "duplicate header":
				body = append(body, append(header, '\n')...)
			case "wrong id":
				body = bytes.ReplaceAll(body, []byte(kimchiTestSession), []byte("019f0000-0000-7000-8000-000000000000"))
			case "empty history":
				body = append(header, '\n')
			case "torn record":
				body = append(body, []byte(`{"type":`)...)
			}
			if err := os.WriteFile(src, body, 0o600); err != nil {
				t.Fatal(err)
			}
			launch := t.TempDir()
			p := New()
			err = p.RelocateImportedSession(context.Background(), ports.ImportedSessionRelocation{
				NativeSessionID: kimchiTestSession,
				ConfigDir:       root,
				TranscriptPath:  src,
				LaunchCWD:       launch,
			})
			if err == nil {
				t.Fatal("invalid native transcript was accepted")
			}
			entries, err := os.ReadDir(filepath.Join(root, "sessions", encodeKimchiCwdDir(launch)))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("failed preparation left visible files: %v", entries)
			}
			original, err := os.ReadFile(src)
			if err != nil || !bytes.Equal(original, body) {
				t.Fatalf("source changed during failed preparation: %v", err)
			}
		})
	}
}

func TestRelocateKimchiRejectsExistingInvalidOrAmbiguousDestination(t *testing.T) {
	for _, kind := range []string{"wrong cwd", "duplicate header", "empty history", "other basename", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := kimchiTestRoot(t)
			src := sourcePath(root, "--source--")
			writeKimchiFixture(t, src, "/source")
			launch := t.TempDir()
			dest := sourcePath(root, encodeKimchiCwdDir(launch))
			writeKimchiFixture(t, dest, launch)
			body, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			header, _, _ := bytes.Cut(body, []byte("\n"))
			switch kind {
			case "wrong cwd":
				writeKimchiFixture(t, dest, "/other")
			case "duplicate header":
				body = append(body, append(header, '\n')...)
				if err := os.WriteFile(dest, body, 0o600); err != nil {
					t.Fatal(err)
				}
			case "empty history":
				if err := os.WriteFile(dest, append(header, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
			case "other basename":
				writeKimchiFixture(t, filepath.Join(filepath.Dir(dest), kimchiTestSession+".jsonl"), launch)
			case "symlink":
				if err := os.Remove(dest); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(src, dest); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := relocateKimchiImportedSession(context.Background(), root, kimchiTestSession, src, launch); err == nil {
				t.Fatal("invalid existing destination was accepted")
			}
			after, err := os.ReadFile(dest)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("existing destination was changed: %v", err)
			}
		})
	}
}

// readKimchiHeader returns the first session header record, or nil when the
// file has none. It reads only the first non-empty line, which is always the
// header in a Kimchi transcript.
func readKimchiHeader(path string) (*kimchiSessionHeader, error) {
	f, err := os.Open(path) //nolint:gosec // provider-owned path resolved from the configured root
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReaderSize(f, 256<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var header kimchiSessionHeader
			if jsonErr := json.Unmarshal(line, &header); jsonErr != nil || header.Type != "session" {
				return nil, nil
			}
			return &header, nil
		}
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// kimchiHasConversation reports whether path holds at least one user or
// assistant message record, so an empty or header-only file is not mistaken
// for replayable history.
func kimchiHasConversation(path string) bool {
	f, err := os.Open(path) //nolint:gosec // provider-owned path resolved from the configured root
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReaderSize(f, 256<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var rec struct {
				Type    string `json:"type"`
				Message struct {
					Role string `json:"role"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &rec) == nil && rec.Type == "message" && (rec.Message.Role == "user" || rec.Message.Role == "assistant") {
				return true
			}
		}
		if errors.Is(err, io.EOF) {
			return false
		}
		if err != nil {
			return false
		}
	}
}
