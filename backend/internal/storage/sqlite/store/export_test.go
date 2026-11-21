package store

// HoldWriterForTest takes the single writer until the returned func runs.
func (s *Store) HoldWriterForTest() func() {
	s.writeMu.Lock()
	return s.writeMu.Unlock
}

// TerminalActivityStatus is what settling a turn writes over its running rows.
var TerminalActivityStatus = terminalActivityStatus
