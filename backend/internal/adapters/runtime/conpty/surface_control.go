package conpty

// The passive capture has no use for OSC side effects. The emulator can leak
// Unicode title payloads into screen cells, inventing an unsent composer draft.
// Strip these strings only from the observer, never from the real PTY output.
type surfaceControlFilter struct {
	escape       bool
	osc          bool
	stringEscape bool
}

func (f *surfaceControlFilter) visible(p []byte) []byte {
	visible := make([]byte, 0, len(p))
	for _, b := range p {
		if f.osc {
			if b == '\a' || b == '\x18' || b == '\x1a' || f.stringEscape && b == '\\' {
				f.osc, f.stringEscape = false, false
				continue
			}
			if !f.stringEscape {
				f.stringEscape = b == '\x1b'
				continue
			}
			// A different escape cancels the string and starts a new sequence.
			f.osc, f.stringEscape, f.escape = false, false, true
		}
		if f.escape {
			f.escape = false
			if b == ']' {
				f.osc = true
				continue
			}
			visible = append(visible, '\x1b')
		}
		if b == '\x1b' {
			f.escape = true
		} else {
			visible = append(visible, b)
		}
	}
	return visible
}
