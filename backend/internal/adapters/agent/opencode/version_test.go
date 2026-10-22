package opencode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func versionBinary(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	return path
}

func TestResolveBinaryForMajor(t *testing.T) {
	for _, tc := range []struct {
		name, output, wantErr string
		major, wantFoundMajor int
	}{
		{"v1", "1.18.33", "", 1, 0},
		{"v2", "2.0.0", "", 2, 0},
		{"prerelease", "2.0.0-beta.3", "", 2, 0},
		{"label", "opencode 2.1.0", "", 2, 0},
		{"v1 rejects v2", "2.0.0", "requires OpenCode 1", 1, 2},
		{"v2 rejects v1", "1.18.33", "requires OpenCode 2", 2, 1},
		{"malformed", "development", "version", 2, 0},
		{"unrelated numbers", "error 2.0.0 failed", "version", 2, 0},
		{"empty", "", "version", 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := versionBinary(t, "[ \"$1\" = --version ] || exit 99\nprintf '%s\\n' '"+tc.output+"'\n")
			got, err := ResolveBinaryForMajor(context.Background(), tc.major)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || got != "" {
					t.Fatalf("resolve = (%q, %v), want %q error and no binary", got, err, tc.wantErr)
				}
				if errors.Is(err, ports.ErrAgentBinaryNotFound) {
					t.Fatal("installed incompatible binary reported as missing")
				}
				if tc.wantFoundMajor != 0 {
					var incompatible *IncompatibleVersionError
					if !errors.As(err, &incompatible) {
						t.Fatalf("error type = %T, want *IncompatibleVersionError", err)
					}
					if incompatible.ExpectedMajor != tc.major || incompatible.FoundMajor != tc.wantFoundMajor || incompatible.FoundVersion != tc.output || incompatible.Path != binary {
						t.Fatalf("incompatible version error = %#v, want expected=%d found=%d version=%q path=%q", incompatible, tc.major, tc.wantFoundMajor, tc.output, binary)
					}
					for _, detail := range []string{binary, tc.output, fmt.Sprintf("OpenCode %d", tc.wantFoundMajor), "select the matching harness"} {
						if !strings.Contains(err.Error(), detail) {
							t.Fatalf("error = %q, want actionable detail %q", err, detail)
						}
					}
				}
				return
			}
			if err != nil || got != binary {
				t.Fatalf("resolve = (%q, %v), want %q", got, err, binary)
			}
		})
	}
}

func TestResolveBinaryForMajorBoundedAndCanceled(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "canceled"}[canceled], func(t *testing.T) {
			versionBinary(t, "exec /bin/sleep 30\n")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				cancel()
			}
			started := time.Now()
			_, err := ResolveBinaryForMajor(ctx, 2)
			want := context.DeadlineExceeded
			if canceled {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
			if time.Since(started) > 12*time.Second {
				t.Fatal("version probe was not bounded")
			}
		})
	}
}

func TestResolveBinaryForMajorAcceptsSlowShim(t *testing.T) {
	dir := t.TempDir()
	name, body := "opencode", "#!/bin/sh\nsleep 4\nprintf '2.0.0\\n'\n"
	if runtime.GOOS == "windows" {
		name = "opencode.cmd"
		body = "@echo off\r\nping -n 5 127.0.0.1 >nul\r\necho 2.0.0\r\n"
	}
	binary := filepath.Join(dir, name)
	if err := os.WriteFile(binary, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := ResolveBinaryForMajor(context.Background(), 2)
	if err != nil || got != binary {
		t.Fatalf("resolve slow shim = (%q, %v), want %q", got, err, binary)
	}
}

func TestResolveBinaryForMajorMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix resolver paths")
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("VOLTA_HOME", t.TempDir())
	t.Setenv("FNM_DIR", t.TempDir())
	old := opencodeUnixPaths
	opencodeUnixPaths = nil
	t.Cleanup(func() { opencodeUnixPaths = old })
	_, err := ResolveBinaryForMajor(context.Background(), 2)
	if !errors.Is(err, ports.ErrAgentBinaryNotFound) {
		t.Fatalf("error = %v, want missing binary", err)
	}
}

