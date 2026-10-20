package store_test

import (
	"context"
	"testing"
)

// The released launch is written only by its narrow write, only while it is
// the session's current launch, and a full-row update leaves it in place.
func TestSessionRuntimeReleaseNamesTheCurrentLaunchOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "released")
	rec := sampleRecord("released")
	rec.Metadata.RuntimeLaunchID = "launch-1"
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if ok, err := s.SetSessionRuntimeReleased(ctx, session.ID, "launch-0"); err != nil || ok {
		t.Fatalf("release of a launch that is not current: ok=%v err=%v", ok, err)
	}
	if ok, err := s.SetSessionRuntimeReleased(ctx, session.ID, "launch-1"); err != nil || !ok {
		t.Fatalf("release the current launch: ok=%v err=%v", ok, err)
	}
	got, _, err := s.GetSession(ctx, session.ID)
	if err != nil || got.Metadata.RuntimeReleasedLaunchID != "launch-1" {
		t.Fatalf("released launch = %q err=%v, want launch-1", got.Metadata.RuntimeReleasedLaunchID, err)
	}

	got.Metadata.RuntimeReleasedLaunchID = ""
	got.DisplayName = "renamed"
	if err := s.UpdateSession(ctx, got); err != nil {
		t.Fatalf("update session: %v", err)
	}
	after, _, _ := s.GetSession(ctx, session.ID)
	if after.Metadata.RuntimeReleasedLaunchID != "launch-1" {
		t.Fatalf("a full-row update cleared the release: %q", after.Metadata.RuntimeReleasedLaunchID)
	}
}
