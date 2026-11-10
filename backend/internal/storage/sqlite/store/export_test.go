package store

// HoldWriterForTest takes the single writer until the returned func runs.
func (s *Store) HoldWriterForTest() func() {
	s.writeMu.Lock()
	return s.writeMu.Unlock
}
