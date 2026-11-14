package conpty

import (
	"strings"
	"sync"
)

const MaxOutputLines = 1000

type Ring struct {
	mu          sync.Mutex
	lines       []string
	partialLine string
}

func NewRing() *Ring {
	return &Ring{}
}

func (r *Ring) Append(raw []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	text := r.partialLine + string(raw)
	parts := strings.Split(text, "\n")
	r.partialLine = parts[len(parts)-1]
	for _, line := range parts[:len(parts)-1] {
		r.lines = append(r.lines, line+"\n")
	}
	if len(r.lines) > MaxOutputLines {
		r.lines = r.lines[len(r.lines)-MaxOutputLines:]
	}
}

func (r *Ring) FlushPartial() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.partialLine == "" {
		return
	}
	r.lines = append(r.lines, r.partialLine)
	r.partialLine = ""
}

func (r *Ring) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	return []byte(strings.Join(r.lines, ""))
}

func (r *Ring) Replay() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	return []byte(strings.Join(r.lines, "") + r.partialLine)
}

func (r *Ring) Tail(n int) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if n <= 0 {
		return ""
	}
	start := len(r.lines) - n
	if start < 0 {
		start = 0
	}
	return strings.Join(r.lines[start:], "")
}
