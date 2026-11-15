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

const acceptRetryBackoff = 200 * time.Millisecond

type Supervisor struct {
	grace            time.Duration
	onLastClientGone func()
	log              *slog.Logger

	mu           sync.Mutex
	liveCount    int
	armed        bool
	pendingTimer *time.Timer

	fireOnce sync.Once
}

func New(grace time.Duration, onLastClientGone func(), log *slog.Logger) *Supervisor {
	return &Supervisor{
		grace:            grace,
		onLastClientGone: onLastClientGone,
		log:              log,
	}
}

func (s *Supervisor) Serve(ctx context.Context, ln net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		defer sentryobs.RecoverBackground()
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
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

func (s *Supervisor) watchConn(conn net.Conn) {
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
