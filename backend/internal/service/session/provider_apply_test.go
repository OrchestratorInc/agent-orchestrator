package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// providerApplyFixture wires a session service over fakes with an injected
// stamp resolver so staleness never touches the developer's ~/.claude.
type providerApplyFixture struct {
	svc       *Service
	store     *fakeStore
	commander *fakeCommander
	resolved  map[string]providerStamp
}

func newProviderApplyFixture() *providerApplyFixture {
	f := &providerApplyFixture{store: newFakeStore(), commander: &fakeCommander{}, resolved: map[string]providerStamp{}}
	f.svc = NewWithDeps(Deps{Manager: f.commander, Store: f.store})
	f.svc.resolveStampOverride = func(_ context.Context, workingDir string, _ map[string]string) (string, string) {
		stamp := f.resolved[workingDir]
		return stamp.baseURL, stamp.model
	}
	return f
}

func (f *providerApplyFixture) session(id, workspacePath string, stamp providerStamp) domain.SessionRecord {
	rec := domain.SessionRecord{
		ID:           domain.SessionID(id),
		ProjectID:    domain.ProjectID("mer"),
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		Mode:         domain.SessionModeTUI,
		Activity:     domain.Activity{State: domain.ActivityActive},
		IsTerminated: false,
		DisplayName:  id,
	}
	rec.Metadata = domain.SessionMetadata{WorkspacePath: workspacePath, ProviderBaseURL: stamp.baseURL, ProviderModel: stamp.model}
	f.store.sessions[domain.SessionID(id)] = rec
	return rec
}

func TestProviderStalenessDetectsChangedGateway(t *testing.T) {
	f := newProviderApplyFixture()
	f.session("mer-1", "/ws/1", providerStamp{baseURL: "https://old.example", model: "m-old"})
	f.resolved["/ws/1"] = providerStamp{baseURL: "", model: ""}

	stale, err := f.svc.ProviderStaleness(context.Background())
	if err != nil {
		t.Fatalf("ProviderStaleness: %v", err)
	}
	if len(stale) != 1 || stale[0].SessionID != "mer-1" {
		t.Fatalf("expected mer-1 stale, got %+v", stale)
	}
	if stale[0].StampBaseURL != "https://old.example" || stale[0].CurrentBaseURL != "" {
		t.Fatalf("unexpected stamp fields: %+v", stale[0])
	}
	if stale[0].Mode != string(domain.SessionModeTUI) {
		t.Fatalf("expected tui mode, got %q", stale[0].Mode)
	}
}

