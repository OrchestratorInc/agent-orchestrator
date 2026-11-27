package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestVersionMoveRecoversAcrossManagerRecreation(t *testing.T) {
	ctx := context.Background()
	rt := &refreshTestRuntime{fakeRestartRuntime: &fakeRestartRuntime{fakeRuntime: &fakeRuntime{
		aliveByHandle: map[string]bool{"tmux-mer-1": true},
		fencedResult:  ports.FencedProbeResult{Liveness: ports.FencedDead},
	}}}
	agent := refreshTestAgent{supervisedLaunchAgent{launchArgvAgent{argv: []string{"pi", "--session", "agent-x"}}}}
	before, base, _ := newExitedResumeManager(t, rt, agent)
	before.launchStartWait = time.Millisecond
	store := &versionRecoveryStore{refreshTestStore: &refreshTestStore{fakeStore: base}}
	before.store = store
	before.SetTerminalInputGate(&refreshInputGate{})
	rec := base.sessions["mer-1"]
	rec.Harness = domain.HarnessPi
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now().Add(-time.Hour)}
	rec.Metadata.AgentSessionIDLaunchID = rec.Metadata.RuntimeLaunchID
	rec.StaleBinary = &domain.SessionStaleBinary{Version: "new", Since: time.Now().Add(-time.Hour)}
	base.sessions[rec.ID] = rec
	if err := before.RefreshStaleTerminals(ctx, 10*time.Minute); err == nil {
		t.Fatal("fixture did not fail the replacement")
	}
	failed := base.sessions[rec.ID]
	if failed.StaleBinary == nil || failed.StaleBinary.SwitchFailedAt == nil || rt.restarted != 1 {
		t.Fatalf("failure not established: restarts=%d record=%+v", rt.restarted, failed)
	}
	newRT := &refreshTestRuntime{fakeRestartRuntime: &fakeRestartRuntime{fakeRuntime: &fakeRuntime{
		aliveByHandle: map[string]bool{"tmux-mer-1": true},
	}}}
	after, _, _ := newExitedResumeManager(t, newRT, agent)
	after.store = store
	after.lcm = &fakeLCM{store: base}
	if err := after.RetryVersionMoves(ctx); err != nil {
		t.Fatal(err)
	}
	got := base.sessions[rec.ID]
	if newRT.restarted != 1 || newRT.created != 0 || got.Metadata.RuntimeLaunchID == rec.Metadata.RuntimeLaunchID || len(store.moves) != 0 {
		t.Fatalf("retry lost on recreation: restarts=%d launch=%s stale=%+v", newRT.restarted, got.Metadata.RuntimeLaunchID, got.StaleBinary)
	}
	if got.Metadata.AgentSessionID != rec.Metadata.AgentSessionID || got.ID != rec.ID || got.IsTerminated {
		t.Fatalf("native session changed: %+v", got)
	}
}

func TestVersionMoveRecoveryRequiresDurableIntent(t *testing.T) {
	rt := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"pi", "--session", "agent-x"}}}
	mgr, base, _ := newExitedResumeManager(t, rt, agent)
	mgr.store = &versionRecoveryStore{refreshTestStore: &refreshTestStore{fakeStore: base}}
	// A late callback after completion must not turn an obsolete, memory-only
	// session ID into permission to resume a changed controller.
	mgr.versionRetries.note("mer-1")
	if err := mgr.RetryVersionMoves(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rt.restarted != 0 || rt.created != 0 || len(mgr.versionRetries.pending()) != 0 {
		t.Fatal("memory-only callback bypassed durable owner fencing")
	}
}

func TestVersionMoveJournalFailureLeavesSourceRunning(t *testing.T) {
	rt := &refreshTestRuntime{fakeRestartRuntime: &fakeRestartRuntime{fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}}}
	agent := refreshTestAgent{supervisedLaunchAgent{launchArgvAgent{argv: []string{"pi", "--session", "agent-x"}}}}
	mgr, base, _ := newExitedResumeManager(t, rt, agent)
	journalErr := errors.New("journal unavailable")
	store := &versionRecoveryStore{refreshTestStore: &refreshTestStore{fakeStore: base}, putErr: journalErr}
	mgr.store = store
	mgr.SetTerminalInputGate(&refreshInputGate{})
	rec := base.sessions["mer-1"]
	rec.Harness = domain.HarnessPi
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now().Add(-time.Hour)}
	rec.Metadata.AgentSessionIDLaunchID = rec.Metadata.RuntimeLaunchID
	rec.StaleBinary = &domain.SessionStaleBinary{Version: "new", Since: time.Now()}
	base.sessions[rec.ID] = rec
	if err := mgr.RefreshStaleTerminals(context.Background(), time.Minute); !errors.Is(err, journalErr) {
		t.Fatalf("refresh = %v", err)
	}
	if rt.restarted != 0 || rt.destroyed != 0 || base.sessions[rec.ID].RuntimeReleased() {
		t.Fatal("source was touched before intent became durable")
	}
}

