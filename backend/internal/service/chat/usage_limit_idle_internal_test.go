package chat

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// blockingAutoResumeStore holds AutoResumeTurns until release closes, so a
// test can arm a turn while a scan reads. Only the usage-limit methods are
// implemented; the embedded Store is nil.
type blockingAutoResumeStore struct {
	Store
	started chan struct{}
	release chan struct{}
}

func (s *blockingAutoResumeStore) AutoResumeTurns(context.Context) ([]domain.ConversationTurn, error) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-s.release
	return nil, nil
}

func (s *blockingAutoResumeStore) SetTurnAutoResume(context.Context, string, string, *time.Time, bool, string) error {
	return nil
}

func (s *blockingAutoResumeStore) PostponeTurnAutoResume(context.Context, string, string, time.Time) (bool, error) {
	return false, nil
}

func (s *blockingAutoResumeStore) LatestAutoResumeTurn(context.Context, string) (domain.ConversationTurn, error) {
	return domain.ConversationTurn{}, nil
}

func (s *blockingAutoResumeStore) AppendAutoResumeRetryUserMessage(context.Context, string, domain.SessionID, string, domain.ConversationMessage, string, string, time.Time) (bool, error) {
	return false, nil
}

// A scan that read before a turn was armed may have missed it, so it must not
// mark the resume loop idle; only a scan that started after the arm may.
func TestAutoResumeScanOverlappingAnArmDoesNotGoIdle(t *testing.T) {
	store := &blockingAutoResumeStore{started: make(chan struct{}, 1), release: make(chan struct{})}
	svc := New(Options{Store: store, Now: time.Now})
	scanned := make(chan error, 1)
	go func() { scanned <- svc.ResumeUsageLimitedTurns(context.Background()) }()
	<-store.started

	svc.autoResumeMu.Lock()
	svc.noteAutoResumeArmedLocked() // what SetTurnAutoResume records after arming
	svc.autoResumeMu.Unlock()
	close(store.release)
	if err := <-scanned; err != nil {
		t.Fatal(err)
	}
	if svc.AutoResumeIdle() {
		t.Fatal("a scan that overlapped an arm marked the resume loop idle")
	}

	if err := svc.ResumeUsageLimitedTurns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !svc.AutoResumeIdle() {
		t.Fatal("a scan that started after the arm and found nothing should leave the loop idle")
	}
}
