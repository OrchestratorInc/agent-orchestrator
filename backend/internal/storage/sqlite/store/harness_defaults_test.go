package store_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestHarnessDefaultsPersistIndependentlyAndReset(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	initial, err := s.GetAppSettings(ctx)
	if err != nil || len(initial.HarnessDefaults) != 0 {
		t.Fatalf("initial defaults = %+v, %v", initial, err)
	}
	var wg sync.WaitGroup
	for _, agent := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SetHarnessDefault(ctx, agent, domain.HarnessDefault{DisabledModels: []string{string(agent) + "-model"}}, time.Now()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	saved, err := s.GetAppSettings(ctx)
	if err != nil || len(saved.HarnessDefaults) != 2 {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	if !slices.Equal(saved.HarnessDefaults["codex"].DisabledModels, []string{"codex-model"}) || !slices.Equal(saved.HarnessDefaults["claude-code"].DisabledModels, []string{"claude-code-model"}) {
		t.Fatalf("defaults lost values: %+v", saved.HarnessDefaults)
	}
	if err := s.SetHarnessDefault(ctx, domain.HarnessCodex, domain.HarnessDefault{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	reset, err := s.GetAppSettings(ctx)
	if err != nil || len(reset.HarnessDefaults) != 1 || reset.HarnessDefaults["claude-code"].IsZero() {
		t.Fatalf("reset changed another harness: %+v, %v", reset, err)
	}
	if reset.DefaultSessionMode != initial.DefaultSessionMode || reset.CloudOffering != initial.CloudOffering {
		t.Fatal("harness defaults changed unrelated settings")
	}
}
