package terminalview

import "testing"

func TestElectUsesVisiblePrimaryGrid(t *testing.T) {
	viewers := []Viewer{
		{Role: "primary", Visible: true, Columns: 120, Rows: 40},
		{Role: "secondary", Visible: true, Columns: 55, Rows: 39},
	}
	if got := Elect(viewers); got != (Grid{Columns: 120, Rows: 40}) {
		t.Fatalf("elected %+v, want desktop 120x40", got)
	}
}

func TestElectFallsBackToPhoneWhenDesktopParks(t *testing.T) {
	viewers := []Viewer{
		{Role: "primary", Visible: false, Columns: 120, Rows: 40},
		{Role: "secondary", Visible: true, Columns: 55, Rows: 39},
	}
	if got := Elect(viewers); got != (Grid{Columns: 55, Rows: 39}) {
		t.Fatalf("elected %+v, want phone 55x39", got)
	}
}

func TestElectKeepsDimensionsFromOneViewer(t *testing.T) {
	viewers := []Viewer{
		{Role: "primary", Visible: true, Columns: 120, Rows: 30},
		{Role: "primary", Visible: true, Columns: 90, Rows: 50},
	}
	if got := Elect(viewers); got != (Grid{Columns: 90, Rows: 50}) {
		t.Fatalf("elected %+v, want the larger viewer's whole 90x50 grid", got)
	}
}

func TestElectIgnoresHiddenAndUnmeasuredViewers(t *testing.T) {
	viewers := []Viewer{
		{Role: "primary", Visible: false, Columns: 120, Rows: 40},
		{Role: "secondary", Visible: true, Columns: 55, Rows: 0},
	}
	if got := Elect(viewers); got != (Grid{}) {
		t.Fatalf("elected %+v, want no eligible grid", got)
	}
}
