package sessionmanager

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// accountRoutingFake is an Account Manager: every session is managed when
// managed is set, and otherwise only the ones it has adopted.
type accountRoutingFake struct {
	route                                  domain.ProviderSessionRoute
	env                                    map[string]string
	managed                                bool
	id                                     string
	err                                    error
	has                                    map[domain.SessionID]bool
	noDefault                              bool // no default account for an older session to move onto
	assigned, forgotten, launches, adopted []domain.SessionID
	resolved                               []string
	launched, planned                      []string // "id harness" and, for launches, "own" or "related"
}

func (f *accountRoutingFake) ResolveAccount(_ context.Context, h domain.AgentHarness, id string) (string, bool, error) {
	f.resolved = append(f.resolved, string(h)+":"+id)
	return f.id, f.managed, f.err
}

func (f *accountRoutingFake) AssignAccount(_ context.Context, id domain.SessionID, _ domain.AgentHarness, _ string) error {
	f.assigned = append(f.assigned, id)
	return f.err
}

func (f *accountRoutingFake) SessionAccount(_ context.Context, id domain.SessionID) (domain.ProviderSessionRoute, bool, error) {
	return f.route, f.managed || f.has[id], f.err
}

func (f *accountRoutingFake) LaunchAccountEnv(_ context.Context, id domain.SessionID, harness domain.AgentHarness, related bool) (map[string]string, error) {
	f.launched = append(f.launched, string(id)+" "+string(harness)+" "+map[bool]string{false: "own", true: "related"}[related])
	if f.err != nil || (!f.managed && !f.has[id]) {
		return nil, f.err
	}
	f.launches = append(f.launches, id)
	return f.env, nil
}

func (f *accountRoutingFake) PlannedAccountEnv(_ context.Context, id domain.SessionID, harness domain.AgentHarness) (map[string]string, error) {
	f.planned = append(f.planned, string(id)+" "+string(harness))
	if f.err != nil || (!f.managed && !f.has[id]) {
		return nil, f.err
	}
	return f.env, nil
}

func (f *accountRoutingFake) ForgetAccount(_ context.Context, id domain.SessionID) error {
	f.forgotten = append(f.forgotten, id)
	return nil
}

func (f *accountRoutingFake) HasDefault(context.Context, domain.AgentHarness) (bool, error) {
	return !f.noDefault, nil
}

func (f *accountRoutingFake) AdoptSession(_ context.Context, id domain.SessionID, _ domain.AgentHarness) (bool, error) {
	if f.err != nil || f.managed || f.has[id] || f.noDefault {
		return false, f.err
	}
	if f.has == nil {
		f.has = map[domain.SessionID]bool{}
	}
	f.has[id] = true
	f.adopted = append(f.adopted, id)
	return true, nil
}

var managedLaunch = map[string]string{"AO_PROXY_ENDPOINT": "http://127.0.0.1:4567", "AO_PROXY_TICKET": "session-ticket"}

func TestAccountEnvIsAddedOnlyToManagedLaunches(t *testing.T) {
	failure := errors.New("routing database unavailable")
	ticket := map[string]string{"AO_PROXY_TICKET": "private", "ANTHROPIC_API_KEY": ""}
	for name, tc := range map[string]struct {
		accounts ports.ProviderAccountRouting
		key      string
		err      error
	}{
		"managed":            {&accountRoutingFake{managed: true, env: ticket}, "", nil},
		"native":             {&accountRoutingFake{env: ticket}, "ambient", nil},
		"failure":            {&accountRoutingFake{managed: true, env: ticket, err: failure}, "ambient", failure},
		"no Account Manager": {nil, "ambient", nil},
	} {
		m, _, _, _ := newManager()
		m.accounts = tc.accounts
		env := map[string]string{"PATH": "/agent-bin", "ANTHROPIC_API_KEY": "ambient"}
		err := m.applyAccountEnv(context.Background(), "s", domain.HarnessClaudeCode, env)
		if !errors.Is(err, tc.err) || env["PATH"] != "/agent-bin" || env["ANTHROPIC_API_KEY"] != tc.key || (tc.key == "") != (env["AO_PROXY_TICKET"] == "private") {
			t.Errorf("%s: env=%v err=%v", name, env, err)
		}
		if managed, err := m.sessionManaged(context.Background(), "s"); managed != (name == "managed") && err == nil {
			t.Errorf("%s: managed=%v", name, managed)
		}
	}
}

