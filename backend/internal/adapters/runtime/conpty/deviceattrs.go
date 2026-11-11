package conpty

import (
	"regexp"
	"runtime"
)

// The pty-host answers Primary Device Attributes (DA1) queries itself.
//
// Shells ask DA1 at startup and wait for the reply: fish 4.1+ sends ESC [ c and
// stalls for 2 s, then warns, when none arrives. The desktop pane deliberately
// forwards only a few of xterm.js's replies (colour reports and cursor
// position), and the surface's emulator discards its replies, so before this no
// one answered. Answering in the host works with no client attached, while the
// desktop is still replaying, and with --surface=lazy.
//
// The host is the only answerer: a client's own DA1 reply (the phone's WebView
// xterm forwards everything it generates, including replies to replayed
// scrollback) is dropped, so a program never reads two answers.
//
// Windows ConPTY answers DA1 inside conhost before the query reaches us, so the
// host stays out of it there.
var answersDeviceAttributes = runtime.GOOS != "windows"

// da1Reply is what xterm.js answers: a VT100 with Advanced Video Option.
var da1Reply = []byte("\x1b[?1;2c")

// da1Replies matches a client input payload made only of DA1 replies.
var da1Replies = regexp.MustCompile(`^(?:\x1b\[\?[0-9;]*c)+$`)

// da1Scanner finds DA1 queries (ESC [ c and ESC [ 0 c) in PTY output, including
// a query split across reads. DA2 (ESC [ > c) and DA3 (ESC [ = c) do not match.
type da1Scanner struct{ state uint8 }

const (
	da1Ground uint8 = iota
	da1Esc          // saw ESC
	da1CSI          // saw ESC [
	da1Zero         // saw ESC [ 0 (or more zeros)
)

// scan reports how many DA1 queries end in b.
func (s *da1Scanner) scan(b []byte) int {
	n := 0
	for _, c := range b {
		switch {
		case c == 0x1b:
			s.state = da1Esc
		case s.state == da1Esc && c == '[':
			s.state = da1CSI
		case (s.state == da1CSI || s.state == da1Zero) && c == '0':
			s.state = da1Zero
		case (s.state == da1CSI || s.state == da1Zero) && c == 'c':
			n++
			s.state = da1Ground
		default:
			s.state = da1Ground
		}
	}
	return n
}

// queueDA1Replies hands n replies to the reply writer. It never blocks the PTY
// read loop: a program flooding queries without reading its input would
// otherwise deadlock the pump against its own full input buffer. Extra replies
// past the queue are dropped.
func (h *host) queueDA1Replies(n int) {
	for range n {
		select {
		case h.da1C <- struct{}{}:
		default:
			return
		}
	}
}

// writeDA1Replies writes queued replies to the PTY until shutdown.
func (h *host) writeDA1Replies() {
	for {
		select {
		case <-h.shutdownC:
			return
		case <-h.da1C:
			if _, exited := h.cfg.PTY.ExitCode(); !exited {
				_, _ = h.cfg.PTY.Write(da1Reply)
			}
		}
	}
}
