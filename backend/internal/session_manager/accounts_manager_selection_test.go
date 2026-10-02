package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	browsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
)

type initialSelectionMemoryStore struct {
	*fakeStore
	bindings map[domain.SessionID]domain.AccountsManagerAccountChoice
	err      error
}

func (s *initialSelectionMemoryStore) CreateSessionWithAccount(ctx context.Context, rec domain.SessionRecord, choice domain.AccountsManagerAccountChoice) (domain.SessionRecord, bool, error) {
	if s.err != nil {
		return domain.SessionRecord{}, false, s.err
	}
	created, err := s.CreateSession(ctx, rec)
	if err == nil {
		s.bindings[created.ID] = choice
	}
	return created, err == nil, err
}

func (s *initialSelectionMemoryStore) PromoteTaskPreparationWithAccount(ctx context.Context, id domain.SessionID, rec domain.SessionRecord, choice domain.AccountsManagerAccountChoice) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	promoted, err := s.PromoteTaskPreparation(ctx, id, rec)
	if err == nil && promoted {
		s.bindings[id] = choice
	}
	return promoted, err
}

func (s *initialSelectionMemoryStore) GetAccountsManagerSessionRoute(_ context.Context, id domain.SessionID, provider domain.AccountsManagerProvider) (domain.AccountsManagerSessionRoute, bool, error) {
	choice, found := s.bindings[id]
	return domain.AccountsManagerSessionRoute{SessionID: id, Provider: provider, Mode: choice.Mode, AccountID: choice.AccountID}, found, s.err
}

type initialSelectionRouter struct {
	store     *initialSelectionMemoryStore
	err       error
	validated int
	prepared  []domain.AccountsManagerAccountChoice
}

func (r *initialSelectionRouter) PrepareAgentLaunchRoute(_ context.Context, id domain.SessionID, _ domain.AccountsManagerProvider, _ string) (*ports.AccountsManagerLaunchRoute, error) {
	choice, exists := r.store.bindings[id]
	if !exists {
		return nil, errors.New("initial account was not durable before launch")
	}
	r.prepared = append(r.prepared, choice)
	if choice.Mode == domain.AccountsManagerNative {
		return nil, nil
	}
	return &ports.AccountsManagerLaunchRoute{BaseURL: "http://127.0.0.1:12345", Token: "synthetic-" + choice.AccountID}, nil
}

func (*initialSelectionRouter) AgentRoutingEnabled(context.Context, domain.AccountsManagerProvider) (bool, error) {
	return true, nil
}

func (r *initialSelectionRouter) HasAgentSessionRoute(_ context.Context, id domain.SessionID, _ domain.AccountsManagerProvider) (bool, error) {
	return r.store.bindings[id].Mode == domain.AccountsManagerManaged, nil
}

func (r *initialSelectionRouter) ValidateAgentAccountTarget(context.Context, domain.AccountsManagerConnectionMode, domain.AccountsManagerProvider, string, string) error {
	r.validated++
	return r.err
}

func initialSelectionFixture() (*Manager, *initialSelectionMemoryStore, *fakeRuntime, *initialSelectionRouter) {
	m, st, rt, _ := newManager()
	store := &initialSelectionMemoryStore{fakeStore: st, bindings: make(map[domain.SessionID]domain.AccountsManagerAccountChoice)}
	router := &initialSelectionRouter{store: store}
	m.store, m.accountsManager = store, router
	return m, store, rt, router
}

func initialSelectionConfig(choice domain.AccountsManagerAccountChoice) ports.SpawnConfig {
	return ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, RequestedMode: domain.SessionModeTUI, Account: &choice}
}

func TestSpawnInitialAccountCommitsBeforeLaunch(t *testing.T) {
	for _, choice := range []domain.AccountsManagerAccountChoice{
		{Mode: domain.AccountsManagerManaged, AccountID: "account-a"},
		{Mode: domain.AccountsManagerManaged, AccountID: "account-b"},
		{Mode: domain.AccountsManagerNative},
	} {
		t.Run(string(choice.Mode)+choice.AccountID, func(t *testing.T) {
			m, st, rt, router := initialSelectionFixture()
			rec, _, _, err := m.Spawn(t.Context(), initialSelectionConfig(choice))
			if err != nil {
				t.Fatalf("explicit selection failed before launch: %v", err)
			}
			if rt.created != 1 || len(router.prepared) != 1 || router.prepared[0] != choice || st.bindings[rec.ID] != choice {
				t.Fatal("first launch did not use the durable explicit choice")
			}
			if choice.Mode == domain.AccountsManagerManaged && router.validated != 1 {
				t.Fatal("selected managed account was not admitted")
			}
		})
	}
}

