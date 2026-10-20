package procinventory

import (
	"fmt"
	"strings"
	"testing"
)

// psRow renders an Entry as one `ps -ww -axo pid=,ppid=,pgid=,rss=,lstart=,args=`
// row (Fields-normalized, as ParseTable produces and consumes).
func psRow(e Entry) string {
	lstart := e.Lstart
	if lstart == "" {
		lstart = "Mon Sep 14 00:13:02 2026"
	}
	return fmt.Sprintf("%d %d %d %d %s %s", e.PID, e.PPID, e.PGID, e.RSSKB, lstart, e.Command)
}

func psTable(entries []Entry) string {
	rows := make([]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, psRow(e))
	}
	return strings.Join(rows, "\n")
}

func TestParseTable(t *testing.T) {
	table := psTable([]Entry{
		{PID: 100, PPID: 1, PGID: 100, RSSKB: 2048, Command: "/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao pty-host web-api-3 /Users/x/My Dir -- claude"},
		{PID: 4001, PPID: 1, PGID: 4001, RSSKB: 92160, Command: "/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao daemon"},
	})
	entries, err := ParseTable(table)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	root := entries[0]
	if root.PID != 100 || root.PPID != 1 || root.PGID != 100 {
		t.Fatalf("root ids = %+v", root)
	}
	if root.RSSKB != 2048 || root.RSSBytes() != 2048*1024 {
		t.Fatalf("rss = %d KB", root.RSSKB)
	}
	if root.Lstart != "Mon Sep 14 00:13:02 2026" {
		t.Fatalf("lstart = %q", root.Lstart)
	}
	// The cwd contains a space; the command must be the re-joined tail.
	if !strings.Contains(root.Command, "/Users/x/My Dir -- claude") {
		t.Fatalf("command = %q", root.Command)
	}
}

func TestParseTableEmptyAndMalformed(t *testing.T) {
	if entries, err := ParseTable("   \n  "); err != nil || entries != nil {
		t.Fatalf("empty table: entries=%v err=%v", entries, err)
	}
	if _, err := ParseTable("100 1 100 2048 Mon Sep 14"); err == nil {
		t.Fatal("row with no command accepted")
	}
	if _, err := ParseTable("abc 1 100 2048 Mon Sep 14 00:13:02 2026 cmd"); err == nil {
		t.Fatal("non-numeric pid accepted")
	}
}

func TestDescendants(t *testing.T) {
	entries := []Entry{
		{PID: 1, PPID: 0},
		{PID: 10, PPID: 1},
		{PID: 11, PPID: 10},
		{PID: 12, PPID: 11},
		{PID: 20, PPID: 1},
	}
	descendants := Descendants(entries, 10)
	if !descendants[10] || !descendants[11] || !descendants[12] {
		t.Fatalf("descendants of 10 = %v", descendants)
	}
	if descendants[20] || descendants[1] {
		t.Fatalf("descendants leaked outside the tree: %v", descendants)
	}
}
