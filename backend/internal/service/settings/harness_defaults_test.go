package settings

import (
	"context"
	"errors"
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
	if value.Model == "" {
		delete(s.defaults, string(agent))
	} else {
		s.defaults[string(agent)] = value
	}
	return nil
}
func TestHarnessDefaultsValidateAndSurviveServiceRestart(t *testing.T) {
	ctx := context.Background()
	store := &harnessSettingsStore{defaults: make(map[string]domain.HarnessDefault)}
	svc := New(store, nil, Offering{}, nil)
	for _, tc := range []struct {
		agent domain.AgentHarness
		value domain.HarnessDefault
	}{
		{"not-an-agent", domain.HarnessDefault{Model: "m"}},
		{domain.HarnessCodex, domain.HarnessDefault{Effort: "high"}},
		{domain.HarnessCodex, domain.HarnessDefault{Model: strings.Repeat("m", 257)}},
		{domain.HarnessCodex, domain.HarnessDefault{Model: "m\nother"}},
		{domain.HarnessCodex, domain.HarnessDefault{Model: "m", Effort: strings.Repeat("x", 33)}},
		{domain.HarnessOpenCode, domain.HarnessDefault{Model: "m", Effort: "high"}},
	} {
		if _, err := svc.SetHarnessDefault(ctx, tc.agent, tc.value); !errors.Is(err, ErrInvalidHarnessDefault) {
			t.Fatalf("input %+v: %v", tc, err)
		}
	}
	if _, err := svc.SetHarnessDefault(ctx, domain.HarnessCodex, domain.HarnessDefault{Model: " m ", Effort: " high "}); err != nil {
		t.Fatal(err)
	}
	restarted := New(store, nil, Offering{}, nil)
	if got := restarted.HarnessDefault(ctx, domain.HarnessCodex); got != (domain.HarnessDefault{Model: "m", Effort: "high"}) {
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
	if got := svc.HarnessDefault(ctx, domain.HarnessCodex); got != (domain.HarnessDefault{}) {
		t.Fatalf("reset = %+v", got)
	}
}
