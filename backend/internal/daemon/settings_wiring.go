package daemon

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelcatalog"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	settingssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/settings"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// settingsStore adapts the SQLite store to the settings service's Store.
//
// The two define their own snapshot types so neither depends on the other's; this
// is the one place that knows both, keeping the translation in the wiring.
type settingsStore struct{ store *sqlite.Store }

var _ settingssvc.Store = settingsStore{}

func (s settingsStore) GetAppSettings(ctx context.Context) (settingssvc.Snapshot, error) {
	row, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return settingssvc.Snapshot{}, err
	}
	return settingssvc.Snapshot{
		DefaultSessionMode: row.DefaultSessionMode,
		HarnessDefaults:    row.HarnessDefaults,
		CloudOffering:      row.CloudOffering,
		UpdatedAt:          row.UpdatedAt,
	}, nil
}

func (s settingsStore) SetDefaultSessionMode(
	ctx context.Context,
	mode domain.SessionMode,
	now time.Time,
) error {
	return s.store.SetDefaultSessionMode(ctx, mode, now)
}

func (s settingsStore) SetCloudOffering(ctx context.Context, enabled bool, now time.Time) error {
	return s.store.SetCloudOffering(ctx, enabled, now)
}

func (s settingsStore) SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault, now time.Time) error {
	return s.store.SetHarnessDefault(ctx, agent, value, now)
}

// launchableDefaults is the settings service as new sessions and reviews read
// it. A saved default model that AO's fixed catalog no longer lists (e.g.
// after an upgrade retires it) is dropped, so launches use the harness's own
// default instead of failing until the user resets the preference.
type launchableDefaults struct{ *settingssvc.Service }

func (d launchableDefaults) HarnessDefault(ctx context.Context, agent domain.AgentHarness) domain.HarnessDefault {
	value := d.Service.HarnessDefault(ctx, agent)
	if !modelcatalog.Accepts(string(agent), value.Model) {
		return domain.HarnessDefault{}
	}
	return value
}
