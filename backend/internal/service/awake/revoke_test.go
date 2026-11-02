package awake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type revokingHost struct {
	fakeHost
	entered       chan struct{}
	release       chan struct{}
	recoveryError error
	recovered     bool
}

func (h *revokingHost) AuthorizeLowPower(context.Context) error {
	return errors.New("unexpected concurrent authorization")
}
func (h *revokingHost) RequestRecovery() error { h.recovered = true; return h.recoveryError }
func (h *revokingHost) RevokeLowPower(ctx context.Context) error {
	close(h.entered)
	select {
	case <-h.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func revokingFixture(t *testing.T) (*Service, *memoryStore, *revokingHost) {
	t.Helper()
	s, store, h, _, _ := fixture(t)
	host := &revokingHost{fakeHost: *h, entered: make(chan struct{}), release: make(chan struct{})}
	s.host = host
	s.doc.Settings.LowPowerBatteryPercent = 30
	s.doc.Settings.LowPowerOnSeriousHeat = true
	return s, store, host
}
func TestLowPowerRevocationDisablesPolicyWithoutBlockingStatus(t *testing.T) {
	s, store, h := revokingFixture(t)
	start(t, s, domain.StartAwakeRequest{Mode: "indefinite"})
	done := make(chan error, 1)
	go func() { done <- s.RevokeLowPower(context.Background()) }()
	<-h.entered
	state := make(chan domain.AwakeState, 1)
	go func() { state <- s.Status() }()
	select {
	case status := <-state:
		if !status.Active || status.Settings.LowPowerBatteryPercent != 0 || status.Settings.LowPowerOnSeriousHeat {
			t.Fatal("revocation did not disable power policy while preserving awake ownership")
		}
	case <-time.After(time.Second):
		t.Fatal("revocation dialog holds policy/status lock")
	}
	if !h.recovered || store.writes < 2 {
		t.Fatal("revocation preceded durable disabling/recovery")
	}
	settings := s.Status().Settings
	settings.LowPowerOnSeriousHeat = true
	if err := s.UpdateSettings(context.Background(), settings); !errors.Is(err, ErrValidation) {
		t.Fatal("pending revocation permitted re-enabling automation")
	}
	if err := s.RevokeLowPower(context.Background()); !errors.Is(err, ErrValidation) {
		t.Fatal("concurrent power action admitted")
	}
	if err := s.AuthorizeLowPower(context.Background()); !errors.Is(err, ErrValidation) {
		t.Fatal("pending revocation admitted a concurrent authorization")
	}
	close(h.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	next, err := New(context.Background(), store, &fakeHost{}, &fakeBindings{})
	if err != nil {
		t.Fatal(err)
	}
	if next.Status().Settings.LowPowerBatteryPercent != 0 || next.Status().Settings.LowPowerOnSeriousHeat {
		t.Fatal("revoked automation returned after restart")
	}
}
func TestLowPowerRevocationFailuresRemainSafe(t *testing.T) {
	t.Run("save", func(t *testing.T) {
		s, store, h := revokingFixture(t)
		store.fail = true
		if err := s.RevokeLowPower(context.Background()); err == nil {
			t.Fatal("failed disable save ignored")
		}
		if h.recovered || s.Status().Settings.LowPowerBatteryPercent != 30 {
			t.Fatal("save failure changed policy or attempted removal")
		}
	})
	t.Run("recovery", func(t *testing.T) {
		s, _, h := revokingFixture(t)
		h.recoveryError = errors.New("recovery unavailable")
		if err := s.RevokeLowPower(context.Background()); !errors.Is(err, h.recoveryError) {
			t.Fatal("recovery failure ignored")
		}
		select {
		case <-h.entered:
			t.Fatal("removal attempted before recovery")
		default:
		}
	})
	t.Run("cancel", func(t *testing.T) {
		s, _, h := revokingFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- s.RevokeLowPower(ctx) }()
		<-h.entered
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error: %v", err)
		}
		if !h.recovered || s.Status().Settings.LowPowerBatteryPercent != 0 || s.Status().Settings.LowPowerOnSeriousHeat {
			t.Fatal("cancellation silently re-enabled automation")
		}
	})
}

type pendingPowerHost struct{ *deferredCapableHost }

func (h *pendingPowerHost) RecoveryReason() string { return "low_power_revocation_pending" }
func (h *pendingPowerHost) Capabilities() []domain.AwakeCapability {
	return append(h.deferredCapableHost.Capabilities(), domain.AwakeCapability{ID: "low_power_authorization", Available: true}, domain.AwakeCapability{ID: "low_power_revocation", Available: true})
}
func TestInterruptedRevocationDoesNotAdvertisePowerAuthorization(t *testing.T) {
	s, _, h, _, _ := fixture(t)
	s.host = &pendingPowerHost{&deferredCapableHost{h}}
	found := map[string]bool{}
	for _, cap := range s.Status().Capabilities {
		found[cap.ID] = true
		if (cap.ID == "low_power" || cap.ID == "low_power_authorization") && cap.Available {
			t.Fatal("interrupted revocation advertised an available power operation")
		}
		if cap.ID == "low_power_revocation" && !cap.Available {
			t.Fatal("interrupted revocation disabled its own retry")
		}
	}
	if !found["low_power"] || !found["low_power_authorization"] || !found["low_power_revocation"] {
		t.Fatal("power capability fixture is incomplete")
	}
}
