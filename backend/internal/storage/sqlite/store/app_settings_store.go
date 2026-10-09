package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// Daemon-owned user preferences.
//
// The row is seeded by migration, so a read is a plain SELECT and no caller has
// to handle "settings do not exist yet".

// AppSettings is the durable preference set. Field-compatible with
// service/settings.Snapshot, which the daemon wiring adapts.
type AppSettings struct {
	HarnessDefaults map[string]domain.HarnessDefault
	// DefaultSessionMode is the interface a new session gets when the spawn does
	// not name one. Never applied to an existing session: only an explicit
	// interface transition changes a live session's committed mode, so
	// changing this only affects sessions created afterwards.
	DefaultSessionMode domain.SessionMode
	// CloudOffering is the user's cloud toggle (Settings, Developer Mode). The
	// daemon gate combines it with the deployment's control-plane URL.
	CloudOffering bool
	UpdatedAt     time.Time
}

// GetAppSettings reads the preference row.
func (s *Store) GetAppSettings(ctx context.Context) (AppSettings, error) {
	row, err := s.qr.GetAppSettings(ctx)
	if err != nil {
		return AppSettings{}, fmt.Errorf("read app settings: %w", err)
	}
	defaults := make(map[string]domain.HarnessDefault)
	if err := json.Unmarshal([]byte(row.HarnessDefaults), &defaults); err != nil {
		return AppSettings{}, fmt.Errorf("read harness defaults: %w", err)
	}
	return AppSettings{
		HarnessDefaults: defaults,
		// Normalized on read: a value written by a build that knows a mode this
		// one does not must still resolve to something dispatchable.
		DefaultSessionMode: domain.NormalizeSessionMode(row.DefaultSessionMode),
		CloudOffering:      row.CloudOffering,
		UpdatedAt:          row.UpdatedAt,
	}, nil
}

// SetDefaultSessionMode persists the default interface for new sessions.
func (s *Store) SetDefaultSessionMode(ctx context.Context, mode domain.SessionMode, now time.Time) error {
	if !mode.Valid() {
		return fmt.Errorf("invalid session mode %q", mode)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetDefaultSessionMode(ctx, gen.SetDefaultSessionModeParams{
		DefaultSessionMode: mode,
		UpdatedAt:          now,
	}); err != nil {
		return fmt.Errorf("set session mode: %w", err)
	}
	return nil
}

// SetCloudOffering persists the user's cloud toggle.
func (s *Store) SetCloudOffering(ctx context.Context, enabled bool, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetCloudOffering(ctx, gen.SetCloudOfferingParams{
		CloudOffering: enabled,
		UpdatedAt:     now,
	}); err != nil {
		return fmt.Errorf("set cloud offering: %w", err)
	}
	return nil
}

// SetHarnessDefault updates one entry atomically so concurrent saves for other
// harnesses cannot overwrite each other. Empty values remove the override.
func (s *Store) SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	path := `$."` + string(agent) + `"`
	if value.Model == "" {
		return s.qw.ResetHarnessDefault(ctx, gen.ResetHarnessDefaultParams{HarnessPath: path, UpdatedAt: now})
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.qw.SetHarnessDefault(ctx, gen.SetHarnessDefaultParams{HarnessPath: path, Config: string(data), UpdatedAt: now})
}
