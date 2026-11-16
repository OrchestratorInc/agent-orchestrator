//go:build linux

package procmem

import "testing"

func TestParseProcStatStart(t *testing.T) {
	// A command name with spaces and parentheses must not shift the fields.
	stat := "4242 (a (weird) name) S 1 4242 4242 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 987654 1000 10 18446744073709551615"
	if got, ok := parseProcStatStart(stat); !ok || got != "987654" {
		t.Fatalf("start = %q, %v; want 987654", got, ok)
	}
	if _, ok := parseProcStatStart("4243 (gone) Z 1 4243 4243 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 987655 0 0 0"); ok {
		t.Fatal("a zombie has a start time")
	}
}
