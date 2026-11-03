package memcode

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func memcodeRelocationFixture(t *testing.T) ports.ImportedSessionRelocation {
	t.Helper()
	source, target := t.TempDir(), t.TempDir()
	seedHistory(t, source, validHistory)
	return ports.ImportedSessionRelocation{NativeSessionID: testID, ConfigDir: source, SourceCWD: source, LaunchCWD: target, TranscriptPath: filepath.Join(source, ".memcode", "sessions", testID, "messages.json")}
}

func TestMemcodeImportCopiesExactNativeStateBeforeInteractiveRestore(t *testing.T) {
	r := memcodeRelocationFixture(t)
	prepared, err := (&Plugin{}).PrepareImportedSession(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.NativeSessionID != testID || prepared.ConfigDir != r.LaunchCWD || prepared.TranscriptPath != filepath.Join(r.LaunchCWD, ".memcode", "sessions", testID, "messages.json") {
		t.Fatalf("prepared native copy = %+v", prepared)
	}
	wantDigest, wantCount, err := transcript(r.ConfigDir, testID)
	if err != nil {
		t.Fatal(err)
	}
	digest, count, err := transcript(r.LaunchCWD, testID)
	if err != nil || digest != wantDigest || count != wantCount {
		t.Fatalf("relocated native history = %x, %d, %v", digest, count, err)
	}
	plan := &restorePlan{workspace: r.LaunchCWD, target: testID, digest: digest, count: count}
	if err := plan.verifyHistory(); err != nil || plan.ResumeInput() != "/resume "+testID || plan.TargetID() != testID {
		t.Fatalf("interactive target = %q, %v", plan.ResumeInput(), err)
	}
	path := filepath.Join(r.LaunchCWD, ".memcode", "sessions", testID, "messages.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("native history permissions = %v", info.Mode())
	}
	if _, err := os.Stat(filepath.Join(r.LaunchCWD, ".memcode", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("copied unrelated project config: %v", err)
	}
	data, err := os.ReadFile(r.TranscriptPath)
	if err != nil || string(data) != validHistory {
		t.Fatalf("source changed = %s, %v", data, err)
	}
}

func TestMemcodeImportRetryRetainsWorktreeTurns(t *testing.T) {
	r := memcodeRelocationFixture(t)
	p := &Plugin{}
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	later := strings.Replace(validHistory, `"role":"user"`, `"role":"assistant"`, 1)
	seedHistory(t, r.LaunchCWD, later)
	if err := os.Remove(r.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if err := p.RelocateImportedSession(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(r.LaunchCWD, ".memcode", "sessions", testID, "messages.json"))
	if err != nil || string(data) != later {
		t.Fatalf("worktree turns replaced = %s, %v", data, err)
	}
}

func TestMemcodeImportRefusesUnrelatedDestination(t *testing.T) {
	r := memcodeRelocationFixture(t)
	seedHistory(t, r.LaunchCWD, validHistory)
	if err := (&Plugin{}).RelocateImportedSession(context.Background(), r); err == nil {
		t.Fatal("accepted a native file without this import's marker")
	}
}

func TestMemcodeNativeProbeWithoutProjectLocationIsUnknown(t *testing.T) {
	status, err := (&Plugin{}).ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: testID})
	if err != nil || status != ports.NativeSessionAvailabilityUnknown {
		t.Fatalf("unknown project location treated as missing history: %s, %v", status, err)
	}
}

func TestMemcodeTransferredHistoryRequiresPinnedSnapshot(t *testing.T) {
	for _, mode := range []string{"match", "digest mismatch", "size mismatch", "missing fingerprint", "changed source", "symlink", "escaping symlink", "wrong marker"} {
		t.Run(mode, func(t *testing.T) {
			r := memcodeRelocationFixture(t)
			r.SourceFingerprint = NativeHistoryFingerprint([]byte(validHistory))
			copied := validHistory
			switch mode {
			case "digest mismatch", "changed source":
				copied = strings.Replace(validHistory, `"role":"user"`, `"role":"tool"`, 1)
			case "size mismatch":
				copied += "\n"
			case "missing fingerprint":
				r.SourceFingerprint = ""
			}
			seedHistory(t, r.LaunchCWD, copied)
			target := filepath.Join(r.LaunchCWD, ".memcode", "sessions", testID, "messages.json")
			if mode == "changed source" {
				seedHistory(t, r.ConfigDir, copied)
			}
			if mode == "symlink" || mode == "escaping symlink" {
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				backing := filepath.Join(r.LaunchCWD, "other-history.json")
				if mode == "escaping symlink" {
					backing = filepath.Join(t.TempDir(), "other-history.json")
				}
				if err := os.WriteFile(backing, []byte(copied), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(backing, target); err != nil {
					t.Skip(err)
				}
			}
			if mode == "wrong marker" {
				if err := os.WriteFile(filepath.Join(filepath.Dir(target), ".ao-import"), []byte("another import\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			prepared, err := (&Plugin{}).PrepareImportedSession(context.Background(), r)
			if mode == "match" {
				if err != nil || prepared.NativeSessionID != testID || prepared.ConfigDir != r.LaunchCWD {
					t.Fatalf("pinned transferred history = %+v, %v", prepared, err)
				}
			} else if err == nil {
				t.Fatal("accepted unproven transferred history")
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != copied {
				t.Fatalf("destination overwritten = %q, %v", data, err)
			}
			data, err = os.ReadFile(r.TranscriptPath)
			wantSource := validHistory
			if mode == "changed source" {
				wantSource = copied
			}
			if err != nil || string(data) != wantSource {
				t.Fatalf("source overwritten = %q, %v", data, err)
			}
		})
	}
}

func TestMemcodeImportRejectsEscapingSourceAndWrongID(t *testing.T) {
	for _, mode := range []string{"symlink", "wrong-id", "wrong-path"} {
		t.Run(mode, func(t *testing.T) {
			r := memcodeRelocationFixture(t)
			switch mode {
			case "symlink":
				outside := filepath.Join(t.TempDir(), "private.json")
				if err := os.WriteFile(outside, []byte(validHistory), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(r.TranscriptPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, r.TranscriptPath); err != nil {
					t.Skip(err)
				}
			case "wrong-id":
				r.NativeSessionID = "sess_b1b1b1b1"
			case "wrong-path":
				r.TranscriptPath = filepath.Join(t.TempDir(), "messages.json")
			}
			if err := (&Plugin{}).RelocateImportedSession(context.Background(), r); err == nil {
				t.Fatal("accepted mismatched or escaping native history")
			}
		})
	}
}
