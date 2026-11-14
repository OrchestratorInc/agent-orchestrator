package settings

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type hibernationSettingsStore struct{ err error }

func (s *hibernationSettingsStore) GetAppSettings(context.Context) (Snapshot, error) {
	return Snapshot{DefaultSessionMode: domain.SessionModeChat}, s.err
}
func (*hibernationSettingsStore) SetDefaultSessionMode(context.Context, domain.SessionMode, time.Time) error {
	return nil
}
func (*hibernationSettingsStore) SetVersionRefresh(context.Context, bool, time.Time) error {
	return nil
}
func (*hibernationSettingsStore) SetComputerUse(context.Context, bool, time.Time) error {
	return nil
}
func (*hibernationSettingsStore) SetSessionRetentionDays(context.Context, int, time.Time) error {
	return nil
}
func (*hibernationSettingsStore) SetAccountHandoff(context.Context, bool, time.Time) error {
	return nil
}

func TestChatHibernationGateResetsOnDaemonBoot(t *testing.T) {
	ctx := context.Background()
	store := &hibernationSettingsStore{}
	first := New(store, nil, Offering{}, nil)
	if first.ChatHibernationEnabled() {
		t.Fatal("new daemon enabled chat hibernation")
	}
	snapshot, err := first.SetChatHibernationEnabled(ctx, true)
	if err != nil || !snapshot.ChatHibernationEnabled || !first.ChatHibernationEnabled() {
		t.Fatalf("enable chat hibernation: snapshot=%+v err=%v", snapshot, err)
	}
	second := New(store, nil, Offering{}, nil)
	snapshot, err = second.Get(ctx)
	if err != nil || snapshot.ChatHibernationEnabled || second.ChatHibernationEnabled() {
		t.Fatalf("restarted daemon retained chat hibernation: snapshot=%+v err=%v", snapshot, err)
	}
}

func TestDisablingChatHibernationSurvivesSettingsReadFailure(t *testing.T) {
	ctx := context.Background()
	store := &hibernationSettingsStore{}
	svc := New(store, nil, Offering{}, nil)
	if _, err := svc.SetChatHibernationEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	store.err = errors.New("database unavailable")
	if _, err := svc.SetChatHibernationEnabled(ctx, false); err == nil || svc.ChatHibernationEnabled() {
		t.Fatalf("disable after read error = %v, enabled=%v", err, svc.ChatHibernationEnabled())
	}
}

func TestOfferingFromConfigCarriesTrackerIntake(t *testing.T) {
	for _, on := range []bool{true, false} {
		got := OfferingFromConfig(config.Config{TrackerIntake: on})
		if got.TrackerIntakeEnabled != on {
			t.Errorf("OfferingFromConfig(TrackerIntake=%v).TrackerIntakeEnabled = %v, want %v", on, got.TrackerIntakeEnabled, on)
		}
	}
}

// Cloud is on for everyone with a configured control plane. Only the
// AO_CLOUD_OFFERING escape hatch or a missing control-plane URL turns it off.
func TestOfferingCloudEnabled(t *testing.T) {
	const url = "https://cp.example.com"
	cases := []struct {
		name     string
		offering bool // AO_CLOUD_OFFERING, on unless switched off
		cpURL    string
		want     bool
	}{
		{"default on with the baked url", true, url, true},
		{"switched off by the env", false, url, false},
		{"no control plane", true, "", false},
		{"all off", false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			offering := Offering{CloudOffering: tc.offering, CloudControlPlaneURL: tc.cpURL}
			if got := offering.CloudEnabled(); got != tc.want {
				t.Errorf("CloudEnabled(offering=%v, url=%q) = %v, want %v", tc.offering, tc.cpURL, got, tc.want)
			}
		})
	}
}

func (*hibernationSettingsStore) SetHarnessDefault(context.Context, domain.AgentHarness, domain.HarnessDefault, time.Time) error {
	return nil
}

type versionRefreshStore struct {
	hibernationSettingsStore
	refresh bool
	reads   int
}

func (s *versionRefreshStore) GetAppSettings(context.Context) (Snapshot, error) {
	s.reads++
	return Snapshot{VersionRefresh: s.refresh}, nil
}

func (s *versionRefreshStore) SetVersionRefresh(_ context.Context, enabled bool, _ time.Time) error {
	s.refresh = enabled
	return nil
}