func TestV1RejectsWrongMajorBeforeOverlay(t *testing.T) {
	versionBinary(t, "printf '2.0.0\\n'\n")
	for _, restore := range []bool{false, true} {
		dir := filepath.Join(t.TempDir(), "not-created")
		cfg := ports.LaunchConfig{SystemPrompt: "rules", SystemPromptFile: filepath.Join(dir, "system.md")}
		var err error
		if restore {
			_, _, err = New().GetRestoreCommand(context.Background(), ports.RestoreConfig{SystemPrompt: cfg.SystemPrompt, SystemPromptFile: cfg.SystemPromptFile, Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "ses_original"}}})
		} else {
			_, err = New().GetLaunchCommand(context.Background(), cfg)
		}
		if err == nil || !strings.Contains(err.Error(), "requires OpenCode 1") {
			t.Fatalf("restore=%v: expected wrong-major rejection, got %v", restore, err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("restore=%v: wrote config before rejecting version", restore)
		}
	}
}

func TestResolveBinaryForMajorPicksMatchingBinaryFromSeveralOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	first, second := t.TempDir(), t.TempDir()
	for dir, version := range map[string]string{first: "2.0.20", second: "1.18.30"} {
		script := "#!/bin/sh\nprintf '%s\n' '" + version + "'\n"
		if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", first+string(os.PathListSeparator)+second)
	for major, want := range map[int]string{2: filepath.Join(first, "opencode"), 1: filepath.Join(second, "opencode")} {
		got, err := ResolveBinaryForMajor(context.Background(), major)
		if err != nil || got != want {
			t.Fatalf("major %d resolved (%q, %v), want %q", major, got, err, want)
		}
	}
	if _, err := ResolveBinaryForMajor(context.Background(), 3); err == nil || !strings.Contains(err.Error(), "requires OpenCode 3") {
		t.Fatalf("major 3 err = %v, want first-candidate mismatch", err)
	}
}

func TestResolveBinaryForMajorPrefersMismatchOverEarlierProbeFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	broken, mismatch := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "opencode"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mismatch, "opencode"), []byte("#!/bin/sh\nprintf '1.18.33\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", broken+string(os.PathListSeparator)+mismatch)
	_, err := ResolveBinaryForMajor(context.Background(), 2)
	var incompatible *IncompatibleVersionError
	if !errors.As(err, &incompatible) || incompatible.Path != filepath.Join(mismatch, "opencode") {
		t.Fatalf("error = %#v, want mismatch from later candidate", err)
	}
}

func TestResolveBinaryForMajorSharesOneProbeDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	oldTimeout := versionProbeTimeout
	versionProbeTimeout = 150 * time.Millisecond
	t.Cleanup(func() { versionProbeTimeout = oldTimeout })
	first, second := t.TempDir(), t.TempDir()
	for _, dir := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte("#!/bin/sh\n/bin/sleep 0.1\nprintf 'development\\n'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", first+string(os.PathListSeparator)+second)
	if _, err := ResolveBinaryForMajor(context.Background(), 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want one overall deadline", err)
	}
}

