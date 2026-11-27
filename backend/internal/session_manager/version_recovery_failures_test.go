package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestVersionMoveFailedChatStartCanRetryAcrossRecreation(t *testing.T) {
	for _, scenario := range []string{"failed start", "inconclusive claim", "live controller"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			startErr := errors.New("replacement failed")
			if scenario == "inconclusive claim" {
				startErr = errors.Join(startErr, ports.ErrChatRecoveryInconclusive)
			}
			launcher := &recordingLauncher{startErr: startErr}
			before, base, _ := newChatManager(launcher)
			seedChatResumeSession(base, domain.ActivityIdle)
			rec := base.sessions["mer-1"]
			rec.Metadata.ControllerGeneration = "source-chat"
			base.sessions[rec.ID] = rec
			store := &versionRecoveryStore{refreshTestStore: &refreshTestStore{fakeStore: base}}
			before.store = store
			if err := before.beginVersionMove(ctx, rec); err != nil {
				t.Fatal(err)
			}
			at := time.Now()
			rec.HibernatedAt = &at
			base.sessions[rec.ID] = rec
			if err := before.versionMoveStage(ctx, rec.ID, domain.VersionMoveRetry); err != nil {
				t.Fatal(err)
			}
			launcher.beforeStart = func(ChatStart) {
				claimed := base.sessions[rec.ID]
				claimed.Metadata.ControllerGeneration = "claim-before-publication"
				base.sessions[rec.ID] = claimed
				launcher.live = scenario == "live controller"
			}
			if err := before.ResumeHarnessSessions(ctx, []domain.SessionID{rec.ID}); !errors.Is(err, startErr) {
				t.Fatalf("failed actual start = %v", err)
			}
			wantStage := domain.VersionMoveStarting
			if scenario == "failed start" {
				wantStage = domain.VersionMoveRetry
				if err := before.RetryVersionMoves(ctx); !errors.Is(err, startErr) {
					t.Fatalf("automatic failed attempt = %v", err)
				}
				if store.moves[rec.ID].Attempts != 1 {
					t.Fatal("failed retry did not consume durable budget")
				}
			}
			if store.moves[rec.ID].Stage != wantStage {
				t.Fatalf("stage = %s, want %s", store.moves[rec.ID].Stage, wantStage)
			}
			recovered := &recordingLauncher{providerConversationID: rec.Metadata.ProviderConversationID}
			recovered.afterReady = func() { recovered.live = true }
			after, _, _ := newChatManager(recovered)
			after.store, after.lcm = store, &fakeLCM{store: base}
			if err := after.RetryVersionMoves(ctx); err != nil {
				t.Fatal(err)
			}
			if scenario != "failed start" {
				if len(recovered.started) != 0 || len(store.moves) != 1 {
					t.Fatal("unpublished/uncertain generation lost its intent or launched")
				}
				return
			}
			if len(recovered.started) != 1 || len(recovered.drained) != 1 || len(store.moves) != 0 {
				t.Fatal("confirmed failed start was not retried after recreation")
			}
			start := recovered.started[0]
			if start.ProviderConversationID != rec.Metadata.ProviderConversationID || start.FreshIfProviderConversationMissing || start.HistoryMode != ports.ChatHistoryRequired {
				t.Fatal("retry did not require the original native conversation")
			}
		})
	}
}

type versionFailureRuntime struct {
	*refreshTestRuntime
	probes      map[string]ports.FencedLiveness
	createCalls int
}

func (r *versionFailureRuntime) ProbeFencedRuntime(ctx context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	if live, ok := r.probes[ref.Generation]; ok {
		return ports.FencedProbeResult{Liveness: live}
	}
	return r.refreshTestRuntime.ProbeFencedRuntime(ctx, ref)
}
func (r *versionFailureRuntime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	r.createCalls++
	return r.refreshTestRuntime.Create(ctx, cfg)
}

func versionFailureManager(t *testing.T, rt runtimeController) (*Manager, *versionRecoveryStore, domain.SessionRecord) {
	t.Helper()
	agent := refreshTestAgent{supervisedLaunchAgent{launchArgvAgent{argv: []string{"pi", "--session", "agent-x"}}}}
	mgr, base, _ := newExitedResumeManager(t, rt, agent)
	mgr.launchStartWait = time.Millisecond
	store := &versionRecoveryStore{refreshTestStore: &refreshTestStore{fakeStore: base}}
	mgr.store = store
	mgr.SetTerminalInputGate(&refreshInputGate{})
	rec := base.sessions["mer-1"]
	rec.Harness = domain.HarnessPi
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now().Add(-time.Hour)}
	rec.Metadata.AgentSessionIDLaunchID = rec.Metadata.RuntimeLaunchID
	rec.StaleBinary = &domain.SessionStaleBinary{Version: "new", Since: time.Now().Add(-time.Hour)}
	base.sessions[rec.ID] = rec
	return mgr, store, rec
}

