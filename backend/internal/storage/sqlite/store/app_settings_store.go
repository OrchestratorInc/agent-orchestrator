package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

type AppSettings struct {
	HarnessDefaults      map[string]domain.HarnessDefault
	DefaultSessionMode   domain.SessionMode
	VersionRefresh       bool
	ComputerUse          bool
	SessionRetentionDays int
	AccountHandoff       bool
	UpdatedAt            time.Time
}

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
		HarnessDefaults:      defaults,
		DefaultSessionMode:   domain.NormalizeSessionMode(row.DefaultSessionMode),
		VersionRefresh:       row.VersionRefresh,
		ComputerUse:          row.ComputerUse,
		SessionRetentionDays: int(row.SessionRetentionDays),
		AccountHandoff:       row.AccountHandoff,
		UpdatedAt:            row.UpdatedAt,
	}, nil
}

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

func (s *Store) SetVersionRefresh(ctx context.Context, enabled bool, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetVersionRefresh(ctx, gen.SetVersionRefreshParams{
		VersionRefresh: enabled,
		UpdatedAt:      now,
	}); err != nil {
		return fmt.Errorf("set version refresh: %w", err)
	}
	return nil
}

func (s *Store) SetComputerUse(ctx context.Context, enabled bool, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetComputerUse(ctx, gen.SetComputerUseParams{
		ComputerUse: enabled,
		UpdatedAt:   now,
	}); err != nil {
		return fmt.Errorf("set computer use: %w", err)
	}
	return nil
}

func (s *Store) SetSessionRetentionDays(ctx context.Context, days int, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetSessionRetentionDays(ctx, gen.SetSessionRetentionDaysParams{
		SessionRetentionDays: int64(days),
		UpdatedAt:            now,
	}); err != nil {
		return fmt.Errorf("set session retention: %w", err)
	}
	return nil
}

func (s *Store) SetAccountHandoff(ctx context.Context, enabled bool, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetAccountHandoff(ctx, gen.SetAccountHandoffParams{
		AccountHandoff: enabled,
		UpdatedAt:      now,
	}); err != nil {
		return fmt.Errorf("set account handoff: %w", err)
	}
	return nil
}

func (s *Store) SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	path := `$."` + string(agent) + `"`
	if value.IsZero() {
		return s.qw.ResetHarnessDefault(ctx, gen.ResetHarnessDefaultParams{HarnessPath: path, UpdatedAt: now})
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.qw.SetHarnessDefault(ctx, gen.SetHarnessDefaultParams{HarnessPath: path, Config: string(data), UpdatedAt: now})
}
