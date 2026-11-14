package panes

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"sync"
	"time"
)

type Command struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionId"`
	Action    string          `json:"action"`
	Args      json.RawMessage `json:"args,omitempty"`
}

type Result struct {
	ID      string          `json:"id"`
	OK      bool            `json:"ok"`
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
	Layout  json.RawMessage `json:"layout,omitempty"`
}

const CodeNotShown = "SESSION_NOT_ON_SCREEN"

var (
	ErrNoWindow = errors.New("no AO desktop window is connected")
	ErrTimeout  = errors.New("the AO desktop window did not answer in time")
)

const DefaultTimeout = 8 * time.Second

const MaxLayoutBytes = 256 << 10

type window struct {
	send func(Command)
}

type pending struct {
	waiting map[*window]struct{}
	results []Result
	done    chan Result
	closed  bool
}

type report struct {
	owner  *window
	layout json.RawMessage
}

type Broker struct {
	mu      sync.Mutex
	windows map[*window]struct{}
	layouts map[string][]report
	pending map[string]*pending
	seq     uint64
	timeout time.Duration
}

func NewBroker() *Broker {
	return &Broker{
		windows: map[*window]struct{}{},
		layouts: map[string][]report{},
		pending: map[string]*pending{},
		timeout: DefaultTimeout,
	}
}

type Window struct {
	b *Broker
	w *window
}

func (b *Broker) Connect(send func(Command)) *Window {
	w := &window{send: send}
	b.mu.Lock()
	b.windows[w] = struct{}{}
	b.mu.Unlock()
	return &Window{b: b, w: w}
}

func (h *Window) Report(sessionID string, layout json.RawMessage) {
	if sessionID == "" || len(layout) > MaxLayoutBytes {
		return
	}
	h.b.mu.Lock()
	defer h.b.mu.Unlock()
	if _, ok := h.b.windows[h.w]; !ok {
		return
	}
	reports := slices.DeleteFunc(h.b.layouts[sessionID], func(r report) bool { return r.owner == h.w })
	if len(layout) != 0 && string(layout) != "null" {
		reports = append(reports, report{owner: h.w, layout: append(json.RawMessage(nil), layout...)})
	}
	if len(reports) == 0 {
		delete(h.b.layouts, sessionID)
		return
	}
	h.b.layouts[sessionID] = reports
}

func (h *Window) Resolve(result Result) {
	h.b.mu.Lock()
	defer h.b.mu.Unlock()
	h.b.answerLocked(h.w, result)
}

func (h *Window) Close() {
	h.b.mu.Lock()
	defer h.b.mu.Unlock()
	if _, ok := h.b.windows[h.w]; !ok {
		return
	}
	delete(h.b.windows, h.w)
	for sessionID, reports := range h.b.layouts {
		reports = slices.DeleteFunc(reports, func(r report) bool { return r.owner == h.w })
		if len(reports) == 0 {
			delete(h.b.layouts, sessionID)
		} else {
			h.b.layouts[sessionID] = reports
		}
	}
	for id := range h.b.pending {
		h.b.answerLocked(h.w, Result{ID: id, Code: CodeNotShown, Message: "The AO window closed."})
	}
}

func (b *Broker) Layout(sessionID string) (json.RawMessage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	reports := b.layouts[sessionID]
	if len(reports) == 0 {
		return nil, false
	}
	return append(json.RawMessage(nil), reports[len(reports)-1].layout...), true
}

func (b *Broker) Connected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.windows) > 0
}

func (b *Broker) Execute(ctx context.Context, sessionID, action string, args json.RawMessage) (Result, error) {
	b.mu.Lock()
	if len(b.windows) == 0 {
		b.mu.Unlock()
		return Result{}, ErrNoWindow
	}
	b.seq++
	id := "pane-" + strconv.FormatUint(b.seq, 10)
	var targets []*window
	if reports := b.layouts[sessionID]; len(reports) > 0 {
		targets = []*window{reports[len(reports)-1].owner}
	} else {
		targets = make([]*window, 0, len(b.windows))
		for w := range b.windows {
			targets = append(targets, w)
		}
	}
	p := &pending{waiting: make(map[*window]struct{}, len(targets)), done: make(chan Result, 1)}
	for _, w := range targets {
		p.waiting[w] = struct{}{}
	}
	b.pending[id] = p
	timeout := b.timeout
	b.mu.Unlock()

	cmd := Command{ID: id, SessionID: sessionID, Action: action, Args: args}
	for _, w := range targets {
		w.send(cmd)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	defer func() {
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
	}()
	select {
	case result := <-p.done:
		return result, nil
	case <-timer.C:
		return Result{}, ErrTimeout
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

func (b *Broker) answerLocked(w *window, result Result) {
	p, ok := b.pending[result.ID]
	if !ok || p.closed {
		return
	}
	if _, waiting := p.waiting[w]; !waiting {
		return
	}
	delete(p.waiting, w)
	if result.OK {
		p.closed = true
		p.done <- result
		return
	}
	p.results = append(p.results, result)
	if len(p.waiting) > 0 {
		return
	}
	p.closed = true
	p.done <- mostSpecific(p.results)
}

func mostSpecific(results []Result) Result {
	for _, result := range results {
		if result.Code != CodeNotShown {
			return result
		}
	}
	return results[0]
}
