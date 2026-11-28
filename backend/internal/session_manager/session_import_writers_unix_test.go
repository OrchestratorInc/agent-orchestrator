//go:build !windows

package sessionmanager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSharedImportWriterNamesDoNotClaimUnrelatedProcesses(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "lsof"), []byte("#!/bin/sh\nprintf '%s' \"$AO_TEST_WRITER_OUTPUT\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	for _, tc := range []struct {
		name, command, access string
		owned                 bool
	}{
		{"unrelated Python", "python3", "w", false},
		{"FX substring", "prefixfxsuffix", "w", false},
		{"FX native", "fx", "w", true},
		{"FX executable", "fx.exe", "u", true},
		{"FX read only", "fx", "r", false},
		{"existing Node writer", "node", "w", true},
		{"existing Claude writer", "claude", "w", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_TEST_WRITER_OUTPUT", "p123\nc"+tc.command+"\nf7\na"+tc.access+"\n")
			if got := writableTranscriptOwner(t.Context(), filepath.Join(t.TempDir(), "transcript")); got != tc.owned {
				t.Fatalf("writer=%v, want %v", got, tc.owned)
			}
		})
	}
}