// A reviewer runs on its worker's account for the reviewer's own provider; which
// account that is, the worker's own or the provider's default, Account Manager decides.
func TestRelatedWorkAsksForTheOwnersAccountOfItsProvider(t *testing.T) {
	for name, tc := range map[string]struct {
		accounts *accountRoutingFake
		want     error
		ticket   bool
	}{
		"managed":       {&accountRoutingFake{managed: true, env: map[string]string{"ticket": "owner"}}, nil, true},
		"needs sign-in": {&accountRoutingFake{managed: true, err: ports.ErrProviderLoginRequired}, ports.ErrProviderLoginRequired, false},
		"not managed":   {&accountRoutingFake{env: map[string]string{"ticket": "owner"}}, nil, false},
	} {
		m, _, _, _ := newManager()
		m.accounts = tc.accounts
		env := map[string]string{}
		err := m.RelatedAccountEnv(context.Background(), "worker", domain.HarnessCodex, env)
		if !errors.Is(err, tc.want) || (env["ticket"] == "owner") != tc.ticket || !reflect.DeepEqual(tc.accounts.launched, []string{"worker codex related"}) {
			t.Errorf("%s: env=%v launched=%v err=%v", name, env, tc.accounts.launched, err)
		}
	}
	m, _, _, _ := newManager()
	if err := m.RelatedAccountEnv(context.Background(), "worker", domain.HarnessCodex, map[string]string{}); err != nil {
		t.Fatalf("without Account Manager: %v", err)
	}
}

func TestSpawnRoutesTheDefaultOrChosenAccountIntoTheTerminalLaunch(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode} {
		for _, choice := range []string{"", "explicit-account"} {
			m, st, rt, _ := newManager()
			f := &accountRoutingFake{managed: true, id: "resolved-account", env: managedLaunch}
			m.accounts = f
			rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: harness, RequestedMode: domain.SessionModeTUI, AccountID: choice})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f.resolved, []string{string(harness) + ":" + choice}) || !reflect.DeepEqual(f.assigned, []domain.SessionID{rec.ID}) || !reflect.DeepEqual(f.launches, []domain.SessionID{rec.ID}) {
				t.Fatalf("resolve=%v assign=%v launch=%v", f.resolved, f.assigned, f.launches)
			}
			if _, saved := st.sessions[rec.ID]; !saved || rt.created != 1 || rt.lastCfg.Env["AO_PROXY_TICKET"] != "session-ticket" {
				t.Fatalf("saved=%v created=%d env=%v", saved, rt.created, rt.lastCfg.Env)
			}
			// Codex is pointed at the helper by argument; the ticket stays in the environment.
			if !slices.Contains(rt.lastCfg.Argv, `model_provider="ao-managed"`) || slices.ContainsFunc(rt.lastCfg.Argv, func(arg string) bool { return arg == "session-ticket" }) {
				t.Fatalf("argv=%v", rt.lastCfg.Argv)
			}
		}
	}
}

func TestSpawnWithoutAUsableAccountLeavesNoSession(t *testing.T) {
	for _, assignOnly := range []bool{false, true} {
		m, st, rt, _ := newManager()
		f := &failingAssign{accountRoutingFake: accountRoutingFake{managed: true, err: ports.ErrProviderLoginRequired}, assignOnly: assignOnly}
		m.accounts = f
		_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, AccountID: "signed-out"})
		if !errors.Is(err, ports.ErrProviderLoginRequired) || len(st.sessions) != 0 || rt.created != 0 {
			t.Fatalf("assignOnly=%v err=%v sessions=%d runtimes=%d", assignOnly, err, len(st.sessions), rt.created)
		}
		if assignOnly != (len(f.forgotten) == 1) {
			t.Fatalf("assignOnly=%v forgotten=%v", assignOnly, f.forgotten)
		}
	}
}

// failingAssign resolves an account and then cannot give it to the session.
type failingAssign struct {
	accountRoutingFake
	assignOnly bool
}

func (f *failingAssign) ResolveAccount(ctx context.Context, h domain.AgentHarness, id string) (string, bool, error) {
	if f.assignOnly {
		return "a", true, nil
	}
	return f.accountRoutingFake.ResolveAccount(ctx, h, id)
}

