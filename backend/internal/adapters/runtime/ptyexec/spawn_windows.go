//go:build windows

package ptyexec

import (
	"context"
	"errors"
	"sync"
	"time"

	winpty "github.com/aymanbagabas/go-pty"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const detachGrace = 250 * time.Millisecond

func Spawn(ctx context.Context, argv, env []string, rows, cols uint16) (ports.Stream, error) {
	if len(argv) == 0 {
		return nil, errors.New("ptyexec: empty attach command")
	}
	pty, err := winpty.New()
	if err != nil {
		return nil, err
	}
	if rows > 0 && cols > 0 {
		if err := pty.Resize(int(cols), int(rows)); err != nil {
			_ = pty.Close()
			return nil, err
		}
	}
	cmd := pty.CommandContext(ctx, argv[0], argv[1:]...)
	if env != nil {
		cmd.Env = env
	}
	if err := cmd.Start(); err != nil {
		_ = pty.Close()
		return nil, err
	}

	p := &conPTYProcess{pty: pty, cmd: cmd, waitDone: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.waitDone)
	}()
	return p, nil
}

type conPTYProcess struct {
	pty       winpty.Pty
	cmd       *winpty.Cmd
	waitDone  chan struct{}
	closeOnce sync.Once
}

func (p *conPTYProcess) Read(b []byte) (int, error)  { return p.pty.Read(b) }
func (p *conPTYProcess) Write(b []byte) (int, error) { return p.pty.Write(b) }

func (p *conPTYProcess) Resize(rows, cols uint16) error {
	if rows == 0 || cols == 0 {
		return nil
	}
	return p.pty.Resize(int(cols), int(rows))
}

func (p *conPTYProcess) Close() error {
	p.closeOnce.Do(func() {
		_ = p.pty.Close()
		select {
		case <-p.waitDone:
		case <-time.After(detachGrace):
			if p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
			}
			waitForExit(p.waitDone, killGrace)
		}
	})
	return nil
}
