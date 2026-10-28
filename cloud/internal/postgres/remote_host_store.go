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

// UpsertRemoteHost saves principal's registration of host. A host the account
// does not list yet is refused with ErrRemoteHostLimit once it lists limit
// hosts; a limit of 0 lists any number. A per-user advisory lock makes the
// count and the insert one step, so concurrent registrations cannot pass the
// limit.
func (s *Store) UpsertRemoteHost(ctx context.Context, principal domain.Principal, host domain.RemoteHost, limit int) error {
	return s.withUser(ctx, principal.UserID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended('ao_remote_hosts:' || $1, 0))`, principal.UserID,
		); err != nil {
			return err
		}
		var listed bool
		var total int
		if err := tx.QueryRow(ctx,
			`SELECT coalesce(bool_or(host_id = $2), false), count(*) FROM ao_remote_hosts WHERE user_id = $1`,
			principal.UserID, host.HostID,
		).Scan(&listed, &total); err != nil {
			return err
		}
		if limit > 0 && !listed && total >= limit {
			return ErrRemoteHostLimit
		}
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

// FindRemoteHostRegistrations reads only rows for one opaque host identity,
// always in the same order. The HTTP edge must prove knowledge of the host
// token hash before it may update any returned account's address.
func (s *Store) FindRemoteHostRegistrations(ctx context.Context, hostID string) ([]domain.RemoteHost, error) {
	hosts := []domain.RemoteHost{}
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT host_id, user_id, label, url, encrypted_token, token_nonce, updated_at
			FROM ao_remote_hosts WHERE host_id = $1 ORDER BY user_id`, hostID)
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
			WHERE user_id = $1 AND host_id = $2 AND url IS DISTINCT FROM $3`, userID, hostID, address)
		return err
	})
}

const remoteHostTunnelColumns = `user_id, host_id, tunnel_id, hostname, encrypted_run_token, run_token_nonce,
	created_at, updated_at, last_seen_at, released_at IS NOT NULL`

func scanRemoteHostTunnel(row pgx.Row) (domain.RemoteHostTunnel, bool, error) {
	var t domain.RemoteHostTunnel
	err := row.Scan(&t.UserID, &t.HostID, &t.TunnelID, &t.Hostname, &t.EncryptedRunToken, &t.RunTokenNonce,
		&t.CreatedAt, &t.UpdatedAt, &t.LastSeenAt, &t.Released)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RemoteHostTunnel{}, false, nil
	}
	return t, err == nil, err
}

// GetRemoteHostTunnel reads the tunnel userID's account gives hostID. The
// HTTP edge must prove the host token digest first.
func (s *Store) GetRemoteHostTunnel(ctx context.Context, userID, hostID string) (domain.RemoteHostTunnel, bool, error) {
	var t domain.RemoteHostTunnel
	var found bool
	err := s.withService(ctx, func(tx pgx.Tx) error {
		var err error
		t, found, err = scanRemoteHostTunnel(tx.QueryRow(ctx, `SELECT `+remoteHostTunnelColumns+`
			FROM ao_remote_host_tunnels WHERE user_id = $1 AND host_id = $2`, userID, hostID))
		return err
	})
	return t, found, err
}

// FindRemoteHostTunnel reads the row naming a tunnel Cloudflare listed.
func (s *Store) FindRemoteHostTunnel(ctx context.Context, tunnelID string) (domain.RemoteHostTunnel, bool, error) {
	var t domain.RemoteHostTunnel
	var found bool
	err := s.withService(ctx, func(tx pgx.Tx) error {
		var err error
		t, found, err = scanRemoteHostTunnel(tx.QueryRow(ctx, `SELECT `+remoteHostTunnelColumns+`
			FROM ao_remote_host_tunnels WHERE tunnel_id = $1`, tunnelID))
		return err
	})
	return t, found, err
}

// SaveRemoteHostTunnel writes a tunnel Cloudflare made or repaired and
// returns when its DNS record was made: now when recordCreated, or for a new
// row, and otherwise kept from the row it replaces.
func (s *Store) SaveRemoteHostTunnel(ctx context.Context, t domain.RemoteHostTunnel, recordCreated bool) (time.Time, error) {
	var since time.Time
	err := s.withService(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO ao_remote_host_tunnels
			(user_id, host_id, tunnel_id, hostname, encrypted_run_token, run_token_nonce)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (user_id, host_id) DO UPDATE SET tunnel_id = EXCLUDED.tunnel_id,
			hostname = EXCLUDED.hostname, encrypted_run_token = EXCLUDED.encrypted_run_token,
			run_token_nonce = EXCLUDED.run_token_nonce, updated_at = now(), released_at = NULL,
			created_at = CASE WHEN $7 THEN now() ELSE ao_remote_host_tunnels.created_at END
			RETURNING created_at`,
			t.UserID, t.HostID, t.TunnelID, t.Hostname, t.EncryptedRunToken, t.RunTokenNonce, recordCreated).Scan(&since)
	})
	return since, err
}

