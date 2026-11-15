package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Real SQL operations reject an expired context; the ordinary fake ignores it.
type contextRespectingStore struct{ *fakeStore }

func (s *contextRespectingStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.SessionRecord{}, false, err
	}
	return s.fakeStore.GetSession(ctx, id)
}
func (s *contextRespectingStore) UpdateSession(ctx context.Context, rec domain.SessionRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.fakeStore.UpdateSession(ctx, rec)
}

type editDuringStartRuntime struct {
	*fakeRuntime
	store *fakeStore
	calls int
}

func (r *editDuringStartRuntime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	r.calls++
	if r.calls == 1 {
		rec := r.store.sessions[cfg.SessionID]
		rec.DisplayName, rec.IsPinned = "renamed while target starts", true
		rec.Metadata.PreviewURL, rec.Metadata.PreviewRevision = "http://localhost:4321", 42
		r.store.sessions[rec.ID] = rec
		<-ctx.Done()
		return ports.RuntimeHandle{}, ctx.Err()
	}
	return r.fakeRuntime.Create(ctx, cfg)
}

func TestTimedOutTerminalSwitchRollbackKeepsEditsMadeDuringIt(t *testing.T) {
	oldLimit := tuiSwitchAfterStopLimit
	tuiSwitchAfterStopLimit = 20 * time.Millisecond
	t.Cleanup(func() { tuiSwitchAfterStopLimit = oldLimit })
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rec := mkLive("mer-1")
	rec.Harness, rec.Mode = domain.HarnessClaudeCode, domain.SessionModeTUI
	rec.Activity.State = domain.ActivityExited
	rec.DisplayName = "original name"
	rec.Metadata.Branch, rec.Metadata.AgentSessionID, rec.Metadata.RuntimeLaunchID = "branch", "source-native", "source-launch"
	st.sessions[rec.ID] = rec
	store := &contextRespectingStore{fakeStore: st}
	runtime := &editDuringStartRuntime{fakeRuntime: &fakeRuntime{}, store: st}
	agent := &recordingAgent{}
	manager := New(Deps{Runtime: runtime, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: store,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})
	err := manager.SwitchTUIProvider(context.Background(), rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessCodex, HandoffNote: "handoff"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, want target timeout", err)
	}
	got := st.sessions[rec.ID]
	// The preview is kept too, by the lifecycle's metadata merge, which this fake does not do.
	if got.DisplayName != "renamed while target starts" || !got.IsPinned {
		t.Fatalf("rollback lost concurrent user edits: %+v", got)
	}
}
