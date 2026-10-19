package runfile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func TestTakeoverWaitsThroughUnknownExitObservations(t *testing.T) {
	for _, recovers := range []bool{true, false} {
		name := "recovers"
		timeout := 5 * time.Second
		if !recovers {
			name = "deadline"
			timeout = 250 * time.Millisecond
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "running.json")
			contents := []byte(`{"pid":1234,"port":3001,"processStartId":"saved-owner"}`)
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(contents)
			checks, signals := 0, 0
			check := func(context.Context, int, string) processalive.Ownership {
				checks++
				if checks == 1 {
					return processalive.OwnershipMatching
				}
				if recovers && checks > 2 {
					return processalive.OwnershipGone
				}
				if remaining, err := os.ReadFile(path); err != nil || string(remaining) != string(contents) {
					t.Fatalf("unknown observation changed the owner record: %s; %v", remaining, err)
				}
				return processalive.OwnershipUnknown
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			err := takeover(ctx, path, hex.EncodeToString(digest[:]), check, func(int) error {
				signals++
				return nil
			})
			if recovers && err != nil {
				t.Fatalf("transient exit observation blocked takeover: %v", err)
			}
			if !recovers && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unknown ownership did not wait for the deadline: %v", err)
			}
			if signals != 1 {
				t.Fatalf("signals = %d, want one verified initial signal", signals)
			}
			remaining, readErr := os.ReadFile(path)
			if recovers && !os.IsNotExist(readErr) {
				t.Fatalf("confirmed-gone owner record remains: %v", readErr)
			}
			if !recovers && (readErr != nil || string(remaining) != string(contents)) {
				t.Fatalf("inconclusive takeover removed owner record: %s; %v", remaining, readErr)
			}
		})
	}
}
