//go:build !windows

package mobilebridge

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// The task-owned executable behaves like cloudflared without making a network
// connection, so the real runner, OS identity probe and stale reaper can meet.
func TestMain(m *testing.M) {
	if os.Getenv("AO_TEST_MOBILE_TUNNEL_HELPER") == "1" && len(os.Args) > 1 && os.Args[1] == "tunnel" {
		if os.Getenv("AO_TEST_MOBILE_TUNNEL_IGNORE_TERM") == "1" {
			signal.Ignore(syscall.SIGTERM)
		}
		fmt.Fprintln(os.Stderr, cloudflaredStartup)
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestTunnelRunnerAndReaperPreserveProcessOwnership(t *testing.T) {
	for _, mode := range []string{"matching", "reused", "legacy", "gone", "ignores SIGTERM"} {
		t.Run(mode, func(t *testing.T) {
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			fixture := filepath.Join(dir, "cloudflared")
			if err := os.Link(binary, fixture); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AO_TEST_MOBILE_TUNNEL_HELPER", "1")
			if mode == "ignores SIGTERM" {
				t.Setenv("AO_TEST_MOBILE_TUNNEL_IGNORE_TERM", "1")
			}
			runner := &TunnelRunner{
				Binary: fixture, LocalPort: 3002, PIDPath: filepath.Join(dir, "tunnel.pid"),
				Runtime: &TunnelRuntime{},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			done := make(chan error, 1)
			go func() { done <- runner.runOnce(ctx) }()
			stopped := false
			defer func() {
				cancel()
				if !stopped {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("task-owned tunnel fixture did not stop")
					}
				}
			}()
			var identity tunnelPIDIdentity
			for {
				identity, err = readTunnelPIDIdentity(runner.PIDPath)
				if err == nil && runner.Runtime.Snapshot().Hostname != "" {
					break
				}
				select {
				case runErr := <-done:
					stopped = true
					t.Fatalf("runner exited before recording fixture: %v", runErr)
				case <-ctx.Done():
					t.Fatal("runner did not record fixture identity")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if identity.ProcessStartID == "" || processalive.Identify(ctx, identity.PID).StartID != identity.ProcessStartID {
				t.Fatalf("runner recorded no matching start identity: %+v", identity)
			}
			if !IsLiveCloudflared(identity.PID) {
				t.Fatal("task-owned fixture command was not identified as cloudflared")
			}
			switch mode {
			case "reused":
				stale := identity
				stale.ProcessStartID = "previous-incarnation"
				if err := writeTunnelPIDIdentity(runner.PIDPath, stale); err != nil {
					t.Fatal(err)
				}
			case "legacy":
				if err := os.WriteFile(runner.PIDPath, fmt.Appendf(nil, "%d\n", identity.PID), 0o600); err != nil {
					t.Fatal(err)
				}
			case "gone":
				cancel()
				<-done
				stopped = true
				if err := writeTunnelPIDIdentity(runner.PIDPath, identity); err != nil {
					t.Fatal(err)
				}
			}
			err = ReapStaleTunnel(runner.PIDPath, IsLiveCloudflared, KillProcess)
			wantRetained := mode == "legacy" || mode == "ignores SIGTERM"
			if (err != nil) != wantRetained {
				t.Fatalf("reap %s: %v", mode, err)
			}
			if mode == "reused" || wantRetained {
				if got := processalive.CheckOwnership(context.Background(), identity.PID, identity.ProcessStartID); got != processalive.OwnershipMatching {
					t.Fatalf("reaper harmed %s fixture: ownership=%s", mode, got)
				}
			} else if mode == "matching" {
				select {
				case <-done:
					stopped = true
				case <-ctx.Done():
					t.Fatal("matching stale fixture was not terminated")
				}
			}
			_, statErr := os.Stat(runner.PIDPath)
			if exists := statErr == nil; exists != wantRetained {
				t.Fatalf("PID record exists=%v after %s reaping", exists, mode)
			}
		})
	}
}
