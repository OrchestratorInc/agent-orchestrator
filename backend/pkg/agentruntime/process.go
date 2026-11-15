package agentruntime

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	defaultShutdownGrace = 5 * time.Second
	killWaitGrace        = 2 * time.Second
)

type ProcessConfig struct {
	Argv          []string
	Dir           string
	Env           []string
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
	ShutdownGrace time.Duration
}

type ProcessResult struct {
	ExitCode int
	Err      error
}

type Process struct {
	cmd           *exec.Cmd
	done          chan struct{}
	shutdownGrace time.Duration
	mu            sync.RWMutex
	result        ProcessResult
}

func StartProcess(ctx context.Context, cfg ProcessConfig) (*Process, error) {
	if len(cfg.Argv) == 0 {
		return nil, errors.New("agentruntime: empty process argv")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cmd := aoprocess.Command(cfg.Argv[0], cfg.Argv[1:]...)
	cmd.Dir = cfg.Dir
	cmd.Env = cfg.Env
	cmd.Stdin = cfg.Stdin
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	grace := cfg.ShutdownGrace
	if grace <= 0 {
		grace = defaultShutdownGrace
	}
	process := &Process{cmd: cmd, done: make(chan struct{}), shutdownGrace: grace}
	shutdownContext := context.WithoutCancel(ctx)
	go process.reap()
	go func() {
		select {
		case <-ctx.Done():
			_ = process.Stop(shutdownContext)
		case <-process.done:
		}
	}()
	return process, nil
}

func (p *Process) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *Process) Wait(ctx context.Context) (ProcessResult, error) {
	if p == nil {
		return ProcessResult{}, errors.New("agentruntime: nil process")
	}
	select {
	case <-p.done:
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.result, nil
	case <-ctx.Done():
		return ProcessResult{}, ctx.Err()
	}
}

func (p *Process) Signal(signal os.Signal) error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return errors.New("agentruntime: process is not running")
	}
	select {
	case <-p.done:
		return os.ErrProcessDone
	default:
		return signalProcessGroup(p.cmd.Process, signal)
	}
}

func (p *Process) Stop(ctx context.Context) error {
	if p == nil {
		return errors.New("agentruntime: nil process")
	}
	select {
	case <-p.done:
		return nil
	default:
	}

	_ = p.Signal(terminationSignal())
	timer := time.NewTimer(p.shutdownGrace)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
	case <-timer.C:
	}

	if err := killProcessGroup(p.cmd.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-p.done:
		return nil
	case <-time.After(killWaitGrace):
		return errors.New("agentruntime: process did not exit after kill")
	}
}

func (p *Process) reap() {
	err := p.cmd.Wait()
	result := ProcessResult{ExitCode: -1, Err: err}
	if p.cmd.ProcessState != nil {
		result.ExitCode = p.cmd.ProcessState.ExitCode()
	}
	p.mu.Lock()
	p.result = result
	p.mu.Unlock()
	close(p.done)
}
