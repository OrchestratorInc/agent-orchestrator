package backgroundtasks

import (
	"fmt"
	"strings"
	"testing"
	"time"

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
	if _, ok := b.Read("cmd-1"); ok {
		t.Fatal("the least recently written command was not evicted")
	}
	if _, ok := b.Read(fmt.Sprintf("other-%d", outputBufferKeys-1)); !ok {
		t.Fatal("evicted a recent command")
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
}
