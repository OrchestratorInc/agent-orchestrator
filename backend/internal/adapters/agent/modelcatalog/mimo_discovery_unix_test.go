//go:build !windows

package modelcatalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func writeMiMoScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mimo")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// Every MiMo command except --version imports all Claude Code history at
// startup, so a background listing must not start that scan.
func TestMiMoDiscoveryDisablesClaudeImport(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "seen")
	binary := writeMiMoScript(t, `printf '%s %s' "$MIMOCODE_DISABLE_CLAUDE_IMPORT" "$CALLER_VALUE" > '`+seen+`'
echo 'mimo/mimo-auto — window 1M, compacts at 900K'`)
	callerEnv := map[string]string{"CALLER_VALUE": "kept"}
	catalog, err := discoverCommandCatalog(context.Background(), "mimo-code", binary, "", callerEnv, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) == 0 {
		t.Fatalf("catalog = %#v, want the listed model", catalog)
	}
	got, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1 kept" {
		t.Fatalf("environment = %q, want the import disabled and the caller's env kept", got)
	}
	if len(callerEnv) != 1 {
		t.Fatalf("caller env mutated: %v", callerEnv)
	}
}

// `mimo` is a Node wrapper that waits on the real binary, which shares its
// stdout. Killing only the wrapper would leave the binary running.
func TestMiMoDiscoveryTimeoutStopsWrappedBinary(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	binary := writeMiMoScript(t, `sleep 8 &
echo $! > '`+pidFile+`'
wait`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := discoverCommandCatalog(ctx, "mimo-code", binary, "", nil, false); err == nil {
		t.Fatal("discovery succeeded, want a timeout")
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		running, err := processRunning(pid)
		if err != nil {
			t.Fatal(err)
		}
		if !running {
			break
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("wrapped binary %d outlived discovery", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// processRunning reports whether pid is alive and not a zombie. An orphan's
// zombie can linger where PID 1 does not reap, and kill(pid, 0) still succeeds
// for it. A failed inspection is an error, never proof the process stopped.
func processRunning(pid int) (bool, error) {
	if processGone(pid) {
		return false, nil
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		// ps exits non-zero when pid has gone since the kill check.
		if processGone(pid) {
			return false, nil
		}
		return false, fmt.Errorf("inspect process %d: %w", pid, err)
	}
	state := strings.TrimSpace(string(out))
	if state == "" {
		return false, fmt.Errorf("inspect process %d: ps printed no state", pid)
	}
	return !strings.HasPrefix(state, "Z"), nil
}

// processGone reports whether kill(pid, 0) finds no process. Only ESRCH says
// so: EPERM means a process exists that we may not signal.
func processGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
