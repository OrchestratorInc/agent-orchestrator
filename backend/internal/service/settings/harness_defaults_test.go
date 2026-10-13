package settings

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type harnessSettingsStore struct {
	hibernationSettingsStore
	defaults map[string]domain.HarnessDefault
}

func (s *harnessSettingsStore) GetAppSettings(context.Context) (Snapshot, error) {
	return Snapshot{HarnessDefaults: s.defaults}, s.err
}
func (s *harnessSettingsStore) SetHarnessDefault(_ context.Context, agent domain.AgentHarness, value domain.HarnessDefault, _ time.Time) error {
	if s.err != nil {
		return s.err
	}
	if value.IsZero() {
		delete(s.defaults, string(agent))
	} else {
		s.defaults[string(agent)] = value
	}
	return nil
}

func harnessDefault(t *testing.T, svc *Service, agent domain.AgentHarness) domain.HarnessDefault {
	t.Helper()
	snapshot, err := svc.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.HarnessDefaults[string(agent)]
}

func TestHarnessDefaultsValidateAndSurviveServiceRestart(t *testing.T) {
	ctx := context.Background()
	store := &harnessSettingsStore{defaults: make(map[string]domain.HarnessDefault)}
	svc := New(store, nil, Offering{}, nil)
	if _, err := svc.SetHarnessDefault(ctx, "not-an-agent", domain.HarnessDefault{EnabledModels: []string{"m"}}); !errors.Is(err, ErrUnknownHarness) {
		t.Fatalf("unknown harness: %v", err)
	}
	if _, err := svc.SetHarnessDefault(ctx, domain.HarnessCodex, domain.HarnessDefault{EnabledModels: []string{"m"}}); err != nil {
		t.Fatal(err)
	}
	restarted := New(store, nil, Offering{}, nil)
	if got := harnessDefault(t, restarted, domain.HarnessCodex); !reflect.DeepEqual(got, domain.HarnessDefault{EnabledModels: []string{"m"}}) {
		t.Fatalf("restart = %+v", got)
	}
	store.err = errors.New("write failed")
	if _, err := svc.SetHarnessDefault(ctx, domain.HarnessCodex, domain.HarnessDefault{}); err == nil {
		t.Fatal("write failure ignored")
	}
	store.err = nil
	if _, err := svc.SetHarnessDefault(ctx, domain.HarnessCodex, domain.HarnessDefault{}); err != nil {
		t.Fatal(err)
	}
	if got := harnessDefault(t, svc, domain.HarnessCodex); !got.IsZero() {
		t.Fatalf("reset = %+v", got)
	}
}

func TestHarnessEnabledModelsAreNormalizedAndBounded(t *testing.T) {
	ctx := context.Background()
	store := &harnessSettingsStore{defaults: make(map[string]domain.HarnessDefault)}
	svc := New(store, nil, Offering{}, nil)
	for _, ids := range [][]string{{" "}, {strings.Repeat("m", 257)}, {"a\nb"}, make([]string, maxEnabledModels+1)} {
		if _, err := svc.SetHarnessDefault(ctx, domain.HarnessAider, domain.HarnessDefault{EnabledModels: ids}); !errors.Is(err, ErrInvalidHarnessDefault) {
			t.Fatalf("enabled models %q: %v", ids, err)
		}
	}
	if _, err := svc.SetHarnessDefault(ctx, domain.HarnessAider, domain.HarnessDefault{EnabledModels: []string{" gpt-5 ", "claude", "gpt-5"}}); err != nil {
		t.Fatal(err)
	}
	if got := harnessDefault(t, svc, domain.HarnessAider); !reflect.DeepEqual(got.EnabledModels, []string{"gpt-5", "claude"}) {
		t.Fatalf("enabled models = %+v", got)
	}
	if _, err := svc.SetHarnessDefault(ctx, domain.HarnessAider, domain.HarnessDefault{EnabledModels: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, saved := store.defaults[string(domain.HarnessAider)]; saved {
		t.Fatal("empty enabled models should remove the entry")
	}
}