func TestVersionRefreshToggleIsReadOnceAndCached(t *testing.T) {
	ctx := context.Background()
	store := &versionRefreshStore{refresh: true}
	svc := New(store, nil, Offering{}, nil)
	first := svc.VersionRefreshEnabled(ctx)
	if second := svc.VersionRefreshEnabled(ctx); !first || !second || store.reads != 1 {
		t.Fatalf("enabled reads = %d, want one cached read", store.reads)
	}
	snapshot, err := svc.SetVersionRefresh(ctx, false)
	if err != nil || snapshot.VersionRefresh || svc.VersionRefreshEnabled(ctx) {
		t.Fatalf("turn off: snapshot=%+v err=%v enabled=%t", snapshot, err, svc.VersionRefreshEnabled(ctx))
	}
}

type computerUseStore struct {
	hibernationSettingsStore
	enabled bool
	err     error
}

func (s *computerUseStore) GetAppSettings(context.Context) (Snapshot, error) {
	return Snapshot{ComputerUse: s.enabled}, s.err
}

func (s *computerUseStore) SetComputerUse(_ context.Context, enabled bool, _ time.Time) error {
	s.enabled = enabled
	return nil
}

// The toggle is read at every spawn, so a change applies to the next session
// without a daemon restart, and an unreadable store never turns it on.
func TestComputerUseToggle(t *testing.T) {
	ctx := context.Background()
	store := &computerUseStore{}
	svc := New(store, nil, Offering{}, nil)
	if svc.ComputerUseEnabled(ctx) {
		t.Fatal("computer use is on by default")
	}
	snapshot, err := svc.SetComputerUse(ctx, true)
	if err != nil || !snapshot.ComputerUse || !svc.ComputerUseEnabled(ctx) {
		t.Fatalf("after enabling: snapshot=%+v err=%v enabled=%v", snapshot, err, svc.ComputerUseEnabled(ctx))
	}
	store.err = errors.New("database is locked")
	if svc.ComputerUseEnabled(ctx) {
		t.Fatal("an unreadable store must count as off")
	}
}

// Turning computer use on needs Cua Driver installed, whatever client asks;
// turning it off always works.
func TestComputerUseNeedsCuaDriverToTurnOn(t *testing.T) {
	ctx := context.Background()
	store := &computerUseStore{}
	svc := New(store, nil, Offering{}, nil)
	installed, checkErr := false, error(nil)
	svc.SetCuaDriverInstalled(func(context.Context) (bool, error) { return installed, checkErr })

	_, err := svc.SetComputerUse(ctx, true)
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_NOT_INSTALLED" || apiErr.Kind != apierr.KindConflict {
		t.Fatalf("enable without Cua Driver: err = %v, want COMPUTER_USE_NOT_INSTALLED", err)
	}
	if svc.ComputerUseEnabled(ctx) {
		t.Fatal("computer use turned on without Cua Driver")
	}

	checkErr = errors.New("permission denied")
	if _, err := svc.SetComputerUse(ctx, true); !errors.Is(err, checkErr) {
		t.Fatalf("enable with a failed check: err = %v, want the check's error", err)
	}

	installed, checkErr = true, nil
	if snapshot, err := svc.SetComputerUse(ctx, true); err != nil || !snapshot.ComputerUse {
		t.Fatalf("enable with Cua Driver: snapshot=%+v err=%v", snapshot, err)
	}

	installed = false
	if snapshot, err := svc.SetComputerUse(ctx, false); err != nil || snapshot.ComputerUse {
		t.Fatalf("disable after Cua Driver was removed: snapshot=%+v err=%v", snapshot, err)
	}
}

type retentionStore struct {
	hibernationSettingsStore
	days int
}

func (s *retentionStore) GetAppSettings(context.Context) (Snapshot, error) {
	return Snapshot{SessionRetentionDays: s.days}, nil
}

func (s *retentionStore) SetSessionRetentionDays(_ context.Context, days int, _ time.Time) error {
	s.days = days
	return nil
}

// Retention is days a session stays archived; anything outside 0 (keep) to ten
// years is refused before it is stored.
func TestSessionRetentionDays(t *testing.T) {
	ctx := context.Background()
	store := &retentionStore{days: 30}
	svc := New(store, nil, Offering{}, nil)
	for _, days := range []int{-1, maxSessionRetentionDays + 1} {
		var apiErr *apierr.Error
		if _, err := svc.SetSessionRetentionDays(ctx, days); !errors.As(err, &apiErr) || apiErr.Code != "SESSION_RETENTION_INVALID" || store.days != 30 {
			t.Fatalf("days=%d: err=%v stored=%d, want SESSION_RETENTION_INVALID and no change", days, err, store.days)
		}
	}
	for _, days := range []int{0, 90} {
		if snapshot, err := svc.SetSessionRetentionDays(ctx, days); err != nil || snapshot.SessionRetentionDays != days {
			t.Fatalf("days=%d: snapshot=%+v err=%v", days, snapshot, err)
		}
	}
}
