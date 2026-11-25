package turnfailure

import (
	"context"
	"errors"
	"fmt"
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
	ackErr   error
}

func (s *fakeStore) ListDueWorkerTurnFailures(_ context.Context, now time.Time, _ int64) ([]domain.WorkerTurnFailure, error) {
	if s.accepted || (!s.next.IsZero() && now.Before(s.next)) {
		return nil, nil
	}
	return []domain.WorkerTurnFailure{s.row}, nil
}
func (s *fakeStore) BindWorkerTurnFailureTarget(_ context.Context, id string, target domain.SessionID) (bool, error) {
	if id != s.row.TurnID || s.accepted || s.row.TargetSessionID != "" {
		return false, nil
	}
	s.row.TargetSessionID = target
	return true, nil
}
func (s *fakeStore) ReleaseWorkerTurnFailureTarget(_ context.Context, id string, target domain.SessionID) (bool, error) {
	if id != s.row.TurnID || s.accepted || s.row.TargetSessionID != target {
		return false, nil
	}
	s.row.TargetSessionID = ""
	return true, nil
}
func (s *fakeStore) AcknowledgeWorkerTurnFailure(_ context.Context, id string, _ time.Time) error {
	if id != s.row.TurnID {
		return errors.New("wrong turn")
	}
	if s.ackErr != nil {
		return s.ackErr
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
	d := &fakeDelivery{err: fmt.Errorf("%w: lost reply", ErrUnacknowledgedDelivery)}
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
		t.Fatal("expected uncertain delivery")
	}
	if d.key != "worker-turn-failed:turn-1" || d.id != "orchestrator" || s.row.TargetSessionID != "orchestrator" {
		t.Fatalf("delivery identity = %s %s, pinned %q", d.id, d.key, s.row.TargetSessionID)
	}
	if strings.Contains(d.message, "secret") || !strings.Contains(d.message, "partially landed") || !strings.Contains(d.message, "timeout") {
		t.Fatalf("unsafe or incomplete message: %q", d.message)
	}
	// While the pinned orchestrator is still live, a newer one must not get a
	// second copy of a message the first may already have accepted.
	s.sessions = append(s.sessions, domain.SessionRecord{ID: "newer", Kind: domain.KindOrchestrator, CreatedAt: now.Add(2 * time.Hour)})
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
	if d.id != "orchestrator" {
		t.Fatalf("uncertain retry moved to %s", d.id)
	}
	if err := restarted.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if d.calls != 2 {
		t.Fatalf("duplicate accepted delivery: %d", d.calls)
	}
}

// A definitive refusal was never accepted, so the next attempt goes to
// whichever orchestrator is newest by then.
func TestCoordinatorRefusedSendDoesNotPinRecipient(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s := &fakeStore{
		row:      domain.WorkerTurnFailure{TurnID: "turn-refused", ProjectID: "p"},
		sessions: []domain.SessionRecord{{ID: "first", Kind: domain.KindOrchestrator, CreatedAt: now}},
	}
	d := &fakeDelivery{err: errors.New("composer busy")}
	c := New(s, d, nil)
	c.now = func() time.Time { return now }
	if err := c.RunDue(ctx); err == nil {
		t.Fatal("expected refused delivery")
	}
	if s.row.TargetSessionID != "" || d.id != "first" {
		t.Fatalf("refused send pinned %q (sent to %s)", s.row.TargetSessionID, d.id)
	}
	s.sessions = []domain.SessionRecord{
		{ID: "first", Kind: domain.KindOrchestrator, IsTerminated: true, CreatedAt: now},
		{ID: "replacement", Kind: domain.KindOrchestrator, CreatedAt: now.Add(time.Hour)},
	}
	d.err = nil
	now = s.next
	if err := c.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if !s.accepted || d.id != "replacement" {
		t.Fatalf("accepted=%v sent to %s", s.accepted, d.id)
	}
}

// A pinned orchestrator that was terminated cannot act on the notice, so the
// pin is released and the replacement gets it.
func TestCoordinatorRetargetsWhenPinnedOrchestratorIsTerminated(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s := &fakeStore{
		row: domain.WorkerTurnFailure{TurnID: "turn-dead", ProjectID: "p", TargetSessionID: "original"},
		sessions: []domain.SessionRecord{
			{ID: "original", Kind: domain.KindOrchestrator, IsTerminated: true, CreatedAt: now},
			{ID: "replacement", Kind: domain.KindOrchestrator, CreatedAt: now.Add(time.Hour)},
		},
	}
	d := &fakeDelivery{}
	c := New(s, d, nil)
	c.now = func() time.Time { return now }
	if err := c.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if !s.accepted || d.calls != 1 || d.id != "replacement" || s.row.TargetSessionID != "replacement" {
		t.Fatalf("accepted=%v calls=%d sent to %s pinned %q", s.accepted, d.calls, d.id, s.row.TargetSessionID)
	}
}

func TestFailureReasonMatchesWholeWords(t *testing.T) {
	for _, tc := range []struct {
		failure domain.WorkerTurnFailure
		want    string
	}{
		{domain.WorkerTurnFailure{ErrorMessage: "Upstream idle timeout exceeded"}, "provider timeout"},
		{domain.WorkerTurnFailure{ErrorMessage: "context deadline exceeded"}, "provider timeout"},
		{domain.WorkerTurnFailure{ErrorMessage: "401 Unauthorized"}, "authentication error"},
		{domain.WorkerTurnFailure{FailureCode: domain.ConversationFailureAuthRequired, ErrorMessage: "refused"}, "authentication error"},
		{domain.WorkerTurnFailure{FailureCode: domain.ConversationFailureProviderKeyMissing}, "missing provider API key"},
		{domain.WorkerTurnFailure{ErrorMessage: "oauth callback server crashed"}, "provider error"},
		{domain.WorkerTurnFailure{ErrorMessage: "could not read the author of commit abc"}, "provider error"},
	} {
		if got := failureReason(tc.failure); got != tc.want {
			t.Errorf("failureReason(%+v) = %q, want %q", tc.failure, got, tc.want)
		}
	}
}

func TestCoordinatorKeepsRecipientAfterLostAcknowledgement(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s := &fakeStore{
		row:      domain.WorkerTurnFailure{TurnID: "turn-ack", ProjectID: "project-1"},
		sessions: []domain.SessionRecord{{ID: "original", Kind: domain.KindOrchestrator, CreatedAt: now}},
		ackErr:   errors.New("acknowledgement response lost"),
	}
	d := &fakeDelivery{}
	c := New(s, d, nil)
	c.now = func() time.Time { return now }
	if err := c.RunDue(ctx); err == nil {
		t.Fatal("expected acknowledgement error")
	}
	if s.row.TargetSessionID != "original" || s.accepted || d.calls != 1 {
		t.Fatalf("after uncertain acknowledgement: row=%+v accepted=%v calls=%d", s.row, s.accepted, d.calls)
	}
	key := d.key
	s.sessions = append(s.sessions, domain.SessionRecord{ID: "newer", Kind: domain.KindOrchestrator, CreatedAt: now.Add(time.Hour)})
	s.ackErr = nil
	restarted := New(s, d, nil)
	restarted.now = func() time.Time { return now.Add(time.Second) }
	if err := restarted.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if !s.accepted || d.calls != 2 || d.id != "original" || d.key != key {
		t.Fatalf("retry moved recipient or identity: accepted=%v delivery=%+v", s.accepted, d)
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
