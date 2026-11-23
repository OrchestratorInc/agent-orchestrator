package backgroundtasks

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestTailOutputKeepsWhatTheTerminalShowed(t *testing.T) {
	raw := "\x1b[32m✓\x1b[0m build\r\n" +
		"progress 10%\rprogress 60%\rprogress 100%\n" +
		"\x1b]0;window title\x07ready on \x1b[1mhttp://localhost:3000\x1b[22m\n" +
		"bell\a and nul\x00 removed\n\n"
	got := TailOutput(raw, false)
	want := "✓ build\nprogress 100%\nready on http://localhost:3000\nbell and nul removed"
	if got.Text != want || got.Truncated {
		t.Fatalf("got %q truncated=%v", got.Text, got.Truncated)
	}
	if got := TailOutput("", false); got.Text != "" || got.Truncated {
		t.Fatalf("empty output = %+v", got)
	}
	if got := TailOutput("ok\xff\n", true); got.Text != "ok�" || !got.Truncated {
		t.Fatalf("invalid UTF-8 or caller truncation lost: %+v", got)
	}
}

func TestTailOutputCapsLinesThenBytes(t *testing.T) {
	var lines []string
	for i := 1; i <= MaxOutputLines+50; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	got := TailOutput(strings.Join(lines, "\n"), false)
	kept := strings.Split(got.Text, "\n")
	if len(kept) != MaxOutputLines || kept[0] != "line 51" || kept[len(kept)-1] != "line 250" || !got.Truncated {
		t.Fatalf("kept %d lines from %q to %q, truncated=%v", len(kept), kept[0], kept[len(kept)-1], got.Truncated)
	}
	long := strings.Repeat("x", MaxOutputBytes/2)
	got = TailOutput(long+"\n"+long+"\n"+long+"\nend", false)
	if len(got.Text) > MaxOutputBytes || !strings.HasSuffix(got.Text, "\nend") || !got.Truncated {
		t.Fatalf("byte cap: len=%d truncated=%v", len(got.Text), got.Truncated)
	}
}

func TestOutputBufferKeepsATailPerItemAndEvictsTheStalest(t *testing.T) {
	var b OutputBuffer
	if _, ok := b.Read("cmd-1"); ok {
		t.Fatal("read output that was never written")
	}
	b.Append("cmd-1", "tick-1\n")
	b.Append("cmd-1", "tick-2\n")
	if got, ok := b.Read("cmd-1"); !ok || got.Text != "tick-1\ntick-2" || got.Truncated {
		t.Fatalf("got %+v ok=%v", got, ok)
	}

	// A chatty command keeps only its tail, starting on a whole line.
	chunk := strings.Repeat("y", 1000) + "\n"
	for range 3 * MaxOutputBytes / len(chunk) {
		b.Append("cmd-1", chunk)
	}
	b.Append("cmd-1", "last\n")
	got, _ := b.Read("cmd-1")
	if !got.Truncated || !strings.HasSuffix(got.Text, "\nlast") || len(got.Text) > MaxOutputBytes {
		t.Fatalf("tail len=%d truncated=%v", len(got.Text), got.Truncated)
	}
	for _, line := range strings.Split(got.Text, "\n") {
		if line != "last" && len(line) != 1000 {
			t.Fatalf("partial line survived the cut: %d bytes", len(line))
		}
	}

	for i := range outputBufferKeys {
		b.Append(fmt.Sprintf("other-%d", i), "x\n")
	}
	// Evicted output says it was dropped rather than reading as none.
	if got, ok := b.Read("cmd-1"); !ok || got.Text != "" || !got.Truncated {
		t.Fatalf("the least recently written command was not evicted: %+v ok=%v", got, ok)
	}
	if _, ok := b.Read(fmt.Sprintf("other-%d", outputBufferKeys-1)); !ok {
		t.Fatal("evicted a recent command")
	}
	b.Append("cmd-1", "again\n")
	if got, _ := b.Read("cmd-1"); got.Text != "again" || !got.Truncated {
		t.Fatalf("output after eviction lost its gap: %+v", got)
	}
}

