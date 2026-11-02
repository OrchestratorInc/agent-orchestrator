package systeminstall

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestManagedCleanupRetainsDetachedNativeProcessRelease(t *testing.T) {
	for _, name := range []string{"background native hub", "mutable launcher", "unavailable snapshot", "malformed snapshot"} {
		t.Run(name, func(t *testing.T) {
			f := newManagedCodexFixture(t, nil)
			if f.update(t, "1.1.0").Status != StatusSucceeded || f.update(t, "1.2.0").Status != StatusSucceeded {
				t.Fatal("fixture update failed")
			}
			old := filepath.Join(f.root, "codex", "versions", "v1")
			f.s.commands = commandRunnerFunc(func(_ context.Context, _ []string, out, _ io.Writer) error {
				if name == "unavailable snapshot" {
					return errors.New("process inventory unavailable")
				}
				if name == "malformed snapshot" {
					_, err := io.WriteString(out, "unparseable inventory")
					return err
				}
				command := filepath.Join(old, "bin", "codex") + " --native-background-hub"
				if name == "mutable launcher" {
					command = "node " + filepath.Join(f.root, "bin", "codex")
				}
				_, err := io.WriteString(out, "123 "+command+"\n")
				return err
			})
			f.s.RemoveUnusedManagedReleases(context.Background())
			if _, err := os.Stat(old); err != nil {
				t.Fatalf("deleted a release without proving its detached process exited: %v", err)
			}
			f.s.commands = commandRunnerFunc(func(_ context.Context, _ []string, out, _ io.Writer) error {
				_, err := io.WriteString(out, "1 /usr/local/bin/ao\n")
				return err
			})
			f.s.RemoveUnusedManagedReleases(context.Background())
			if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unused release not removed after process exit: %v", err)
			}
		})
	}
}

func TestBrewCleanupRetainsDetachedProviderHub(t *testing.T) {
	s, ran := updateWhileRunningService(t, nil)
	s.SetHarnessUpdateObserver(&updateObserverStub{})
	s.deferredCleanups = map[Target]string{TargetKimi: "kimi-code"}
	alive := true
	s.commands = commandRunnerFunc(func(_ context.Context, argv []string, out, _ io.Writer) error {
		text := "1 /usr/local/bin/ao\n"
		if argv[0] == "ps" && alive {
			text = "/opt/homebrew/Cellar/kimi-code/1.2.0/bin/kimi --provider-hub\n"
		}
		if len(argv) > 1 && argv[1] == "--cellar" {
			text = "/opt/homebrew/Cellar/kimi-code\n"
		}
		if len(argv) > 0 && argv[0] == "ps" && !strings.HasPrefix(text, "1 ") {
			text = "123 " + text
		}
		_, err := io.WriteString(out, text)
		return err
	})
	s.RunDeferredCleanups(context.Background())
	if len(*ran) != 0 {
		t.Fatal("cleaned old keg while detached provider hub still uses it")
	}
	alive = false
	s.RunDeferredCleanups(context.Background())
	if len(*ran) != 1 || (*ran)[0].Argv[1] != "cleanup" {
		t.Fatalf("cleanup did not run after provider exit: %+v", *ran)
	}
}

func TestManagedProcessReferencesRespectReleaseBoundaries(t *testing.T) {
	s := newTestService("darwin")
	s.managedRoot = filepath.Join(t.TempDir(), "with spaces")
	record := managedRecord{CLI: "codex", Method: "npm"}
	for _, tt := range []struct {
		name, command string
		want          bool
	}{
		{"old native process", filepath.Join(s.managedRoot, "codex", "versions", "v1", "bin", "codex"), true},
		{"different release", filepath.Join(s.managedRoot, "codex", "versions", "v10", "bin", "codex"), false},
		{"different root", s.managedRoot + "-other/codex/versions/v1/bin/codex", false},
		{"mutable launcher", "node " + filepath.Join(s.managedRoot, "bin", "codex"), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := s.managedReleaseHasProcess(managedProcessSnapshot{commands: []string{tt.command}}, TargetCodex, record, "v1")
			if got != tt.want {
				t.Fatalf("release referenced=%v want %v", got, tt.want)
			}
		})
	}
}

