package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionmove"
)

type moveStore struct{ *fakeStore }

func (m moveStore) SetSessionMovedTo(_ context.Context, id domain.SessionID, move domain.SessionMove, _ time.Time) (bool, error) {
	rec := m.sessions[id]
	rec.MovedTo = &move
	m.sessions[id] = rec
	return true, nil
}

// fakeStore counts these calls without a lock, and the orchestrator notice
// reads sessions on its own goroutine. No session here has pull requests.
func (moveStore) ListPRFactsForSession(context.Context, domain.SessionID) ([]domain.PRFacts, error) {
	return nil, nil
}

func (moveStore) ListPRFactsForSessions(context.Context, []domain.SessionID) (map[domain.SessionID][]domain.PRFacts, error) {
	return nil, nil
}

func TestCompleteMoveDoesNotWaitForTheOrchestrator(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker}
	release := make(chan struct{})
	told := make(chan string, 1)
	fc := &fakeCommander{sendFunc: func(id domain.SessionID, message string) error {
		<-release
		told <- string(id) + " " + message
		return nil
	}}
	svc := NewWithDeps(Deps{Manager: fc, Store: moveStore{st}})

	dest := domain.SessionMove{MoveID: "move-1", OrgID: "org-1", SessionID: "cloud-1"}
	if _, err := svc.CompleteMove(context.Background(), "mer-2", MoveCompleteInput{Destination: dest}); err != nil {
		t.Fatalf("CompleteMove: %v", err)
	}
	if st.sessions["mer-2"].MovedTo == nil || len(fc.killed) != 1 {
		t.Fatalf("CompleteMove returned before marking and archiving: moved=%v killed=%v", st.sessions["mer-2"].MovedTo, fc.killed)
	}
	close(release)
	select {
	case got := <-told:
		if !strings.HasPrefix(got, "mer-1 ") || !strings.Contains(got, "mer-2") || !strings.Contains(got, "cloud-1") {
			t.Fatalf("orchestrator message = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the orchestrator was not told")
	}
}

func TestMoveRefusalsNameTheFiles(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("%w: model.bin, data/weights.bin", sessionmove.ErrLFSObjects), "Git LFS files cannot move yet: model.bin, data/weights.bin."},
		{fmt.Errorf("%w: vendor/sub", sessionmove.ErrDirtySubmodule), "Commit or discard the changes inside vendor/sub first."},
		{&sessionmove.FileTooLargeError{Limit: 1, Files: []sessionmove.Blob{{Path: "big.bin"}, {Path: "big.bin"}, {Path: "data.csv"}}}, "Over GitHub's 100 MiB file limit: big.bin, data.csv."},
		{fmt.Errorf("%w: MERGE_HEAD exists", sessionmove.ErrOperationInProgress), "Finish or abort the merge, rebase or conflict in the worktree first."},
	} {
		var got *apierr.Error
		if !errors.As(moveAPIError(tc.err), &got) || got.Message != tc.want {
			t.Errorf("moveAPIError(%v) = %v, want message %q", tc.err, got, tc.want)
		}
	}
}
