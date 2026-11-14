package runtimeselect

import (
	"context"
	"log/slog"
	"runtime"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Runtime interface {
	ports.Runtime
	ports.RuntimeChildInspector
	ports.RuntimeProcessRootInspector
	ports.FencedRuntimeProber
	ports.ExactSupervisedProcessInspector
	ports.SupervisedProcessRecordInspector
	ports.Attacher
	Interrupt(ctx context.Context, handle ports.RuntimeHandle) error
	SendInput(ctx context.Context, handle ports.RuntimeHandle, input string) error
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
	ServerPID(ctx context.Context) (int, bool)
}

var _ Runtime = (*tmux.Runtime)(nil)
var _ Runtime = (*conpty.Runtime)(nil)

func New(log *slog.Logger, runFilePath string) Runtime {
	switch runtime.GOOS {
	case "windows":
		return conpty.New(conpty.Options{RunFilePath: runFilePath})
	case "darwin":
		return newHybridRuntime(
			tmux.New(tmux.Options{}),
			conpty.New(conpty.Options{RunFilePath: runFilePath}),
			log,
			"macOS",
		)
	case "linux":
		return newHybridRuntime(
			tmux.New(tmux.Options{}),
			conpty.New(conpty.Options{RunFilePath: runFilePath}),
			log,
			"Linux",
		)
	default:
		return tmux.New(tmux.Options{})
	}
}