func TestVersionMoveRecoversStopBeforeReplacement(t *testing.T) {
	for _, stage := range []string{domain.VersionMoveStopping, domain.VersionMoveStarting, "stale stopping snapshot"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			rt := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}}
			agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"pi", "--session", "agent-x"}}}
			before, base, _ := newExitedResumeManager(t, rt, agent)
			rec := base.sessions["mer-1"]
			rec.Activity.State = domain.ActivityIdle
			rec.StaleBinary = &domain.SessionStaleBinary{Version: "new", Since: time.Now()}
			base.sessions[rec.ID] = rec
			store := &versionRecoveryStore{refreshTestStore: &refreshTestStore{fakeStore: base}}
			before.store = store
			if err := before.beginVersionMove(ctx, rec); err != nil {
				t.Fatal(err)
			}
			if err := before.releaseTerminalRuntime(ctx, rec); err != nil {
				t.Fatal(err)
			}
			if stage == "stale stopping snapshot" {
				store.afterList = func() { _ = store.SetSessionVersionMoveStage(ctx, rec.ID, domain.VersionMoveStarting) }
			} else if err := before.versionMoveStage(ctx, rec.ID, stage); err != nil {
				t.Fatal(err)
			}
			after, _, _ := newExitedResumeManager(t, rt, agent)
			after.store, after.lcm = store, &fakeLCM{store: base}
			if err := after.RetryVersionMoves(ctx); err != nil {
				t.Fatal(err)
			}
			if stage != domain.VersionMoveStopping {
				if rt.restarted != 0 || len(store.moves) != 1 {
					t.Fatal("uncommitted replacement may still be alive; automatic restart was unsafe")
				}
			} else if rt.restarted != 1 || len(store.moves) != 0 || base.sessions[rec.ID].Metadata.AgentSessionID != "agent-x" {
				t.Fatalf("confirmed stop was not recovered: restarts=%d moves=%+v", rt.restarted, store.moves)
			}
		})
	}
}

type versionRecoveryStore struct {
	*refreshTestStore
	moves     map[domain.SessionID]domain.SessionVersionMove
	putErr    error
	afterList func()
}

func (s *versionRecoveryStore) PutSessionVersionMove(_ context.Context, move domain.SessionVersionMove) error {
	if s.putErr != nil {
		return s.putErr
	}
	if s.moves == nil {
		s.moves = make(map[domain.SessionID]domain.SessionVersionMove)
	}
	if _, exists := s.moves[move.SessionID]; exists {
		return errors.New("version move already exists")
	}
	s.moves[move.SessionID] = move
	return nil
}
func (s *versionRecoveryStore) ListSessionVersionMoves(context.Context) ([]domain.SessionVersionMove, error) {
	moves := make([]domain.SessionVersionMove, 0, len(s.moves))
	for _, move := range s.moves {
		moves = append(moves, move)
	}
	if s.afterList != nil {
		hook := s.afterList
		s.afterList = nil
		hook()
	}
	return moves, nil
}
func (s *versionRecoveryStore) SetSessionVersionMoveStage(_ context.Context, id domain.SessionID, stage string) error {
	if move, ok := s.moves[id]; ok {
		move.Stage = stage
		if stage == domain.VersionMoveRetry {
			move.SourceLaunch = domain.NewSessionVersionMove(s.sessions[id]).SourceLaunch
		}
		s.moves[id] = move
	}
	return nil
}
func (s *versionRecoveryStore) AttemptSessionVersionMove(_ context.Context, id domain.SessionID, limit int) (bool, error) {
	move, ok := s.moves[id]
	if !ok || move.Stage != domain.VersionMoveRetry || move.Attempts >= limit {
		return false, nil
	}
	move.Attempts++
	s.moves[id] = move
	return true, nil
}
func (s *versionRecoveryStore) DeleteSessionVersionMove(ctx context.Context, id domain.SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(s.moves, id)
	return nil
}

