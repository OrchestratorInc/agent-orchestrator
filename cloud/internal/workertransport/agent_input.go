package workertransport

import (
	"bytes"
	"sync/atomic"
	"time"
)

// The worker types a queued turn (a Chat session's first prompt after a switch
// to the terminal, or a message sent to a terminal session) into the agent
// terminal. A live PTY is not yet an agent taking input: a harness TUI that is
// still booting reads the prompt and its Enter in one read once it gets there,
// takes the burst as a paste and leaves the prompt in its input box,
// unsubmitted, while the turn reads completed. So the forwarder waits for the
// agent to take input before it types; then the prompt and its Enter, a beat
// later, arrive as two reads.
//
// Claude Code and Codex enable bracketed paste mode (DECSET 2004) when their
// input reader goes live, the same signal the browser terminal's paste wrap
// follows. Other harnesses are forwarded to as they always were.

// agentInputReadyTimeout bounds the wait for an agent that never enables
// bracketed paste. The queued turn is then forwarded as it was before the
// wait. A variable so tests can shorten it.
var agentInputReadyTimeout = 60 * time.Second

var bracketedPasteOn = []byte("\x1b[?2004h")

// harnessSignalsInputReady reports whether a harness's TUI enables bracketed
// paste once it takes input (verified with Claude Code 2.1.304 and Codex
// 0.150, both within a fraction of a second of their first frame).
func harnessSignalsInputReady(harness string) bool {
	return harness == "claude-code" || harness == "codex"
}

// agentInput is whether an agent terminal's process takes input yet. The zero
// value forwards as before, without waiting.
type agentInput struct {
	// gated holds forwarded turns until ready, or until
	// agentInputReadyTimeout after startedAt.
	gated     bool
	startedAt time.Time
	ready     atomic.Bool
	wakeArmed atomic.Bool
	timedOut  atomic.Bool
}

// start configures an agent terminal's input tracking before its output is
// read.
func (a *agentInput) start(harness string, reattached bool) {
	a.gated = harnessSignalsInputReady(harness)
	a.startedAt = time.Now()
	if reattached {
		a.ready.Store(true)
	}
}

// waiting reports whether the terminal's output still has to show the agent
// taking input.
func (a *agentInput) waiting() bool {
	return a.gated && !a.ready.Load()
}

// bracketedPasteScan finds DECSET 2004 in terminal output, also when the
// sequence is split across two reads.
type bracketedPasteScan struct{ carry []byte }

func (b *bracketedPasteScan) enabled(data []byte) bool {
	scan := append(b.carry, data...)
	if bytes.Contains(scan, bracketedPasteOn) {
		return true
	}
	keep := min(len(scan), len(bracketedPasteOn)-1)
	b.carry = append([]byte(nil), scan[len(scan)-keep:]...)
	return false
}

// agentInputBecameReady marks the agent taking input and wakes the loop to
// forward a turn that waited for it.
func (s *Supervisor) agentInputBecameReady(terminal *terminalProcess) {
	if terminal.input.ready.Swap(true) {
		return
	}
	if terminal.input.wakeArmed.Load() {
		s.Logger.Info("agent terminal takes input; forwarding the queued turn",
			"harness", s.Harness, "after", time.Since(terminal.input.startedAt).Round(time.Millisecond))
	}
	s.wakeLoop()
}

// agentInputReady reports whether the forwarder may type into the agent
// terminal. While it may not, a timer wakes the loop at the wait's bound.
func (s *Supervisor) agentInputReady(terminalID string) bool {
	s.mu.Lock()
	terminal := s.terminals[terminalID]
	s.mu.Unlock()
	if terminal == nil || !terminal.input.waiting() {
		return true
	}
	wait := agentInputReadyTimeout - time.Since(terminal.input.startedAt)
	if wait <= 0 {
		if !terminal.input.timedOut.Swap(true) {
			s.Logger.Warn("agent terminal never enabled bracketed paste; forwarding the queued turn anyway",
				"harness", s.Harness, "waited", agentInputReadyTimeout)
		}
		return true
	}
	if !terminal.input.wakeArmed.Swap(true) {
		s.Logger.Info("waiting for the agent terminal to take input before forwarding the queued turn",
			"harness", s.Harness, "bound", wait.Round(time.Millisecond))
		time.AfterFunc(wait, s.wakeLoop)
	}
	return false
}