// MarkRemoteHostTunnelLinked records that the account linked the host to its
// tunnel again, which starts the hold on quick addresses.
func (s *Store) MarkRemoteHostTunnelLinked(ctx context.Context, userID, hostID string) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_remote_host_tunnels SET updated_at = now() WHERE user_id = $1 AND host_id = $2`, userID, hostID)
		return err
	})
}

func (s *Store) TouchRemoteHostTunnel(ctx context.Context, tunnelID string, seenAt time.Time) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_remote_host_tunnels SET last_seen_at = $2 WHERE tunnel_id = $1`, tunnelID, seenAt)
		return err
	})
}

// ReleaseRemoteHostTunnel records that tunnelID was deleted while its host was
// offline, only while a row still names it: a host that got a new tunnel in
// the meantime keeps it.
func (s *Store) ReleaseRemoteHostTunnel(ctx context.Context, tunnelID string) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		// updated_at too: a row whose account no longer lists the host keeps
		// its record for the sweep's grace from now, time to be listed again.
		_, err := tx.Exec(ctx, `UPDATE ao_remote_host_tunnels SET released_at = now(), updated_at = now()
			WHERE tunnel_id = $1 AND released_at IS NULL`, tunnelID)
		return err
	})
}

// DeleteRemoteHostTunnel removes the row only while it still names tunnelID,
// so a sweep never drops a tunnel the machine recreated in the meantime.
func (s *Store) DeleteRemoteHostTunnel(ctx context.Context, tunnelID string) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM ao_remote_host_tunnels WHERE tunnel_id = $1`, tunnelID)
		return err
	})
}

// RemoteHostListed reports whether userID's account lists hostID now.
func (s *Store) RemoteHostListed(ctx context.Context, userID, hostID string) (bool, error) {
	var listed bool
	err := s.withService(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ao_remote_hosts WHERE user_id = $1 AND host_id = $2)`,
			userID, hostID).Scan(&listed)
	})
	return listed, err
}

// ListRemoteHostTunnelSweepCandidates returns tunnels whose account no longer
// lists their host, once unwritten since unregisteredBefore, and tunnels whose
// machine has not fetched its token since cutoff, released or not.
func (s *Store) ListRemoteHostTunnelSweepCandidates(ctx context.Context, cutoff, unregisteredBefore time.Time) ([]domain.RemoteHostTunnelCandidate, error) {
	items := []domain.RemoteHostTunnelCandidate{}
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT user_id, host_id, tunnel_id, hostname, registered, released FROM (
				SELECT t.user_id, t.host_id, t.tunnel_id, t.hostname, t.last_seen_at, t.updated_at, t.released_at IS NOT NULL AS released,
					EXISTS (SELECT 1 FROM ao_remote_hosts h WHERE h.user_id = t.user_id AND h.host_id = t.host_id) AS registered
				FROM ao_remote_host_tunnels t) c
			WHERE c.last_seen_at < $1 OR (NOT c.registered AND c.updated_at < $2)
			ORDER BY c.last_seen_at LIMIT 500`, cutoff, unregisteredBefore)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c domain.RemoteHostTunnelCandidate
			if err := rows.Scan(&c.UserID, &c.HostID, &c.TunnelID, &c.Hostname, &c.Registered, &c.Released); err != nil {
				return err
			}
			items = append(items, c)
		}
		return rows.Err()
	})
	return items, err
}
