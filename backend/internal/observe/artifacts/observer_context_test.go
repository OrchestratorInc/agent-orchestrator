package artifacts

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type contextRecordingSessions struct{ cancellable []bool }

func (s *contextRecordingSessions) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	s.cancellable = append(s.cancellable, ctx.Done() != nil)
	return nil, nil
}

// A cancellable context makes the SQLite driver and database/sql start two
// watcher goroutines per query; the observer lists sessions every tick and
// must not pay that.
func TestPollListsWithoutCancellableContext(t *testing.T) {
	sessions := &contextRecordingSessions{}
	o := New(sessions, &fakeSink{}, nil, Config{})
	pollCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := o.Poll(pollCtx); err != nil {
		t.Fatal(err)
	}
	if len(sessions.cancellable) != 1 || sessions.cancellable[0] {
		t.Fatalf("sessions listed with a cancellable context: %v", sessions.cancellable)
	}
}
