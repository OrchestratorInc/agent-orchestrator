package store

import "context"

func (s *Store) LifetimeSessions(ctx context.Context) (int64, error) {
	var count int64
	err := s.readDB.QueryRowContext(ctx, `SELECT count FROM telemetry_lifetime_sessions WHERE singleton = 1`).Scan(&count)
	return count, err
}