func TestManagedChatLaunchAndRestoreCarryTheSameTicket(t *testing.T) {
	launcher := &recordingLauncher{}
	m, sessions, runtime := newChatManager(launcher)
	f := &accountRoutingFake{managed: true, id: "a", route: domain.ProviderSessionRoute{Provider: "codex", AccountID: "a"}, env: managedLaunch}
	m.accounts = f
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex, RequestedMode: domain.SessionModeChat})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Kill(context.Background(), rec.ID); err != nil {
		t.Fatal(err)
	}
	native := sessions.sessions[rec.ID].Metadata.ProviderConversationID
	if result, err := m.RestoreWithMode(context.Background(), rec.ID); err != nil || result.Mode != RestoreModeNative {
		t.Fatalf("restore=%+v err=%v", result, err)
	}
	if len(launcher.started) != 2 || runtime.created != 0 || native == "" || launcher.started[1].ProviderConversationID != native {
		t.Fatalf("started=%d terminals=%d conversation=%q", len(launcher.started), runtime.created, native)
	}
	for _, start := range launcher.started {
		if start.SessionID != rec.ID || start.Env["AO_PROXY_TICKET"] != "session-ticket" || start.Env["AO_PROXY_ENDPOINT"] != managedLaunch["AO_PROXY_ENDPOINT"] {
			t.Fatalf("launch env=%v", start.Env)
		}
	}
	if !reflect.DeepEqual(f.assigned, []domain.SessionID{rec.ID}) || len(f.resolved) != 1 {
		t.Fatalf("restore chose an account again: resolved=%v assigned=%v", f.resolved, f.assigned)
	}
}

// A managed session switches provider onto its account for the target: planned
// while the source still runs its handoff turn, moved once the source stopped.
func TestAManagedTerminalSwitchesProviderOntoItsAccountForTheTarget(t *testing.T) {
	runtime := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{}}
	m, _, _ := newSwitchTestManager(t, runtime)
	routes := &accountRoutingFake{managed: true, route: domain.ProviderSessionRoute{Provider: "claude", AccountID: "alice"}, env: managedLaunch}
	m.accounts = routes
	if _, err := m.SwitchAgent(context.Background(), "proj-1", SwitchAgentConfig{TargetHarness: domain.HarnessCodex, IdempotencyKey: "managed-provider-switch"}); err != nil {
		t.Fatal(err)
	}
	waitForSwitchWorkers(t, m)
	if !reflect.DeepEqual(routes.planned, []string{"proj-1 codex"}) || !slices.Contains(routes.launched, "proj-1 codex own") {
		t.Fatalf("planned=%v launched=%v", routes.planned, routes.launched)
	}
	if cfg := runtime.lastCfg; cfg.Env["AO_PROXY_TICKET"] != "session-ticket" || !slices.Contains(cfg.Argv, `model_provider="ao-managed"`) {
		t.Fatalf("target launch env=%v argv=%v", cfg.Env, cfg.Argv)
	}
}

func TestManagedSessionsSkipTheNativeSignInProbe(t *testing.T) {
	for _, managed := range []bool{false, true} {
		m, _, _, _ := newManager()
		m.agents = singleAgent{agent: &launchAuthAgent{recordingAgent: &recordingAgent{}, status: ports.AgentAuthStatusUnauthorized}}
		m.accounts = &accountRoutingFake{managed: managed, id: "a"}
		_, _, _, spawnErr := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, RequestedMode: domain.SessionModeTUI})

		switching, store, _, _, _ := newTransitionManager(t, domain.SessionModeChat)
		switching.agents = singleAgent{agent: &transitionLaunchAuthAgent{status: ports.AgentAuthStatusUnauthorized}}
		switching.accounts = &accountRoutingFake{managed: managed}
		switchErr := switching.preflightInterfaceTarget(context.Background(), store.sessions["session-1"], domain.SessionInterfaceTransition{TargetMode: domain.SessionModeTUI, NativeConversationID: "native-1"})
		for name, err := range map[string]error{"spawn": spawnErr, "interface switch": switchErr} {
			if managed == errors.Is(err, ports.ErrAgentAuthRequired) {
				t.Errorf("%s managed=%v: err=%v", name, managed, err)
			}
		}
	}
}
