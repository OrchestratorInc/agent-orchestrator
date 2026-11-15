package terminal

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Source interface {
	ports.Attacher
	IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
}

const (
	defaultMaxReattach       = 5
	defaultReattachResetTime = 5 * time.Second
)

type attachment struct {
	id            string
	handle        ports.RuntimeHandle
	src           Source
	log           *slog.Logger
	onOpen        func()
	onData        func(data []byte)
	onExit        func()
	lockInput     func() (unlock func())
	onInputReplay func([]byte)

	maxReattach int
	resetGrace  time.Duration

	mu           sync.Mutex
	pty          ports.Stream
	cancel       context.CancelFunc
	rows         uint16
	cols         uint16
	closed       bool
	exited       bool
	opened       bool
	inputReady   bool
	pendingInput []pendingInput

	inputForgotten bool
}

type pendingInput struct {
	data    []byte
	release func()
}

func newAttachment(id string, handle ports.RuntimeHandle, src Source, onOpen func(), onData func([]byte), onExit func(), log *slog.Logger) *attachment {
	if log == nil {
		log = slog.Default()
	}
	if onData == nil {
		onData = func([]byte) {}
	}
	return &attachment{
		id:          id,
		handle:      handle,
		src:         src,
		log:         log,
		onOpen:      onOpen,
		onData:      onData,
		onExit:      onExit,
		maxReattach: defaultMaxReattach,
		resetGrace:  defaultReattachResetTime,
	}
}

func (a *attachment) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	if !a.setRunCancel(cancel) {
		cancel()
		return
	}
	defer a.clearRunCancel(cancel)
	defer a.releasePendingInput()

	failures := 0
	for {
		if a.shouldStop(ctx) {
			return
		}
		if a.isInputForgotten() {
			a.markExited()
			return
		}

		alive, err := a.src.IsAlive(ctx, a.handle)
		if a.shouldStop(ctx) {
			return
		}
		if err != nil {
			failures++
			if failures > a.maxReattach {
				a.fail("liveness probe: " + err.Error())
				return
			}
			if !a.backoff(ctx, failures) {
				return
			}
			continue
		}
		if !alive {
			a.markExited()
			return
		}

		rows, cols := a.size()
		if a.shouldStop(ctx) {
			return
		}
		p, err := a.src.Attach(ctx, a.handle, rows, cols)
		if a.shouldStop(ctx) {
			if p != nil {
				_ = p.Close()
			}
			return
		}
		if err != nil {
			failures++
			if failures > a.maxReattach {
				a.fail("attach: " + err.Error())
				return
			}
			if !a.backoff(ctx, failures) {
				return
			}
			continue
		}

		if !a.setPTY(p) {
			_ = p.Close()
			return
		}
		start := time.Now()
		readErr := a.copyOut(p)
		a.clearPTY(p)
		_ = p.Close()
		if a.shouldStop(ctx) {
			return
		}
		if errors.Is(readErr, ports.ErrRuntimeProcessExited) {
			a.markExited()
			return
		}

		if time.Since(start) >= a.resetGrace {
			failures = 0
		}
		failures++

		if failures > a.maxReattach {
			a.markExited()
			return
		}
		if !a.backoff(ctx, failures) {
			return
		}
		a.log.Debug("terminal re-attaching", "id", a.id, "failures", failures)
	}
}

func (a *attachment) copyOut(p ports.Stream) error {
	buf := make([]byte, 32*1024)
	for {
		n, err := p.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			a.onData(chunk)
		}
		if err != nil {
			return err
		}
	}
}

func (a *attachment) backoff(ctx context.Context, failures int) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(reattachBackoff(failures)):
		return true
	}
}

func reattachBackoff(failures int) time.Duration {
	d := time.Duration(failures) * 200 * time.Millisecond
	if d > time.Second {
		d = time.Second
	}
	return d
}

var errInputRefused = errors.New("terminal: attachment closed")