func TestWindowsManagedProcessReferencesKeepOpaqueScriptRuntimes(t *testing.T) {
	s := newTestService("windows")
	s.managedRoot = t.TempDir()
	for _, tt := range []struct {
		name, image, command, process string
		want                          bool
	}{
		{"native image", filepath.Join(s.managedRoot, "codex", "versions", "v1", "bin", "codex.exe"), "", "codex.exe", true},
		{"new native image", filepath.Join(s.managedRoot, "codex", "versions", "v2", "bin", "codex.exe"), "", "codex.exe", false},
		{"opaque node script", `C:\Program Files\nodejs\node.exe`, "", "node.exe", true},
		{"opaque command script", `C:\Windows\System32\cmd.exe`, "", "cmd.exe", true},
		{"case insensitive command", "", strings.ToUpper(filepath.Join(s.managedRoot, "codex", "versions", "v1", "bin", "codex.exe")), "codex.exe", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal([]map[string]string{{"ExecutablePath": tt.image, "CommandLine": tt.command, "Name": tt.process}})
			if err != nil {
				t.Fatal(err)
			}
			s.commands = commandRunnerFunc(func(_ context.Context, argv []string, out, _ io.Writer) error {
				if argv[0] != "powershell.exe" {
					t.Fatalf("unexpected process probe %q", argv)
				}
				_, err := out.Write(data)
				return err
			})
			snapshot, err := s.snapshotManagedProcesses(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := s.managedReleaseHasProcess(snapshot, TargetCodex, managedRecord{CLI: "codex", Method: "npm"}, "v1"); got != tt.want {
				t.Fatalf("referenced=%v want %v", got, tt.want)
			}
		})
	}
}

func TestWindowsManagedProcessReferencesKeepOpaqueNativeChildren(t *testing.T) {
	s := newTestService("windows")
	s.managedRoot = t.TempDir()
	record := managedRecord{CLI: "copilot", Method: "npm"}
	for _, tt := range []struct {
		process string
		want    bool
	}{
		{"copilot-win32-x64.exe", true},
		{"copilot.exe", true},
		{"codex.exe", false},
	} {
		t.Run(tt.process, func(t *testing.T) {
			snapshot := managedProcessSnapshot{opaque: []string{tt.process}}
			if got := s.managedReleaseHasProcess(snapshot, TargetCopilot, record, "v1"); got != tt.want {
				t.Fatalf("referenced=%v want %v", got, tt.want)
			}
		})
	}
}

func TestManagedCleanupRetainsRealDetachedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	f := newManagedCodexFixture(t, nil)
	f.update(t, "1.1.0")
	f.update(t, "1.2.0")
	old := filepath.Join(f.root, "codex", "versions", "v1")
	script := filepath.Join(old, "bin", "provider-hub")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nread ignored\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		_ = stdin.Close()
		if !waited {
			_ = cmd.Wait()
		}
	})
	f.s.commands = testCommandRunner(func(ctx context.Context, argv []string) *exec.Cmd {
		return exec.CommandContext(ctx, argv[0], argv[1:]...)
	})
	f.s.RemoveUnusedManagedReleases(context.Background())
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("removed release used by real detached process: %v", err)
	}
	restarted := NewWithDeps(f.s.executables, f.s.commands, Deps{
		ManagedRoot: f.root, Releases: f.releases, Sessions: sessionListerStub{},
	})
	t.Cleanup(func() { _ = restarted.Close(context.Background()) })
	restarted.RemoveUnusedManagedReleases(context.Background())
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("restart lost detached process reference: %v", err)
	}
	_ = stdin.Close()
	_ = cmd.Wait()
	waited = true
	restarted.RemoveUnusedManagedReleases(context.Background())
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old release not removed after process exited: %v", err)
	}
}

func TestBrewCleanupRetainsMutableScriptLauncher(t *testing.T) {
	s, ran := updateWhileRunningService(t, nil)
	s.SetHarnessUpdateObserver(&updateObserverStub{})
	s.deferredCleanups = map[Target]string{TargetKimi: "kimi-code"}
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/opt/homebrew/bin/kimi"}, nil
	})
	s.commands = commandRunnerFunc(func(_ context.Context, argv []string, out, _ io.Writer) error {
		text := "node /opt/homebrew/bin/kimi\n"
		if len(argv) > 1 && argv[1] == "--cellar" {
			text = "/opt/homebrew/Cellar/kimi-code\n"
		}
		if len(argv) > 0 && argv[0] == "ps" && !strings.HasPrefix(text, "1 ") {
			text = "123 " + text
		}
		_, err := io.WriteString(out, text)
		return err
	})
	s.RunDeferredCleanups(context.Background())
	if len(*ran) != 0 {
		t.Fatal("cleaned keg while mutable Node launcher could still load it")
	}
}
