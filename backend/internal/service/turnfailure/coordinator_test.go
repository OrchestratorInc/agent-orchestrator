package turnfailure

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeStore struct {
	row      domain.WorkerTurnFailure
	sessions []domain.SessionRecord
	accepted bool
	next     time.Time
	retries  int
}

func (s *fakeStore) ListDueWorkerTurnFailures(_ context.Context, now time.Time, _ int64) ([]domain.WorkerTurnFailure, error) {
	if s.accepted || (!s.next.IsZero() && now.Before(s.next)) {
		return nil, nil
	}
	return []domain.WorkerTurnFailure{s.row}, nil
}
func (s *fakeStore) AcknowledgeWorkerTurnFailure(_ context.Context, id string, _ time.Time) error {
	if id != s.row.TurnID {
		return errors.New("wrong turn")
	}
	s.accepted = true
	return nil
}
func (s *fakeStore) RetryWorkerTurnFailure(_ context.Context, id string, next time.Time, _ string) error {
	if id != s.row.TurnID {
		return errors.New("wrong turn")
	}
	s.next = next
	s.row.Attempts++
	s.retries++
	return nil
}
func (s *fakeStore) ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error) {
	return s.sessions, nil
}

type fakeDelivery struct {
	calls        int
	id           domain.SessionID
	key, message string
	err          error
	sent         chan struct{}
}

func (d *fakeDelivery) SendSemantic(_ context.Context, id domain.SessionID, message, key string) error {
	d.calls++
	d.id = id
	d.message = message
	d.key = key
	if d.sent != nil {
		close(d.sent)
	}
	return d.err
}

func TestCoordinatorRetriesWithStableKeyAndAcknowledgesOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s := &fakeStore{row: domain.WorkerTurnFailure{
		TurnID: "turn-1", SessionID: "worker-1", ProjectID: "project-1", DisplayName: "Worker",
		ErrorMessage: "JSON-RPC -32603: internal error; token=secret; 504 Gateway Timeout",
	}}
	d := &fakeDelivery{err: errors.New("not accepted")}
	c := New(s, d, nil)
	c.now = func() time.Time { return now }
	if err := c.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if d.calls != 0 || s.retries != 1 {
		t.Fatalf("absent orchestrator: calls=%d retries=%d", d.calls, s.retries)
	}
	s.sessions = []domain.SessionRecord{
		{ID: "old", Kind: domain.KindOrchestrator, IsTerminated: true, CreatedAt: now.Add(time.Hour)},
		{ID: "orchestrator", Kind: domain.KindOrchestrator, CreatedAt: now,
			Activity: domain.Activity{State: domain.ActivityActive}},
	}
	now = s.next
	if err := c.RunDue(ctx); err == nil {
		t.Fatal("expected delivery rejection")
	}
	if d.key != "worker-turn-failed:turn-1" || d.id != "orchestrator" {
		t.Fatalf("delivery identity = %s %s", d.id, d.key)
	}
	if strings.Contains(d.message, "secret") || !strings.Contains(d.message, "partially landed") || !strings.Contains(d.message, "timeout") {
		t.Fatalf("unsafe or incomplete message: %q", d.message)
	}
	d.err = nil
	now = s.next
	// This simulates a fresh daemon coordinator reading the same durable row.
	restarted := New(s, d, nil)
	restarted.now = func() time.Time { return now }
	if err := restarted.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if !s.accepted || d.calls != 2 {
		t.Fatalf("accepted=%v calls=%d", s.accepted, d.calls)
	}
	if err := restarted.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if d.calls != 2 {
		t.Fatalf("duplicate accepted delivery: %d", d.calls)
	}
}

func TestCoordinatorRetainsFailureWhenOrchestratorUnsupported(t *testing.T) {
	now := time.Now().UTC()
	s := &fakeStore{row: domain.WorkerTurnFailure{TurnID: "turn-2", ProjectID: "p"}, sessions: []domain.SessionRecord{
		{ID: "orchestrator", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, CreatedAt: now},
	}}
	d := &fakeDelivery{err: errors.New("unsupported")}
	c := New(s, d, nil)
	c.now = func() time.Time { return now }
	if err := c.RunDue(context.Background()); err == nil {
		t.Fatal("expected unsupported delivery")
	}
	if s.accepted || s.retries != 1 || d.calls != 1 {
		t.Fatalf("accepted=%v retries=%d calls=%d", s.accepted, s.retries, d.calls)
	}
}

func TestCoordinatorProcessesPendingRowOnStartup(t *testing.T) {
	now := time.Now().UTC()
	s := &fakeStore{row: domain.WorkerTurnFailure{TurnID: "turn-startup", ProjectID: "p"}, sessions: []domain.SessionRecord{
		{ID: "orchestrator", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, CreatedAt: now},
	}}
	d := &fakeDelivery{sent: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	c := New(s, d, nil)
	done := c.Start(ctx)
	select {
	case <-d.sent:
	case <-time.After(time.Second):
		t.Fatal("startup delivery did not run")
	}
	cancel()
	<-done
	if !s.accepted || d.calls != 1 {
		t.Fatalf("startup delivery accepted=%v calls=%d", s.accepted, d.calls)
	}
}
