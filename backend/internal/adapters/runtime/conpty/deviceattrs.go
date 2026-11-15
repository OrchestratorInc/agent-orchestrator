package conpty

import (
	"regexp"
	"runtime"
)

var answersDeviceAttributes = runtime.GOOS != "windows"

var da1Reply = []byte("\x1b[?1;2c")

var da1Replies = regexp.MustCompile(`^(?:\x1b\[\?[0-9;]*c)+$`)

type da1Scanner struct{ state uint8 }

const (
	da1Ground uint8 = iota
	da1Esc
	da1CSI
	da1Zero
)

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

func (h *host) queueDA1Replies(n int) {
	for range n {
		select {
		case h.da1C <- struct{}{}:
		default:
			return
		}
	}
}

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
