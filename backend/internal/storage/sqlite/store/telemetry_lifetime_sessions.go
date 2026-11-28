package store

import "context"

// LifetimeSessions counts committed sessions, including sessions later deleted.
func (s *Store) LifetimeSessions(ctx context.Context) (int64, error) {
	var count int64
	err := s.readDB.QueryRowContext(ctx, `SELECT count FROM telemetry_lifetime_sessions WHERE singleton = 1`).Scan(&count)
	return count, err
}

// LifetimeWorkSessions counts committed worker sessions (project workers and
// standalone sessions), including sessions later deleted. Orchestrators are
// not work.
func (s *Store) LifetimeWorkSessions(ctx context.Context) (int64, error) {
	var count int64
	err := s.readDB.QueryRowContext(ctx, `SELECT count FROM telemetry_lifetime_work_sessions WHERE singleton = 1`).Scan(&count)
	return count, err
}
