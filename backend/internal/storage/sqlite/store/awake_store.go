package store

import "context"

// LoadAwake reads the durable wake document.
func (s *Store) LoadAwake(ctx context.Context) (string, error) {
	return s.qr.GetAwakeDocument(ctx)
}

// SaveAwake replaces the durable wake document.
func (s *Store) SaveAwake(ctx context.Context, document string) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.qw.SaveAwakeDocument(ctx, document)
}

// HasAwakeAgentActivity reports whether an AO agent has active work.
func (s *Store) HasAwakeAgentActivity(ctx context.Context) (bool, error) {
	count, err := s.qr.HasAwakeAgentActivity(ctx)
	return count != 0, err
}
