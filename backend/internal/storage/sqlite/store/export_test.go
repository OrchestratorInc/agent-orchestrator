package store

// HoldWriter takes the single writer lock the way a long write does and
// returns its release.
func (s *Store) HoldWriter() (release func()) {
	s.writeMu.Lock()
	return s.writeMu.Unlock
}
