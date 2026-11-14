//go:build darwin

package conpty

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type darwinPTYConn struct {
	pty *os.File
	cmd *exec.Cmd

	closeOnce sync.Once
	doneC     chan struct{}
	exitMu    sync.Mutex
	exitCode  int
	exited    bool
}

const darwinPTYCloseGrace = 500 * time.Millisecond

func newConPTY(cwd, shellCmd string, shellArgs []string, cols, rows uint16) (ptyConn, error) {
	//nolint:forbidigo // Interactive ConPTY children require direct process control.
	cmd := exec.Command(shellCmd, shellArgs...) // #nosec G702 -- intentional direct argv execution
	cmd.Dir = cwd
	cmd.Env = os.Environ()

	f, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: cols,
		Rows: rows,
	})
	if err != nil {
		return nil, fmt.Errorf("darwin pty: start command: %w", err)
	}

	c := &darwinPTYConn{pty: f, cmd: cmd, doneC: make(chan struct{})}
	go c.wait()
	return c, nil
}

func (c *darwinPTYConn) wait() {
	err := c.cmd.Wait()
	code := 0
	if c.cmd.ProcessState != nil {
		code = c.cmd.ProcessState.ExitCode()
	} else if err != nil {
		code = -1
	}
	c.exitMu.Lock()
	c.exitCode = code
	c.exited = true
	c.exitMu.Unlock()
	close(c.doneC)
}

func (c *darwinPTYConn) Read(b []byte) (int, error)  { return c.pty.Read(b) }
func (c *darwinPTYConn) Write(b []byte) (int, error) { return c.pty.Write(b) }

func (c *darwinPTYConn) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 || cols > math.MaxUint16 || rows > math.MaxUint16 {
		return fmt.Errorf("darwin pty: invalid size %dx%d", cols, rows)
	}
	return pty.Setsize(c.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (c *darwinPTYConn) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		if c.cmd.Process != nil {
			select {
			case <-c.doneC:
			default:
				pgid := c.cmd.Process.Pid
				_ = syscall.Kill(-pgid, syscall.SIGHUP)
				_ = syscall.Kill(-pgid, syscall.SIGTERM)
				if !waitForDarwinProcessGroupExit(pgid, darwinPTYCloseGrace) {
					_ = syscall.Kill(-pgid, syscall.SIGKILL)
					select {
					case <-c.doneC:
					case <-time.After(darwinPTYCloseGrace):
					}
				}
			}
		}
		closeErr = c.pty.Close()
	})
	return closeErr
}

func waitForDarwinProcessGroupExit(pgid int, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !darwinProcessGroupAlive(pgid) {
			return true
		}
		select {
		case <-deadline.C:
			return !darwinProcessGroupAlive(pgid)
		case <-ticker.C:
		}
	}
}

func darwinProcessGroupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (c *darwinPTYConn) Done() <-chan struct{} { return c.doneC }

func (c *darwinPTYConn) PID() int {
	if c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

func (c *darwinPTYConn) ExitCode() (int, bool) {
	c.exitMu.Lock()
	defer c.exitMu.Unlock()
	return c.exitCode, c.exited
}
