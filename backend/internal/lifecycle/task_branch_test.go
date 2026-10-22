package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// heldReadStore pauses the first row read so a test can hold a lifecycle
// read-modify-write between its read and its write.
type heldReadStore struct {
	*fakeStore
	read, release chan struct{}
	held          bool
}

func (s *heldReadStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok, err := s.fakeStore.GetSession(ctx, id)
	if !s.held {
		s.held = true
		close(s.read)
		<-s.release
	}
	return rec, ok, err
}

func (s *heldReadStore) SetSessionTaskBranch(_ context.Context, id domain.SessionID, expected, branch string, _ time.Time) (bool, error) {
	rec, ok := s.sessions[id]
	if !ok || rec.Metadata.Branch != expected {
		return false, nil
	}
	rec.Metadata.Branch = branch
	s.sessions[id] = rec
	return true, nil
}

// A background branch rename waits for an in-flight row rewrite, so that
// rewrite cannot put the temporary branch back.
func TestSetTaskBranchSerializesWithRowRewrites(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.Branch = "ao/mer-1/root"
	st.sessions[rec.ID] = rec
	store := &heldReadStore{fakeStore: st, read: make(chan struct{}), release: make(chan struct{})}
	m.store = store
	rewrote := make(chan error, 1)
	go func() {
		rewrote <- m.mutate(context.Background(), rec.ID, func(cur domain.SessionRecord, _ time.Time) (domain.SessionRecord, bool) {
			cur.Activity.State = domain.ActivityIdle
			return cur, true
		})
	}()
	<-store.read
	renamed := make(chan bool, 1)
	go func() {
		ok, err := m.SetTaskBranch(context.Background(), rec.ID, "ao/mer-1/root", "ao/login/root")
		if err != nil {
			t.Error(err)
		}
		renamed <- ok
	}()
	select {
	case <-renamed:
		t.Fatal("rename did not wait for the row rewrite")
	case <-time.After(50 * time.Millisecond):
	}
	close(store.release)
	if err := <-rewrote; err != nil {
		t.Fatal(err)
	}
	if !<-renamed || st.sessions[rec.ID].Metadata.Branch != "ao/login/root" {
		t.Fatalf("branch = %q", st.sessions[rec.ID].Metadata.Branch)
	}
}
