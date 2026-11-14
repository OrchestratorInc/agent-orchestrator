//go:build !windows

package ptyexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func Spawn(ctx context.Context, argv, env []string, rows, cols uint16) (ports.Stream, error) {
	if len(argv) == 0 {
		return nil, errors.New("ptyexec: empty attach command")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:forbidigo // interactive PTY child requires direct exec.Cmd ownership
	if env != nil {
		cmd.Env = env
	}
	var f *os.File
	var err error
	if rows > 0 && cols > 0 {
		f, err = pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	} else {
		f, err = pty.Start(cmd)
	}
	if err != nil {
		return nil, err
	}
	proc := &creackPTY{f: f, cmd: cmd}
	go func() {
		<-ctx.Done()
		_ = proc.Close()
	}()
	return proc, nil
}

type creackPTY struct {
	f         *os.File
	cmd       *exec.Cmd
	closeOnce sync.Once
	closeErr  error
}

func (p *creackPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *creackPTY) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *creackPTY) Resize(rows, cols uint16) error {
	err := pty.Setsize(p.f, &pty.Winsize{Rows: rows, Cols: cols})
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(syscall.SIGWINCH)
	}
	return err
}

const detachGrace = 250 * time.Millisecond

func (p *creackPTY) Close() error {
	p.closeOnce.Do(func() {
		done := make(chan struct{})
		go func() {
			_ = p.cmd.Wait()
			close(done)
		}()
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-done:
		case <-time.After(detachGrace):
			if p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
			}
			waitForExit(done, killGrace)
		}
		p.closeErr = p.f.Close()
	})
	return p.closeErr
}
