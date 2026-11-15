//go:build !windows

package mimocode

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func writeProbeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mimo")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// Every MiMo command except --version imports all Claude Code history at
// startup, so a status probe must not start that scan.
func TestAuthProbeDisablesClaudeImport(t *testing.T) {
	binary := writeProbeScript(t, `printf '%s' "$MIMOCODE_DISABLE_CLAUDE_IMPORT"`)
	out, err := runAuthProbe(context.Background(), binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "1" {
		t.Fatalf("MIMOCODE_DISABLE_CLAUDE_IMPORT = %q, want 1", out)
	}
}

// `mimo` is a Node wrapper that waits on the real binary, which shares its
// stdout. Killing only the wrapper leaves the binary running and keeps the
// probe blocked on the pipe long past its deadline.
func TestAuthProbeTimeoutStopsWrappedBinary(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	binary := writeProbeScript(t, `sleep 8 &
echo $! > '`+pidFile+`'
wait`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	_, _ = runAuthProbe(ctx, binary)
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("probe returned after %v, want it stopped at its deadline", elapsed)
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
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("wrapped binary %d outlived the probe", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestNativeConversationExistsDisablesClaudeImport(t *testing.T) {
	dir := t.TempDir()
	body := "#!/bin/sh\nprintf '{\"info\":{\"id\":\"%s\"},\"import\":\"%s\",\"caller\":\"%s\"}' \"$2\" \"$MIMOCODE_DISABLE_CLAUDE_IMPORT\" \"$CALLER_VALUE\" > '" + filepath.Join(dir, "seen") + "'\ncat '" + filepath.Join(dir, "seen") + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "mimo"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	p := New()
	p.InvalidateBinaryResolution()
	callerEnv := map[string]string{"CALLER_VALUE": "kept"}
	_, _ = p.NativeConversationExists(context.Background(), ports.SessionRef{WorkspacePath: dir}, "ses_1", callerEnv)
	seen, err := os.ReadFile(filepath.Join(dir, "seen"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(seen), `"import":"1"`) || !strings.Contains(string(seen), `"caller":"kept"`) {
		t.Fatalf("export env = %s, want the import disabled and the caller's env kept", seen)
	}
	if len(callerEnv) != 1 {
		t.Fatalf("caller env mutated: %v", callerEnv)
	}
}
