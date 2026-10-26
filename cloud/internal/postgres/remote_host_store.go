package postgres

import (
	"context"
	"errors"
	"time"

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

// GetRemoteHostTunnel reads one host's tunnel. The HTTP edge must prove the
// host token digest first.
func (s *Store) GetRemoteHostTunnel(ctx context.Context, hostID string) (domain.RemoteHostTunnel, bool, error) {
	var t domain.RemoteHostTunnel
	err := s.withService(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT host_id, tunnel_id, hostname, encrypted_run_token, run_token_nonce, updated_at, last_seen_at
			FROM ao_remote_host_tunnels WHERE host_id = $1`, hostID).
			Scan(&t.HostID, &t.TunnelID, &t.Hostname, &t.EncryptedRunToken, &t.RunTokenNonce, &t.UpdatedAt, &t.LastSeenAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RemoteHostTunnel{}, false, nil
	}
	return t, err == nil, err
}

func (s *Store) SaveRemoteHostTunnel(ctx context.Context, t domain.RemoteHostTunnel) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ao_remote_host_tunnels
			(host_id, tunnel_id, hostname, encrypted_run_token, run_token_nonce)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (host_id) DO UPDATE SET tunnel_id = EXCLUDED.tunnel_id,
			hostname = EXCLUDED.hostname, encrypted_run_token = EXCLUDED.encrypted_run_token,
			run_token_nonce = EXCLUDED.run_token_nonce, updated_at = now(), last_seen_at = now()`,
			t.HostID, t.TunnelID, t.Hostname, t.EncryptedRunToken, t.RunTokenNonce)
		return err
	})
}

func (s *Store) TouchRemoteHostTunnel(ctx context.Context, hostID string, seenAt time.Time) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_remote_host_tunnels SET last_seen_at = $2 WHERE host_id = $1`, hostID, seenAt)
		return err
	})
}

// DeleteRemoteHostTunnel removes the row only while it still names tunnelID,
// so a sweep never drops a tunnel the machine recreated in the meantime.
func (s *Store) DeleteRemoteHostTunnel(ctx context.Context, hostID, tunnelID string) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM ao_remote_host_tunnels WHERE host_id = $1 AND tunnel_id = $2`, hostID, tunnelID)
		return err
	})
}

// ListRemoteHostTunnelSweepCandidates returns tunnels no account registers any
// more, and tunnels whose machine has not fetched its token since cutoff.
func (s *Store) ListRemoteHostTunnelSweepCandidates(ctx context.Context, cutoff time.Time) ([]domain.RemoteHostTunnelCandidate, error) {
	items := []domain.RemoteHostTunnelCandidate{}
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT t.host_id, t.tunnel_id, t.hostname, t.last_seen_at,
				EXISTS (SELECT 1 FROM ao_remote_hosts h WHERE h.host_id = t.host_id)
			FROM ao_remote_host_tunnels t
			WHERE t.last_seen_at < $1
				OR NOT EXISTS (SELECT 1 FROM ao_remote_hosts h WHERE h.host_id = t.host_id)
			ORDER BY t.last_seen_at LIMIT 500`, cutoff)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c domain.RemoteHostTunnelCandidate
			if err := rows.Scan(&c.HostID, &c.TunnelID, &c.Hostname, &c.LastSeenAt, &c.Registered); err != nil {
				return err
			}
			items = append(items, c)
		}
		return rows.Err()
	})
	return items, err
}
