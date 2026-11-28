package daemon

import (
	"context"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	settingssvc "github.com/OrchestratorInc/agent-orchestrator/backend/internal/service/settings"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite"
)

// settingsStore adapts the SQLite store to the settings service's Store.
//
// The two define their own snapshot types so neither depends on the other's; this
// is the one place that knows both, keeping the translation in the wiring.
type settingsStore struct {
	store *sqlite.Store
	// accountHandoffChanged, when set, is signalled (coalesced) after account
	// handoff is turned on or off, so the usage-limit loop resting while
	// handoff was off scans again at once.
	accountHandoffChanged chan<- struct{}
}

var _ settingssvc.Store = settingsStore{}

func (s settingsStore) GetAppSettings(ctx context.Context) (settingssvc.Snapshot, error) {
	row, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return settingssvc.Snapshot{}, err
	}
	return settingssvc.Snapshot{
		DefaultSessionMode:   row.DefaultSessionMode,
		HarnessDefaults:      row.HarnessDefaults,
		VersionRefresh:       row.VersionRefresh,
		ComputerUse:          row.ComputerUse,
		CommitAttribution:    row.CommitAttribution,
		SkillSharing:         row.SkillSharing,
		SessionRetentionDays: row.SessionRetentionDays,
		AccountHandoff:       row.AccountHandoff,
		UpdatedAt:            row.UpdatedAt,
	}, nil
}

func (s settingsStore) SetDefaultSessionMode(
	ctx context.Context,
	mode domain.SessionMode,
	now time.Time,
) error {
	return s.store.SetDefaultSessionMode(ctx, mode, now)
}

func (s settingsStore) SetVersionRefresh(ctx context.Context, enabled bool, now time.Time) error {
	return s.store.SetVersionRefresh(ctx, enabled, now)
}

func (s settingsStore) SetComputerUse(ctx context.Context, enabled bool, now time.Time) error {
	return s.store.SetComputerUse(ctx, enabled, now)
}

func (s settingsStore) SetCommitAttribution(ctx context.Context, enabled bool, now time.Time) error {
	return s.store.SetCommitAttribution(ctx, enabled, now)
}

func (s settingsStore) SetSkillSharing(ctx context.Context, enabled bool, now time.Time) error {
	return s.store.SetSkillSharing(ctx, enabled, now)
}

func (s settingsStore) SetSessionRetentionDays(ctx context.Context, days int, now time.Time) error {
	return s.store.SetSessionRetentionDays(ctx, days, now)
}

func (s settingsStore) SetAccountHandoff(ctx context.Context, enabled bool, now time.Time) error {
	if err := s.store.SetAccountHandoff(ctx, enabled, now); err != nil {
		return err
	}
	if s.accountHandoffChanged != nil {
		select {
		case s.accountHandoffChanged <- struct{}{}:
		default:
		}
	}
	return nil
}

func (s settingsStore) SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault, now time.Time) error {
	return s.store.SetHarnessDefault(ctx, agent, value, now)
}
