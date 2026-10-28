package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workertransport"
)

// chatStartChecks are what startInterface asks of the workspace and the
// control plane; tests replace them.
type chatStartChecks struct {
	// firstStart is true on the session's first worker start in this
	// workspace (no persisted worker token). Only then may a session move off
	// Chat: a restarted or reconnecting worker leaves the interface alone.
	firstStart bool
	// harnessInstalled reports whether the harness binary is on PATH.
	harnessInstalled func(harness string) bool
	// claudeChatStartable is workerexec.ClaudeACPAdapterStartable.
	claudeChatStartable func() error
	// fallBack asks the control plane to move the session to the terminal
	// (POST /worker/interface/chat-start-fallback) and reports whether it did.
	fallBack func(ctx context.Context, code string) (bool, error)
}

// startInterface is the interface the worker starts its session in: the one
// the control plane committed, except that a Claude session committed to Chat
// in a workspace with no ACP adapter and no npm to install one starts in the
// terminal, with an interface notice, instead of in a Chat whose every turn
// would fail. The control plane decides: it moves only a session created in
// Chat that never started, and a session the person switched to Chat stays
// there.
//
// The check waits for nothing. It runs only once the harness itself is
// installed: a template that installs its tools in the background may add
// npm along with the harness, and until then the adapter's install is not
// known to be impossible. An install that fails, or does not finish soon
// after the workspace is ready, is settled then (settleClaudeChatStart).
func startInterface(ctx context.Context, logger *slog.Logger, launch worker.LaunchContext, checks chatStartChecks) string {
	committed := strings.TrimSpace(launch.Interface)
	if committed == "" {
		return workertransport.InterfaceTUI
	}
	if !checks.firstStart || committed != workertransport.InterfaceChat || launch.Harness != "claude-code" ||
		!checks.harnessInstalled(launch.Harness) {
		return committed
	}
	reason := checks.claudeChatStartable()
	if !errors.Is(reason, workerexec.ErrChatAdapterUnavailable) {
		return committed
	}
	moved, err := checks.fallBack(ctx, worker.ChatStartAdapterUnavailable)
	switch {
	case err != nil:
		logger.Warn("chat start: Claude Chat cannot run here and the session could not move to the terminal",
			"reason", reason, "error", err)
		return committed
	case !moved:
		logger.Warn("chat start: Claude Chat cannot run here; the session stays in Chat", "reason", reason)
		return committed
	}
	logger.Info("chat start: Claude Chat cannot run here; the session starts in the terminal", "reason", reason)
	return workertransport.InterfaceTUI
}

// claudeChatAdapterWait bounds how long a Claude session that starts in Chat
// waits, once its workspace is ready, for the Claude ACP adapter. A template
// that installs Node in its own startup script lets the worker start the
// install only then, and it takes about 20 s, so a short wait moved every
// first start of such a session to the terminal. The app shows the startup
// steps meanwhile. Past it the session starts in the terminal while the
// install carries on in the background for a later switch to Chat.
const claudeChatAdapterWait = 90 * time.Second

// chatAdapterSettle is what settleClaudeChatStart asks of the workspace, the
// control plane and the transport; tests replace them.
type chatAdapterSettle struct {
	// firstStart is chatStartChecks.firstStart.
	firstStart bool
	// ensure waits for the Claude ACP adapter (workerexec.EnsureClaudeACPAdapter).
	ensure func(ctx context.Context) error
	// fallBack is chatStartChecks.fallBack.
	fallBack func(ctx context.Context, code string) (bool, error)
	// switchToTUI replaces the Chat start with the terminal agent
	// (workertransport.Supervisor.FallBackToTUI).
	switchToTUI func(ctx context.Context) error
}

// settleClaudeChatStart runs once the workspace is ready and before the Chat
// controller may take a turn. startInterface settled the case it can see at
// once (no adapter and no npm); here the session starts in Chat only if the
// adapter is installed or its install finishes within claudeChatAdapterWait.
// Otherwise (the install failed, or is still running) the session moves to
// the terminal with an informational notice and its task on the agent's
// command line, rather than the person waiting on npm or every Chat turn
// failing on the missing adapter. An install still running carries on in the
// background, so a later switch to Chat finds the adapter. It reports whether
// the session moved.
func settleClaudeChatStart(ctx context.Context, logger *slog.Logger, launch worker.LaunchContext, settle chatAdapterSettle) bool {
	if !settle.firstStart || launch.Interface != workertransport.InterfaceChat || launch.Harness != "claude-code" {
		return false
	}
	waitCtx, cancel := context.WithTimeout(ctx, claudeChatAdapterWait)
	reason := settle.ensure(waitCtx)
	cancel()
	if ctx.Err() != nil || (!errors.Is(reason, workerexec.ErrChatAdapterUnavailable) &&
		!errors.Is(reason, workerexec.ErrChatAdapterInstalling)) {
		return false
	}
	moved, err := settle.fallBack(ctx, worker.ChatStartAdapterUnavailable)
	switch {
	case err != nil:
		logger.Warn("chat start: the Claude ACP adapter is not ready and the session could not move to the terminal",
			"reason", reason, "error", err)
		return false
	case !moved:
		logger.Warn("chat start: the Claude ACP adapter is not ready; the session stays in Chat", "reason", reason)
		return false
	}
	if err := settle.switchToTUI(ctx); err != nil {
		logger.Error("chat start: start the terminal agent while the Claude ACP adapter is not ready", "error", err)
		return false
	}
	logger.Info("chat start: the Claude ACP adapter is not ready; the session runs in the terminal", "reason", reason)
	return true
}

func (c *client) fallBackChatStart(ctx context.Context, code string) (bool, error) {
	var response worker.ChatStartFallbackResponse
	if err := c.do(ctx, "/worker/interface/chat-start-fallback", worker.ChatStartFallbackRequest{Code: code}, &response); err != nil {
		return false, err
	}
	return response.MovedToTUI, nil
}
