package settings

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type hibernationSettingsStore struct{ err error }

func (s *hibernationSettingsStore) GetAppSettings(context.Context) (Snapshot, error) {
	return Snapshot{DefaultSessionMode: domain.SessionModeChat}, s.err
}
func (*hibernationSettingsStore) SetDefaultSessionMode(context.Context, domain.SessionMode, time.Time) error {
	return nil
}
func (*hibernationSettingsStore) SetCloudOffering(context.Context, bool, time.Time) error { return nil }
func (*hibernationSettingsStore) SetVersionRefresh(context.Context, bool, time.Time) error {
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

// The cloud gate is the single most safety-critical expression in the offering:
// a false positive would surface cloud UI (and let a local-only build reach a
// control plane) without the user opting in. Pin the whole (forced x toggle x
// url) matrix so no future edit can flip a default open.
func TestOfferingCloudEnabled(t *testing.T) {
	const url = "https://cp.example.com"
	cases := []struct {
		name   string
		forced bool // AO_CLOUD_OFFERING env override
		toggle bool // persisted app_settings.cloud_offering
		cpURL  string
		want   bool
	}{
		{"default off (baked url, no toggle, no force)", false, false, url, false},
		{"toggle on with url", false, true, url, true},
		{"env force on with url", true, false, url, true},
		{"force and toggle on", true, true, url, true},
		{"toggle on but no control plane", false, true, "", false},
		{"force on but no control plane", true, false, "", false},
		{"all off, no url", false, false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			offering := Offering{CloudForced: tc.forced, CloudControlPlaneURL: tc.cpURL}
			got := offering.CloudEnabled(Snapshot{CloudOffering: tc.toggle})
			if got != tc.want {
				t.Errorf("CloudEnabled(forced=%v, toggle=%v, url=%q) = %v, want %v",
					tc.forced, tc.toggle, tc.cpURL, got, tc.want)
			}
		})
	}
}

// The baked control-plane URL must never enable cloud on its own: a stock local
// install has the URL set but neither the toggle nor the env override, and must
// resolve closed. This is the exact upgrade path for existing local users.
func TestOfferingBakedURLDoesNotEnableCloud(t *testing.T) {
	offering := Offering{
		CloudForced:          false,
		CloudControlPlaneURL: "https://staging-api.aoagents.dev",
	}
	if offering.CloudEnabled(Snapshot{CloudOffering: false}) {
		t.Fatal("cloud enabled with only the baked URL set; a local-only install must stay off")
	}
}

func (*hibernationSettingsStore) SetHarnessDefault(context.Context, domain.AgentHarness, domain.HarnessDefault, time.Time) error {
	return nil
}

func (*hibernationSettingsStore) SetScmProxyURL(context.Context, string, time.Time) error {
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

// scmProxyStore persists whatever was last set so Set/Get round-trips behave.
type scmProxyStore struct {
	hibernationSettingsStore
	url   string
	reads int
}

func (s *scmProxyStore) GetAppSettings(context.Context) (Snapshot, error) {
	s.reads++
	return Snapshot{ScmProxyURL: s.url}, nil
}

func (s *scmProxyStore) SetScmProxyURL(_ context.Context, url string, _ time.Time) error {
	s.url = url
	return nil
}

func TestSetScmProxyURLValidationMatrix(t *testing.T) {
	ctx := context.Background()
	valid := []string{
		"socks5h://127.0.0.1:10808",
		"socks5://proxy.internal:1080",
		"http://127.0.0.1:10809",
		"https://proxy.corp.example:3128",
		"  socks5h://127.0.0.1:10808  ",
	}
	invalid := []string{
		"ftp://127.0.0.1:21",
		"socks5h://",      // no host
		"127.0.0.1:10808", // no scheme
		"://no-scheme",
		"socks5h://host:99999", // parsed, but the port overflows on dial
	}

	for _, raw := range valid {
		store := &scmProxyStore{}
		svc := New(store, nil, Offering{}, nil)
		snapshot, err := svc.SetScmProxyURL(ctx, raw)
		if err != nil {
			t.Fatalf("SetScmProxyURL(%q): %v", raw, err)
		}
		if snapshot.ScmProxyURL != strings.TrimSpace(raw) {
			t.Fatalf("stored %q, want trimmed %q", snapshot.ScmProxyURL, raw)
		}
		if svc.ScmProxyURL(ctx) != strings.TrimSpace(raw) {
			t.Fatalf("accessor %q after set, want %q", svc.ScmProxyURL(ctx), raw)
		}
	}
	for _, raw := range invalid {
		store := &scmProxyStore{}
		svc := New(store, nil, Offering{}, nil)
		if _, err := svc.SetScmProxyURL(ctx, raw); !errors.Is(err, ErrInvalidScmProxyURL) {
			t.Fatalf("SetScmProxyURL(%q) err=%v, want ErrInvalidScmProxyURL", raw, err)
		}
		if store.url != "" {
			t.Fatalf("invalid value %q leaked to the store as %q", raw, store.url)
		}
	}
}

func TestSetScmProxyURLEmptyClearsAndAccessorCaches(t *testing.T) {
	ctx := context.Background()
	store := &scmProxyStore{url: "socks5h://127.0.0.1:10808"}
	svc := New(store, nil, Offering{}, nil)
	if svc.ScmProxyURL(ctx) != "socks5h://127.0.0.1:10808" {
		t.Fatalf("pre-set accessor %q", svc.ScmProxyURL(ctx))
	}
	if svc.ScmProxyURL(ctx) != "socks5h://127.0.0.1:10808" || store.reads != 1 {
		t.Fatalf("accessor reads = %d, want one cached read", store.reads)
	}
	snapshot, err := svc.SetScmProxyURL(ctx, "")
	if err != nil || snapshot.ScmProxyURL != "" || svc.ScmProxyURL(ctx) != "" {
		t.Fatalf("clear: snapshot=%+v err=%v accessor=%q", snapshot, err, svc.ScmProxyURL(ctx))
	}
}

func TestScmProxyURLUnreadableStoreCountsAsUnset(t *testing.T) {
	ctx := context.Background()
	svc := New(&hibernationSettingsStore{err: errors.New("db gone")}, nil, Offering{}, nil)
	if got := svc.ScmProxyURL(ctx); got != "" {
		t.Fatalf("accessor on broken store = %q, want empty", got)
	}
}