func TestProviderStalenessCurrentSessionNotStale(t *testing.T) {
	f := newProviderApplyFixture()
	f.session("mer-1", "/ws/1", providerStamp{baseURL: "https://gw.example", model: "m-1"})
	f.resolved["/ws/1"] = providerStamp{baseURL: "https://gw.example", model: "m-1"}

	stale, err := f.svc.ProviderStaleness(context.Background())
	if err != nil {
		t.Fatalf("ProviderStaleness: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("expected no stale sessions, got %+v", stale)
	}
}

func TestProviderStalenessModelChangeOnly(t *testing.T) {
	f := newProviderApplyFixture()
	f.session("mer-1", "/ws/1", providerStamp{baseURL: "https://gw.example", model: "m-old"})
	f.resolved["/ws/1"] = providerStamp{baseURL: "https://gw.example", model: "m-new"}

	stale, err := f.svc.ProviderStaleness(context.Background())
	if err != nil {
		t.Fatalf("ProviderStaleness: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("expected model-only change to be stale, got %+v", stale)
	}
}

func TestProviderStalenessNoGatewayNowAndNoStamp(t *testing.T) {
	f := newProviderApplyFixture()
	f.session("mer-1", "/ws/1", providerStamp{})
	f.resolved["/ws/1"] = providerStamp{}

	stale, err := f.svc.ProviderStaleness(context.Background())
	if err != nil {
		t.Fatalf("ProviderStaleness: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("expected no stale sessions without any gateway, got %+v", stale)
	}
}

func TestProviderStalenessExcludesIneligibleSessions(t *testing.T) {
	f := newProviderApplyFixture()
	f.resolved["/ws/1"] = providerStamp{baseURL: "https://new.example"}

	f.session("mer-1", "/ws/1", providerStamp{baseURL: "https://old.example"})
	terminated := f.session("mer-2", "/ws/2", providerStamp{baseURL: "https://old.example"})
	terminated.IsTerminated = true
	f.store.sessions["mer-2"] = terminated
	exited := f.session("mer-3", "/ws/3", providerStamp{baseURL: "https://old.example"})
	exited.Activity.State = domain.ActivityExited
	f.store.sessions["mer-3"] = exited
	otherHarness := f.session("mer-4", "/ws/4", providerStamp{baseURL: "https://old.example"})
	otherHarness.Harness = domain.HarnessCodex
	f.store.sessions["mer-4"] = otherHarness
	preparation := f.session("mer-5", "/ws/5", providerStamp{})
	preparation.IsTaskPreparation = true
	f.store.sessions["mer-5"] = preparation

	out, err := f.svc.ProviderStaleness(context.Background())
	if err != nil {
		t.Fatalf("ProviderStaleness: %v", err)
	}
	if len(out) != 1 || out[0].SessionID != "mer-1" {
		t.Fatalf("expected only mer-1 stale, got %+v", out)
	}
}

func TestProviderStalenessReadsProjectEnv(t *testing.T) {
	f := newProviderApplyFixture()
	f.session("mer-1", "/ws/1", providerStamp{})
	f.store.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{Env: map[string]string{"PROBE": "1"}}}
	var seen map[string]string
	f.svc.resolveStampOverride = func(_ context.Context, _ string, projectEnv map[string]string) (string, string) {
		seen = projectEnv
		return "", ""
	}
	if _, err := f.svc.ProviderStaleness(context.Background()); err != nil {
		t.Fatalf("ProviderStaleness: %v", err)
	}
	if seen == nil {
		t.Fatal("expected project env to reach the resolver")
	}
}

func TestApplyProviderSwitchRelaunchesStaleSession(t *testing.T) {
	f := newProviderApplyFixture()
	f.session("mer-1", "/ws/1", providerStamp{baseURL: "https://old.example"})
	f.resolved["/ws/1"] = providerStamp{}

	results, err := f.svc.ApplyProviderSwitch(context.Background(), nil)
	if err != nil {
		t.Fatalf("ApplyProviderSwitch: %v", err)
	}
	if len(results) != 1 || results[0].State != providerApplyApplied || results[0].SessionID != "mer-1" {
		t.Fatalf("expected applied mer-1, got %+v", results)
	}
	if len(f.commander.exited) != 1 || f.commander.exited[0] != "mer-1" {
		t.Fatalf("expected exit, got %v", f.commander.exited)
	}
	if len(f.commander.resumed) != 1 || f.commander.resumed[0] != "mer-1" {
		t.Fatalf("expected resume, got %v", f.commander.resumed)
	}
}

func TestApplyProviderSwitchSkipsCurrentSession(t *testing.T) {
	f := newProviderApplyFixture()
	f.session("mer-1", "/ws/1", providerStamp{baseURL: "https://gw.example", model: "m"})
	f.resolved["/ws/1"] = providerStamp{baseURL: "https://gw.example", model: "m"}

	results, err := f.svc.ApplyProviderSwitch(context.Background(), []domain.SessionID{"mer-1"})
	if err != nil {
		t.Fatalf("ApplyProviderSwitch: %v", err)
	}
	if len(results) != 1 || results[0].State != providerApplySkipped {
		t.Fatalf("expected skipped, got %+v", results)
	}
	if len(f.commander.exited) != 0 || len(f.commander.resumed) != 0 {
		t.Fatalf("expected no lifecycle calls, got exit=%v resume=%v", f.commander.exited, f.commander.resumed)
	}
}

func TestApplyProviderSwitchExitFailureSkipsResume(t *testing.T) {
	f := newProviderApplyFixture()
	f.session("mer-1", "/ws/1", providerStamp{baseURL: "https://old.example"})
	f.resolved["/ws/1"] = providerStamp{}
	f.commander.exitErr = errors.New("exit boom")

	results, err := f.svc.ApplyProviderSwitch(context.Background(), []domain.SessionID{"mer-1"})
	if err != nil {
		t.Fatalf("ApplyProviderSwitch: %v", err)
	}
	if results[0].State != providerApplyFailed || !strings.Contains(results[0].Error, "exit boom") {
		t.Fatalf("expected failed with exit error, got %+v", results[0])
	}
	if len(f.commander.resumed) != 0 {
		t.Fatalf("resume must not run after a failed exit, got %v", f.commander.resumed)
	}
}

func TestApplyProviderSwitchResumeFailureReportsError(t *testing.T) {
	f := newProviderApplyFixture()
	f.session("mer-1", "/ws/1", providerStamp{baseURL: "https://old.example"})
	f.resolved["/ws/1"] = providerStamp{}
	// The exit succeeded; only the resume fails. The session stays exited and
	// restorable — the fake commander mirrors that by recording the exit.
	f.commander.exitErr = nil
	f.commander.resumeErr = errors.New("resume boom")

	results, err := f.svc.ApplyProviderSwitch(context.Background(), []domain.SessionID{"mer-1"})
	if err != nil {
		t.Fatalf("ApplyProviderSwitch: %v", err)
	}
	if results[0].State != providerApplyFailed || !strings.Contains(results[0].Error, "resume boom") {
		t.Fatalf("expected failed with resume error, got %+v", results[0])
	}
	if len(f.commander.exited) != 1 {
		t.Fatalf("expected the session exited (restorable), got %v", f.commander.exited)
	}
}

func TestApplyProviderSwitchUnknownSessionFails(t *testing.T) {
	f := newProviderApplyFixture()

	results, err := f.svc.ApplyProviderSwitch(context.Background(), []domain.SessionID{"ghost"})
	if err != nil {
		t.Fatalf("ApplyProviderSwitch: %v", err)
	}
	if results[0].State != providerApplyFailed || results[0].Error == "" {
		t.Fatalf("expected failed with error, got %+v", results[0])
	}
}

func TestApplyProviderSwitchDeduplicatesSessionIDs(t *testing.T) {
	f := newProviderApplyFixture()
	f.session("mer-1", "/ws/1", providerStamp{baseURL: "https://old.example"})
	f.resolved["/ws/1"] = providerStamp{}

	results, err := f.svc.ApplyProviderSwitch(context.Background(), []domain.SessionID{"mer-1", "mer-1", "mer-1"})
	if err != nil {
		t.Fatalf("ApplyProviderSwitch: %v", err)
	}
	if len(results) != 1 || results[0].State != providerApplyApplied {
		t.Fatalf("expected a single applied result, got %+v", results)
	}
	if len(f.commander.exited) != 1 || len(f.commander.resumed) != 1 {
		t.Fatalf("duplicate ids must run one exit+resume cycle, got exit=%v resume=%v", f.commander.exited, f.commander.resumed)
	}
}

func TestApplyProviderSwitchBatchOrderPreservedAndIsolated(t *testing.T) {
	f := newProviderApplyFixture()
	f.session("mer-1", "/ws/1", providerStamp{baseURL: "https://old.example"})
	f.session("mer-2", "/ws/2", providerStamp{baseURL: "https://gw.example", model: "m"})
	f.session("mer-3", "/ws/3", providerStamp{baseURL: "https://old.example"})
	f.resolved["/ws/1"] = providerStamp{}
	f.resolved["/ws/2"] = providerStamp{baseURL: "https://gw.example", model: "m"}
	f.resolved["/ws/3"] = providerStamp{}
	f.commander.exitErrFunc = func(id domain.SessionID) error {
		if id == "mer-1" {
			return errors.New("exit boom")
		}
		return nil
	}

	results, err := f.svc.ApplyProviderSwitch(context.Background(), []domain.SessionID{"mer-1", "mer-2", "mer-3"})
	if err != nil {
		t.Fatalf("ApplyProviderSwitch: %v", err)
	}
	wantStates := []string{providerApplyFailed, providerApplySkipped, providerApplyApplied}
	wantIDs := []domain.SessionID{"mer-1", "mer-2", "mer-3"}
	for i := range wantStates {
		if results[i].State != wantStates[i] {
			t.Fatalf("result[%d] = %s, want %s (%+v)", i, results[i].State, wantStates[i], results[i])
		}
		if results[i].SessionID != wantIDs[i] {
			t.Fatalf("result[%d] session = %s, want %s", i, results[i].SessionID, wantIDs[i])
		}
	}
	// mer-3 must be applied even though mer-1 failed in the same batch.
	if len(f.commander.resumed) != 1 || f.commander.resumed[0] != "mer-3" {
		t.Fatalf("expected only mer-3 resumed, got %v", f.commander.resumed)
	}
}

// A role provider pin changes what a relaunch resolves: the direct pin shadows
// the gateway, so a session still running on the gateway must be flagged stale
// and a relaunch pick up the pin (issue #6156).
func TestProviderStalenessFoldsRolePinIntoCurrentResolution(t *testing.T) {
	f := newProviderApplyFixture()
	f.session("mer-1", "/ws/1", providerStamp{baseURL: "https://gw.example", model: "m-1"})
	f.store.projects["mer"] = domain.ProjectRecord{
		ID:   "mer",
		Path: "/proj",
		Config: domain.ProjectConfig{
			Worker: domain.RoleOverride{Provider: domain.ProviderDirect},
		},
	}
	f.svc.resolveStampOverride = func(_ context.Context, _ string, env map[string]string) (string, string) {
		// Mirror the resolver: an explicit empty value shadows the settings.
		if base, ok := env["ANTHROPIC_BASE_URL"]; ok && base == "" {
			return "", ""
		}
		return "https://gw.example", "m-1"
	}

	stale, err := f.svc.ProviderStaleness(context.Background())
	if err != nil {
		t.Fatalf("ProviderStaleness: %v", err)
	}
	if len(stale) != 1 || stale[0].SessionID != "mer-1" {
		t.Fatalf("expected mer-1 stale under a direct role pin, got %+v", stale)
	}

	// Removing the pin restores the gateway resolution: no staleness.
	f.store.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/proj"}
	stale, err = f.svc.ProviderStaleness(context.Background())
	if err != nil {
		t.Fatalf("ProviderStaleness: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("expected no stale sessions without the pin, got %+v", stale)
	}
}
