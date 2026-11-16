package conpty

import (
	"bytes"
	"runtime"
	"testing"
)

// tuiFrame is one repaint of a full-screen TUI: cursor home, rows drawn with
// cursor moves and carriage returns, no newline anywhere.
func tuiFrame() []byte {
	var b bytes.Buffer
	b.WriteString("\x1b[H")
	for row := 1; row <= 40; row++ {
		b.WriteString("\x1b[")
		b.WriteString("2K\r\x1b[38;5;245m│ working on the thing… ─────────────────────────────── │\x1b[0m\x1b[1B")
	}
	return b.Bytes()
}

func BenchmarkRingTUIRepaint(b *testing.B) {
	frame := tuiFrame()
	for range b.N {
		r := NewRing()
		for range 3000 { // ~3000 repaints, a few minutes of a busy agent
			r.Append(frame)
		}
		runtime.KeepAlive(r)
		b.ReportMetric(float64(len(r.Replay())), "replay-bytes")
	}
}
