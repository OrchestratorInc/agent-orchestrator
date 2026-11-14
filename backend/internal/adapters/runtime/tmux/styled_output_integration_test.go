package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestStyledOutputExcludesHistoricalEmptyComposer(t *testing.T) {
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	ctx := context.Background()
	id := t.Name()
	r := New(Options{Binary: binary, SocketName: fmt.Sprintf("ao-styled-%d-%d", os.Getpid(), time.Now().UnixNano()), Timeout: 5 * time.Second})
	workspace := t.TempDir()
	readyFile := filepath.Join(workspace, "ready")
	handle, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(id),
		WorkspacePath: workspace,
		// Scroll an empty-looking input entirely into history. The current
		// viewport is blank, so it cannot prove a TUI composer is ready.
		Argv: []string{"/bin/sh", "-c", `printf '❯ \033[2mEnter a coding task\033[0m\n'; i=0; while [ "$i" -lt 55 ]; do printf '\n'; i=$((i + 1)); done; printf ready > "$AO_READY_FILE"; sleep 60`},
		Env:  map[string]string{"AO_READY_FILE": readyFile},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = r.Destroy(context.Background(), handle) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not finish writing its screen")
		}
		time.Sleep(10 * time.Millisecond)
	}
	history, err := r.GetOutput(ctx, handle, 100)
	if err != nil {
		t.Fatalf("GetOutput: %v", err)
	}
	if !strings.Contains(history, "Enter a coding task") {
		t.Fatalf("ordinary output should retain history, got %q", history)
	}
	current, err := r.GetStyledOutput(ctx, handle, 100)
	if err != nil {
		t.Fatalf("GetStyledOutput: %v", err)
	}
	if strings.TrimSpace(current) != "" {
		t.Fatalf("styled viewport contains historical composer: %q", current)
	}
}
