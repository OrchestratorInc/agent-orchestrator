package mobilebridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func TestReapStaleTunnelDoesNotKillSameCommandReusedPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.pid")
	pid := os.Getpid()
	current := processalive.Identify(context.Background(), pid).StartID
	if current == "" {
		t.Fatal("test process identity unavailable")
	}
	if err := WriteTunnelPID(path, pid); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The saved process exited; another cloudflared now holds its numeric PID.
	data = bytes.ReplaceAll(data, []byte(current), []byte("previous-incarnation"))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	killed := false
	if err := ReapStaleTunnel(path, func(int) bool { return true }, func(int) error { killed = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if killed {
		t.Fatal("signalled a same-command process with a different start identity")
	}
}

func TestWriteTunnelPIDCapturesProcessIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.pid")
	if err := WriteTunnelPID(path, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		PID            int    `json:"pid"`
		ProcessStartID string `json:"processStartId"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("PID record has no process identity: %v", err)
	}
	want := processalive.Identify(context.Background(), os.Getpid()).StartID
	if want == "" {
		t.Fatal("test process identity unavailable")
	}
	if record.PID != os.Getpid() || record.ProcessStartID != want {
		t.Fatalf("PID record = %+v, want pid=%d processStartId=%q", record, os.Getpid(), want)
	}
}

func TestReapStaleTunnelOwnership(t *testing.T) {
	pid := os.Getpid()
	startID := processalive.Identify(context.Background(), pid).StartID
	if startID == "" {
		t.Fatal("test process identity unavailable")
	}
	for _, tc := range []struct {
		name       string
		pid        int
		startID    string
		legacy     bool
		wantSignal bool
		wantRecord bool
		wantErr    bool
	}{
		{name: "matching", pid: pid, startID: startID, wantSignal: true},
		{name: "reused same command", pid: pid, startID: "previous-incarnation"},
		{name: "unknown legacy live", pid: pid, legacy: true, wantRecord: true, wantErr: true},
		{name: "unknown missing identity", pid: pid, wantRecord: true, wantErr: true},
		{name: "known dead", pid: 1 << 30, startID: "previous-incarnation"},
		{name: "legacy known dead", pid: 1 << 30, legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tunnel.pid")
			var data []byte
			if tc.legacy {
				data = fmt.Appendf(nil, "%d\n", tc.pid)
			} else {
				data, _ = json.Marshal(map[string]any{"pid": tc.pid, "processStartId": tc.startID})
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			signalled := false
			err := reapStaleTunnel(context.Background(), path, func(ctx context.Context, pid int, startID string) processalive.Ownership {
				if signalled {
					return processalive.OwnershipGone
				}
				return processalive.CheckOwnership(ctx, pid, startID)
			}, func(int) bool { return true }, func(int) error {
				signalled = true
				return nil
			})
			if (err != nil) != tc.wantErr {
				t.Errorf("reap error = %v, want error=%v", err, tc.wantErr)
			}
			if signalled != tc.wantSignal {
				t.Errorf("signalled=%v, want %v", signalled, tc.wantSignal)
			}
			_, statErr := os.Stat(path)
			if exists := statErr == nil; exists != tc.wantRecord {
				t.Errorf("PID record exists=%v, want %v", exists, tc.wantRecord)
			}
		})
	}
}

func TestReapStaleTunnelRequiresConfirmedExitAfterSignal(t *testing.T) {
	for _, outcome := range []processalive.Ownership{processalive.OwnershipGone, processalive.OwnershipReused, processalive.OwnershipUnknown, processalive.OwnershipMatching} {
		t.Run(string(outcome), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tunnel.pid")
			identity := tunnelPIDIdentity{PID: 4242, ProcessStartID: "recorded-start"}
			if err := writeTunnelPIDIdentity(path, identity); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			signalled := false
			err := reapStaleTunnel(ctx, path, func(context.Context, int, string) processalive.Ownership {
				if signalled {
					return outcome
				}
				return processalive.OwnershipMatching
			}, func(int) bool { return true }, func(int) error { signalled = true; return nil })
			wantRetained := outcome == processalive.OwnershipUnknown || outcome == processalive.OwnershipMatching
			if !signalled || (err != nil) != wantRetained {
				t.Fatalf("post-signal %s: signalled=%v error=%v", outcome, signalled, err)
			}
			_, statErr := os.Stat(path)
			if exists := statErr == nil; exists != wantRetained {
				t.Fatalf("post-signal %s: record exists=%v, want %v", outcome, exists, wantRetained)
			}
		})
	}
}

func TestReapStaleTunnelRetainsUnknownCurrentIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.pid")
	identity := tunnelPIDIdentity{PID: os.Getpid(), ProcessStartID: "recorded-start"}
	if err := writeTunnelPIDIdentity(path, identity); err != nil {
		t.Fatal(err)
	}
	err := reapStaleTunnel(context.Background(), path,
		func(_ context.Context, pid int, expected string) processalive.Ownership {
			if pid != identity.PID || expected != identity.ProcessStartID {
				t.Fatalf("ownership query=(%d,%q), want %+v", pid, expected, identity)
			}
			return processalive.OwnershipUnknown
		},
		func(int) bool { t.Fatal("unknown ownership reached command probe"); return false },
		func(int) error { t.Fatal("unknown ownership reached signal"); return nil },
	)
	if err == nil {
		t.Fatal("unknown current identity must block replacement")
	}
	if got, err := readTunnelPIDIdentity(path); err != nil || got != identity {
		t.Fatalf("unknown identity record changed: %+v, %v", got, err)
	}
}

func TestReapStaleTunnelRechecksOwnershipAfterCommandProbe(t *testing.T) {
	for _, outcome := range []processalive.Ownership{processalive.OwnershipReused, processalive.OwnershipUnknown} {
		t.Run(string(outcome), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tunnel.pid")
			identity := tunnelPIDIdentity{PID: 4242, ProcessStartID: "recorded-start"}
			if err := writeTunnelPIDIdentity(path, identity); err != nil {
				t.Fatal(err)
			}
			current := processalive.OwnershipMatching
			err := reapStaleTunnel(context.Background(), path,
				func(context.Context, int, string) processalive.Ownership { return current },
				func(int) bool { current = outcome; return true },
				func(int) error { t.Fatal("changed ownership reached signal"); return nil },
			)
			if err == nil {
				t.Fatal("changed ownership must block replacement")
			}
			if got, err := readTunnelPIDIdentity(path); err != nil || got != identity {
				t.Fatalf("changed identity record lost: %+v, %v", got, err)
			}
		})
	}
}

func TestTunnelPIDReleaseKeepsSamePIDSuccessor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.pid")
	old := tunnelPIDIdentity{PID: 4242, ProcessStartID: "old-start"}
	next := tunnelPIDIdentity{PID: 4242, ProcessStartID: "new-start"}
	a, b := newTunnelPIDOwner(path), newTunnelPIDOwner(path)
	if err := a.claim(old); err != nil {
		t.Fatal(err)
	}
	if err := b.claim(next); err != nil {
		t.Fatal(err)
	}
	if err := a.release(old); err != nil {
		t.Fatal(err)
	}
	if got, err := readTunnelPIDIdentity(path); err != nil || got != next {
		t.Fatalf("successor record changed: %+v, %v", got, err)
	}
}

func TestReapStaleTunnelSerializesSuccessorClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.pid")
	old := tunnelPIDIdentity{PID: 4242, ProcessStartID: "old-start"}
	next := tunnelPIDIdentity{PID: 4242, ProcessStartID: "new-start"}
	owner := newTunnelPIDOwner(path)
	if err := owner.claim(old); err != nil {
		t.Fatal(err)
	}
	checking, resume := make(chan struct{}), make(chan struct{})
	reaped := make(chan error, 1)
	go func() {
		reaped <- reapStaleTunnel(context.Background(), path,
			func(context.Context, int, string) processalive.Ownership {
				close(checking)
				<-resume
				return processalive.OwnershipGone
			}, nil, nil,
		)
	}()
	<-checking
	claimed := make(chan error, 1)
	go func() { claimed <- owner.claim(next) }()
	select {
	case err := <-claimed:
		close(resume)
		<-reaped
		t.Fatalf("successor claim crossed reaping lock: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(resume)
	if err := <-reaped; err != nil {
		t.Fatal(err)
	}
	if err := <-claimed; err != nil {
		t.Fatal(err)
	}
	if got, err := readTunnelPIDIdentity(path); err != nil || got != next {
		t.Fatalf("successor record changed: %+v, %v", got, err)
	}
}
