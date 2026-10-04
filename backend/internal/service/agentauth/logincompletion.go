package agentauth

import (
	"context"
	"time"
)

const (
	// defaultLoginPollInterval mirrors the Codex account login monitor cadence.
	defaultLoginPollInterval = 250 * time.Millisecond
	// defaultLoginInitialGrace lets a just-spawned login command appear before a
	// first "not alive" reading is trusted, so a brief spawn race is not mistaken
	// for immediate completion.
	defaultLoginInitialGrace = 2 * time.Second
	// defaultLoginMaxLifetime bounds the monitor so an abandoned login terminal
	// cannot keep a goroutine alive forever; it matches the Codex login lifetime.
	defaultLoginMaxLifetime = 15 * time.Minute
)

// LoginCompletion observes the child process of a login terminal so the daemon
// can tell when a native sign-in flow has finished. The shell-terminal service
// satisfies it.
type LoginCompletion interface {
	IsShellTerminalChildAlive(ctx context.Context, handleID string) (bool, error)
}

// ReadinessRefresher re-checks a harness's readiness after a login flow so a
// completed sign-in is reflected without a client polling. The agent service
// satisfies it.
type ReadinessRefresher interface {
	InvalidateAgentAuthentication(agentID string)
	RecheckAgent(agentID string)
}

// EnableLoginCompletionRefresh wires daemon-side login-completion detection.
// After it is called, every login terminal the service opens is watched; when
// the native login command exits, the harness's cached authentication
// observation is invalidated and re-checked so a finished sign-in becomes usable
// even when no Settings panel (or any client) is driving the refresh. Callers
// that leave it unset keep the previous client-driven behavior unchanged.
func (s *Service) EnableLoginCompletionRefresh(ctx context.Context, watcher LoginCompletion, refresher ReadinessRefresher) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.monitorCtx = ctx
	s.loginWatcher = watcher
	s.refresher = refresher
	if s.pollInterval <= 0 {
		s.pollInterval = defaultLoginPollInterval
	}
	if s.initialGrace <= 0 {
		s.initialGrace = defaultLoginInitialGrace
	}
	if s.maxLifetime <= 0 {
		s.maxLifetime = defaultLoginMaxLifetime
	}
	if s.after == nil {
		s.after = time.After
	}
}

// startLoginCompletionWatch launches the login-completion monitor for a freshly
// opened login terminal. It is a no-op unless EnableLoginCompletionRefresh wired
// the dependencies and the terminal has a handle.
func (s *Service) startLoginCompletionWatch(agentID, handleID string) {
	if s.loginWatcher == nil || s.refresher == nil || handleID == "" {
		return
	}
	go s.watchLoginCompletion(agentID, handleID)
}

// watchLoginCompletion polls the login command's liveness and, once it exits,
// refreshes the harness readiness so the new credentials are picked up. It
// mirrors the Codex account login monitor: child-process exit — not terminal
// host liveness — is the completion signal.
func (s *Service) watchLoginCompletion(agentID, handleID string) {
	ctx := s.monitorCtx
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(s.maxLifetime)
	start := time.Now()
	sawAlive := false
	for {
		alive, err := s.loginWatcher.IsShellTerminalChildAlive(ctx, handleID)
		switch {
		case err == nil && alive:
			sawAlive = true
		case err == nil && !alive && (sawAlive || time.Since(start) >= s.initialGrace):
			// The login command is gone. Treat this as "the user finished (or
			// abandoned) the flow" and re-probe so a successful sign-in flips the
			// harness to authenticated without the client polling.
			s.refresher.InvalidateAgentAuthentication(agentID)
			s.refresher.RecheckAgent(agentID)
			return
		}
		if !time.Now().Before(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-s.after(s.pollInterval):
		}
	}
}
