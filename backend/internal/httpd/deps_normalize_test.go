package httpd

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/presence"
)

func TestNormalizeAPIDepsDefaultsDeviceLiveToPresence(t *testing.T) {
	tracker := presence.NewTracker()
	tracker.Touch("inst-1")

	deps := APIDeps{Presence: tracker}
	got := normalizeAPIDeps(deps, discardLogger())

	if got.DeviceLive == nil {
		t.Fatal("DeviceLive should default to Presence when left nil")
	}
	live := got.DeviceLive.Live()
	if !live["inst-1"] {
		t.Fatalf("DeviceLive fallback did not read through to the same tracker: %+v", live)
	}
}

func TestNormalizeAPIDepsDoesNotOverrideExplicitDeviceLive(t *testing.T) {
	explicit := fakeLiveSet{"only-explicit": true}
	tracker := presence.NewTracker()
	tracker.Touch("only-presence")

	deps := APIDeps{Presence: tracker, DeviceLive: explicit}
	got := normalizeAPIDeps(deps, discardLogger())

	if _, ok := got.DeviceLive.(fakeLiveSet); !ok {
		t.Fatalf("explicit DeviceLive was overwritten: %#v", got.DeviceLive)
	}
}

func TestNormalizeAPIDepsWarnsWhenRosterHasNoLiveness(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	deps := APIDeps{DeviceRoster: fakeDeviceRoster{}}
	normalizeAPIDeps(deps, log)

	if !strings.Contains(buf.String(), "liveness") {
		t.Fatalf("expected a startup warning about missing liveness, got: %q", buf.String())
	}
}

func TestNormalizeAPIDepsNoWarnWhenPresenceCoversRoster(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	deps := APIDeps{DeviceRoster: fakeDeviceRoster{}, Presence: presence.NewTracker()}
	normalizeAPIDeps(deps, log)

	if strings.Contains(buf.String(), "liveness") {
		t.Fatalf("unexpected liveness warning once Presence covers the roster: %q", buf.String())
	}
}

func TestNormalizeAPIDepsNoWarnWithoutRoster(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	normalizeAPIDeps(APIDeps{}, log)

	if buf.Len() != 0 {
		t.Fatalf("expected no log output without a DeviceRoster, got: %q", buf.String())
	}
}

type fakeLiveSet map[string]bool

func (f fakeLiveSet) Live() map[string]bool { return f }

func (f fakeLiveSet) LastSeen(string) (time.Time, bool) { return time.Time{}, false }

type fakeDeviceRoster struct{}

func (fakeDeviceRoster) List() []mobilebridge.PushDevice { return nil }
func (fakeDeviceRoster) Delete(installID string) error   { return nil }
