package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// GetUserSandboxProvider distinguishes a missing row from an intentional
// default (a row whose provider is NULL).
func (s *Store) GetUserSandboxProvider(ctx context.Context, userID string) (string, bool, error) {
	var provider *string
	configured := false
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT sandbox_provider FROM ao_user_sandbox_preferences WHERE user_id = $1`,
			userID,
		).Scan(&provider)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err == nil {
			configured = true
		}
		return err
	})
	if err != nil {
		return "", false, err
	}
	if provider == nil {
		return "", configured, nil
	}
	return *provider, configured, nil
}

// PutUserSandboxProvider stores a user-level preference. initializeOnly is a
// compare-and-insert used when migrating a desktop's legacy local setting.
func (s *Store) PutUserSandboxProvider(ctx context.Context, userID, provider string, initializeOnly bool) (string, error) {
	var stored *string
	err := s.withUser(ctx, userID, func(tx pgx.Tx) error {
		query := `INSERT INTO ao_user_sandbox_preferences (user_id, sandbox_provider)
			VALUES ($1, NULLIF($2, ''))
			ON CONFLICT (user_id) DO UPDATE SET
				sandbox_provider = EXCLUDED.sandbox_provider,
				updated_at = now()
			RETURNING sandbox_provider`
		if initializeOnly {
			query = `INSERT INTO ao_user_sandbox_preferences (user_id, sandbox_provider)
				VALUES ($1, NULLIF($2, ''))
				ON CONFLICT (user_id) DO NOTHING
				RETURNING sandbox_provider`
		}
		err := tx.QueryRow(ctx, query, userID, provider).Scan(&stored)
		if initializeOnly && errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return err
	})
	if err != nil {
		return "", err
	}
	if stored == nil {
		return "", nil
	}
	return *stored, nil
}
