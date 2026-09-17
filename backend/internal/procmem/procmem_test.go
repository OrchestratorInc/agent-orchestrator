package procmem

import "testing"

const table = `
    1     0  1200 systemd
  100     1   900 tmux: server
  200   100  3000 bash
  300   200 1600000 claude
  310   300 410000 go
  320   300 88000 node
  400   100  2500 bash
  500     1 50000 firefox
`

func TestTreeSumsDescendantsOnly(t *testing.T) {
	tbl, err := Parse(table)
	if err != nil {
		t.Fatal(err)
	}
	tree := tbl.Tree(200)
	wantKiB := uint64(3000 + 1600000 + 410000 + 88000)
	if tree.RSSBytes != wantKiB*1024 {
		t.Fatalf("rss = %d, want %d", tree.RSSBytes, wantKiB*1024)
	}
	if len(tree.Processes) != 4 {
		t.Fatalf("processes = %d, want 4", len(tree.Processes))
	}
	if tree.Processes[1].Command != "claude" {
		t.Fatalf("second process = %q, want claude", tree.Processes[1].Command)
	}
}

func TestTreeCountsSharedDescendantsOnce(t *testing.T) {
	tbl, _ := Parse(table)
	if got, want := tbl.Tree(200, 300).RSSBytes, tbl.Tree(200).RSSBytes; got != want {
		t.Fatalf("shared roots rss = %d, want %d", got, want)
	}
}

func TestTreeMissingRootIsEmpty(t *testing.T) {
	tbl, _ := Parse(table)
	if tree := tbl.Tree(9999); tree.RSSBytes != 0 || len(tree.Processes) != 0 {
		t.Fatalf("missing root = %+v, want empty", tree)
	}
}

func TestParseRejectsMalformedRow(t *testing.T) {
	if _, err := Parse("abc def ghi\n"); err == nil {
		t.Fatal("expected parse error")
	}
}
