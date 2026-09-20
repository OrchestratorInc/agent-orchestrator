package store

import (
	"context"
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
	// DefaultSessionMode is the interface a new session gets when the spawn does
	// not name one. Never applied to an existing session: only an explicit
	// interface transition changes a live session's committed mode, so
	// changing this only affects sessions created afterwards.
	DefaultSessionMode domain.SessionMode
	// CloudOffering is the user's cloud toggle (Settings, Developer Mode). The
	// daemon gate combines it with the deployment's control-plane URL.
	CloudOffering bool
	// AutoPauseIdleMinutes exits agents idle this long; zero is off.
	AutoPauseIdleMinutes int
	// MemoryBudgetBytes is what the user lets AO hold; zero means Auto.
	MemoryBudgetBytes int64
	UpdatedAt         time.Time
}

// GetAppSettings reads the preference row.
func (s *Store) GetAppSettings(ctx context.Context) (AppSettings, error) {
	row, err := s.qr.GetAppSettings(ctx)
	if err != nil {
		return AppSettings{}, fmt.Errorf("read app settings: %w", err)
	}
	return AppSettings{
		// Normalized on read: a value written by a build that knows a mode this
		// one does not must still resolve to something dispatchable.
		DefaultSessionMode: domain.NormalizeSessionMode(row.DefaultSessionMode),
		CloudOffering:        row.CloudOffering,
		AutoPauseIdleMinutes: int(row.AutoPauseIdleMinutes),
		MemoryBudgetBytes:    row.MemoryBudgetBytes,
		UpdatedAt:            row.UpdatedAt,
	}, nil
}

// SetMemoryBudgetBytes persists the memory budget; zero means Auto.
func (s *Store) SetMemoryBudgetBytes(ctx context.Context, bytes int64, now time.Time) error {
	if bytes < 0 {
		return fmt.Errorf("memory budget must not be negative: %d", bytes)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetMemoryBudgetBytes(ctx, gen.SetMemoryBudgetBytesParams{
		MemoryBudgetBytes: bytes,
		UpdatedAt:         now,
	}); err != nil {
		return fmt.Errorf("set memory budget: %w", err)
	}
	return nil
}

// SetAutoPauseIdleMinutes persists the idle auto-pause threshold; zero disables it.
func (s *Store) SetAutoPauseIdleMinutes(ctx context.Context, minutes int, now time.Time) error {
	if minutes < 0 {
		return fmt.Errorf("auto-pause minutes must not be negative: %d", minutes)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetAutoPauseIdleMinutes(ctx, gen.SetAutoPauseIdleMinutesParams{
		AutoPauseIdleMinutes: int64(minutes),
		UpdatedAt:            now,
	}); err != nil {
		return fmt.Errorf("set auto-pause idle minutes: %w", err)
	}
	return nil
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
