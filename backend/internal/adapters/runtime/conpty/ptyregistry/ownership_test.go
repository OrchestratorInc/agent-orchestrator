package ptyregistry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func TestScanDistinguishesReusedAndUnknownProcessOwnership(t *testing.T) {
	startID := processalive.Identify(context.Background(), os.Getpid()).StartID
	if startID == "" {
		t.Fatal("test process identity unavailable")
	}
	for _, tc := range []struct {
		name    string
		startID string
		want    int
	}{
		{name: "matching", startID: startID, want: 1},
		{name: "reused", startID: "different-process-incarnation", want: 0},
		{name: "missing identity", want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := setupHome(t)
			data, err := json.Marshal([]map[string]any{{
				"sessionId": "host", "ptyHostPid": os.Getpid(), "pipePath": "127.0.0.1:1",
				"launchId": "launch", "processStartId": tc.startID,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			entries, complete, err := Scan(context.Background())
			if err != nil || !complete || len(entries) != tc.want {
				t.Fatalf("Scan = %+v, complete=%v, err=%v; want %d entries", entries, complete, err, tc.want)
			}
		})
	}
}

func TestScanRetainsUnavailableModernIdentity(t *testing.T) {
	setupHome(t)
	original := checkOwnership
	checkOwnership = func(context.Context, int, string) processalive.Ownership { return processalive.OwnershipUnknown }
	t.Cleanup(func() { checkOwnership = original })
	entry := Entry{SessionID: "unknown", PtyHostPID: os.Getpid(), ProcessStartID: "captured-start", PipePath: "127.0.0.1:1"}
	if err := Register(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	entries, complete, err := Scan(context.Background())
	if err != nil || !complete || len(entries) != 1 || entries[0] != entry {
		t.Fatalf("Scan unknown = %+v complete=%v err=%v, want retained entry", entries, complete, err)
	}
}

func TestUnregisterOwnedPreservesSuccessor(t *testing.T) {
	setupHome(t)
	startID := processalive.Identify(context.Background(), os.Getpid()).StartID
	if startID == "" {
		t.Fatal("test process identity unavailable")
	}
	old := Entry{SessionID: "same-session", PtyHostPID: os.Getpid(), ProcessStartID: startID, PipePath: "127.0.0.1:1", LaunchID: "old"}
	next := old
	next.LaunchID = "next"
	if err := Register(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if err := UnregisterOwned(context.Background(), old); err == nil {
		t.Fatal("UnregisterOwned silently accepted changed generation")
	}
	entries, _, err := Scan(context.Background())
	if err != nil || len(entries) != 1 || entries[0] != next {
		t.Fatalf("successor after old unregister = %+v, %v", entries, err)
	}
	if err := UnregisterOwned(context.Background(), next); err != nil {
		t.Fatalf("UnregisterOwned exact successor = %v", err)
	}
	entries, _, err = Scan(context.Background())
	if err != nil || len(entries) != 0 {
		t.Fatalf("registry after exact unregister = %+v, %v", entries, err)
	}
}

func TestUnregisterOwnedLaunchReservationRequiresExactGeneration(t *testing.T) {
	for _, generation := range []string{"same-launch", "different-launch", ""} {
		t.Run(generation, func(t *testing.T) {
			setupHome(t)
			reservation := Entry{SessionID: "pending", PtyHostPID: 0, PipePath: UnresolvedPipePath, LaunchID: generation}
			if err := Register(context.Background(), reservation); err != nil {
				t.Fatal(err)
			}
			expected := Entry{
				SessionID: "pending", PtyHostPID: os.Getpid(), ProcessStartID: "captured-start",
				PipePath: "127.0.0.1:1", LaunchID: "same-launch",
			}
			err := UnregisterOwned(context.Background(), expected)
			if generation == "same-launch" {
				if err != nil {
					t.Fatalf("remove own prelaunch reservation: %v", err)
				}
			} else if err == nil {
				t.Fatal("removed reservation without matching generation")
			}
			entries, _, scanErr := Scan(context.Background())
			want := 1
			if generation == "same-launch" {
				want = 0
			}
			if scanErr != nil || len(entries) != want {
				t.Fatalf("remaining reservations = %+v, %v; want %d", entries, scanErr, want)
			}
		})
	}
}
