package conpty

import (
	"io"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	vt "github.com/unixshells/vt-go"
)

// renderedSurface applies the ConPTY VT output stream to a cell grid. Unlike
// Ring, it represents only the current viewport, so overwritten transcript
// text cannot be mistaken for live provider chrome during a destructive
// interface handoff.
type renderedSurface struct {
	emulator *vt.SafeEmulator
	titles   oscTitleFilter
}

func newRenderedSurface(cols, rows int) *renderedSurface {
	emulator := vt.NewSafeEmulator(cols, rows)
	// The emulator answers terminal capability and status queries through its
	// input pipe. This surface is a passive capture-only observer, so nobody
	// consumes those replies unless we drain them here. Leaving the pipe unread
	// makes Emulator.Write block on the first query (for example DA1), which in
	// turn stalls the real PTY output pump and leaves the attached xterm blank.
	go func() { _, _ = io.Copy(io.Discard, emulator) }()
	return &renderedSurface{emulator: emulator}
}

func (s *renderedSurface) Write(p []byte) {
	_, _ = s.emulator.Write(s.titles.filter(p))
}

func (s *renderedSurface) Resize(cols, rows int) {
	// vt-go preserves top rows on shrink. A normal REPL may emit its prompt
	// only once, so retain the current cursor-bearing rows rather than losing
	// them below the new bottom. Alternate-screen redraw behavior is unchanged.
	cursor := s.emulator.CursorPosition()
	shift := cursor.Y - rows + 1
	if rows <= 0 || cols <= 0 || s.emulator.IsAltScreen() || shift <= 0 {
		s.emulator.Resize(cols, rows)
		return
	}
	width := min(cols, s.emulator.Width())
	cells := make([][]*uv.Cell, rows)
	for y := range rows {
		cells[y] = make([]*uv.Cell, cols)
		for x := range width {
			cell := s.emulator.CellAt(x, y+shift)
			if cell != nil && (cell.Width <= 1 || x+cell.Width <= cols) {
				cells[y][x] = cell.Clone()
			}
		}
	}
	s.emulator.Resize(cols, rows)
	for y := range rows {
		for x := range cols {
			s.emulator.SetCell(x, y, nil)
		}
		for x := 0; x < cols; x++ {
			if cell := cells[y][x]; cell != nil && cell.Width > 0 {
				s.emulator.SetCell(x, y, cell)
				x += cell.Width - 1
			}
		}
	}
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
