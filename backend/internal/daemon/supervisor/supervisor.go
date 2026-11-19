// Package supervisor provides a transport-agnostic watchdog that fires a
// callback when the last connected client disconnects and stays gone for a
// configurable grace period. It arms only after the FIRST client ever
// connects so a daemon started with no frontend (e.g. CLI "ao start") never
// self-stops.
package supervisor

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
)

// acceptRetryBackoff bounds the retry after a transient Accept error so a
// persistent failure cannot hot-spin the accept loop.
const acceptRetryBackoff = 200 * time.Millisecond

// Supervisor watches connections on a net.Listener and calls onLastClientGone
// exactly once (per process lifetime) when the live-count drops to zero and
// stays zero for the grace period.
//
// Concurrency model:
//   - mu guards liveCount, armed, and pendingTimer.
//   - armed flips to true on the first accepted connection and never resets;
//     it is the "headless-safety" gate that prevents a pre-connect fire.
//   - pendingTimer holds the *time.Timer from time.AfterFunc so it can be
//     stopped on reconnect. A non-nil pendingTimer means a grace countdown is
//     running.
//   - fireOnce ensures onLastClientGone is called at most once for the entire
//     process lifetime, even if the timer fires concurrently with a reconnect.
type Supervisor struct {
	grace            time.Duration
	onLastClientGone func()
	log              *slog.Logger

	mu           sync.Mutex
	liveCount    int
	armed        bool        // true once any connection has been accepted
	pendingTimer *time.Timer // non-nil while grace countdown is running

	fireOnce sync.Once
}

// New creates a Supervisor. grace is the delay before the callback fires after
// the last connection closes. onLastClientGone is called at most once for the
// process lifetime, so it is safe to use it to trigger os.Exit or context
// cancellation.
func New(grace time.Duration, onLastClientGone func(), log *slog.Logger) *Supervisor {
	return &Supervisor{
		grace:            grace,
		onLastClientGone: onLastClientGone,
		log:              log,
	}
}

// Serve runs the accept loop on ln until ctx is cancelled or ln is closed.
// It returns nil on a clean shutdown (context cancelled or listener closed
// normally); it only returns a non-nil error for unexpected Accept failures.
func (s *Supervisor) Serve(ctx context.Context, ln net.Listener) error {
	// Derive a cancellable context so the watcher goroutine always unblocks
	// when Serve returns, even if ctx itself is not cancelled (e.g. listener
	// closed directly).
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Close the listener when ctx is cancelled so Accept() unblocks.
	go func() {
		defer sentryobs.RecoverBackground()
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			// A closed listener or context cancellation is a clean stop.
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			// net.ErrClosed is what real listeners return when closed normally.
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			// A transient Accept error (e.g. EMFILE) must NOT silently kill the
			// watchdog: that would leave the daemon unable to self-stop on
			// frontend death. Back off briefly and keep accepting. A genuinely
			// closed listener returns net.ErrClosed (handled above) or trips
			// ctx.Done during the backoff.
			s.log.Warn("supervisor: accept error, retrying", "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(acceptRetryBackoff):
			}
			continue
		}

		s.mu.Lock()
		s.armed = true
		s.liveCount++
		// If a grace timer was pending (reconnect before grace elapsed), cancel it.
		if s.pendingTimer != nil {
			s.pendingTimer.Stop()
			s.pendingTimer = nil
		}
		live := s.liveCount
		s.mu.Unlock()

		s.log.Debug("supervisor: client connected", "liveCount", live)
		go func() { defer sentryobs.RecoverBackground(); s.watchConn(conn) }()
	}
}

// watchConn drains conn (reads into a scratch buffer) purely to detect close.
// When read returns io.EOF or any error, the connection is gone.
func (s *Supervisor) watchConn(conn net.Conn) {
	// ponytail: 32-byte scratch buffer; we never process the payload.
	scratch := make([]byte, 32)
	for {
		_, err := conn.Read(scratch)
		if err != nil {
			break
		}
	}
	_ = conn.Close()

	s.mu.Lock()
	s.liveCount--
	live := s.liveCount
	armed := s.armed
	s.mu.Unlock()

	s.log.Debug("supervisor: client disconnected", "liveCount", live)

	if armed && live == 0 {
		s.armGrace()
	}
}

// armGrace starts the grace countdown. If another client connects before it
// elapses, Serve() will Stop() the timer via pendingTimer.
func (s *Supervisor) armGrace() {
	s.mu.Lock()
	s.pendingTimer = time.AfterFunc(s.grace, func() {
		defer sentryobs.RecoverBackground()
		s.mu.Lock()
		live := s.liveCount
		s.pendingTimer = nil
		s.mu.Unlock()

		if live == 0 {
			s.log.Info("supervisor: last client gone; grace elapsed, firing callback")
			s.fireOnce.Do(s.onLastClientGone)
		}
	})
	s.mu.Unlock()
}
