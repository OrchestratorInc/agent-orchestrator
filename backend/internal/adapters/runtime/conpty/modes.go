package conpty

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type modeTracker struct {
	mu sync.Mutex

	state   parseState
	params  []byte
	belEnds bool

	st modeState
}

type modeState struct {
	mouseProtocol  int
	mouseEncoding  int
	altScreen      int
	appCursorKeys  bool
	appKeypad      bool
	focusEvents    bool
	bracketedPaste bool
	wrapOff        bool
	cursorHidden   bool
}

type parseState uint8

const (
	stateGround parseState = iota
	stateEscape
	stateCSI
	stateString
	stateStringEscape
)

const maxParams = 64

func newModeTracker() *modeTracker {
	return &modeTracker{}
}

func (m *modeTracker) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range p {
		m.step(b)
	}
	return len(p), nil
}

func (m *modeTracker) step(b byte) {
	switch m.state {
	case stateGround:
		if b == 0x1b {
			m.state = stateEscape
		}
	case stateEscape:
		switch {
		case b == '[':
			m.state = stateCSI
			m.params = m.params[:0]
		case b == 'c':
			m.st.fullReset()
			m.state = stateGround
		case b == '=':
			m.st.appKeypad = true
			m.state = stateGround
		case b == '>':
			m.st.appKeypad = false
			m.state = stateGround
		case b == 'P' || b == ']' || b == 'X' || b == '^' || b == '_':
			m.state = stateString
			m.belEnds = b == ']'
		case b == 0x1b:
		case b == 0x18 || b == 0x1a:
			m.state = stateGround
		case b < 0x20 || b == 0x7f:
		default:
			m.state = stateGround
		}
	case stateCSI:
		switch {
		case b == 0x1b:
			m.state = stateEscape
		case b == 0x18 || b == 0x1a:
			m.state = stateGround
		case b < 0x20 || b == 0x7f:
		case b <= 0x3f:
			if len(m.params) < maxParams {
				m.params = append(m.params, b)
			} else {
				m.state = stateGround
			}
		case b <= 0x7e:
			m.dispatch(b)
			m.state = stateGround
		default:
			m.state = stateGround
		}
	case stateString:
		switch {
		case b == 0x18 || b == 0x1a:
			m.state = stateGround
		case b == 0x07 && m.belEnds:
			m.state = stateGround
		case b == 0x1b:
			m.state = stateStringEscape
		}
	case stateStringEscape:
		if b == '\\' {
			m.state = stateGround
		} else {
			m.state = stateEscape
			m.step(b)
		}
	}
}

func (m *modeTracker) dispatch(final byte) {
	switch final {
	case 'h', 'l':
		if len(m.params) == 0 || m.params[0] != '?' {
			return
		}
		on := final == 'h'
		n, digits, valid := 0, false, true
		for _, c := range m.params[1:] {
			switch {
			case c >= '0' && c <= '9':
				n = n*10 + int(c-'0')
				digits = true
			case c == ';':
				if valid && digits {
					m.st.set(n, on)
				}
				n, digits, valid = 0, false, true
			default:
				valid = false
			}
		}
		if valid && digits {
			m.st.set(n, on)
		}
	case 'p':
		if len(m.params) == 1 && m.params[0] == '!' {
			m.st.softReset()
		}
	}
}

func (s *modeState) set(n int, on bool) {
	switch n {
	case 9, 1000, 1002, 1003:
		s.mouseProtocol = 0
		if on {
			s.mouseProtocol = n
		}
	case 1006, 1016:
		s.mouseEncoding = 0
		if on {
			s.mouseEncoding = n
		}
	case 47, 1047, 1049:
		s.altScreen = 0
		if on {
			s.altScreen = n
		}
	case 1:
		s.appCursorKeys = on
	case 66:
		s.appKeypad = on
	case 1004:
		s.focusEvents = on
	case 2004:
		s.bracketedPaste = on
	case 7:
		s.wrapOff = !on
	case 25:
		s.cursorHidden = !on
	}
}

func (s *modeState) softReset() {
	s.appCursorKeys, s.appKeypad, s.focusEvents, s.bracketedPaste = false, false, false, false
	s.wrapOff, s.cursorHidden = false, false
}

func (s *modeState) fullReset() {
	*s = modeState{cursorHidden: s.cursorHidden}
}

func (m *modeTracker) Restore(replay []byte) []byte {
	m.mu.Lock()
	live := m.st
	m.mu.Unlock()

	fromReplay := newModeTracker()
	_, _ = fromReplay.Write(replay)
	rep := fromReplay.st

	type entry struct {
		n  int
		on bool
	}
	var out []entry
	if live.altScreen != 0 && rep.altScreen != live.altScreen {
		out = append(out, entry{live.altScreen, true})
	}
	var rest []entry
	flag := func(n int, want, have bool) {
		if want && !have {
			rest = append(rest, entry{n, true})
		}
	}
	reset := func(n int, want, have bool) {
		if want && !have {
			rest = append(rest, entry{n, false})
		}
	}
	if live.mouseProtocol != 0 && rep.mouseProtocol != live.mouseProtocol {
		rest = append(rest, entry{live.mouseProtocol, true})
	}
	if live.mouseEncoding != 0 && rep.mouseEncoding != live.mouseEncoding {
		rest = append(rest, entry{live.mouseEncoding, true})
	}
	flag(1, live.appCursorKeys, rep.appCursorKeys)
	flag(66, live.appKeypad, rep.appKeypad)
	flag(1004, live.focusEvents, rep.focusEvents)
	flag(2004, live.bracketedPaste, rep.bracketedPaste)
	reset(7, live.wrapOff, rep.wrapOff)
	reset(25, live.cursorHidden, rep.cursorHidden)
	sort.Slice(rest, func(i, j int) bool { return rest[i].n < rest[j].n })
	out = append(out, rest...)

	if len(out) == 0 {
		return nil
	}
	var sb strings.Builder
	for _, e := range out {
		final := byte('h')
		if !e.on {
			final = 'l'
		}
		fmt.Fprintf(&sb, "\x1b[?%d%c", e.n, final)
	}
	return []byte(sb.String())
}