func (a *attachment) writeLeased(p []byte, release func()) error {
	if len(p) == 0 {
		releaseInput(release)
		return nil
	}
	chunk := append([]byte(nil), p...)

	a.mu.Lock()
	if a.closed || a.exited || a.inputForgotten {
		a.mu.Unlock()
		releaseInput(release)
		return errInputRefused
	}
	pty := a.pty
	if pty == nil || !a.inputReady {
		a.pendingInput = append(a.pendingInput, pendingInput{data: chunk, release: release})
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	_, err := pty.Write(chunk)
	releaseInput(release)
	return err
}

func (a *attachment) resize(rows, cols uint16) error {
	a.mu.Lock()
	a.rows, a.cols = rows, cols
	pty := a.pty
	a.mu.Unlock()
	if pty == nil {
		return nil
	}
	return pty.Resize(rows, cols)
}

func (a *attachment) size() (rows, cols uint16) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rows, a.cols
}

func (a *attachment) setPTY(p ports.Stream) bool {
	a.mu.Lock()
	if a.closed || a.exited {
		a.mu.Unlock()
		return false
	}
	a.pty = p
	a.inputReady = false
	rows, cols := a.rows, a.cols
	shouldOpen := !a.opened
	if shouldOpen {
		a.opened = true
	}
	onOpen := a.onOpen
	a.mu.Unlock()
	if rows > 0 && cols > 0 {
		_ = p.Resize(rows, cols)
	}
	if shouldOpen && onOpen != nil {
		onOpen()
	}

	for {
		a.mu.Lock()
		if len(a.pendingInput) == 0 {
			a.inputReady = true
			a.mu.Unlock()
			return true
		}
		a.mu.Unlock()
		if err := a.flushPendingInput(p); err != nil {
			a.fail("flush pending input: " + err.Error())
			return false
		}
	}
}

func (a *attachment) flushPendingInput(p ports.Stream) error {
	if a.lockInput != nil {
		defer a.lockInput()()
	}
	a.mu.Lock()
	pending := a.pendingInput
	a.pendingInput = nil
	a.mu.Unlock()
	for i, input := range pending {
		n, err := p.Write(input.data)
		if n > 0 && a.onInputReplay != nil {
			a.onInputReplay(input.data)
		}
		releaseInput(input.release)
		if err != nil {
			releasePendingInput(pending[i+1:])
			return err
		}
	}
	return nil
}

func (a *attachment) clearPTY(p ports.Stream) {
	a.mu.Lock()
	if a.pty == p {
		a.pty = nil
		a.inputReady = false
	}
	a.mu.Unlock()
}

func (a *attachment) close() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	pty := a.pty
	a.pty = nil
	a.inputReady = false
	pending := a.pendingInput
	a.pendingInput = nil
	cancel := a.cancel
	a.mu.Unlock()
	releasePendingInput(pending)
	if pty != nil {
		_ = pty.Close()
	}
	if cancel != nil {
		cancel()
	}
}

func (a *attachment) setRunCancel(cancel context.CancelFunc) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return false
	}
	a.cancel = cancel
	return true
}

func (a *attachment) clearRunCancel(cancel context.CancelFunc) {
	a.mu.Lock()
	a.cancel = nil
	a.mu.Unlock()
	cancel()
}

func (a *attachment) isClosed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

func (a *attachment) forgetInput() {
	a.mu.Lock()
	a.inputForgotten = true
	pending := a.pendingInput
	a.pendingInput = nil
	a.mu.Unlock()
	releasePendingInput(pending)
}

func (a *attachment) isInputForgotten() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inputForgotten
}

func (a *attachment) shouldStop(ctx context.Context) bool {
	return ctx.Err() != nil || a.isClosed()
}

func (a *attachment) isExited() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.exited
}

func (a *attachment) markExited() {
	a.mu.Lock()
	if a.exited {
		a.mu.Unlock()
		return
	}
	a.exited = true
	pending := a.pendingInput
	a.pendingInput = nil
	a.mu.Unlock()
	releasePendingInput(pending)
	if a.onExit != nil {
		a.onExit()
	}
}

func (a *attachment) fail(reason string) {
	a.log.Warn("terminal attachment failed", "id", a.id, "reason", reason)
	a.markExited()
}

func (a *attachment) releasePendingInput() {
	a.mu.Lock()
	pending := a.pendingInput
	a.pendingInput = nil
	a.mu.Unlock()
	releasePendingInput(pending)
}

func releasePendingInput(pending []pendingInput) {
	for _, input := range pending {
		releaseInput(input.release)
	}
}

func releaseInput(release func()) {
	if release != nil {
		release()
	}
}
