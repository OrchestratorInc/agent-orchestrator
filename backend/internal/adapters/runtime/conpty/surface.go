package conpty

import (
	"io"
	"strings"

	vt "github.com/unixshells/vt-go"
)

type renderedSurface struct {
	emulator *vt.SafeEmulator
	titles   oscTitleFilter
}

func newRenderedSurface(cols, rows int) *renderedSurface {
	emulator := vt.NewSafeEmulator(cols, rows)
	go func() { _, _ = io.Copy(io.Discard, emulator) }()
	return &renderedSurface{emulator: emulator}
}

func (s *renderedSurface) Write(p []byte) {
	_, _ = s.emulator.Write(s.titles.filter(p))
}

func (s *renderedSurface) Resize(cols, rows int) {
	s.emulator.Resize(cols, rows)
}

func (s *renderedSurface) Tail(lines int) string {
	if lines <= 0 {
		return ""
	}
	rendered := strings.TrimRight(s.emulator.Render(), "\n")
	if rendered == "" {
		return ""
	}
	rows := strings.Split(rendered, "\n")
	if len(rows) > lines {
		rows = rows[len(rows)-lines:]
	}
	return strings.Join(rows, "\n")
}
