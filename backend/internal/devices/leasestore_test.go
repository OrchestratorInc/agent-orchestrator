package devices

import (
	"context"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

// A daemon restart (an app update, a crash) keeps every session's device:
// the session still holds it, nobody else is handed it, and a released or
// ended session's lease does not come back.
func TestLeasesSurviveADaemonRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	ended := map[domain.SessionID]bool{}
	sessions := func(_ context.Context, id domain.SessionID) (SessionInfo, error) {
		return SessionInfo{Terminated: ended[id]}, nil
	}
	start := func(p *fakeProvider) *Service {
		svc := newAvailable(Config{DataDir: dir, Provider: p, Sessions: sessions, Now: func() time.Time { return time.Unix(1, 0) }})
		if _, err := svc.Setup(ctx); err != nil {
			t.Fatal(err)
		}
		return svc
	}

	first := start(iosFleet())
	held, err := first.Open(ctx, "s-1", OpenRequest{DeviceID: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Open(ctx, "s-2", OpenRequest{DeviceID: "C"}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(ctx, "s-2", false); err != nil {
		t.Fatal(err)
	}

	second := start(iosFleet())
	got, ok := second.Lease("s-1")
	if !ok || got.Device.ID != held.Device.ID || got.AgentControl.Command != held.AgentControl.Command {
		t.Fatalf("after restart s-1 holds %+v (ok=%v), want %s with its bound command", got, ok, held.Device.ID)
	}
	if _, ok := second.Lease("s-2"); ok {
		t.Fatal("a released lease came back after the restart")
	}
	if _, err := second.Open(ctx, "s-3", OpenRequest{DeviceID: "A"}); apiCode(err) != "DEVICE_IN_USE" {
		t.Fatalf("another session took s-1's device after the restart: err = %v", err)
	}
	// A device this service booted is still its to reuse: an unnamed open takes
	// a free device rather than s-1's.
	if l, err := second.Open(ctx, "s-3", OpenRequest{}); err != nil || l.Device.ID == "A" {
		t.Fatalf("unnamed open after restart = %+v, %v", l, err)
	}

	ended["s-1"] = true
	third := start(iosFleet())
	third.Status(ctx)
	if _, ok := third.Lease("s-1"); ok {
		t.Fatal("an ended session's restored lease was kept")
	}
}

func TestUnreadableLeaseRecordStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := writeFileAtomic(leasesPath(dir), "not a lease file"); err != nil {
		t.Fatal(err)
	}
	leases, owned := loadLeases(dir)
	if len(leases) != 0 || len(owned) != 0 {
		t.Fatalf("leases=%v owned=%v from a corrupt file", leases, owned)
	}
}
