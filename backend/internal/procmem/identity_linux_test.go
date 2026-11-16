//go:build linux

package procmem

import "testing"

// Parent and start time come from one /proc read; a command name with spaces
// or parentheses must not shift the fields, and a zombie is not alive.
func TestParseProcStat(t *testing.T) {
	stat := "4242 (a (weird) name) S 17 4242 4242 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 987654 1000 10 18446744073709551615"
	if id, alive, ok := parseProcStat(stat); !ok || !alive || id.PPID != 17 || id.Start != "987654" {
		t.Fatalf("parse = %+v alive=%v ok=%v; want ppid 17 start 987654", id, alive, ok)
	}
	if _, alive, ok := parseProcStat("4243 (gone) Z 1 4243 4243 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 987655 0 0 0"); !ok || alive {
		t.Fatal("a zombie is alive")
	}
}
