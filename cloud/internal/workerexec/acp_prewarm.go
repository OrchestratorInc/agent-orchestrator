package workerexec

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"maps"
	"slices"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// A Claude Chat turn spends seconds before its prompt reaches the model:
// launching the ACP adapter, its initialize handshake, and opening the
// session (about 1 s on a laptop, 2-3 s on a 2 vCPU sandbox). The controller
// starts that agent ahead of the turn, once while the workspace is still being
// prepared and again after each turn for the next one, and a turn whose launch
// is the same takes it instead of starting its own.

// warmACP is one agent being started ahead of its turn.
type warmACP struct {
	cancel context.CancelFunc
	// keyed closes once key is set; done once agent is set (nil on failure).
	keyed chan struct{}
	key   [32]byte
	done  chan struct{}
	agent *startedACP
}

// Prewarm starts, without waiting, the agent the controller's first turn is
// expected to use (WarmTurn). The agent is stopped when ctx ends unless a turn
// took it first. Only the worker's first Chat activation prewarms: a later
// one follows a handoff from the terminal, whose agent may still be releasing
// the conversation.
func (s *Supervisor) Prewarm(ctx context.Context) {
	if s.WarmTurn != nil && s.startupPrewarmed.CompareAndSwap(false, true) {
		s.prewarm(ctx, *s.WarmTurn)
	}
}

func (s *Supervisor) prewarm(ctx context.Context, turn worker.Turn) {
	control, ok := s.Control.(approvalControl)
	if !ok || !s.UseProviderProtocol || s.Builder == nil || turn.Harness != "claude-code" || len(turn.DeniedCommands) > 0 {
		return
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	warmCtx, cancel := context.WithCancel(ctx)
	warm := &warmACP{cancel: cancel, keyed: make(chan struct{}), done: make(chan struct{})}
	s.warmMu.Lock()
	previous := s.warm
	s.warm = warm
	s.warmMu.Unlock()
	go previous.discard()
	go func() {
		keyed := false
		defer func() {
			if !keyed {
				close(warm.keyed)
			}
			close(warm.done)
		}()
		credential, err := s.Control.Credential(warmCtx)
		if err != nil {
			return
		}
		command, err := s.Builder.Build(warmCtx, turn, credential, s.Workspace)
		credential.Secret = ""
		if err != nil {
			return
		}
		warm.key = acpLaunchKey(turn, command)
		close(warm.keyed)
		keyed = true
		agent, err := startACP(warmCtx, control, turn, command)
		if err != nil {
			if command.Cleanup != nil {
				command.Cleanup()
			}
			logger.Debug("prewarm ACP agent", "error", err)
			return
		}
		agent.cleanup = func() {
			if command.Cleanup != nil {
				command.Cleanup()
			}
			cancel()
		}
		// Claude reads the checkout (CLAUDE.md, the branch) when it opens the
		// session, so a session prewarmed during startup waits for the
		// checkout; the process and handshake above did not.
		if gate := s.SessionGate; gate != nil {
			select {
			case <-gate:
			case <-warmCtx.Done():
				agent.close()
				return
			}
		}
		if err := agent.openSession(warmCtx, turn, command); err != nil {
			agent.close()
			logger.Debug("prewarm ACP session", "error", err)
			return
		}
		context.AfterFunc(warmCtx, agent.close)
		warm.agent = agent
	}()
}

// takeWarmACP returns the prewarmed agent when it was started for exactly
// this turn's launch, waiting for it to finish starting: it began before the
// turn, so it is ready sooner than a new one. Any other prewarmed agent is
// stopped and nil returned.
func (s *Supervisor) takeWarmACP(ctx context.Context, turn worker.Turn, command Command) *startedACP {
	s.warmMu.Lock()
	warm := s.warm
	s.warm = nil
	s.warmMu.Unlock()
	if warm == nil {
		return nil
	}
	select {
	case <-warm.keyed:
	case <-ctx.Done():
		go warm.discard()
		return nil
	}
	if warm.key != acpLaunchKey(turn, command) {
		// Stopped before the turn starts its own: the two may hold the same
		// conversation. Cancelling ends a start in progress at once.
		warm.discard()
		return nil
	}
	select {
	case <-warm.done:
	case <-ctx.Done():
		go warm.discard()
		return nil
	}
	if warm.agent == nil {
		return nil
	}
	return warm.agent
}

// discardWarmACP stops any prewarmed agent and waits for it to exit.
func (s *Supervisor) discardWarmACP() {
	s.warmMu.Lock()
	warm := s.warm
	s.warm = nil
	s.warmMu.Unlock()
	warm.discard()
}

func (w *warmACP) discard() {
	if w == nil {
		return
	}
	w.cancel()
	<-w.done
	if w.agent != nil {
		w.agent.close()
	}
}

// acpLaunchKey identifies everything an ACP agent is started and its session
// opened with, so a prewarmed agent serves only a turn that would have
// started the same one. The environment holds the credential, which is
// hashed here rather than kept in a second copy.
func acpLaunchKey(turn worker.Turn, command Command) [32]byte {
	env := claudeModelEnvironment(command, turn.Model, nil)
	hash := sha256.New()
	write := func(value string) {
		hash.Write([]byte(value))
		hash.Write([]byte{0})
	}
	write(turn.Harness)
	write(turn.AgentSessionID)
	write(command.Path)
	write(command.Dir)
	write(command.SystemPrompt)
	for _, dir := range command.AdditionalDirectories {
		write(dir)
	}
	write("")
	for _, key := range slices.Sorted(maps.Keys(env)) {
		write(key)
		write(env[key])
	}
	var key [32]byte
	copy(key[:], hash.Sum(nil))
	return key
}
