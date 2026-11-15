package httpd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
)

type Server struct {
	cfg     config.Config
	log     *slog.Logger
	http    *http.Server
	listen  net.Listener
	listen6 net.Listener

	shutdownRequested chan struct{}
	shutdownOnce      sync.Once
	stopAgents        atomic.Bool
}

func NewWithDeps(cfg config.Config, log *slog.Logger, termMgr *terminal.Manager, deps APIDeps) (*Server, error) {
	log = loggerOrDefault(log)
	ln, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		if !isAddrInUse(err) {
			return nil, fmt.Errorf("bind %s: %w", cfg.Addr(), err)
		}
		fallback, ferr := net.Listen("tcp", net.JoinHostPort(cfg.Host, "0"))
		if ferr != nil {
			return nil, fmt.Errorf("bind %s (in use) and ephemeral fallback: %w", cfg.Addr(), ferr)
		}
		log.Warn("configured port in use; bound an ephemeral port instead",
			"configured", cfg.Addr(), "bound", fallback.Addr().String())
		ln = fallback
	}

	srv := &Server{
		cfg:               cfg,
		log:               log,
		listen:            ln,
		listen6:           bindIPv6Loopback(cfg, ln, log),
		shutdownRequested: make(chan struct{}),
	}
	srv.http = &http.Server{
		Handler: NewRouterWithControl(cfg, log, termMgr, deps, ControlDeps{
			RequestShutdown:      srv.requestShutdown,
			RequestAgentShutdown: srv.requestAgentShutdown,
			IPv6Loopback:         srv.listen6 != nil,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv, nil
}

func bindIPv6Loopback(cfg config.Config, primary net.Listener, log *slog.Logger) net.Listener {
	if cfg.Host != config.LoopbackHost {
		return nil
	}
	tcp, ok := primary.Addr().(*net.TCPAddr)
	if !ok {
		return nil
	}
	ln, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(tcp.Port)))
	if err != nil {
		log.Info("IPv6 loopback listener unavailable; serving 127.0.0.1 only", "port", tcp.Port, "err", err)
		return nil
	}
	return ln
}

func (s *Server) Addr() net.Addr { return s.listen.Addr() }

func (s *Server) IPv6LoopbackAddr() net.Addr {
	if s.listen6 == nil {
		return nil
	}
	return s.listen6.Addr()
}

func (s *Server) Handler() http.Handler { return s.http.Handler }

func (s *Server) Run(ctx context.Context) error {
	return s.run(ctx, nil)
}

func (s *Server) RunWithReady(ctx context.Context, onReady func()) error {
	return s.run(ctx, onReady)
}

func (s *Server) run(ctx context.Context, onReady func()) error {
	info := runfile.Info{
		PID:                   os.Getpid(),
		Port:                  s.boundPort(),
		StartedAt:             time.Now().UTC(),
		Owner:                 os.Getenv("AO_OWNER"),
		AppRunID:              s.cfg.AppRunID,
		BrowserRuntimeAddress: os.Getenv("AO_BROWSER_RUNTIME_ADDRESS"),
		BrowserStreamAddress:  os.Getenv("AO_BROWSER_STREAM_ADDRESS"),
	}
	if err := runfile.Write(s.cfg.RunFilePath, info); err != nil {
		_ = s.listen.Close()
		if s.listen6 != nil {
			_ = s.listen6.Close()
		}
		return fmt.Errorf("write run-file: %w", err)
	}
	defer func() {
		if err := runfile.RemoveIfOwned(s.cfg.RunFilePath, info.PID); err != nil {
			s.log.Warn("failed to remove run-file", "path", s.cfg.RunFilePath, "err", err)
		}
	}()

	serveErr := make(chan error, 1)
	go func() {
		s.log.Info("daemon listening", "addr", s.Addr().String(), "pid", info.PID)
		if err := s.http.Serve(s.listen); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()
	if s.listen6 != nil {
		go func() {
			if err := s.http.Serve(s.listen6); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.log.Warn("IPv6 loopback listener stopped", "addr", s.listen6.Addr().String(), "err", err)
			}
		}()
	}
	if onReady != nil {
		onReady()
	}

	select {
	case err := <-serveErr:
		return err
	case <-s.shutdownRequested:
		s.log.Info("shutdown requested over HTTP", "timeout", s.cfg.ShutdownTimeout)
	case <-ctx.Done():
		s.requestShutdown()
		s.log.Info("shutdown signal received, draining connections", "timeout", s.cfg.ShutdownTimeout)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	if err := s.http.Shutdown(shutdownCtx); err != nil {
		s.log.Warn("graceful shutdown timed out, forcing close", "err", err)
		_ = s.http.Close()
		return fmt.Errorf("graceful shutdown exceeded %s: %w", s.cfg.ShutdownTimeout, err)
	}

	s.log.Info("daemon stopped cleanly")
	return <-serveErr
}

func (s *Server) boundPort() int {
	if tcp, ok := s.listen.Addr().(*net.TCPAddr); ok {
		return tcp.Port
	}
	return s.cfg.Port
}

func (s *Server) requestShutdown() {
	s.shutdownOnce.Do(func() {
		close(s.shutdownRequested)
	})
}

func (s *Server) RequestShutdown() { s.requestShutdown() }

func (s *Server) RequestShutdownAfterDesktopExit() {
	s.shutdownOnce.Do(func() {
		if os.Getenv("AO_OWNER") == "app" && s.cfg.AppRunID != "" {
			s.stopAgents.Store(true)
		}
		close(s.shutdownRequested)
	})
}

func (s *Server) requestAgentShutdown(appRunID string) bool {
	if appRunID == "" || appRunID != s.cfg.AppRunID || os.Getenv("AO_OWNER") != "app" {
		return false
	}
	s.stopAgents.Store(true)
	return true
}

func (s *Server) StopAgentsRequested() bool { return s.stopAgents.Load() }
