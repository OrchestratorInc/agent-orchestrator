package conpty

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

func TestCountWorkloadCommands(t *testing.T) {
	host := 100
	base := []procmem.Process{
		{PID: host, PPID: 1, Command: "ao pty-host s1"},
		{PID: 101, PPID: host, Command: "ao agent-process supervise --session s1 --launch l1 -- claude"},
		{PID: 102, PPID: 101, Command: "claude"},
		{PID: 103, PPID: 102, Command: "node mcp-server.js"},
		{PID: 99, PPID: host, Command: "conhost.exe"},
	}
	if got, err := countWorkloadCommands(base, host); err != nil || got != 0 {
		t.Fatalf("idle agent = %d, %v; want 0 (its own helpers are not commands)", got, err)
	}
	busy := append(base,
		procmem.Process{PID: 104, PPID: 102, Command: "/bin/zsh -c npm test"},
		procmem.Process{PID: 105, PPID: 104, Command: "bash -c vitest"},
		procmem.Process{PID: 106, PPID: 105, Command: "node vitest"},
	)
	if got, err := countWorkloadCommands(busy, host); err != nil || got != 3 {
		t.Fatalf("agent running a command = %d, %v; want 3", got, err)
	}
	windows := append(base, procmem.Process{PID: 107, PPID: 102, Command: "powershell.exe"})
	if got, err := countWorkloadCommands(windows, host); err != nil || got != 1 {
		t.Fatalf("agent running PowerShell = %d, %v; want 1", got, err)
	}
	// A login shell between the host and the supervisor is not a command.
	wrapped := []procmem.Process{
		{PID: host, PPID: 1, Command: "ao pty-host s1"},
		{PID: 110, PPID: host, Command: "/bin/zsh -lc ao agent-process supervise --session s1"},
		{PID: 111, PPID: 110, Command: "ao agent-process supervise --session s1 --launch l1 -- codex"},
		{PID: 112, PPID: 111, Command: "codex"},
	}
	if got, err := countWorkloadCommands(wrapped, host); err != nil || got != 0 {
		t.Fatalf("idle agent under a login shell = %d, %v; want 0", got, err)
	}
	if _, err := countWorkloadCommands([]procmem.Process{{PID: host, PPID: 1}}, host); err == nil {
		t.Fatal("no supervised agent was reported as no commands")
	}
}
