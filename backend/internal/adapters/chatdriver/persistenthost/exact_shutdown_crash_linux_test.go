//go:build linux

package persistenthost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type removalCrashProcesses struct {
	Provider int `json:"provider"`
	Child    int `json:"child"`
}

func TestRemovalHostCrashProviderHelper(t *testing.T) {
	if os.Getenv("AO_REMOVAL_CRASH_HELPER") != "1" {
		return
	}
	if os.Getenv("AO_REMOVAL_CRASH_DESCENDANT") != "1" {
		child := exec.Command(os.Args[0], "-test.run=^TestRemovalHostCrashProviderHelper$")
		child.Env = append(os.Environ(), "AO_REMOVAL_CRASH_DESCENDANT=1")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(removalCrashProcesses{Provider: os.Getpid(), Child: child.Process.Pid})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv("AO_REMOVAL_CRASH_PIDS"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for {
		time.Sleep(time.Hour)
	}
}

func removalCrashProcessRunning(pid int) bool {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data[strings.LastIndexByte(string(data), ')')+1:]))
	return len(fields) > 0 && fields[0] != "Z" && fields[0] != "X"
}

func startRemovalCrashHost(t *testing.T, dataDir, sessionID string) (*exec.Cmd, Descriptor, removalCrashProcesses) {
	t.Helper()
	workdir := t.TempDir()
	pidPath := filepath.Join(workdir, "provider-pids.json")
	cfg := Config{SessionID: sessionID, DataDir: dataDir, Workdir: workdir,
		Env:  append(os.Environ(), "AO_REMOVAL_CRASH_HELPER=1", "AO_REMOVAL_CRASH_PIDS="+pidPath),
		Argv: []string{os.Args[0], "-test.run=^TestRemovalHostCrashProviderHelper$"}}
	cmd := exec.Command(os.Args[0], hostArgs(cfg)...)
	cmd.Env, cmd.Dir = cfg.Env, cfg.Workdir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var processes removalCrashProcesses
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if data, err := os.ReadFile(pidPath); err == nil {
			_ = json.Unmarshal(data, &processes)
		}
		if processes.Provider > 0 {
			_ = syscall.Kill(-processes.Provider, syscall.SIGKILL)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, err := readDescriptor(dataDir, sessionID)
		data, pidErr := os.ReadFile(pidPath)
		if err == nil && d.PID == cmd.Process.Pid && pidErr == nil && json.Unmarshal(data, &processes) == nil &&
			removalCrashProcessRunning(processes.Provider) && removalCrashProcessRunning(processes.Child) {
			return cmd, d, processes
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("crash fixture did not publish the host and both provider processes")
	return nil, Descriptor{}, removalCrashProcesses{}
}

func TestRemovalHostCrashReplacementCannotHideOriginalProvider(t *testing.T) {
	for _, removeReplacement := range []bool{false, true} {
		t.Run(strconv.FormatBool(removeReplacement), func(t *testing.T) {
			if !retirementAcceptance {
				t.Skip("deferred retirement acceptance; run with -tags=retirement_acceptance")
			}
			dataDir := t.TempDir()
			originalHost, original, originalProcesses := startRemovalCrashHost(t, dataDir, "managed")
			identity := descriptorIdentity(original)
			_, _, nativeProcesses := startRemovalCrashHost(t, dataDir, "native-other")
			if err := originalHost.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := originalHost.Wait(); err == nil {
				t.Fatal("original host was not forcibly terminated")
			}
			if !removalCrashProcessRunning(originalProcesses.Provider) || !removalCrashProcessRunning(originalProcesses.Child) {
				t.Fatal("fixture failed to preserve the original provider group after parent death")
			}
			replacementHost, replacement, replacementProcesses := startRemovalCrashHost(t, dataDir, "managed")
			if descriptorIdentity(replacement) == identity {
				t.Fatal("fixture reused the original host identity")
			}
			if removeReplacement {
				if err := Shutdown(t.Context(), dataDir, "managed"); err != nil {
					t.Fatal(err)
				}
				if err := replacementHost.Wait(); err != nil {
					t.Fatal(err)
				}
				if _, err := readDescriptor(dataDir, "managed"); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("replacement descriptor was not removed", err)
				}
				path, err := lockPath(dataDir, "managed")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("replacement launch lock was not removed", err)
				}
				if !removalCrashProcessRunning(originalProcesses.Provider) || !removalCrashProcessRunning(originalProcesses.Child) {
					t.Fatal("replacement shutdown unexpectedly retired the old provider group")
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := ShutdownExact(ctx, dataDir, "managed", identity); err != nil {
				t.Fatal("exact original provider teardown failed", err)
			}
			if removalCrashProcessRunning(originalProcesses.Provider) || removalCrashProcessRunning(originalProcesses.Child) {
				t.Error("deletion acknowledged teardown while the original provider group still runs")
			}
			if !removeReplacement && (!removalCrashProcessRunning(replacementProcesses.Provider) || !removalCrashProcessRunning(replacementProcesses.Child)) {
				t.Error("original-owner teardown stopped the replacement provider group")
			}
			if !removalCrashProcessRunning(nativeProcesses.Provider) || !removalCrashProcessRunning(nativeProcesses.Child) {
				t.Error("original-owner teardown stopped an unrelated native provider group")
			}
		})
	}
}
