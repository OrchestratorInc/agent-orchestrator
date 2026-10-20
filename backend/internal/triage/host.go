package triage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"time"
)

const (
	maxCommandBytes = 1 << 20
	maxReplyBytes   = 8 << 10
)

type host struct {
	ctx       context.Context
	cancel    context.CancelFunc
	deps      Dependencies
	store     *runStore
	out       io.Writer
	outMu     sync.Mutex
	outErr    error
	mu        sync.Mutex
	active    *investigation
	handoffMu sync.Mutex
	work      sync.WaitGroup
}

// Serve runs the private newline-JSON support protocol. It owns only the
// conversations it starts and never discovers or starts the AO daemon.
func Serve(ctx context.Context, in io.Reader, out io.Writer, opts Options) error {
	store, err := openRunStore(opts.DataDir)
	if err != nil {
		return err
	}
	// One helper owns all private reporting writes and native provider cleanup.
	release, err := claim(filepath.Join(store.root, "active.lock"))
	if err != nil {
		return err
	}
	defer release()
	if err := store.recoverInterrupted(ctx, opts.Deps.RecoverConversation); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	h := &host{ctx: ctx, cancel: cancel, deps: opts.Deps, store: store, out: out}
	if h.deps.Now == nil {
		h.deps.Now = time.Now
	}
	defer cancel()
	defer h.shutdown()
	if closer, ok := in.(io.Closer); ok {
		defer func() { _ = closer.Close() }()
	}
	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		defer close(lines)
		defer close(readErr)
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 64<<10), maxCommandBytes+1)
		for scanner.Scan() {
			select {
			case lines <- append([]byte(nil), scanner.Bytes()...):
			case <-ctx.Done():
				return
			}
		}
		readErr <- scanner.Err()
	}()
	for {
		select {
		case <-ctx.Done():
			return h.outputError()
		case line, ok := <-lines:
			if !ok {
				if err := <-readErr; err != nil {
					h.emit(Response{Type: "error", Error: "triage command input failed or exceeded 1 MiB"})
					return err
				}
				return h.outputError()
			}
			var cmd Command
			err := decodeJSON(line, &cmd, true)
			if err != nil || cmd.Type == "" || len(line) > maxCommandBytes {
				h.emit(Response{ID: cmd.ID, Type: "error", Error: "invalid triage command"})
				continue
			}
			if h.command(cmd) {
				return h.outputError()
			}
		}
	}
}

func (h *host) outputError() error {
	h.outMu.Lock()
	defer h.outMu.Unlock()
	return h.outErr
}

func (h *host) emit(response Response) {
	response.Version = ProtocolVersion
	// Frozen attachments are desktop-main input, not renderer run state.
	if response.Run != nil {
		run := *response.Run
		run.Launch = nil
		response.Run = &run
	}
	for n := range response.Runs {
		response.Runs[n].Launch = nil
	}
	raw, err := json.Marshal(response)
	h.outMu.Lock()
	if h.outErr == nil {
		if err == nil {
			_, err = io.Copy(h.out, bytes.NewReader(append(raw, '\n')))
		}
		h.outErr = err
	}
	failed := h.outErr != nil
	h.outMu.Unlock()
	if failed {
		h.cancel()
	}
}

func (h *host) fail(id string, err error) {
	h.emit(Response{ID: id, Type: "error", Error: Redact(err.Error())})
}

func (h *host) catalog() ([]Choice, error) {
	if h.deps.Catalog == nil {
		return []Choice{}, nil
	}
	return h.deps.Catalog(h.ctx)
}

func (h *host) command(cmd Command) bool {
	if cmd.Type == "quit" {
		h.emit(Response{ID: cmd.ID, Type: "quit"})
		return true
	}
	if cmd.Type == "handoff" {
		h.work.Add(1)
		go func() {
			defer h.work.Done()
			run, launch, err := h.sessionHandoff(cmd.Start)
			if err != nil {
				h.fail(cmd.ID, err)
			} else {
				h.emit(Response{ID: cmd.ID, Type: "session-launch", Run: &run, Launch: &launch})
			}
		}()
		return false
	}
	if cmd.Type == "reply" || cmd.Type == "terminal-input" || cmd.Type == "terminal-resize" {
		i := h.current(cmd.RunID)
		if i == nil {
			h.fail(cmd.ID, errors.New("triage run is not active in this host"))
			return false
		}
		select {
		case i.replies <- cmd:
		default:
			h.fail(cmd.ID, errors.New("an agent action is already being sent"))
		}
		return false
	}
	response, err := h.execute(cmd)
	if err != nil {
		h.fail(cmd.ID, err)
	} else {
		response.ID = cmd.ID
		h.emit(response)
	}
	return false
}

func (h *host) execute(cmd Command) (response Response, err error) {
	response.Type = "run"
	switch cmd.Type {
	case "catalog", "configure":
		if cmd.Type == "configure" {
			if h.deps.Configure == nil {
				return response, errors.New("triage configuration is unavailable")
			}
			if err := h.deps.Configure(h.ctx, cmd.Choices); err != nil {
				return response, err
			}
		}
		response.Type = "catalog"
		response.Choices, err = h.catalog()
	case "list":
		response.Type = "runs"
		response.Runs, err = h.store.list()
	case "get":
		h.mu.Lock()
		i := h.active
		h.mu.Unlock()
		var run Run
		if i != nil && i.id == cmd.RunID {
			run = i.snapshot()
		}
		if run.ID == "" || run.Execution == "session" {
			run, err = h.store.get(cmd.RunID)
		}
		response.Run = &run
	case "prepare", "start":
		var run Run
		if cmd.Type == "prepare" {
			run, err = h.prepare(cmd.Origin)
		} else {
			run, err = h.start(cmd.Start)
		}
		response.Run = &run
	case "session-create-intent", "session-linked":
		if !h.handoffMu.TryLock() {
			return response, errors.New("ordinary session handoff is still being prepared")
		}
		defer h.handoffMu.Unlock()
		var run Run
		run, err = h.store.sessionCheckpoint(cmd.RunID, cmd.SessionID, cmd.Type == "session-create-intent", h.deps.Now)
		response.Run = &run
	case "cancel":
		i := h.current(cmd.RunID)
		if i == nil {
			return response, errors.New("triage run is not active in this host")
		}
		i.stop()
		run := i.snapshot()
		response.Run = &run
	default:
		err = errors.New("unknown triage command")
	}
	return response, err
}

func (h *host) current(id string) *investigation {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active == nil || id != "" && h.active.id != id {
		return nil
	}
	select {
	case <-h.active.done:
		return nil
	default:
		return h.active
	}
}

func (h *host) selection(selection Selection) (Selection, error) {
	choices, err := h.catalog()
	if err != nil {
		return Selection{}, err
	}
	return configuredSelection(selection, choices)
}

func (h *host) newInvestigation(run Run) *investigation {
	ctx, cancel := context.WithCancel(h.ctx)
	i := &investigation{host: h, id: run.ID, ctx: ctx, cancel: cancel, run: run, replies: make(chan Command, 8), activation: make(chan struct{}, 1), done: make(chan struct{})}
	h.mu.Lock()
	h.active = i
	h.mu.Unlock()
	return i
}

func (h *host) shutdown() {
	h.cancel()
	if i := h.current(""); i != nil {
		i.stop()
		<-i.done
	}
	h.work.Wait()
}
