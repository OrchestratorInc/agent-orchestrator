package triage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty/ptyregistry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type harnessRuntime interface {
	ports.Runtime
	ports.RuntimeChildInspector
	ports.Attacher
	SendMessage(context.Context, ports.RuntimeHandle, string) error
}

// terminalHarnesses uses only the registered launch adapters. The chooser never
// supplies an executable, argv, shell command, or environment value.
func terminalHarnesses() map[string]ports.Agent {
	out := map[string]ports.Agent{}
	for _, item := range agentregistry.Harnessed() {
		out[string(item.Harness)] = item.Agent
	}
	return out
}

// TerminalAgent owns one selected adapter process and its terminal stream.
type TerminalAgent struct {
	runtime harnessRuntime
	handle  ports.RuntimeHandle
	stream  ports.Stream
	startup <-chan error
	cancel  context.CancelFunc
	once    sync.Once
	err     error
}

var _ ports.Stream = (*TerminalAgent)(nil)

func (a *TerminalAgent) Read(p []byte) (int, error)  { return a.stream.Read(p) }
func (a *TerminalAgent) Write(p []byte) (int, error) { return a.stream.Write(p) }

// Resize updates the attached native terminal dimensions.
func (a *TerminalAgent) Resize(rows, cols uint16) error { return a.stream.Resize(rows, cols) }

// Close releases the stream and terminates only this owned agent.
func (a *TerminalAgent) Close() error {
	a.once.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
		a.err = errors.Join(a.stream.Close(), destroyTerminal(a.runtime, a.handle))
	})
	return a.err
}

func destroyTerminal(runtime ports.Runtime, handle ports.RuntimeHandle) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return runtime.Destroy(ctx, handle)
}

func launchTerminalAgent(ctx context.Context, run Run, workspace, dataDir, prompt string, env map[string]string, agent ports.Agent, runtime harnessRuntime) (*TerminalAgent, error) {
	selection := run.Request.Selection
	cfg := ports.LaunchConfig{
		Config:  domain.AgentConfig{Model: selection.Model, Effort: selection.Effort, Mode: selection.Mode, Permissions: selection.Permissions},
		DataDir: dataDir, Kind: domain.KindWorker, Permissions: selection.Permissions,
		Prompt: prompt, SessionID: "triage-" + run.ID, WorkspacePath: workspace, Env: env,
	}
	if selection.Harness == "amp" && cfg.Config.Model != "" {
		cfg.Config.Mode, cfg.Config.Model = cfg.Config.Model, ""
	}
	argv, err := agent.GetLaunchCommand(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("prepare selected agent: %w", err)
	}
	strategy, err := agent.GetPromptDeliveryStrategy(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("prepare selected agent prompt: %w", err)
	}
	if strategy != ports.PromptDeliveryInCommand && strategy != ports.PromptDeliveryAfterStart {
		return nil, errors.New("selected agent does not support native prompt delivery")
	}
	handle, err := runtime.Create(ctx, ports.RuntimeConfig{
		SessionID: domain.SessionID(cfg.SessionID), WorkspacePath: workspace,
		Argv: argv, Env: env,
	})
	if err != nil {
		var effect ports.RuntimeEffectError
		if errors.As(err, &effect) && effect.PossibleHandle().ID != "" {
			err = errors.Join(err, destroyTerminal(runtime, effect.PossibleHandle()))
		}
		return nil, fmt.Errorf("start selected agent: %w", err)
	}
	stream, err := runtime.Attach(ctx, handle, 24, 80)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("attach selected agent terminal: %w", err), destroyTerminal(runtime, handle))
	}
	terminal := &TerminalAgent{runtime: runtime, handle: handle, stream: stream}
	if strategy == ports.PromptDeliveryAfterStart {
		startup := make(chan error, 1)
		startupCtx, cancel := context.WithCancel(ctx)
		terminal.startup, terminal.cancel = startup, cancel
		go func() { startup <- deliverInitialPrompt(startupCtx, runtime, agent, handle, cfg, prompt) }()
	}
	return terminal, nil
}

func deliverInitialPrompt(ctx context.Context, runtime harnessRuntime, agent ports.Agent, handle ports.RuntimeHandle, cfg ports.LaunchConfig, prompt string) error {
	if err := waitForAgentPrompt(ctx, runtime, agent, handle, cfg); err != nil {
		return err
	}
	if selector, ok := agent.(ports.AgentAfterStartCommand); ok {
		command, err := selector.AfterStartCommand(ctx, cfg)
		if err != nil {
			return err
		}
		if command != "" {
			if err := runtime.SendMessage(ctx, handle, command); err != nil {
				return err
			}
			if err := waitForTerminal(ctx, runtime, handle, 10*time.Second, 100*time.Millisecond, 100, func(output string) bool {
				return selector.AfterStartCommandApplied(cfg, output)
			}); err != nil {
				return err
			}
		}
	}
	if builder, ok := agent.(ports.AgentAfterStartPromptBuilder); ok {
		var err error
		prompt, err = builder.BuildAfterStartPrompt(ctx, cfg)
		if err != nil {
			return err
		}
	}
	if err := runtime.SendMessage(ctx, handle, prompt); err != nil {
		return fmt.Errorf("deliver investigation prompt: %w", err)
	}
	return nil
}

func waitForAgentPrompt(ctx context.Context, runtime harnessRuntime, agent ports.Agent, handle ports.RuntimeHandle, cfg ports.LaunchConfig) error {
	provider, ok := agent.(ports.AgentPromptReadinessProvider)
	if !ok {
		return nil
	}
	hints, err := provider.PromptReadinessHints(ctx, cfg)
	if err != nil {
		return err
	}
	if hints.InitialDelay > 0 {
		timer := time.NewTimer(hints.InitialDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if hints.Timeout <= 0 {
		return nil
	}
	interval := hints.PollInterval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	lines := hints.Lines
	if lines <= 0 {
		lines = 80
	}
	return waitForTerminal(ctx, runtime, handle, hints.Timeout, interval, lines, func(output string) bool {
		if detector, ok := agent.(ports.TerminalActivityDetector); ok {
			if state, known := detector.DetectTerminalActivity(output); known && state == domain.ActivityIdle {
				return true
			}
		}
		for _, pattern := range hints.Patterns {
			if strings.Contains(output, pattern) {
				return true
			}
		}
		return false
	})
}

func waitForTerminal(ctx context.Context, runtime harnessRuntime, handle ports.RuntimeHandle, timeout, interval time.Duration, lines int, ready func(string) bool) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		output, err := runtime.GetOutput(ctx, handle, lines)
		if err != nil {
			return err
		}
		if ready(output) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// newHarnessRuntime owns a private registry so restart cleanup cannot adopt or
// destroy an ordinary AO session.
func newHarnessRuntime(ctx context.Context, dataDir string) (harnessRuntime, error) {
	root := filepath.Join(dataDir, "reports", "triage", "provider-hosts", "terminals")
	if err := privateDir(root); err != nil {
		return nil, err
	}
	runtime := conpty.New(conpty.Options{RunFilePath: filepath.Join(root, "running.json")})
	entries, complete, err := ptyregistry.Scan(ctx)
	if err != nil || !complete {
		return nil, errors.New("private diagnostic terminal ownership is unavailable")
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.SessionID, "triage-") {
			return nil, errors.New("unexpected private diagnostic terminal ownership")
		}
		if err := runtime.Destroy(ctx, ports.RuntimeHandle{ID: entry.SessionID}); err != nil {
			return nil, fmt.Errorf("stop interrupted diagnostic terminal: %w", err)
		}
	}
	return runtime, nil
}