func TestSpawnInitialAccountRejectsBeforeCreation(t *testing.T) {
	for _, failure := range []string{"service unavailable", "store unavailable", "account unavailable", "account deleting", "invalid choice", "unsupported chat"} {
		t.Run(failure, func(t *testing.T) {
			m, st, rt, router := initialSelectionFixture()
			cfg := initialSelectionConfig(domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-a"})
			var want error
			switch failure {
			case "service unavailable":
				m.accountsManager = nil
				want = domain.ErrAccountsManagerSelectionUnavailable
			case "store unavailable":
				m.store = st.fakeStore
				want = domain.ErrAccountsManagerSelectionUnavailable
			case "account unavailable":
				want = errors.New("synthetic selected account unavailable")
				router.err = want
			case "account deleting":
				want = domain.ErrAccountsManagerAccountDeleting
				st.err = want
			case "invalid choice":
				cfg.Account.AccountID = ""
				want = domain.ErrAccountsManagerSelectionInvalid
			case "unsupported chat":
				cfg.RequestedMode = domain.SessionModeChat
				m.chat = &transitionChat{log: new([]string), startErr: errors.New("unexpected managed Chat launch")}
				want = ports.ErrChatUnsupported
			}
			if _, _, _, err := m.Spawn(t.Context(), cfg); !errors.Is(err, want) {
				t.Fatalf("spawn error=%v, want %v", err, want)
			}
			if len(st.sessions) != 0 || rt.created != 0 || len(router.prepared) != 0 {
				t.Fatal("rejected initial choice created or launched a session")
			}
		})
	}
}

func TestSpawnInitialAccountPreparedCommitBeforeLaunch(t *testing.T) {
	m, st, rt, router := initialSelectionFixture()
	m.runBackground = func(work func()) { work() }
	token, err := m.PrepareTaskWorkspace(t.Context(), st.projects["mer"])
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialSelectionConfig(domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-b"})
	cfg.TaskPreparation = token
	rec, _, _, err := m.Spawn(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != "mer-1" || len(st.sessions) != 1 || st.sessions[rec.ID].IsTaskPreparation || rt.created != 1 || len(router.prepared) != 1 || router.prepared[0] != *cfg.Account {
		t.Fatal("prepared launch lost its explicit account or created a second session")
	}
}

func TestSpawnInitialAccountAsyncCommitBeforeLaunch(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, rt, _ := initialSelectionFixture()
	m.chat = launcher
	m.browserCapabilities = browsersvc.NewAuthority()
	deferred := deferredBackground(m)
	choice := domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-a"}
	cfg := asyncChatSpawnConfig("preserve initial request")
	cfg.Harness = domain.HarnessClaudeCode
	cfg.Account = &choice
	var launchedChoice domain.AccountsManagerAccountChoice
	launcher.beforeStart = func(start ChatStart) { launchedChoice = st.bindings[start.SessionID] }
	rec, _, _, err := m.Spawn(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st.bindings[rec.ID] != choice || len(launcher.started) != 0 || len(*deferred) != 1 || len(launcher.queued) != 1 {
		t.Fatal("async reply exposed an unbound or already launched session")
	}
	choice.AccountID = "account-b"
	(*deferred)[0]()
	if len(launcher.started) != 1 || launchedChoice.AccountID != "account-a" || rt.created != 0 {
		t.Fatal("async launch changed initial choice or launched a terminal")
	}
}

func TestInitialAccountBackgroundCannotUseNativeCredentials(t *testing.T) {
	for _, failure := range []string{"managed", "read failure", "native"} {
		t.Run(failure, func(t *testing.T) {
			m, st, _, _ := initialSelectionFixture()
			launcher := &recordingLauncher{}
			m.chat, m.dataDir, m.accountsManager = launcher, t.TempDir(), nil
			id := domain.SessionID("mer-1")
			st.sessions[id] = domain.SessionRecord{ID: id, ProjectID: "mer", Harness: domain.HarnessCodex}
			st.bindings[id] = domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-a"}
			if failure == "native" {
				st.bindings[id] = domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerNative}
			}
			if failure == "read failure" {
				st.err = errors.New("synthetic binding read failure")
			}
			_, err := m.RunBackgroundTask(t.Context(), id, "title", "test")
			if failure == "native" {
				if err != nil || len(launcher.background) != 1 {
					t.Fatal("unrelated native background task changed", err)
				}
			} else if err == nil || len(launcher.background) != 0 {
				t.Fatal("managed or unknown binding launched with native credentials")
			}
		})
	}
}

func TestInitialAccountUnavailableRouterNeverFallsBack(t *testing.T) {
	m, st, _, _ := initialSelectionFixture()
	m.accountsManager = nil
	st.bindings["mer-1"] = domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-a"}
	if _, err := m.prepareAccountsManagerRoute(t.Context(), "mer-1", domain.HarnessCodex, "", map[string]string{}); err == nil {
		t.Fatal("durable managed binding became a native launch when its router disappeared")
	}
}
