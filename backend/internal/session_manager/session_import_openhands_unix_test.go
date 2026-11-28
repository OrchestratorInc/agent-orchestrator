//go:build !windows

package sessionmanager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestOpenHandsWritablePythonDescriptorRequiresEntrypoint(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for _, tc := range []struct {
		name    string
		command string
		owned   bool
	}{
		{"unrelated Python", "python3 unrelated.py", false},
		{"native console script", "python3 /venv/bin/openhands --resume native", true},
		{"native module", "python3 -m openhands_cli --resume native", true},
		{"look-alike module", "python3 -m openhands_cli_other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(bin, "lsof"), []byte("#!/bin/sh\nprintf 'p123\\ncpython3\\nf7\\naw\\n'\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "ps"), []byte("#!/bin/sh\nprintf '%s\\n' '"+tc.command+"'\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			rec := domain.SessionRecord{Harness: domain.HarnessOpenHands}
			rec.Metadata.NativeTranscriptPath = filepath.Join(t.TempDir(), "base_state.json")
			rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "native", ConfigDir: filepath.Dir(rec.Metadata.NativeTranscriptPath)}
			if got := importOwnedElsewhere(t.Context(), rec); got != tc.owned {
				t.Fatalf("ownership = %v, want %v", got, tc.owned)
			}
			if got := adoptedOwnedElsewhere(t.Context(), rec); got != tc.owned {
				t.Fatalf("adopted ownership = %v, want %v", got, tc.owned)
			}
		})
	}
}
