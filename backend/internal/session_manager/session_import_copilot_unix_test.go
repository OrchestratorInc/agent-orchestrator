//go:build !windows

package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestCopilotImportOwnershipRequiresPositiveWritableDescriptor(t *testing.T) {
	bin := t.TempDir()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	rec := domain.SessionRecord{Harness: domain.HarnessCopilot}
	rec.Metadata = domain.SessionMetadata{NativeTranscriptPath: path, ImportSource: &domain.SessionImportSource{NativeID: "native", ConfigDir: filepath.Dir(path), Adopted: true}}
	for _, tc := range []struct {
		name   string
		fields string
		owned  bool
	}{
		{name: "writer", fields: "p123\nccopilot\nf7\naw\n", owned: true},
		{name: "read-write", fields: "p123\nccopilot\nf7\nau\n", owned: true},
		{name: "reader", fields: "p123\nccopilot\nf7\nar\n"},
		{name: "unrelated writer", fields: "p123\nceditor\nf7\naw\n"},
		{name: "no descriptor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := "#!/bin/sh\nprintf '%s' '" + tc.fields + "'\n"
			if err := os.WriteFile(filepath.Join(bin, "lsof"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			if got := importOwnedElsewhere(context.Background(), rec); got != tc.owned {
				t.Fatalf("before adoption owned = %v, want %v", got, tc.owned)
			}
			if got := adoptedOwnedElsewhere(context.Background(), rec); got != tc.owned {
				t.Fatalf("after adoption owned = %v, want %v", got, tc.owned)
			}
		})
	}
}
