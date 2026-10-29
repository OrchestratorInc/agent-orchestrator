package litmus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const prURL = "https://github.com/aoagents/agent-orchestrator/pull/6394"

var ctx = context.Background()

func newSvc(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	svc := New(NewStore(t.TempDir()), func() time.Time { return now }).WithGH(fixtureGH(t))
	svc.runner, svc.retryPause = gitFixture(t), 0
	svc.spawnMarkerWait = time.Millisecond
	is(t, "")(nil, svc.store.markSpawned("s1", spawned{At: now, Repo: "aoagents/agent-orchestrator", PR: 6394}))
	return svc, &now
}

func is(t *testing.T, want string) func(any, error) {
	return func(_ any, err error) {
		t.Helper()
		var e *apierr.Error
		got := ""
		if errors.As(err, &e) {
			got = e.Code
		} else if err != nil {
			got = err.Error()
		}
		if got != want {
			t.Fatalf("err = %v, want code %q", err, want)
		}
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func pathExists(p string) bool { _, err := os.Lstat(p); return err == nil }
