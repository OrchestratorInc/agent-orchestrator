package store

func (s *Store) HoldWriterForTest() func() {
	s.writeMu.Lock()
	return s.writeMu.Unlock
}