func TestOutputBufferNeverEvictsAPinnedCommand(t *testing.T) {
	var b OutputBuffer
	b.Append("server", "QUIET-START server on http://localhost:4000\n")
	b.Pin([]string{"server"})
	for i := range 3 * outputBufferKeys {
		b.Append(fmt.Sprintf("ping-%d", i), "Reply from 127.0.0.1\n")
	}
	if got, ok := b.Read("server"); !ok || got.Text != "QUIET-START server on http://localhost:4000" || got.Truncated {
		t.Fatalf("listed terminal's output was evicted: %+v ok=%v", got, ok)
	}
	b.Pin(nil)
	b.Append("ping-new", "x\n")
	if got, _ := b.Read("server"); got.Text != "" || !got.Truncated {
		t.Fatalf("an unpinned command was kept past the bound: %+v", got)
	}
}

func TestOutputBufferMarksMissedStartsAndStoppedStreams(t *testing.T) {
	b := OutputBuffer{MaxDeltas: 3}
	b.MarkDropped("late")
	b.Append("late", "tick-9\n")
	if got, _ := b.Read("late"); got.Text != "tick-9" || !got.Truncated || got.Stale {
		t.Fatalf("late attach: %+v", got)
	}
	for i := range 3 {
		b.Append("flood", fmt.Sprintf("flood-%d\n", i))
	}
	if got, _ := b.Read("flood"); !got.Stale {
		t.Fatalf("stream cap not reported: %+v", got)
	}
	// The completed item's full output replaces the gaps.
	b.Replace("flood", "start\nflood-0\nflood-1\nflood-2\nflood-3\n")
	if got, _ := b.Read("flood"); got.Text != "start\nflood-0\nflood-1\nflood-2\nflood-3" || got.Stale || got.Truncated {
		t.Fatalf("replaced output: %+v", got)
	}
}

func TestTailOutputKeepsConPTYBlanksAndStripsRawEscapes(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		// Measured from Codex under Windows ConPTY.
		{"colored", "A-sgr:\x1b[31m\x1b[1Cred \x1b[32m\x1b[1mbold\x1b[m\x1b[38;5;208m\x1b[1C256", "A-sgr: red bold 256"},
		{"tabs", "tab:\x1b[2CA\x1b[7CBB\x1b[6CCCC", "tab:  A       BB      CCC"},
		{"bounded", "a\x1b[99999999Cb", "a" + strings.Repeat(" ", maxCursorForward) + "b"},
		// Raw files, as Claude Code writes them.
		{"charset", "\x1b(Bplain\x1b)0", "plain"},
		{"two-byte", "\x1b7saved\x1b8\x1b=\x1b>", "saved"},
		{"dcs", "\x1bPq#0;2;0;0;0\x1b\\after", "after"},
		{"8-bit csi", "\u009b31mred\u009b0m", "red"},
		{"unterminated osc", "\x1b]0;title\nnext line", "\nnext line"},
		{"bidi", "abc\u202eevil\u202c zero\u200bwidth \U0001F468\u200d\U0001F469", "abcevil zerowidth \U0001F468\u200d\U0001F469"},
		{"format chars", "a\u2060b\ufeffc\u00add\U000E0041e", "abcde"},
		// A partial repaint skips the cells it leaves unchanged.
		{"partial repaint", "building 10%\r\x1b[9C90%", "building 90%"},
		{"repaint past the end", "ab\r\x1b[4Cc", "ab  c"},
		{"whole redraw", "50%\r100%", "100%"},
	} {
		if got := TailOutput(tc.raw, false); got.Text != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got.Text, tc.want)
		}
	}
}

