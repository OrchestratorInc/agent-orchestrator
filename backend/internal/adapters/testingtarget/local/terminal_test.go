package local

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func terminalFixture(t *testing.T) (*fakeSystem, *[]terminalWindow, *int) {
	t.Helper()
	f := fixture(t)
	windows := []terminalWindow{{PID: 90, ID: "old", TTY: "/dev/ttys001"}}
	f.pids[90] = processInfo{PID: 90, Parent: 1}
	f.times[90] = time.Now().UTC()
	f.pids[91] = processInfo{PID: 91, Parent: 90}
	f.times[91] = time.Now().UTC()
	reads := new(int)
	f.a.ops.terminals = func(context.Context) ([]terminalWindow, error) { return windows, nil }
	f.a.ops.terminalText = func(_ context.Context, w terminalWindow) (string, error) {
		*reads++
		if w.ID == "old" {
			t.Fatal("read pre-existing terminal text")
		}
		return "exec env AO_DATA_DIR=" + strings.ReplaceAll(f.s.target.DataDir, "/", "/\n") + " AO_RUN_FILE=" + strings.ReplaceAll(f.s.root, "/", "/\r\n") + "/running.json ao triage\nWhat went wrong?", nil
	}
	f.a.ops.windows = func(_ context.Context, pid int) ([]string, error) {
		if pid != 90 {
			return nil, nil
		}
		var ids []string
		for _, w := range windows {
			ids = append(ids, w.ID)
		}
		return ids, nil
	}
	f.a.ops.terminalProcesses = func(_ context.Context, tty string) ([]int, error) {
		if tty != "/dev/ttys002" {
			t.Fatal("unowned tty", tty)
		}
		return []int{91}, nil
	}
	f.a.ops.closeTerminal = func(_ context.Context, w terminalWindow) error {
		if w.ID != "new" {
			t.Fatal("closed unowned window", w)
		}
		windows = windows[:1]
		return nil
	}
	return f, &windows, reads
}

func control(t *testing.T, f *fakeSystem, action string) ports.TestingControlResult {
	t.Helper()
	result, err := f.a.Control(context.Background(), f.s.target, action)
	if err != nil {
		t.Fatal(action, err)
	}
	return result
}

func TestTerminalAdmissionInputIdentityTextAndCleanup(t *testing.T) {
	f, windows, reads := terminalFixture(t)
	control(t, f, "arm_terminal")
	if *reads != 0 {
		t.Fatal("arming read unowned contents")
	}
	*windows = append(*windows, terminalWindow{PID: 90, ID: "new", TTY: "/dev/ttys002"})
	result := control(t, f, "admit_terminal")
	if result.Window == nil || result.Window.ElectronPID != 90 || result.Window.WindowID != "new" || result.Window.ID == f.s.target.ID {
		t.Fatal(result)
	}
	if _, owned := f.s.owned[90]; owned {
		t.Fatal("Terminal.app process became owned")
	}
	if _, owned := f.s.owned[91]; !owned {
		t.Fatal("terminal child cleanup not retained")
	}
	if !strings.Contains(control(t, f, "read_terminal").Text, "What went wrong?") {
		t.Fatal("missing terminal text")
	}
	if err := f.a.closeOwnedTerminal(context.Background(), f.s); err != nil {
		t.Fatal(err)
	}
	if len(*windows) != 1 || (*windows)[0].ID != "old" {
		t.Fatal("pre-existing window was closed")
	}
}

func TestTerminalAdmissionFailsClosed(t *testing.T) {
	for _, kind := range []string{"unarmed", "existing", "ambiguous", "unrelated", "expired", "reused", "placeholder"} {
		t.Run(kind, func(t *testing.T) {
			f, windows, reads := terminalFixture(t)
			if kind != "unarmed" {
				control(t, f, "arm_terminal")
			}
			if kind != "existing" {
				*windows = append(*windows, terminalWindow{PID: 90, ID: "new", TTY: "/dev/ttys002"})
			}
			switch kind {
			case "ambiguous":
				*windows = append(*windows, terminalWindow{PID: 90, ID: "other", TTY: "/dev/ttys003"})
			case "unrelated":
				f.a.ops.terminalText = func(context.Context, terminalWindow) (string, error) { return "unrelated command", nil }
			case "expired":
				f.s.terminalArmedAt = time.Now().Add(-3 * time.Minute)
			case "reused":
				f.times[90] = f.times[90].Add(time.Hour)
			case "placeholder":
				(*windows)[len(*windows)-1] = terminalWindow{PID: 90, ID: "new", Closed: true}
			}
			if _, err := f.a.Control(context.Background(), f.s.target, "admit_terminal"); err == nil {
				t.Fatal("admitted unsafe terminal")
			}
			if f.s.terminal != nil {
				t.Fatal("unsafe terminal retained")
			}
			if kind == "existing" && *reads != 0 {
				t.Fatal("read pre-existing window")
			}
		})
	}
}

func TestTerminalCleanupAcceptsOnlyProvedClosedPlaceholder(t *testing.T) {
	for _, state := range []string{"after close", "already closed", "hidden live tab", "unknown metadata", "reused PID"} {
		t.Run(state, func(t *testing.T) {
			f, windows, _ := terminalFixture(t)
			control(t, f, "arm_terminal")
			*windows = append(*windows, terminalWindow{PID: 90, ID: "new", TTY: "/dev/ttys002"})
			control(t, f, "admit_terminal")
			closed := terminalWindow{PID: 90, ID: "new", Closed: true}
			if state == "already closed" || state == "reused PID" {
				(*windows)[1] = closed
			}
			if state == "reused PID" {
				f.times[90] = f.times[90].Add(time.Hour)
			}
			closes := 0
			f.a.ops.closeTerminal = func(_ context.Context, w terminalWindow) error {
				closes++
				if w.ID != "new" {
					t.Fatal("closed an unowned window")
				}
				if state == "after close" {
					(*windows)[1] = closed
				}
				return nil
			}
			if state == "unknown metadata" {
				inventory := f.a.ops.terminals
				f.a.ops.terminals = func(ctx context.Context) ([]terminalWindow, error) {
					if closes > 0 {
						return nil, errors.New("metadata unavailable")
					}
					return inventory(ctx)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			err := f.a.closeOwnedTerminal(ctx, f.s)
			wantClosed := state == "after close" || state == "already closed"
			if (err == nil) != wantClosed {
				t.Fatalf("state=%s err=%v", state, err)
			}
			if (state == "already closed" || state == "reused PID") && closes != 0 {
				t.Fatal("closed a placeholder or reused identity")
			}
			if len(*windows) != 2 || (*windows)[0].ID != "old" {
				t.Fatal("lost pre-existing window")
			}
		})
	}
}