func TestVersionMoveRecoveryKeepsUncertainOwnership(t *testing.T) {
	for _, stage := range []string{domain.VersionMoveStopping, domain.VersionMoveStarting, domain.VersionMoveRetry} {
		t.Run(stage, func(t *testing.T) {
			rt := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{fencedResult: ports.FencedProbeResult{Liveness: ports.FencedUnknown}}}
			mgr, base, _ := newExitedResumeManager(t, rt, fakeAgent{})
			rec := base.sessions["mer-1"]
			rec.StaleBinary = &domain.SessionStaleBinary{Version: "new", Since: time.Now()}
			base.sessions[rec.ID] = rec
			move := domain.NewSessionVersionMove(rec)
			move.Stage = stage
			store := &versionRecoveryStore{refreshTestStore: &refreshTestStore{fakeStore: base}, moves: map[domain.SessionID]domain.SessionVersionMove{rec.ID: move}}
			mgr.store = store
			mgr.versionRetries.note(rec.ID) // even a same-manager callback cannot bypass the journal
			if err := mgr.RetryVersionMoves(context.Background()); err != nil {
				t.Fatal(err)
			}
			if rt.restarted != 0 || rt.created != 0 || len(store.moves) != 1 || store.moves[rec.ID].Attempts != 0 {
				t.Fatalf("uncertain move launched or lost: restarted=%d created=%d moves=%+v", rt.restarted, rt.created, store.moves)
			}
			if stage != domain.VersionMoveRetry && base.sessions[rec.ID].StaleBinary.SwitchFailedAt == nil {
				t.Fatal("interrupted move has no actionable failure marker")
			}
		})
	}
}

func TestVersionMoveRetryBudgetSurvivesRecreation(t *testing.T) {
	ctx := context.Background()
	rt := &fakeRuntime{fencedResult: ports.FencedProbeResult{Liveness: ports.FencedDead}}
	agent := lostConversationAgent{recordingAgent: &recordingAgent{}}
	mgr, base, _ := newExitedResumeManager(t, rt, agent)
	rec := base.sessions["mer-1"]
	move := domain.NewSessionVersionMove(rec)
	move.Stage, move.Attempts = domain.VersionMoveRetry, maxVersionResumeAttempts-1
	store := &versionRecoveryStore{refreshTestStore: &refreshTestStore{fakeStore: base}, moves: map[domain.SessionID]domain.SessionVersionMove{rec.ID: move}}
	mgr.store = store
	if err := mgr.RetryVersionMoves(ctx); !errors.Is(err, ErrNotResumable) {
		t.Fatalf("last retry = %v", err)
	}
	after, _, _ := newExitedResumeManager(t, rt, agent)
	after.store, after.lcm = store, &fakeLCM{store: base}
	if err := after.RetryVersionMoves(ctx); err != nil {
		t.Fatal(err)
	}
	if got := store.moves[rec.ID].Attempts; got != maxVersionResumeAttempts || rt.created != 0 {
		t.Fatalf("budget reset or fresh session launched: attempts=%d created=%d", got, rt.created)
	}
	if base.sessions[rec.ID].StaleBinary == nil || base.sessions[rec.ID].StaleBinary.SwitchFailedAt == nil {
		t.Fatal("exhausted move lost its recovery notice")
	}
}

func TestUpdateStopRecoversChatAcrossManagerRecreation(t *testing.T) {
	ctx := context.Background()
	before, base, _ := newChatManager(&recordingLauncher{})
	seedChatResumeSession(base, domain.ActivityIdle)
	rec := base.sessions["mer-1"]
	store := &versionRecoveryStore{refreshTestStore: &refreshTestStore{fakeStore: base}}
	before.store = store
	if err := before.beginVersionMove(ctx, rec); err != nil {
		t.Fatal(err)
	}
	// Model a confirmed update stop before the install/resume batch loses its
	// in-memory stopped IDs. No stale-binary marker exists for in-place updates.
	at := time.Now()
	rec.HibernatedAt = &at
	base.sessions[rec.ID] = rec
	if err := before.versionMoveStage(ctx, rec.ID, domain.VersionMoveRetry); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	launcher.providerConversationID = rec.Metadata.ProviderConversationID
	launcher.afterReady = func() { launcher.live = true }
	after, _, _ := newChatManager(launcher)
	after.store, after.lcm = store, &fakeLCM{store: base}
	if err := after.RetryVersionMoves(ctx); err != nil {
		t.Fatal(err)
	}
	if len(launcher.started) != 1 || len(launcher.drained) != 1 || len(store.moves) != 0 {
		t.Fatalf("lost update stop or queued delivery: starts=%d drains=%v moves=%+v", len(launcher.started), launcher.drained, store.moves)
	}
	start := launcher.started[0]
	if start.ProviderConversationID != rec.Metadata.ProviderConversationID || start.FreshIfProviderConversationMissing || start.HistoryMode != ports.ChatHistoryRequired {
		t.Fatalf("recovery did not require the same native conversation: %+v", start)
	}
}
