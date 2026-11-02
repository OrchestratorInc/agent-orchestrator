package sessionmanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestVibeImportLease(t *testing.T) {
	root := t.TempDir()
	id := "synthetic-session"
	dir := filepath.Join(root, "logs", "session", "active")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{}
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: id, ConfigDir: root}
	path := filepath.Join(dir, id+".lock.json")
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{name: "live", body: fmt.Sprintf(`{"session_id":%q,"process_id":%d}`, id, os.Getpid()), want: true},
		{name: "other id", body: fmt.Sprintf(`{"session_id":"other","process_id":%d}`, os.Getpid())},
		{name: "invalid pid", body: `{"session_id":"synthetic-session","process_id":0}`},
		{name: "torn", body: `{"session_id":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := vibeImportOwnedElsewhere(context.Background(), rec); got != tc.want {
				t.Fatalf("owned = %v", got)
			}
		})
	}
}
