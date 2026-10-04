package sessionmanager

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type effortUpdateBarrierStore struct {
	*sqlite.Store
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *effortUpdateBarrierStore) pause() {
	s.once.Do(func() {
		close(s.entered)
		<-s.release
	})
}

func (s *effortUpdateBarrierStore) UpdateSessionEffort(ctx context.Context, id domain.SessionID, effort string) (bool, error) {
	s.pause()
	return s.Store.UpdateSessionEffort(ctx, id, effort)
}

func TestPersistChatEffortPreservesConcurrentLifecycleUpdate(t *testing.T) {
	testCtx := context.Background()
	base := sqlitetest.MustOpen(t)
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	if err := base.UpsertProject(testCtx, domain.ProjectRecord{
		ID: "repro", Path: t.TempDir(), RegisteredAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	created, err := base.CreateSession(testCtx, domain.SessionRecord{
		ProjectID: "repro",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Mode:      domain.SessionModeChat,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata: domain.SessionMetadata{
			RuntimeHandleID:        "runtime-old",
			AgentSessionID:         "native-1",
			ProviderConversationID: "provider-old",
			ControllerGeneration:   "generation-old",
		},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	blocked := &effortUpdateBarrierStore{
		Store: base, entered: make(chan struct{}), release: make(chan struct{}),
	}
	manager := &Manager{store: blocked}
	done := make(chan error, 1)
	go func() {
		done <- manager.PersistChatEffort(testCtx, created.ID, "high")
	}()

	select {
	case <-blocked.entered:
	case err := <-done:
		t.Fatalf("effort update returned before the barrier: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("effort update did not reach the barrier")
	}

	newer, ok, err := base.GetSession(testCtx, created.ID)
	if err != nil || !ok {
		t.Fatalf("read concurrent state: ok=%v err=%v", ok, err)
	}
	newer.IsTerminated = true
	newer.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: now.Add(time.Second)}
	newer.Metadata.RuntimeHandleID = ""
	newer.Metadata.ProviderConversationID = "provider-new"
	newer.Metadata.ControllerGeneration = "generation-new"
	newer.UpdatedAt = now.Add(time.Second)
	if err := base.UpdateSession(testCtx, newer); err != nil {
		t.Fatal(err)
	}

	close(blocked.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	got, ok, err := base.GetSession(testCtx, created.ID)
	if err != nil || !ok {
		t.Fatalf("read result: ok=%v err=%v", ok, err)
	}
	if got.Metadata.Effort != "high" || !got.IsTerminated ||
		got.Activity.State != domain.ActivityExited || got.Metadata.RuntimeHandleID != "" ||
		got.Metadata.ProviderConversationID != "provider-new" ||
		got.Metadata.ControllerGeneration != "generation-new" {
		t.Fatalf("effort update overwrote concurrent lifecycle state: %+v", got)
	}
}

func TestPersistChatEffortClearsAndTrimsSelection(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	st.sessions["effort"] = domain.SessionRecord{ID: "effort", Metadata: domain.SessionMetadata{Model: "chosen", Effort: "medium"}}
	manager := &Manager{store: st}
	for _, picked := range []string{" high ", ""} {
		if err := manager.PersistChatEffort(ctx, "effort", picked); err != nil {
			t.Fatal(err)
		}
		want := "high"
		if picked == "" {
			want = ""
		}
		rec := st.sessions["effort"]
		if rec.Metadata.Effort != want || rec.Metadata.Model != "chosen" {
			t.Fatalf("metadata = %#v, want effort %q", rec.Metadata, want)
		}
	}
	if err := manager.PersistChatEffort(ctx, "missing", "high"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing session = %v", err)
	}
}