func TestResolveBinaryForMajorReturnsCancellationOverEarlierMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	first, second := t.TempDir(), t.TempDir()
	firstRan, secondRan := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")
	if err := os.WriteFile(filepath.Join(first, "opencode"), []byte("#!/bin/sh\nprintf hit > '"+firstRan+"'\nprintf '1.18.33\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "opencode"), []byte("#!/bin/sh\nprintf hit > '"+secondRan+"'\nexec /bin/sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", first+string(os.PathListSeparator)+second)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("VOLTA_HOME", t.TempDir())
	t.Setenv("FNM_DIR", t.TempDir())
	oldPaths := opencodeUnixPaths
	opencodeUnixPaths = nil
	t.Cleanup(func() { opencodeUnixPaths = oldPaths })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ResolveBinaryForMajor(ctx, 2)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(secondRan); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second candidate was not probed")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	err := <-done
	if _, statErr := os.Stat(firstRan); statErr != nil {
		t.Fatalf("first candidate was not probed: %v", statErr)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation instead of earlier mismatch", err)
	}
}

func TestResolveBinaryForMajorUsesDistinctManagedLaunchers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixtures")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	binaryutil.SetManagedBinDir(dir)
	t.Cleanup(func() { binaryutil.SetManagedBinDir("") })
	for name, version := range map[string]string{"opencode": "1.2.27", "opencode-v2": "2.0.0"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho "+version+"\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for major, name := range map[int]string{1: "opencode", 2: "opencode-v2"} {
		got, err := ResolveBinaryForMajor(context.Background(), major)
		if err != nil || got != filepath.Join(dir, name) {
			t.Fatalf("major %d=%q,%v", major, got, err)
		}
	}
}

func TestReplacedOpenCode1MessageOnlyForDefaultLocations(t *testing.T) {
	dataHome := filepath.Join(t.TempDir(), "share")
	t.Setenv("XDG_DATA_HOME", dataHome)
	v2Home := filepath.Join(dataHome, "opencode-v2-home")
	for _, tc := range []struct {
		name     string
		err      IncompatibleVersionError
		replaced bool
	}{
		{"v2 at the installer default", IncompatibleVersionError{ExpectedMajor: 1, FoundMajor: 2, FoundVersion: "opencode v2.1.2", Path: "/Users/me/.opencode/bin/opencode"}, true},
		{"v2 linked by Homebrew", IncompatibleVersionError{ExpectedMajor: 1, FoundMajor: 2, FoundVersion: "2.1.2", Path: "/opt/homebrew/bin/opencode"}, true},
		{"v2 in AO's npm prefix", IncompatibleVersionError{ExpectedMajor: 1, FoundMajor: 2, FoundVersion: "2.1.2", Path: filepath.Join(v2Home, "npm", "bin", "opencode")}, false},
		{"v2 in AO's installer home", IncompatibleVersionError{ExpectedMajor: 1, FoundMajor: 2, FoundVersion: "2.1.2", Path: filepath.Join(v2Home, "installer", ".opencode", "bin", "opencode")}, false},
		{"v2 through the unlinked formula", IncompatibleVersionError{ExpectedMajor: 1, FoundMajor: 2, FoundVersion: "2.1.2", Path: "/opt/homebrew/opt/opencode-v2/bin/opencode"}, false},
		{"v1 found for the OpenCode 2 harness", IncompatibleVersionError{ExpectedMajor: 2, FoundMajor: 1, FoundVersion: "1.19.3", Path: "/Users/me/.opencode/bin/opencode"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.err.ReplacedOpenCode1Message()
			if tc.replaced != (got != "") {
				t.Fatalf("ReplacedOpenCode1Message() = %q, want replaced=%v", got, tc.replaced)
			}
			if tc.replaced && (!strings.Contains(got, tc.err.Path) || !strings.Contains(got, "reinstall OpenCode 1")) {
				t.Fatalf("message %q must name %s and the repair steps", got, tc.err.Path)
			}
		})
	}
}

func TestReplacedOpenCode1MessageForHomebrewLinkedV2(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew links are POSIX symlinks")
	}
	prefix := t.TempDir()
	keg := filepath.Join(prefix, "Cellar", "opencode-v2", "2.1.2", "bin", "opencode")
	if err := os.MkdirAll(filepath.Dir(keg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keg, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(prefix, "bin", "opencode")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(keg, link); err != nil {
		t.Fatal(err)
	}
	err := IncompatibleVersionError{ExpectedMajor: 1, FoundMajor: 2, FoundVersion: "2.1.2", Path: link}
	if got := err.ReplacedOpenCode1Message(); !strings.Contains(got, "brew unlink opencode-v2") || !strings.Contains(got, link) {
		t.Fatalf("message = %q, want the Homebrew unlink repair for %s", got, link)
	}
}

func TestV2HomebrewCandidatesIncludeCustomPrefix(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "brew")
	t.Setenv("HOMEBREW_PREFIX", prefix)
	want := filepath.ToSlash(filepath.Join(prefix, "opt", "opencode-v2", "bin", "opencode"))
	candidates := v2HomebrewCandidates()
	if !slices.Contains(candidates, want) {
		t.Fatalf("candidates = %v, want %s", candidates, want)
	}
	err := IncompatibleVersionError{ExpectedMajor: 1, FoundMajor: 2, FoundVersion: "2.1.2", Path: filepath.FromSlash(want)}
	if got := err.ReplacedOpenCode1Message(); got != "" {
		t.Fatalf("OpenCode 2 at a custom prefix's opt link = %q, want no replacement message", got)
	}
}
