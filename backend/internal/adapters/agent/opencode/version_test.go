package opencode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
	return path
}

func TestResolveBinaryForMajor(t *testing.T) {
	for _, tc := range []struct {
		name, output, wantErr string
		major                 int
	}{
		{"v1", "1.18.33", "", 1},
		{"v2", "2.0.0", "", 2},
		{"prerelease", "2.0.0-beta.3", "", 2},
		{"label", "opencode 2.1.0", "", 2},
		{"v1 rejects v2", "2.0.0", "requires OpenCode 1", 1},
		{"v2 rejects v1", "1.18.33", "requires OpenCode 2", 2},
		{"malformed", "development", "version", 2},
		{"unrelated numbers", "error 2.0.0 failed", "version", 2},
		{"empty", "", "version", 2},
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
			if time.Since(started) > 5*time.Second {
				t.Fatal("version probe was not bounded")
			}
		})
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
