package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GetCustomThemes reads the desktop app's custom themes as stored JSON; "{}"
// means none. service/apptheme owns the shape and validation.
func (s *Store) GetCustomThemes(ctx context.Context) (string, error) {
	value, err := s.qr.GetThemeLibrary(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "{}", nil
	}
	if err != nil {
		return "", fmt.Errorf("read custom themes: %w", err)
	}
	return value, nil
}

// SetCustomThemes replaces the custom themes with already validated JSON.
func (s *Store) SetCustomThemes(ctx context.Context, value string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetThemeLibrary(ctx, value); err != nil {
		return fmt.Errorf("set custom themes: %w", err)
	}
	return nil
}
