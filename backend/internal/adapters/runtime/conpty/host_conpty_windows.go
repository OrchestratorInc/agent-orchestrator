//go:build windows

package conpty

import (
	"errors"
	"fmt"
	"os"
	"sync"

	gopty "github.com/aymanbagabas/go-pty"
	"golang.org/x/sys/windows"
)

type conptyConn struct {
	pty gopty.ConPty
	cmd *gopty.Cmd

	doneOnce      sync.Once
	doneC         chan struct{}
	consoleMu     sync.RWMutex
	consoleClosed bool
	disposeOnce   sync.Once
	disposeErr    error
	exitCode      int
	exited        bool
	exitMu        sync.Mutex
}

func newConPTY(cwd, shellCmd string, shellArgs []string, cols, rows uint16) (ptyConn, error) {
	p, err := gopty.New()
	if err != nil {
		return nil, fmt.Errorf("conpty: create pty: %w", err)
	}
	cp, ok := p.(gopty.ConPty)
	if !ok {
		_ = p.Close()
		return nil, fmt.Errorf("conpty: expected ConPty on windows, got %T", p)
	}

	if err := cp.Resize(int(cols), int(rows)); err != nil {
		_ = cp.Close()
		return nil, fmt.Errorf("conpty: initial resize: %w", err)
	}

	cmd := cp.Command(shellCmd, shellArgs...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		_ = cp.Close()
		return nil, fmt.Errorf("conpty: start command: %w", err)
	}

	c := &conptyConn{
		pty:   cp,
		cmd:   cmd,
		doneC: make(chan struct{}),
	}

	go c.wait()
	return c, nil
}

func (c *conptyConn) wait() {
	_ = c.cmd.Wait()
	code := 0
	if c.cmd.ProcessState != nil {
		code = c.cmd.ProcessState.ExitCode()
	}
	c.exitMu.Lock()
	c.exitCode = code
	c.exited = true
	c.exitMu.Unlock()
	c.doneOnce.Do(func() { close(c.doneC) })

	c.closeConsole()
}

func (c *conptyConn) Read(b []byte) (int, error)  { return c.pty.Read(b) }
func (c *conptyConn) Write(b []byte) (int, error) { return c.pty.Write(b) }
func (c *conptyConn) Close() error {
	c.closeConsole()
	c.disposeOnce.Do(func() {
		c.disposeErr = errors.Join(
			c.pty.InputPipe().Close(),
			c.pty.OutputPipe().Close(),
		)
	})
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	return c.disposeErr
}

func (c *conptyConn) closeConsole() {
	c.consoleMu.Lock()
	defer c.consoleMu.Unlock()
	if c.consoleClosed {
		return
	}
	windows.ClosePseudoConsole(windows.Handle(c.pty.Fd()))
	c.consoleClosed = true
}
func (c *conptyConn) Resize(cols, rows int) error {
	c.consoleMu.RLock()
	defer c.consoleMu.RUnlock()
	if c.consoleClosed {
		return fmt.Errorf("conpty: resize: %w", os.ErrClosed)
	}
	return c.pty.Resize(cols, rows)
}
func (c *conptyConn) Done() <-chan struct{} { return c.doneC }
func (c *conptyConn) PID() int {
	if c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}
func (c *conptyConn) ExitCode() (int, bool) {
	c.exitMu.Lock()
	defer c.exitMu.Unlock()
	return c.exitCode, c.exited
}
