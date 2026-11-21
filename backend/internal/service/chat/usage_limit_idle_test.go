package chat_test

import (
	"context"
	"testing"
)

// The daemon's resume loop scans rarely while AutoResumeIdle holds, so arming
// a turn must clear it and wake the loop, and the next scan must keep it clear
// until the armed turn is consumed.
func TestUsageLimitAutoResumeIdleUntilArmed(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	armed := h.svc.AutoResumeArmed()
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if !h.svc.AutoResumeIdle() {
		t.Fatal("a scan with no armed turn should leave the loop idle")
	}

	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-armed:
	default:
		t.Fatal("arming a turn did not wake the resume loop")
	}
	if h.svc.AutoResumeIdle() {
		t.Fatal("an armed turn must keep the loop on its one-second scan")
	}
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if h.svc.AutoResumeIdle() {
		t.Fatal("a scan that finds the armed turn must not go idle")
	}
}