func TestTailOutputBoundsCursorForwardPerRead(t *testing.T) {
	got := TailOutput(strings.Repeat("\x1b[256C", MaxOutputBytes/6), false)
	if len(got.Text) > MaxOutputBytes || strings.Trim(got.Text, " ") != "" {
		t.Fatalf("len=%d", len(got.Text))
	}
}

func TestOutputBufferReplaceKeepsOnlyTheTail(t *testing.T) {
	var b OutputBuffer
	b.Replace("big", strings.Repeat("x\n", 4*MaxOutputBytes))
	if data := b.entries["big"].data; len(data) != MaxOutputBytes || cap(data) > 2*MaxOutputBytes {
		t.Fatalf("len=%d cap=%d", len(data), cap(data))
	}
}

func TestOutputBufferAppendBoundsAnOversizedDelta(t *testing.T) {
	var b OutputBuffer
	b.Append("big", "start\n")
	b.Append("big", strings.Repeat("x\n", 4*MaxOutputBytes))
	if data := b.entries["big"].data; len(data) != MaxOutputBytes || cap(data) > 2*MaxOutputBytes {
		t.Fatalf("len=%d cap=%d", len(data), cap(data))
	}
	if got, _ := b.Read("big"); !got.Truncated || strings.Contains(got.Text, "start") {
		t.Fatalf("truncated=%v", got.Truncated)
	}
}

func TestOutputBufferReadKeepsAPartialLineWithNothingAfterIt(t *testing.T) {
	var b OutputBuffer
	b.Replace("one", strings.Repeat("x", 2*MaxOutputBytes)+"end\n")
	if got, _ := b.Read("one"); !got.Truncated || !strings.HasSuffix(got.Text, "end") {
		t.Fatalf("len=%d truncated=%v", len(got.Text), got.Truncated)
	}
	b.Replace("two", strings.Repeat("x", 2*MaxOutputBytes)+"\nlast\n")
	if got, _ := b.Read("two"); got.Text != "last" || !got.Truncated {
		t.Fatalf("output=%+v", got)
	}
}

func TestTailOutputKeepsTheEndOfAnOverlongLastLine(t *testing.T) {
	line := "é" + strings.Repeat("x", MaxOutputBytes-2) + "end"
	got := TailOutput("before\n"+line, false)
	if !got.Truncated || !strings.HasSuffix(got.Text, "end") || len(got.Text) > MaxOutputBytes || !utf8.ValidString(got.Text) || strings.Contains(got.Text, "before") {
		t.Fatalf("len=%d truncated=%v", len(got.Text), got.Truncated)
	}
	exact := strings.Repeat("x", MaxOutputBytes)
	if got := TailOutput(exact, false); got.Text != exact || got.Truncated {
		t.Fatalf("a line of exactly the budget: len=%d truncated=%v", len(got.Text), got.Truncated)
	}
}

func TestTrackerKeepsTheCommandAcrossUpdates(t *testing.T) {
	var tracker Tracker
	now := time.Now()
	tracker.Update(ports.ChatBackgroundTask{ID: "s", Kind: "shell", Description: "Dev server", Command: "npm run\x1b dev", State: "running"}, now)
	tracker.Update(ports.ChatBackgroundTask{ID: "s", Kind: "shell", State: "running"}, now)
	tasks := tracker.Snapshot(now)
	if len(tasks) != 1 || tasks[0].Command != "npm run dev" || tasks[0].Description != "Dev server" {
		t.Fatalf("tasks=%+v", tasks)
	}
	// A new run that names no command does not inherit the old one's.
	tracker.Update(ports.ChatBackgroundTask{ID: "r", RunID: "run-1", Kind: "shell", Command: "npm test", State: "running"}, now)
	tracker.Update(ports.ChatBackgroundTask{ID: "r", RunID: "run-2", Kind: "shell", State: "running"}, now)
	for _, task := range tracker.Snapshot(now) {
		if task.ID == "r" && (task.RunID != "run-2" || task.Command != "") {
			t.Fatalf("new run=%+v", task)
		}
	}
}
