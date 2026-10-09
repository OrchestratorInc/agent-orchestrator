package postgres

import (
	"context"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ListRemoteHosts(ctx context.Context, principal domain.Principal) ([]domain.RemoteHost, error) {
	hosts := make([]domain.RemoteHost, 0)
	err := s.withUser(ctx, principal.UserID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT host_id, user_id, label, url, encrypted_token, token_nonce, updated_at
			FROM ao_remote_hosts WHERE user_id = $1 ORDER BY label, host_id`, principal.UserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var host domain.RemoteHost
			if err := rows.Scan(&host.HostID, &host.UserID, &host.Label, &host.URL,
				&host.EncryptedToken, &host.TokenNonce, &host.UpdatedAt); err != nil {
				return err
			}
			hosts = append(hosts, host)
		}
		return rows.Err()
	})
	return hosts, err
}

func (s *Store) UpsertRemoteHost(ctx context.Context, principal domain.Principal, host domain.RemoteHost) error {
	return s.withUser(ctx, principal.UserID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ao_remote_hosts
			(user_id, host_id, label, url, encrypted_token, token_nonce)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (user_id, host_id) DO UPDATE SET label = EXCLUDED.label,
			url = EXCLUDED.url, encrypted_token = EXCLUDED.encrypted_token,
			token_nonce = EXCLUDED.token_nonce, updated_at = now()`,
			principal.UserID, host.HostID, host.Label, host.URL, host.EncryptedToken, host.TokenNonce)
		return err
	})
}

func (s *Store) DeleteRemoteHost(ctx context.Context, principal domain.Principal, hostID string) error {
	return s.withUser(ctx, principal.UserID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM ao_remote_hosts WHERE user_id = $1 AND host_id = $2`, principal.UserID, hostID)
		return err
	})
}

// FindRemoteHostRegistrations reads only rows for one opaque host identity.
// The HTTP edge must prove knowledge of the host token hash before it may
// update any returned account's address.
func (s *Store) FindRemoteHostRegistrations(ctx context.Context, hostID string) ([]domain.RemoteHost, error) {
	hosts := []domain.RemoteHost{}
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT host_id, user_id, label, url, encrypted_token, token_nonce, updated_at
			FROM ao_remote_hosts WHERE host_id = $1`, hostID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var host domain.RemoteHost
			if err := rows.Scan(&host.HostID, &host.UserID, &host.Label, &host.URL,
				&host.EncryptedToken, &host.TokenNonce, &host.UpdatedAt); err != nil {
				return err
			}
			hosts = append(hosts, host)
		}
		return rows.Err()
	})
	return hosts, err
}

func (s *Store) UpdateRemoteHostAddress(ctx context.Context, userID, hostID, address string) error {
	return s.withUser(ctx, userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_remote_hosts SET url = $3, updated_at = now()
			WHERE user_id = $1 AND host_id = $2`, userID, hostID, address)
		return err
	})
}
