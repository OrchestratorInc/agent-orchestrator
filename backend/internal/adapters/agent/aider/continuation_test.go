package aider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func aiderImportFixture(t *testing.T) ports.ImportedSessionRelocation {
	t.Helper()
	src, dst := t.TempDir(), t.TempDir()
	path := filepath.Join(src, aiderHistoryFile)
	if err := os.WriteFile(path, []byte("# aider chat started\n\n#### make a greeting\n\nHello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return ports.ImportedSessionRelocation{NativeSessionID: NativeHistoryID(src), ConfigDir: src, SourceCWD: src, TranscriptPath: path, LaunchCWD: dst}
}

func TestImportedAiderRestoresNativeHistoryFromWorktree(t *testing.T) {
	r := aiderImportFixture(t)
	p := &Plugin{resolvedBinary: "aider"}
	prepared, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.NativeSessionID != r.NativeSessionID || prepared.ConfigDir != r.LaunchCWD || prepared.TranscriptPath != filepath.Join(r.LaunchCWD, aiderHistoryFile) {
		t.Fatalf("prepared identity = %+v", prepared)
	}
	cfg := ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: r.LaunchCWD, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: r.NativeSessionID}}, SystemPromptFile: "/ao/context.md"}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), cfg)
	if err != nil || !ok {
		t.Fatalf("restore = %v, %v, %v", cmd, ok, err)
	}
	want := []string{"--restore-chat-history", "--chat-history-file", filepath.Join(r.LaunchCWD, aiderHistoryFile)}
	if !reflect.DeepEqual(cmd[len(cmd)-3:], want) || !strings.Contains(strings.Join(cmd, " "), "--read /ao/context.md") {
		t.Fatalf("restore argv = %v", cmd)
	}
	if strings.Contains(strings.Join(cmd, " "), r.SourceCWD) {
		t.Fatalf("restore points at source repository: %v", cmd)
	}
	info, err := os.Stat(filepath.Join(r.LaunchCWD, aiderHistoryFile))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("copy permissions = %v, %v", info, err)
	}
	src, err := os.ReadFile(r.TranscriptPath)
	if err != nil || string(src) != "# aider chat started\n\n#### make a greeting\n\nHello\n" {
		t.Fatalf("source changed: %q, %v", src, err)
	}
}

func TestAiderRelocationRefusesToReplaceExistingWorktreeHistory(t *testing.T) {
	r := aiderImportFixture(t)
	p := &Plugin{}
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(r.LaunchCWD, aiderHistoryFile)
	later := []byte("#### AO's later turn\n\nA later answer\n")
	if err := os.WriteFile(dst, later, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if err := p.RelocateImportedSession(context.Background(), r); err == nil {
		t.Fatal("accepted an existing native history file")
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != string(later) {
		t.Fatalf("later turns = %q, %v", got, err)
	}
}

func TestAiderRefusesUnrelatedDestinationAndEscapingHistory(t *testing.T) {
	for _, mode := range []string{"unrelated", "symlink", "wrong-id"} {
		t.Run(mode, func(t *testing.T) {
			r := aiderImportFixture(t)
			switch mode {
			case "unrelated":
				if err := os.WriteFile(filepath.Join(r.LaunchCWD, aiderHistoryFile), []byte("#### other chat\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(r.TranscriptPath); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(t.TempDir(), "private.md")
				if err := os.WriteFile(outside, []byte("#### private history\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, r.TranscriptPath); err != nil {
					t.Skip(err)
				}
			case "wrong-id":
				r.NativeSessionID = NativeHistoryID(t.TempDir())
			}
			if err := (&Plugin{}).RelocateImportedSession(context.Background(), r); err == nil {
				t.Fatal("accepted mismatched or escaping history")
			}
		})
	}
}

func TestAiderMissingOrUnmatchedHistoryNeverBuildsRestore(t *testing.T) {
	r := aiderImportFixture(t)
	p := &Plugin{resolvedBinary: "aider"}
	cfg := ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: r.LaunchCWD, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: r.NativeSessionID}}}
	if cmd, ok, err := p.GetRestoreCommand(context.Background(), cfg); !errors.Is(err, ports.ErrNativeHistoryUnavailable) || ok || cmd != nil {
		t.Fatalf("missing history = %v, %v, %v", cmd, ok, err)
	}
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.LaunchCWD, aiderHistoryMarker), []byte(NativeHistoryID(t.TempDir())), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := p.GetRestoreCommand(context.Background(), cfg); ok || !errors.Is(err, ports.ErrNativeHistoryUnavailable) {
		t.Fatalf("mismatched marker = %v, %v", ok, err)
	}
}

func TestAiderProbeWithoutKnownLocationIsUnknown(t *testing.T) {
	availability, err := (&Plugin{}).ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: NativeHistoryID(t.TempDir())})
	if err != nil || availability != ports.NativeSessionAvailabilityUnknown {
		t.Fatalf("unknown location = %v, %v", availability, err)
	}
}