func TestVersionMoveRuntimeErrorsRetainOnlySafeRetries(t *testing.T) {
	for _, scenario := range []string{"restart dead", "create dead", "restart unknown", "create unknown", "source alive"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			attemptErr := errors.New("runtime start failed")
			rt := &versionFailureRuntime{refreshTestRuntime: &refreshTestRuntime{fakeRestartRuntime: &fakeRestartRuntime{fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}, restartErr: attemptErr}}, probes: map[string]ports.FencedLiveness{"launch-old": ports.FencedDead, "launch-new": ports.FencedDead}}
			create := scenario == "create dead" || scenario == "create unknown"
			unknown := scenario == "restart unknown" || scenario == "create unknown"
			if create {
				rt.aliveByHandle["tmux-mer-1"] = false
				rt.createErr = attemptErr
			}
			if unknown {
				rt.probes["launch-old"], rt.probes["launch-new"] = ports.FencedUnknown, ports.FencedUnknown
			}
			if scenario == "source alive" {
				rt.probes["launch-old"] = ports.FencedAlive
			}
			before, store, rec := versionFailureManager(t, rt)
			if err := before.RefreshStaleTerminals(ctx, time.Minute); !errors.Is(err, attemptErr) {
				t.Fatalf("actual runtime error = %v", err)
			}
			if create && rt.createCalls != 1 || !create && rt.restarted != 1 {
				t.Fatal("fixture did not execute the failed runtime attempt")
			}
			if scenario == "source alive" {
				if len(store.moves) != 0 {
					t.Fatal("untouched live source retained blocking intent")
				}
				return
			}
			stage := domain.VersionMoveRetry
			if unknown {
				stage = domain.VersionMoveStarting
			}
			if store.moves[rec.ID].Stage != stage {
				t.Fatalf("stage = %s, want %s", store.moves[rec.ID].Stage, stage)
			}
			newRT := &refreshTestRuntime{fakeRestartRuntime: &fakeRestartRuntime{fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": !create}}}}
			agent := refreshTestAgent{supervisedLaunchAgent{launchArgvAgent{argv: []string{"pi", "--session", "agent-x"}}}}
			after, _, _ := newExitedResumeManager(t, newRT, agent)
			after.store, after.lcm = store, &fakeLCM{store: store.fakeStore}
			if err := after.RetryVersionMoves(ctx); err != nil {
				t.Fatal(err)
			}
			if unknown {
				if newRT.restarted != 0 || newRT.created != 0 || len(store.moves) != 1 {
					t.Fatal("uncertain runtime error authorized a replacement")
				}
			} else if newRT.restarted+newRT.created != 1 || len(store.moves) != 0 || store.sessions[rec.ID].Metadata.AgentSessionID != rec.Metadata.AgentSessionID {
				t.Fatal("dead failed replacement did not recover natively")
			}
		})
	}
}

type versionReleaseErrorStore struct {
	*versionRecoveryStore
	releaseErr error
	cancel     context.CancelFunc
}

func (s *versionReleaseErrorStore) SetSessionRuntimeReleased(ctx context.Context, id domain.SessionID, launch string) (bool, error) {
	if s.releaseErr != nil {
		if s.cancel != nil {
			s.cancel()
		}
		return false, s.releaseErr
	}
	return s.versionRecoveryStore.SetSessionRuntimeReleased(ctx, id, launch)
}

func TestVersionMoveAbortedUpdateStopDoesNotBlockNextBatch(t *testing.T) {
	for _, scenario := range []string{"release cancelled", "stop source alive", "stop source unknown"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stopErr := errors.New("could not stop")
			rt := &versionFailureRuntime{refreshTestRuntime: &refreshTestRuntime{fakeRestartRuntime: &fakeRestartRuntime{fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}}}, probes: map[string]ports.FencedLiveness{"launch-old": ports.FencedAlive}}
			mgr, base, rec := versionFailureManager(t, rt)
			store := &versionReleaseErrorStore{versionRecoveryStore: base}
			mgr.store = store
			if scenario == "release cancelled" {
				store.releaseErr, store.cancel = stopErr, cancel
			} else {
				rt.destroyErr = stopErr
			}
			if scenario == "stop source unknown" {
				rt.probes["launch-old"] = ports.FencedUnknown
			}
			if _, err := mgr.StopHarnessSessions(ctx, rec.Harness); !errors.Is(err, stopErr) {
				t.Fatalf("stop = %v", err)
			}
			if scenario == "stop source unknown" {
				if len(store.moves) != 1 || store.moves[rec.ID].Stage != domain.VersionMoveStopping {
					t.Fatal("uncertain stop discarded recovery intent")
				}
				return
			}
			if len(store.moves) != 0 {
				t.Fatal("pre-stop error left a blocking journal row")
			}
			store.releaseErr, rt.destroyErr = nil, nil
			ids, err := mgr.StopHarnessSessions(context.Background(), rec.Harness)
			if err != nil || len(ids) != 1 || ids[0] != rec.ID || store.moves[rec.ID].Stage != domain.VersionMoveRetry {
				t.Fatalf("next batch blocked: ids=%v err=%v moves=%+v", ids, err, store.moves)
			}
		})
	}
}

