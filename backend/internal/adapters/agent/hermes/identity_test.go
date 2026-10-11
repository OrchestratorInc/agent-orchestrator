package hermes

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/creack/pty"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCurrentIdentityUsesOwnedPTYAfterNativeNewWithoutDatabaseCwd(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("scoped Linux process ownership contract")
	}
	workspace, data, home := t.TempDir(), t.TempDir(), t.TempDir()
	cmd := exec.CommandContext(t.Context(), "sh", "-c", "exec sleep 30")
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "AO_SESSION_ID=identity-fixture", "AO_RUNTIME_LAUNCH_ID=owned-launch")
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); _ = terminal.Close() }()
	proc := filepath.Join(string(filepath.Separator), "proc", strconv.Itoa(cmd.Process.Pid))
	stat, err := os.ReadFile(filepath.Join(proc, "stat"))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(stat)[strings.LastIndex(string(stat), ")")+2:])
	ticks, err := strconv.ParseFloat(fields[19], 64)
	if err != nil {
		t.Fatal(err)
	}
	kernel, err := os.ReadFile("/proc/stat")
	if err != nil {
		t.Fatal(err)
	}
	var boot float64
	for line := range strings.SplitSeq(string(kernel), "\n") {
		if strings.HasPrefix(line, "btime ") {
			boot, err = strconv.ParseFloat(strings.TrimPrefix(line, "btime "), 64)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	start := boot + ticks/100
	tty, err := os.Readlink(filepath.Join(proc, "fd", "0"))
	if err != nil {
		t.Fatal(err)
	}
	crumbPath := filepath.Join(home, "terminal-sessions", "tty-"+strings.ReplaceAll(strings.Trim(tty, "/"), "/", "-"))
	leases, err := json.Marshal(map[string]any{"entries": []map[string]any{{"pid": cmd.Process.Pid, "process_start_time": start, "session_id": "startup-old"}}})
	if err != nil {
		t.Fatal(err)
	}
	crumb, err := json.Marshal(map[string]any{"session_id": "current-new", "cwd": workspace, "ts": start + 1})
	if err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string][]byte{
		filepath.Join(sessionDir(data, "identity-fixture"), "native-home"):      []byte(home),
		filepath.Join(sessionDir(data, "identity-fixture"), "native-workspace"): []byte(workspace),
		filepath.Join(home, "runtime", "active_sessions.json"):                  leases,
		crumbPath: crumb,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE sessions(id TEXT PRIMARY KEY, cwd TEXT); INSERT INTO sessions VALUES('current-new', NULL)"); err != nil {
		t.Fatal(err)
	}
	cfg := ports.NativeSessionResolveConfig{DataDir: data, SessionID: "identity-fixture", LaunchID: "owned-launch"}
	id, ok, err := New().ResolveNativeSessionID(context.Background(), cfg)
	if err != nil || !ok || id != "current-new" {
		t.Fatalf("current PTY identity: %q %v %v", id, ok, err)
	}
	cfg.LaunchID = "different-launch"
	if _, ok, err := New().ResolveNativeSessionID(context.Background(), cfg); err != nil || ok {
		t.Fatalf("accepted unowned generation: %v %v", ok, err)
	}
	cfg.LaunchID = "owned-launch"
	if _, err := db.ExecContext(t.Context(), "UPDATE sessions SET cwd='/different-workspace'"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := New().ResolveNativeSessionID(context.Background(), cfg); err != nil || ok {
		t.Fatalf("accepted conflicting native cwd: %v %v", ok, err)
	}
}
