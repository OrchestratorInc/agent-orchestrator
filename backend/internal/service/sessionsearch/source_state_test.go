package sessionsearch

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// sourceContextStore records whether each source read could be cancelled. Only
// GetSessionSearchSource is implemented; the embedded Store is nil.
type sourceContextStore struct {
	Store
	cancellable []bool
}

func (s *sourceContextStore) GetSessionSearchSource(ctx context.Context, _ string) (domain.SessionSearchSource, bool, error) {
	s.cancellable = append(s.cancellable, ctx.Done() != nil)
	return domain.SessionSearchSource{}, false, nil
}

// A sweep reads every conversation's source state. A cancellable context makes
// the SQLite driver and database/sql start two watcher goroutines per query, so
// the read must not use one.
func TestSourceStateReadsWithoutCancellableContext(t *testing.T) {
	store := &sourceContextStore{}
	svc := New(Options{Store: store})
	readCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, _, err := svc.sourceState(readCtx, "native:claude:abc"); err != nil {
		t.Fatal(err)
	}
	if len(store.cancellable) != 1 || store.cancellable[0] {
		t.Fatalf("source state read with a cancellable context: %v", store.cancellable)
	}
}
