package terminalview

// Viewer is one live terminal attachment's own preferred grid. A parked or
// unmeasured viewer does not drive the shared PTY.
type Viewer struct {
	Role    string
	Visible bool
	Columns uint16
	Rows    uint16
}

// Grid is the terminal's complete column/row pair from one viewer.
type Grid struct {
	Columns uint16
	Rows    uint16
}

// Elect chooses the largest visible primary grid, falling back to the largest
// visible secondary grid. An empty result means no viewer can size the PTY.
func Elect(viewers []Viewer) Grid {
	hasPrimary := false
	for _, viewer := range viewers {
		if viewer.Visible && viewer.Role == "primary" && viewer.Columns > 0 && viewer.Rows > 0 {
			hasPrimary = true
			break
		}
	}

	best := Grid{}
	bestArea := 0
	for _, viewer := range viewers {
		if !viewer.Visible || viewer.Columns == 0 || viewer.Rows == 0 ||
			(hasPrimary && viewer.Role != "primary") {
			continue
		}
		area := int(viewer.Columns) * int(viewer.Rows)
		if area > bestArea {
			bestArea = area
			best = Grid{Columns: viewer.Columns, Rows: viewer.Rows}
		}
	}
	return best
}
