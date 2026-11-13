// Package panes relays split-pane requests between agents and the desktop
// window. The window owns the layout: it reports each session's tabs and
// splits as it changes, and applies the requests an agent makes (split a tab
// beside another, take one out, ungroup). The daemon only keeps the latest
// report per session and routes each request to the connected windows,
// waiting briefly for the window's answer so the agent learns whether the
// split happened. Nothing here is persisted.
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

// Command is one request sent to the windows.
type Command struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionId"`
	Action    string          `json:"action"`
	Args      json.RawMessage `json:"args,omitempty"`
}

// Result is a window's answer to a Command.
type Result struct {
	ID      string          `json:"id"`
	OK      bool            `json:"ok"`
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
	Layout  json.RawMessage `json:"layout,omitempty"`
}

// CodeNotShown is a window's answer when the session is not on its screen.
// Several windows may be open; one that is not showing the session says so,
// and only when none shows it does the agent hear about it.
const CodeNotShown = "SESSION_NOT_ON_SCREEN"

var (
	// ErrNoWindow means no desktop window is connected to the daemon.
	ErrNoWindow = errors.New("no AO desktop window is connected")
	// ErrTimeout means no window answered in time.
	ErrTimeout = errors.New("the AO desktop window did not answer in time")
)

// DefaultTimeout bounds how long Execute waits for the windows.
const DefaultTimeout = 8 * time.Second

// MaxLayoutBytes caps one reported layout. A real layout is a few kilobytes;
// anything larger is not one, and a window must not be able to make the
// daemon hold an unbounded blob per session.
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

// Broker routes pane requests to desktop windows. The zero value is not
// usable; use NewBroker.
type Broker struct {
	mu      sync.Mutex
	windows map[*window]struct{}
	// layouts keeps each session's reports per window, the most recent last, so
	// when the window that reported last goes away another window that still
	// shows the session is the one asked and heard.
	layouts map[string][]report
	pending map[string]*pending
	seq     uint64
	timeout time.Duration
}

// NewBroker returns an empty broker.
func NewBroker() *Broker {
	return &Broker{
		windows: map[*window]struct{}{},
		layouts: map[string][]report{},
		pending: map[string]*pending{},
		timeout: DefaultTimeout,
	}
}

// Window is one connected desktop window's handle on the broker.
type Window struct {
	b *Broker
	w *window
}

// Connect registers a window. send delivers a command to it and must not
// block. Close the returned Window when the connection ends.
func (b *Broker) Connect(send func(Command)) *Window {
	w := &window{send: send}
	b.mu.Lock()
	b.windows[w] = struct{}{}
	b.mu.Unlock()
	return &Window{b: b, w: w}
}

// Report records the layout the window shows for a session; a null or empty
// layout means the window no longer shows it.
func (h *Window) Report(sessionID string, layout json.RawMessage) {
	if sessionID == "" || len(layout) > MaxLayoutBytes {
		return
	}
	h.b.mu.Lock()
	defer h.b.mu.Unlock()
	if _, ok := h.b.windows[h.w]; !ok {
		return
	}
	// Only this window's earlier report is replaced; other windows' reports of
	// the session stay as fallbacks.
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

// Resolve delivers the window's answer to a command it was sent.
func (h *Window) Resolve(result Result) {
	h.b.mu.Lock()
	defer h.b.mu.Unlock()
	h.b.answerLocked(h.w, result)
}

// Close removes the window: its layouts are forgotten and any command still
// waiting on it counts it as not showing the session.
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

// Layout returns the latest layout a window reported for the session.
func (b *Broker) Layout(sessionID string) (json.RawMessage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	reports := b.layouts[sessionID]
	if len(reports) == 0 {
		return nil, false
	}
	return append(json.RawMessage(nil), reports[len(reports)-1].layout...), true
}

// Connected reports whether any desktop window is connected.
func (b *Broker) Connected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.windows) > 0
}

// Execute sends a command to every connected window and returns the first
// successful answer. When every window declines, the most specific refusal
// is returned (a window that shows the session but refused beats one that
// does not show it).
func (b *Broker) Execute(ctx context.Context, sessionID, action string, args json.RawMessage) (Result, error) {
	b.mu.Lock()
	if len(b.windows) == 0 {
		b.mu.Unlock()
		return Result{}, ErrNoWindow
	}
	b.seq++
	id := "pane-" + strconv.FormatUint(b.seq, 10)
	// The window that last reported the session's layout is the one showing it.
	// Asking only that window keeps a request from being applied twice when two
	// windows are open. With no report, every window is asked and each says
	// whether it shows the session.
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

// answerLocked records one window's answer and finishes the command once a
// window succeeds or every window has answered.
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
