package conpty

// maxOSCTitlePayload bounds the bytes buffered for one title. A longer payload
// is not a title; the whole sequence is dropped instead of being truncated.
const maxOSCTitlePayload = 4096

type oscTitleState uint8

const (
	oscTitleGround oscTitleState = iota
	oscTitleEscape
	oscTitleCommand
	oscTitlePayload
)

// oscTitleScanner finds OSC 0 and OSC 2 window titles in PTY output. It only
// reads the stream, so what clients receive is untouched, and it keeps its
// state between calls because a sequence can span any number of PTY reads.
//
// It follows xterm: BEL or ST (ESC \ or C1 0x9c) ends a title, any other ESC
// also ends it, and CAN or SUB abandons it. Output inside a UTF-8 sequence is
// never taken for a terminator, because Claude's ✳ is E2 9C B3 and contains the
// C1 ST byte.
type oscTitleScanner struct {
	state         oscTitleState
	command       int
	sawDigit      bool
	payload       []byte
	overflow      bool
	utf8Remaining int
}

// scan returns the last complete title in p. ok is false when p completed none;
// an empty title with ok true is a program resetting its title.
func (s *oscTitleScanner) scan(p []byte) (title string, ok bool) {
	for _, b := range p {
		switch s.state {
		case oscTitleGround:
			if b == 0x1b {
				s.state = oscTitleEscape
			}
		case oscTitleEscape:
			switch b {
			case ']':
				s.state, s.command, s.sawDigit = oscTitleCommand, 0, false
			case 0x1b:
				// Still an escape introducer.
			default:
				s.state = oscTitleGround
			}
		case oscTitleCommand:
			s.scanCommand(b)
		case oscTitlePayload:
			if done, complete := s.scanPayload(b); done {
				if complete {
					title, ok = string(s.payload), true
				}
				s.payload, s.overflow, s.utf8Remaining = s.payload[:0], false, 0
			}
		}
	}
	return title, ok
}

func (s *oscTitleScanner) scanCommand(b byte) {
	switch {
	case b >= '0' && b <= '9':
		s.sawDigit = true
		// Clamped so a long digit run cannot overflow into a real command.
		s.command = min(3, s.command*10+int(b-'0'))
	case b == ';' && s.sawDigit:
		s.state = oscTitlePayload
		// Other OSC commands share this path but are never reported.
		s.overflow = s.command != 0 && s.command != 2
	case b == 0x1b:
		s.state = oscTitleEscape
	default:
		s.state = oscTitleGround
	}
}

// scanPayload consumes one payload byte. done ends the sequence and complete
// says a title should be reported.
func (s *oscTitleScanner) scanPayload(b byte) (done, complete bool) {
	if s.utf8Remaining > 0 && b >= 0x80 && b <= 0xbf {
		s.utf8Remaining--
		s.appendPayload(b)
		return false, false
	}
	s.utf8Remaining = 0
	switch {
	case b >= 0xc2 && b <= 0xdf:
		s.utf8Remaining = 1
	case b >= 0xe0 && b <= 0xef:
		s.utf8Remaining = 2
	case b >= 0xf0 && b <= 0xf4:
		s.utf8Remaining = 3
	}
	switch b {
	case '\a', 0x9c:
		s.state = oscTitleGround
		return true, !s.overflow
	case 0x1b:
		s.state = oscTitleEscape
		return true, !s.overflow
	case 0x18, 0x1a:
		s.state = oscTitleGround
		return true, false
	}
	s.appendPayload(b)
	return false, false
}

func (s *oscTitleScanner) appendPayload(b byte) {
	if s.overflow {
		return
	}
	if len(s.payload) == maxOSCTitlePayload {
		s.overflow = true
		return
	}
	s.payload = append(s.payload, b)
}
