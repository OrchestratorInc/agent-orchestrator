package coderagents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestToolPathsStayInsideWorktree(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	runner := &toolRunner{root: root}
	for _, path := range []string{"../escape.txt", filepath.Join(outside, "abs.txt")} {
		outcome := runner.run(context.Background(), toolWrite, `{"path":`+jsonString(path)+`,"content":"x"}`)
		if !outcome.IsError {
			t.Fatalf("write to %s was allowed", path)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
			t.Fatal(err)
		}
		if outcome := runner.run(context.Background(), toolWrite, `{"path":"link/through.txt","content":"x"}`); !outcome.IsError {
			t.Fatal("write through a symlink out of the worktree was allowed")
		}
		if _, err := os.Stat(filepath.Join(outside, "through.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("symlink escape wrote outside: %v", err)
		}
	}
	if outcome := runner.run(context.Background(), toolWrite, `{"path":"in/side.txt","content":"x"}`); outcome.IsError {
		t.Fatalf("write inside the worktree failed: %s", outcome.Text)
	}
}

func TestEditRequiresUniqueMatch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("a a b"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &toolRunner{root: root}
	if outcome := runner.run(context.Background(), toolEdit, `{"path":"f.txt","old_text":"a","new_text":"z"}`); !outcome.IsError {
		t.Fatal("ambiguous edit applied")
	}
	if outcome := runner.run(context.Background(), toolEdit, `{"path":"f.txt","old_text":"b","new_text":"c"}`); outcome.IsError {
		t.Fatalf("unique edit failed: %s", outcome.Text)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "f.txt")); string(data) != "a a c" {
		t.Fatalf("file = %q", data)
	}
}

func TestReadOnlyRunnerRefusesChanges(t *testing.T) {
	runner := &toolRunner{root: t.TempDir(), readOnly: true}
	if outcome := runner.run(context.Background(), toolExecute, `{"command":"true"}`); !outcome.IsError {
		t.Fatal("read-only conversation ran a command")
	}
	for _, tool := range declaredTools(true, false) {
		if spec, _ := specFor(tool.Name); spec.class != toolClassRead {
			t.Fatalf("read-only chat declared %s", tool.Name)
		}
	}
	if len(declaredTools(false, true)) != 0 {
		t.Fatal("tool-less conversation declared tools")
	}
}

func TestExecuteRunsInWorktreeAndReportsExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	root := t.TempDir()
	runner := &toolRunner{root: root, env: toolEnv(os.Environ())}
	outcome := runner.run(context.Background(), toolExecute, `{"command":"pwd -P; exit 3"}`)
	if outcome.IsError || outcome.ExitCode == nil || *outcome.ExitCode != 3 {
		t.Fatalf("outcome = %+v", outcome)
	}
	realRoot, _ := filepath.EvalSymlinks(root)
	if !strings.Contains(outcome.Text, realRoot) {
		t.Fatalf("command ran outside the worktree: %q", outcome.Text)
	}
}

func TestToolEnvDropsCoderCredentials(t *testing.T) {
	env := toolEnv([]string{"PATH=/bin", coderapi.EnvSessionToken + "=secret", "CODER_AGENT_TOKEN=agent", "CODER_URL=https://coder.example.com"})
	for _, entry := range env {
		if strings.Contains(entry, "secret") || strings.HasPrefix(entry, "CODER_AGENT_TOKEN=") {
			t.Fatalf("tool env kept a Coder credential: %v", env)
		}
	}
	if len(env) != 2 {
		t.Fatalf("tool env = %v", env)
	}
}

// A call that started but never recorded a result has an unknown effect. A
// replay must report that instead of running it a second time.
func TestUnfinishedJournaledCallIsNotRerun(t *testing.T) {
	root := t.TempDir()
	journal := newToolJournal(t.TempDir(), "conversation")
	if err := journal.start("call-x"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &conversation{
		cfg: conversationConfig{key: "conversation"}, journal: journal, tools: &toolRunner{root: root},
		ctx: ctx, events: make(chan ports.ChatEvent, 64), approvals: map[string]*pendingApproval{},
	}
	turn := c.newTurn("t1", ports.PermissionModeBypassPermissions)
	outcome, ok := c.executeCall(turn, coderapi.ToolCall{ToolCallID: "call-x", ToolName: toolWrite, Args: `{"path":"x.txt","content":"again"}`})
	if !ok || !outcome.IsError || !strings.Contains(outcome.Text, "effect is unknown") {
		t.Fatalf("outcome = %+v, %v", outcome, ok)
	}
	if _, err := os.Stat(filepath.Join(root, "x.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unfinished call ran again: %v", err)
	}

	finished := toolOutcome{Output: json.RawMessage(`{"ok":true}`), Text: "done"}
	if err := journal.finish("call-y", finished); err != nil {
		t.Fatal(err)
	}
	outcome, ok = c.executeCall(turn, coderapi.ToolCall{ToolCallID: "call-y", ToolName: toolWrite, Args: `{"path":"y.txt","content":"again"}`})
	if !ok || outcome.IsError || string(outcome.Output) != `{"ok":true}` {
		t.Fatalf("finished call outcome = %+v", outcome)
	}
	if _, err := os.Stat(filepath.Join(root, "y.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("finished call ran again: %v", err)
	}
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
