//go:build linux

package persistenthost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func TestHostOwnershipWithoutLinuxWallClock(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	identity := processalive.Identify(context.Background(), os.Getpid())
	if identity.StartID == "" || !identity.StartedAt.IsZero() {
		t.Fatalf("identity without ps = %+v, want start identity without wall-clock time", identity)
	}

	for _, tc := range []struct {
		name    string
		startID string
		want    error
	}{
		{"legacy descriptor", "", ErrOwnershipInconclusive},
		{"modern stale descriptor", "previous-process-incarnation", ErrNotRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{SessionID: "unknown-wall-clock", DataDir: t.TempDir(), ReconnectOnly: true}
			if err := writeDescriptor(cfg.DataDir, Descriptor{
				Version: ProtocolVersion, SessionID: cfg.SessionID, Address: closedHostAddress(t),
				Token: "stale", PID: os.Getpid(), ProcessStartID: tc.startID, StartedAt: time.Unix(0, 0).UTC(),
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := ConnectOrStart(context.Background(), cfg); !errors.Is(err, tc.want) {
				t.Fatalf("reconnect without ps = %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("legacy lock", func(t *testing.T) {
		dataDir := t.TempDir()
		const sessionID = "unknown-wall-clock-lock"
		dir, err := hostDir(dataDir, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path, err := lockPath(dataDir, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, time.Unix(0, 0), time.Unix(0, 0)); err != nil {
			t.Fatal(err)
		}
		release, err := acquireHostLock(dataDir, sessionID)
		if release != nil {
			defer release()
		}
		if !errors.Is(err, ErrHostExists) {
			t.Fatalf("acquire legacy lock without ps = %v, want ErrHostExists", err)
		}
	})
}
