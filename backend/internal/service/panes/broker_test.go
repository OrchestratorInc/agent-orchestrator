package panes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExecuteWithoutWindowFails(t *testing.T) {
	b := NewBroker()
	if _, err := b.Execute(context.Background(), "s-1", "split", nil); !errors.Is(err, ErrNoWindow) {
		t.Fatalf("err = %v, want ErrNoWindow", err)
	}
}

func TestExecuteReturnsTheShowingWindowsAnswer(t *testing.T) {
	b := NewBroker()
	var showing, other *Window
	showing = b.Connect(func(cmd Command) {
		go showing.Resolve(Result{ID: cmd.ID, OK: true, Layout: json.RawMessage(`{"selected":"sh-1"}`)})
	})
	other = b.Connect(func(cmd Command) {
		go other.Resolve(Result{ID: cmd.ID, Code: CodeNotShown})
	})
	result, err := b.Execute(context.Background(), "s-1", "split", json.RawMessage(`{"tab":"terminal"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.OK || string(result.Layout) != `{"selected":"sh-1"}` {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecutePrefersARealRefusalOverNotShown(t *testing.T) {
	b := NewBroker()
	var a, c *Window
	a = b.Connect(func(cmd Command) { go a.Resolve(Result{ID: cmd.ID, Code: CodeNotShown}) })
	c = b.Connect(func(cmd Command) {
		go c.Resolve(Result{ID: cmd.ID, Code: "SPLIT_PANES_OFF", Message: "Split panes is off."})
	})
	result, err := b.Execute(context.Background(), "s-1", "split", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.OK || result.Code != "SPLIT_PANES_OFF" {
		t.Fatalf("result = %+v, want SPLIT_PANES_OFF", result)
	}
}

func TestExecuteTimesOutAndForgetsTheCommand(t *testing.T) {
	b := NewBroker()
	b.timeout = 20 * time.Millisecond
	b.Connect(func(Command) {})
	if _, err := b.Execute(context.Background(), "s-1", "split", nil); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) != 0 {
		t.Fatalf("pending = %d, want 0", len(b.pending))
	}
}

func TestClosingAWindowAnswersForItAndDropsItsLayouts(t *testing.T) {
	b := NewBroker()
	var w *Window
	w = b.Connect(func(Command) { go w.Close() })
	w.Report("s-1", json.RawMessage(`{"selected":"session"}`))
	if _, ok := b.Layout("s-1"); !ok {
		t.Fatal("layout not recorded")
	}
	result, err := b.Execute(context.Background(), "s-1", "split", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Code != CodeNotShown {
		t.Fatalf("result = %+v, want %s", result, CodeNotShown)
	}
	if _, ok := b.Layout("s-1"); ok {
		t.Fatal("closed window's layout kept")
	}
	if b.Connected() {
		t.Fatal("closed window still connected")
	}
}

func TestReportingNullForgetsOnlyThatWindowsLayout(t *testing.T) {
	b := NewBroker()
	first := b.Connect(func(Command) {})
	second := b.Connect(func(Command) {})
	first.Report("s-1", json.RawMessage(`{"selected":"a"}`))
	second.Report("s-1", json.RawMessage(`{"selected":"b"}`))
	first.Report("s-1", json.RawMessage(`null`))
	layout, ok := b.Layout("s-1")
	if !ok || string(layout) != `{"selected":"b"}` {
		t.Fatalf("layout = %s, %v; want the second window's", layout, ok)
	}
	second.Report("s-1", nil)
	if _, ok := b.Layout("s-1"); ok {
		t.Fatal("layout kept after its window stopped showing the session")
	}
}

func TestExecuteAsksOnlyTheWindowShowingTheSession(t *testing.T) {
	b := NewBroker()
	asked := map[string]int{}
	var first, second *Window
	first = b.Connect(func(cmd Command) {
		asked["first"]++
		go first.Resolve(Result{ID: cmd.ID, OK: true})
	})
	second = b.Connect(func(cmd Command) {
		asked["second"]++
		go second.Resolve(Result{ID: cmd.ID, OK: true})
	})
	// Both windows show the session; the one that reported last owns it.
	first.Report("s-1", json.RawMessage(`{"selected":"a"}`))
	second.Report("s-1", json.RawMessage(`{"selected":"b"}`))
	if _, err := b.Execute(context.Background(), "s-1", "split", nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if asked["second"] != 1 || asked["first"] != 0 {
		t.Fatalf("asked = %v, want only the window that reported last", asked)
	}
}

func TestReportIgnoresAnOversizedLayout(t *testing.T) {
	b := NewBroker()
	w := b.Connect(func(Command) {})
	w.Report("s-1", json.RawMessage(`{"selected":"a"}`))
	huge := json.RawMessage(`"` + strings.Repeat("x", MaxLayoutBytes) + `"`)
	w.Report("s-1", huge)
	layout, ok := b.Layout("s-1")
	if !ok || string(layout) != `{"selected":"a"}` {
		t.Fatalf("layout = %.40s, %v; want the previous one kept", layout, ok)
	}
}

func TestAClosedWindowFallsBackToAnotherWindowsReport(t *testing.T) {
	b := NewBroker()
	first := b.Connect(func(Command) {})
	second := b.Connect(func(Command) {})
	first.Report("s-1", json.RawMessage(`{"selected":"a"}`))
	second.Report("s-1", json.RawMessage(`{"selected":"b"}`))
	second.Close()
	layout, ok := b.Layout("s-1")
	if !ok || string(layout) != `{"selected":"a"}` {
		t.Fatalf("layout = %s, %v; want the remaining window's", layout, ok)
	}
}