func TestVersionMovePreflightErrorDoesNotBlockNextRelease(t *testing.T) {
	rt := &versionFailureRuntime{refreshTestRuntime: &refreshTestRuntime{fakeRestartRuntime: &fakeRestartRuntime{fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}}}, probes: map[string]ports.FencedLiveness{"launch-old": ports.FencedAlive}}
	mgr, store, rec := versionFailureManager(t, rt)
	agent := refreshTestAgent{supervisedLaunchAgent{launchArgvAgent{argv: []string{"pi", "--session", "agent-x"}}}}
	mgr.agents = singleAgent{agent: refreshHistoryAgent{refreshTestAgent: agent}}
	if err := mgr.RefreshStaleTerminals(context.Background(), time.Minute); !errors.Is(err, ErrNotResumable) {
		t.Fatalf("preflight = %v", err)
	}
	if len(store.moves) != 0 || rt.restarted != 0 {
		t.Fatal("preflight failure stopped source or retained intent")
	}
	updated := store.sessions[rec.ID]
	updated.StaleBinary.SwitchFailedAt = nil // a later installed release clears this mark
	store.sessions[rec.ID] = updated
	mgr.agents = singleAgent{agent: agent}
	if err := mgr.RefreshStaleTerminals(context.Background(), time.Minute); err != nil {
		t.Fatal(err)
	}
	if rt.restarted != 1 || len(store.moves) != 0 {
		t.Fatal("later release remained blocked by old preflight intent")
	}
}

type versionWakeFailureLauncher struct{ *recordingLauncher }

func (l versionWakeFailureLauncher) WaitForWake(context.Context, domain.SessionID) error {
	l.live = false
	return nil
}

func stoppedVersionChat(t *testing.T, launcher ChatLauncher) (*Manager, *versionRecoveryStore, domain.SessionRecord) {
	t.Helper()
	mgr, base, _ := newChatManager(launcher)
	seedChatResumeSession(base, domain.ActivityIdle)
	rec := base.sessions["mer-1"]
	rec.Metadata.ControllerGeneration = "source-chat"
	at := time.Now()
	rec.HibernatedAt = &at
	base.sessions[rec.ID] = rec
	store := &versionRecoveryStore{refreshTestStore: &refreshTestStore{fakeStore: base}}
	mgr.store = store
	if err := mgr.beginVersionMove(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if err := mgr.versionMoveStage(context.Background(), rec.ID, domain.VersionMoveRetry); err != nil {
		t.Fatal(err)
	}
	return mgr, store, rec
}

func TestVersionMoveNilWakeWaitDoesNotCompleteStoppedChat(t *testing.T) {
	launcher := &recordingLauncher{}
	launcher.afterReady = func() { launcher.live = true }
	mgr, store, rec := stoppedVersionChat(t, versionWakeFailureLauncher{launcher})
	launcher.providerConversationID = rec.Metadata.ProviderConversationID
	if err := mgr.RetryVersionMoves(context.Background()); !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatalf("lost controller after nil wait = %v", err)
	}
	if len(store.moves) != 1 || len(launcher.drained) != 0 {
		t.Fatal("nil wake wait discarded intent or drained without a controller")
	}
	if err := mgr.RetryVersionMoves(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.moves) != 1 || len(launcher.started) != 1 {
		t.Fatal("unpublished/ended replacement was forgotten or duplicated")
	}
}

func TestVersionMoveRetriesQueueDeliveryWithoutAnotherController(t *testing.T) {
	queueErr := errors.New("queue delivery unavailable")
	launcher := &recordingLauncher{drainErr: queueErr}
	launcher.afterReady = func() { launcher.live = true }
	mgr, store, rec := stoppedVersionChat(t, launcher)
	launcher.providerConversationID = rec.Metadata.ProviderConversationID
	if err := mgr.RetryVersionMoves(context.Background()); !errors.Is(err, queueErr) {
		t.Fatalf("drain = %v", err)
	}
	if len(store.moves) != 1 || len(launcher.started) != 1 {
		t.Fatal("failed drain completed or duplicated the move")
	}
	launcher.drainErr = nil
	if err := mgr.RetryVersionMoves(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.moves) != 0 || len(launcher.started) != 1 || len(launcher.drained) != 2 {
		t.Fatal("queue delivery did not recover without a second start")
	}
}
