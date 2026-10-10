package review

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type routingGuardStore struct {
	*fakeStore
	runsError, reviewsError error
	runReads, reviewReads   int
}

func (s *routingGuardStore) ListRunningReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error) {
	s.runReads++
	if s.runsError != nil {
		return nil, s.runsError
	}
	return s.fakeStore.ListRunningReviewRunsBySession(ctx, id)
}
func (s *routingGuardStore) ListReviewsBySession(ctx context.Context, id domain.SessionID) ([]domain.Review, error) {
	s.reviewReads++
	if s.reviewsError != nil {
		return nil, s.reviewsError
	}
	return s.fakeStore.ListReviewsBySession(ctx, id)
}
func TestProviderReviewGuardHoldsLaunchAdmissionUntilIdempotentRelease(t *testing.T) {
	store := &routingGuardStore{fakeStore: &fakeStore{}}
	engine := newEngineForTest(store, nil, nil, nil, &fakeLauncher{})
	release, err := engine.AcquireAccountRoutingPause(context.Background(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	if store.runReads != 1 || store.reviewReads != 1 {
		t.Fatalf("read proof=%d/%d", store.runReads, store.reviewReads)
	}
	if engine.workerMutex("worker").TryLock() {
		engine.workerMutex("worker").Unlock()
		t.Fatal("new review launch can bypass account mutation")
	}
	other := engine.workerMutex("other")
	if !other.TryLock() {
		t.Fatal("account mutation blocked unrelated worker")
	}
	other.Unlock()
	if _, err := engine.AcquireAccountRoutingPause(context.Background(), "worker"); !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("second guard=%v", err)
	}
	if store.runReads != 1 || store.reviewReads != 1 {
		t.Fatal("competing mutation read state without owning launch fence")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); release() }()
	}
	wg.Wait()
	if !engine.workerMutex("worker").TryLock() {
		t.Fatal("release left reviewer launch fenced")
	}
	engine.workerMutex("worker").Unlock()
	retry, err := engine.AcquireAccountRoutingPause(context.Background(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	retry()
	if store.runReads != 2 || store.reviewReads != 2 {
		t.Fatalf("retry proof=%d/%d", store.runReads, store.reviewReads)
	}
}
func TestProviderReviewGuardRefusesRunningRelatedWorkWithoutInterrupting(t *testing.T) {
	for _, harness := range []domain.ReviewerHarness{domain.ReviewerCodex, domain.ReviewerClaudeCode} {
		t.Run(string(harness), func(t *testing.T) {
			store := &routingGuardStore{fakeStore: &fakeStore{runs: []domain.ReviewRun{{ID: "active", SessionID: "worker", Harness: harness, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone}}}}
			launcher := &fakeLauncher{}
			engine := newEngineForTest(store, nil, nil, nil, launcher)
			release, err := engine.AcquireAccountRoutingPause(context.Background(), "worker")
			if release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
				t.Fatalf("release=%v error=%v", release != nil, err)
			}
			if launcher.cancelled || launcher.destroyed || launcher.spawned {
				t.Fatal("account guard changed a running reviewer")
			}
			if store.reviewReads != 0 {
				t.Fatal("guard continued after durable running review proof")
			}
			if !engine.workerMutex("worker").TryLock() {
				t.Fatal("refused account change left reviewer mutex locked")
			}
			engine.workerMutex("worker").Unlock()
			store.runs[0].Status = domain.ReviewRunComplete
			retry, err := engine.AcquireAccountRoutingPause(context.Background(), "worker")
			if err != nil {
				t.Fatal(err)
			}
			retry()
			if store.reviewReads != 1 {
				t.Fatal("idle retry skipped native activity proof")
			}
			if len(store.runs) != 1 || store.runs[0].ID != "active" {
				t.Fatal("guard rewrote review history")
			}
		})
	}
}
func TestProviderReviewGuardRefusesActivityEvenWithoutAnOpenRun(t *testing.T) {
	store := &routingGuardStore{fakeStore: &fakeStore{review: &domain.Review{ID: "r", SessionID: "worker", Harness: domain.ReviewerCodex, ReviewerActivityState: domain.ActivityActive}}}
	launcher := &fakeLauncher{}
	engine := newEngineForTest(store, nil, nil, nil, launcher)
	release, err := engine.AcquireAccountRoutingPause(context.Background(), "worker")
	if release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("release=%v error=%v", release != nil, err)
	}
	if store.runReads != 1 || store.reviewReads != 1 {
		t.Fatalf("guard proof=%d/%d", store.runReads, store.reviewReads)
	}
	if launcher.cancelled || launcher.destroyed {
		t.Fatal("guard interrupted active native reviewer")
	}
	store.review.ReviewerActivityState = domain.ActivityIdle
	retry, err := engine.AcquireAccountRoutingPause(context.Background(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	retry()
	if store.review.ReviewerActivityState != domain.ActivityIdle {
		t.Fatal("guard persisted a derived status")
	}
}
func TestProviderReviewGuardPropagatesStorageFailureAndUnlocks(t *testing.T) {
	for _, boundary := range []string{"runs", "reviews"} {
		t.Run(boundary, func(t *testing.T) {
			failure := errors.New("review database unavailable")
			store := &routingGuardStore{fakeStore: &fakeStore{}}
			if boundary == "runs" {
				store.runsError = failure
			} else {
				store.reviewsError = failure
			}
			engine := newEngineForTest(store, nil, nil, nil, &fakeLauncher{})
			release, err := engine.AcquireAccountRoutingPause(context.Background(), "worker")
			if release != nil || !errors.Is(err, failure) {
				t.Fatalf("release=%v error=%v", release != nil, err)
			}
			if !engine.workerMutex("worker").TryLock() {
				t.Fatal("storage failure retained launch fence")
			}
			engine.workerMutex("worker").Unlock()
			store.runsError = nil
			store.reviewsError = nil
			retry, err := engine.AcquireAccountRoutingPause(context.Background(), "worker")
			if err != nil {
				t.Fatal(err)
			}
			retry()
		})
	}
}
func TestProviderReviewGuardCancellationDoesNotAcquireOrRead(t *testing.T) {
	store := &routingGuardStore{fakeStore: &fakeStore{}}
	engine := newEngineForTest(store, nil, nil, nil, &fakeLauncher{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, err := engine.AcquireAccountRoutingPause(ctx, "worker")
	if release != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("release=%v error=%v", release != nil, err)
	}
	if store.runReads != 0 || store.reviewReads != 0 {
		t.Fatal("cancelled mutation read review state")
	}
	if !engine.workerMutex("worker").TryLock() {
		t.Fatal("cancelled mutation retained reviewer mutex")
	}
	engine.workerMutex("worker").Unlock()
}
func TestProviderReviewGuardRacingLaunchRefusesWithoutWaiting(t *testing.T) {
	store := &routingGuardStore{fakeStore: &fakeStore{}}
	engine := newEngineForTest(store, nil, nil, nil, &fakeLauncher{})
	unlock := engine.lockWorker("worker")
	result := make(chan error, 1)
	go func() {
		release, err := engine.AcquireAccountRoutingPause(context.Background(), "worker")
		if release != nil {
			release()
		}
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, ports.ErrProviderAccountBusy) {
			t.Fatalf("race error=%v", err)
		}
	case <-time.After(time.Second):
		unlock()
		t.Fatal("account guard queued behind an in-progress reviewer launch")
	}
	unlock()
	if store.runReads != 0 || store.reviewReads != 0 {
		t.Fatal("racing mutation read incomplete reviewer state")
	}
	release, err := engine.AcquireAccountRoutingPause(context.Background(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	release()
}
func TestProviderReviewerTerminalReceivesOwningSessionRoute(t *testing.T) {
	for _, harness := range []domain.ReviewerHarness{domain.ReviewerCodex, domain.ReviewerClaudeCode} {
		t.Run(string(harness), func(t *testing.T) {
			command := []string{"codex", "--model", "gpt-test"}
			accountEnv := map[string]string{"AO_PROXY_ENDPOINT": "http://127.0.0.1:4321", "AO_PROXY_TICKET": "private-session-ticket"}
			if harness == domain.ReviewerClaudeCode {
				command = []string{"claude", "--model", "claude-test"}
				accountEnv = map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:4321", "ANTHROPIC_AUTH_TOKEN": "private-session-ticket", "ANTHROPIC_API_KEY": ""}
			}
			reviewer := &fakeReviewerWithLaunchSpec{spec: ports.ReviewCommandSpec{Argv: command, Env: map[string]string{"KEEP": "adapter-value", "ANTHROPIC_API_KEY": "ambient-key"}}}
			runtime := &fakeRuntime{}
			calls := 0
			launcher := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, runtime, t.TempDir(), WithRelatedAccountEnv(func(_ context.Context, id domain.SessionID, h domain.AgentHarness) (map[string]string, error) {
				calls++
				if id != "mer-1" || h != domain.AgentHarness(harness) {
					t.Errorf("owner=%s harness=%s", id, h)
				}
				return accountEnv, nil
			}))
			spec := launchSpec()
			spec.Harness = harness
			spec.LaunchID = "managed-review-launch"
			result, err := launcher.Spawn(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || !runtime.created {
				t.Fatalf("account lookups=%d runtime=%v", calls, runtime.created)
			}
			if result.HandleID != "review-mer-1" || result.LaunchID != spec.LaunchID {
				t.Fatalf("result=%+v", result)
			}
			for key, value := range accountEnv {
				if runtime.createCfg.Env[key] != value {
					t.Errorf("environment %s was not inherited", key)
				}
			}
			if runtime.createCfg.Env["KEEP"] != "adapter-value" {
				t.Fatal("account injection erased reviewer adapter environment")
			}
			joined := strings.Join(runtime.createCfg.Argv, " ")
			if strings.Contains(joined, "private-session-ticket") || strings.Contains(joined, "ambient-key") {
				t.Fatal("reviewer arguments contain credentials")
			}
			if harness == domain.ReviewerCodex {
				if !strings.Contains(joined, `model_provider="ao-managed"`) || !strings.Contains(joined, "supports_websockets=false") {
					t.Fatalf("Codex custom provider missing: %v", runtime.createCfg.Argv)
				}
			} else {
				if strings.Contains(joined, "ao-managed") {
					t.Fatal("Codex options injected into Claude reviewer")
				}
				if runtime.createCfg.Env["ANTHROPIC_API_KEY"] != "" {
					t.Fatal("ambient Claude API key could override session route")
				}
			}
			if runtime.interrupts != 0 {
				t.Fatal("route injection interrupted reviewer")
			}
		})
	}
}
func TestProviderReviewerRouteFailureDoesNotDestroyExistingTerminal(t *testing.T) {
	for _, failure := range []error{ports.ErrProviderLoginRequired, ports.ErrProviderAccountRecovery, errors.New("account database unavailable")} {
		t.Run(failure.Error(), func(t *testing.T) {
			runtime := &fakeRuntime{alive: true}
			reviewer := &fakeReviewerWithLaunchSpec{spec: ports.ReviewCommandSpec{Argv: []string{"codex"}}}
			launcher := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, runtime, t.TempDir(), WithRelatedAccountEnv(func(context.Context, domain.SessionID, domain.AgentHarness) (map[string]string, error) {
				return nil, failure
			}))
			spec := launchSpec()
			spec.Harness = domain.ReviewerCodex
			_, err := launcher.Spawn(context.Background(), spec)
			if !errors.Is(err, failure) {
				t.Fatalf("spawn error=%v", err)
			}
			if runtime.created || runtime.destroyed != "" || runtime.interrupts != 0 {
				t.Fatalf("failure changed existing terminal: %+v", runtime)
			}
			if runtime.sentMsg != "" || runtime.sentInput != "" {
				t.Fatal("failed route sent native reviewer work")
			}
		})
	}
}
func TestProviderReviewerPreflightUsesManagedAuthAfterBinaryCheck(t *testing.T) {
	for _, harness := range []domain.ReviewerHarness{domain.ReviewerCodex, domain.ReviewerClaudeCode} {
		t.Run(string(harness), func(t *testing.T) {
			adapterAuthCalls := 0
			reviewer := &fakeReviewerForPreflight{Argv: []string{"go"}, Preflight: func(context.Context, string) error { adapterAuthCalls++; return ports.ErrChatAuthRequired }}
			lookups := 0
			launcher := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, &fakeRuntime{}, "", WithAgentAuth(fakeAgentAuthResolver{status: ports.AgentAuthStatusUnauthorized, ok: true}), WithRelatedAccountEnv(func(_ context.Context, id domain.SessionID, h domain.AgentHarness) (map[string]string, error) {
				lookups++
				if id != "worker" || h != domain.AgentHarness(harness) {
					t.Errorf("managed lookup=%s/%s", id, h)
				}
				return map[string]string{"managed": "ticket"}, nil
			}))
			ctx := context.WithValue(context.Background(), reviewerAccountWorkerKey{}, domain.SessionID("worker"))
			if err := launcher.Preflight(ctx, harness, "/scratch"); err != nil {
				t.Fatal(err)
			}
			if lookups != 1 || adapterAuthCalls != 0 {
				t.Fatalf("preflight lookups=%d global-auth-calls=%d", lookups, adapterAuthCalls)
			}
			err := launcher.Preflight(context.Background(), harness, "/scratch")
			if !errors.Is(err, ports.ErrChatAuthRequired) {
				t.Fatalf("native preflight=%v", err)
			}
			if lookups != 1 {
				t.Fatal("older native reviewer was converted to managed account")
			}
		})
	}
}
func TestProviderReviewerPreflightStillRequiresInstalledBinary(t *testing.T) {
	called := false
	reviewer := &fakeReviewerForPreflight{Argv: []string{"ao-nonexistent-reviewer-test-binary"}}
	launcher := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, &fakeRuntime{}, "", WithRelatedAccountEnv(func(context.Context, domain.SessionID, domain.AgentHarness) (map[string]string, error) {
		called = true
		return map[string]string{"ticket": "private"}, nil
	}))
	ctx := context.WithValue(context.Background(), reviewerAccountWorkerKey{}, domain.SessionID("worker"))
	err := launcher.Preflight(ctx, domain.ReviewerCodex, "/scratch")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("preflight=%v", err)
	}
	if called {
		t.Fatal("managed auth bypassed reviewer installation validation")
	}
}
func TestProviderReviewerNoRelatedRoutePreservesAdapterEnvironment(t *testing.T) {
	runtime := &fakeRuntime{}
	original := map[string]string{"KEEP": "value", "ANTHROPIC_API_KEY": "native-key"}
	reviewer := &fakeReviewerWithLaunchSpec{spec: ports.ReviewCommandSpec{Argv: []string{"claude"}, Env: original}}
	launcher := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, runtime, t.TempDir(), WithRelatedAccountEnv(func(context.Context, domain.SessionID, domain.AgentHarness) (map[string]string, error) {
		return nil, nil
	}))
	if _, err := launcher.Spawn(context.Background(), launchSpec()); err != nil {
		t.Fatal(err)
	}
	if runtime.createCfg.Env["KEEP"] != "value" || runtime.createCfg.Env["ANTHROPIC_API_KEY"] != "native-key" {
		t.Fatal("native reviewer account environment was changed")
	}
	if !reflect.DeepEqual(original, map[string]string{"KEEP": "value", "ANTHROPIC_API_KEY": "native-key"}) {
		t.Fatal("launch mutated adapter-owned environment map")
	}
	if strings.Contains(strings.Join(runtime.createCfg.Argv, " "), "ao-managed") {
		t.Fatal("native reviewer received custom managed provider")
	}
}
