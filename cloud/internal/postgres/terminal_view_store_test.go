package postgres

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/terminalview"
)

func TestSelectTerminalResizeQueuesOnlyChangedGrid(t *testing.T) {
	current := terminalview.Grid{Columns: 120, Rows: 40}
	if target, queue := selectTerminalResize(current, current, "succeeded", false); queue || target != current {
		t.Fatalf("unchanged desktop grid target=%+v queue=%v", target, queue)
	}
	phone := terminalview.Grid{Columns: 55, Rows: 39}
	if target, queue := selectTerminalResize(current, phone, "succeeded", false); !queue || target != phone {
		t.Fatalf("parked desktop target=%+v queue=%v, want phone resize", target, queue)
	}
}

func TestSelectTerminalResizeRetainsGridWithoutViewer(t *testing.T) {
	current := terminalview.Grid{Columns: 120, Rows: 40}
	if target, queue := selectTerminalResize(current, terminalview.Grid{}, "succeeded", false); queue || target != current {
		t.Fatalf("no viewer target=%+v queue=%v, want retained grid", target, queue)
	}
}

func TestSelectTerminalResizeRetriesFailedOrExpiredRequest(t *testing.T) {
	grid := terminalview.Grid{Columns: 55, Rows: 39}
	for _, scenario := range []struct {
		name    string
		status  string
		expired bool
	}{
		{name: "worker failed", status: "failed"},
		{name: "request expired", status: "pending", expired: true},
		{name: "request missing"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if target, queue := selectTerminalResize(grid, grid, scenario.status, scenario.expired); !queue || target != grid {
				t.Fatalf("target=%+v queue=%v, want retry", target, queue)
			}
		})
	}
	for _, status := range []string{"pending", "claimed", "succeeded"} {
		if _, queue := selectTerminalResize(grid, grid, status, false); queue {
			t.Fatalf("%s request must not be duplicated", status)
		}
	}
	if _, queue := selectTerminalResize(grid, grid, "succeeded", true); queue {
		t.Fatal("a completed resize stays complete after its request TTL")
	}
}

func TestSelectTerminalResizeDoesNotQueueZeroGrid(t *testing.T) {
	if target, queue := selectTerminalResize(terminalview.Grid{}, terminalview.Grid{}, "", false); queue || target != (terminalview.Grid{}) {
		t.Fatalf("target=%+v queue=%v, want no resize", target, queue)
	}
}
