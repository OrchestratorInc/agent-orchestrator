package cli

import (
	"bytes"
	"strings"
	"testing"
)

// A failed download on a terminal must not leave the live progress line open:
// the error printed next, including the --json envelope, needs its own line.
func TestProgressWriterAbortEndsLiveLine(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tty     bool
		written bool
		want    string
	}{
		{"tty after progress", true, true, "\r  3 B\n"},
		{"tty before progress", true, false, ""},
		{"not a tty", false, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			pw := &progressWriter{w: &out, tty: tc.tty}
			if tc.written {
				_, _ = pw.Write([]byte("abc"))
			}
			pw.abort()
			if got := out.String(); got != tc.want {
				t.Fatalf("output = %q, want %q", got, tc.want)
			}
			if tc.want != "" && !strings.HasSuffix(out.String(), "\n") {
				t.Fatal("live line left open")
			}
		})
	}
}
