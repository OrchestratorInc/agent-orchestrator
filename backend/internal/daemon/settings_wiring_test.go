package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	settingssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/settings"
)

type harnessDefaultsStore struct {
	settingssvc.Store
	defaults map[string]domain.HarnessDefault
}

func (s harnessDefaultsStore) GetAppSettings(context.Context) (settingssvc.Snapshot, error) {
	return settingssvc.Snapshot{HarnessDefaults: s.defaults}, nil
}

func TestLaunchableDefaultsDropsModelsTheFixedCatalogNoLongerLists(t *testing.T) {
	store := harnessDefaultsStore{defaults: map[string]domain.HarnessDefault{
		string(domain.HarnessAmp):        {Model: "retired-mode"},
		string(domain.HarnessClaudeCode): {Model: "any-provider-model", Effort: "high"},
	}}
	defaults := launchableDefaults{settingssvc.New(store, nil, settingssvc.Offering{}, time.Now)}
	if got := defaults.HarnessDefault(context.Background(), domain.HarnessAmp); !got.IsZero() {
		t.Fatalf("amp default = %+v, want dropped", got)
	}
	// Dynamic catalogs are validated at launch, not here.
	if got := defaults.HarnessDefault(context.Background(), domain.HarnessClaudeCode); got.Model != "any-provider-model" || got.Effort != "high" {
		t.Fatalf("claude default = %+v, want kept", got)
	}
}
