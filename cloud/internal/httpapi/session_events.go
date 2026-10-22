package httpapi

import "time"

var (
	// sessionEventFallbackPoll is how often a session event stream re-reads
	// PostgreSQL when the ao_session_event NOTIFY listener is running: only a
	// lost notification waits for it.
	sessionEventFallbackPoll = 3 * time.Second
	// sessionEventPoll is the stream's only cadence when no listener runs.
	sessionEventPoll = time.Second
	// sessionEventMinInterval batches a burst of wakes (assistant output
	// streams many small deltas) into at most one replay query per interval
	// for each open stream.
	sessionEventMinInterval = 100 * time.Millisecond
)

// HandleSessionEventNotify is the Postgres NOTIFY callback for
// ao_session_event; the payload is the session id whose event log just grew.
// Every replica runs its own listener, so each wakes only its own streams.
func (s *Server) HandleSessionEventNotify(sessionID string) {
	if s.sessionEventWaiters != nil {
		s.sessionEventWaiters.notify(sessionID)
	}
}

// EnableSessionEventNotify records that an ao_session_event listener is
// running, so event streams can rely on wakes and poll only as a fallback.
func (s *Server) EnableSessionEventNotify() {
	s.sessionEventNotify.Store(true)
}

// subscribeSessionEvents returns the session's wake channel and the stream's
// fallback poll interval. A server without a waiter registry (tests) gets a
// channel that never fires and the plain poll.
func (s *Server) subscribeSessionEvents(sessionID string) (<-chan struct{}, time.Duration, func()) {
	if s.sessionEventWaiters == nil {
		return nil, sessionEventPoll, func() {}
	}
	wake, cancel := s.sessionEventWaiters.subscribe(sessionID)
	if s.sessionEventNotify.Load() {
		return wake, sessionEventFallbackPoll, cancel
	}
	return wake, sessionEventPoll, cancel
}
