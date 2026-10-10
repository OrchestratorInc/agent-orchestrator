package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/provideraccounts"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type managerAccountProxy struct {
	snapshot ports.ProviderRouteSnapshot
	deleted  []string
	applyErr error
}

func (p *managerAccountProxy) ApplyRoutes(_ context.Context, next ports.ProviderRouteSnapshot) error {
	if p.applyErr != nil {
		return p.applyErr
	}
	if next.Revision != p.snapshot.Revision+1 && !reflect.DeepEqual(next, p.snapshot) {
		return ports.ErrProviderAccountRecovery
	}
	p.snapshot = next
	return nil
}
func (p *managerAccountProxy) DeleteCredential(_ context.Context, ref string) error {
	p.deleted = append(p.deleted, ref)
	return nil
}
func managedChatFixture(t *testing.T) (*Manager, *fakeStore, *fakeRuntime, *accountPauseLauncher, *provideraccounts.Service, *managerAccountProxy) {
	t.Helper()
	launcher := &accountPauseLauncher{}
	manager, sessions, runtime := newChatManager(launcher)
	proxy := &managerAccountProxy{}
	n := 0
	accounts := provideraccounts.New(sqlitetest.MustOpen(t), proxy, manager, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:4321", func() string { n++; return fmt.Sprintf("account-%d", n) })
	manager.SetProviderAccounts(accounts)
	return manager, sessions, runtime, launcher, accounts, proxy
}
func managerLogin(t *testing.T, accounts *provideraccounts.Service, provider, email string) string {
	t.Helper()
	id, err := accounts.RecordCredential(context.Background(), ports.VerifiedProviderLogin{Provider: provider, Email: email, CredentialRef: email + ".json", AuthID: "auth-" + email}, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func managerSpawn(t *testing.T, m *Manager, harness domain.AgentHarness, selection string) domain.SessionRecord {
	t.Helper()
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: harness, RequestedMode: domain.SessionModeChat, ProviderAccountID: selection})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestPreparedManagedSessionUsesSelectedProviderInsteadOfProjectDefault(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		for _, async := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/async=%t", provider, async), func(t *testing.T) {
				m, sessions, runtime, launcher, accounts, _ := managedChatFixture(t)
				m.runBackground = func(work func()) { work() }
				selected, prepared := domain.HarnessCodex, domain.HarnessClaudeCode
				if provider == "claude" {
					selected, prepared = prepared, selected
				}
				project := sessions.projects[string(chatTestProject)]
				project.Config.Worker.Harness = prepared
				sessions.projects[string(chatTestProject)] = project
				accountID := managerLogin(t, accounts, provider, "prepared@example.test")
				token, err := m.PrepareTaskWorkspace(context.Background(), project)
				if err != nil {
					t.Fatal(err)
				}
				prep := m.taskPreparations[token]
				if prep == nil || prep.record.Harness != "" {
					t.Fatal("fixture did not reserve a session with its provider unresolved")
				}
				id := prep.record.ID
				rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
					ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: selected,
					ProviderAccountID: accountID, RequestedMode: domain.SessionModeChat,
					TaskPreparation: token, Async: async,
				})
				if err != nil {
					t.Fatal(err)
				}
				if rec.ID != id || rec.Harness != selected || sessions.sessions[id].IsTaskPreparation {
					t.Fatal("selected provider did not promote the reserved session")
				}
				route, managed, err := accounts.SessionAccount(context.Background(), id)
				if err != nil || !managed || route.Provider != provider || route.AccountID != accountID {
					t.Fatalf("prepared session selected the wrong account: route=%+v error=%v", route, err)
				}
				if len(launcher.started) != 1 || launcher.started[0].Harness != selected || runtime.created != 0 {
					t.Fatal("prepared managed chat launched another provider or an extra terminal")
				}
				env, err := accounts.LaunchAccountEnv(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}
				for name, value := range env {
					if launcher.started[0].Env[name] != value {
						t.Fatalf("selected provider launch lost its account field %s", name)
					}
				}
			})
		}
	}
}

func TestManagedChatLaunchCarriesOneSessionTicketAndNoAccountCredential(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			m, sessions, runtime, launcher, accounts, _ := managedChatFixture(t)
			account := managerLogin(t, accounts, provider, "alice@example.test")
			harness := domain.HarnessCodex
			if provider == "claude" {
				harness = domain.HarnessClaudeCode
			}
			rec := managerSpawn(t, m, harness, account)
			if len(launcher.started) != 1 || runtime.created != 0 {
				t.Fatal("managed chat did not have exactly one native controller")
			}
			start := launcher.started[0]
			if start.SessionID != rec.ID || start.Harness != harness || start.Env["AO_SESSION_ID"] != string(rec.ID) {
				t.Fatal("launch environment lost session ownership")
			}
			managedEnv, err := accounts.LaunchAccountEnv(context.Background(), rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			for name, value := range managedEnv {
				if start.Env[name] != value {
					t.Fatalf("managed launch field %s not forwarded", name)
				}
				if strings.Contains(value, "alice@example.test") || strings.Contains(value, "auth-alice") {
					t.Fatalf("launch exposes account credential through %s", name)
				}
			}
			stored := sessions.sessions[rec.ID]
			if stored.Metadata.ProviderConversationID == "" || stored.Metadata.ControllerGeneration == "" {
				t.Fatal("managed routing lost native conversation ownership")
			}
			if stored.Metadata.RuntimeHandleID != "" {
				t.Fatal("managed Chat launch accidentally owns a terminal")
			}
			route, managed, err := accounts.SessionAccount(context.Background(), rec.ID)
			if err != nil || !managed || route.AccountID != account || route.Provider != provider {
				t.Fatalf("saved assignment=%+v managed=%v err=%v", route, managed, err)
			}
			if strings.Contains(stored.Metadata.Prompt, "auth-alice") {
				t.Fatal("saved prompt contains account credential")
			}
		})
	}
}
func TestManagedChatPrimaryChangeLeavesExistingNativeControllerAndRoute(t *testing.T) {
	m, sessions, runtime, launcher, accounts, _ := managedChatFixture(t)
	alice := managerLogin(t, accounts, "codex", "alice@example.test")
	bob := managerLogin(t, accounts, "codex", "bob@example.test")
	first := managerSpawn(t, m, domain.HarnessCodex, "")
	firstEnv, err := accounts.LaunchAccountEnv(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner := sessions.sessions[first.ID].ControllerOwner()
	stored := sessions.sessions[first.ID]
	stored.Activity.State = domain.ActivityActive
	sessions.sessions[first.ID] = stored
	if err = accounts.SetPrimary(context.Background(), bob); err != nil {
		t.Fatal(err)
	}
	if len(launcher.stopped) != 0 || len(launcher.started) != 1 || runtime.destroyed != 0 {
		t.Fatal("new primary restarted the existing native conversation")
	}
	route, managed, err := accounts.SessionAccount(context.Background(), first.ID)
	if err != nil || !managed || route.AccountID != alice {
		t.Fatalf("existing primary route changed=%+v %v", route, err)
	}
	if sessions.sessions[first.ID].ControllerOwner() != owner {
		t.Fatal("new primary changed existing durable controller owner")
	}
	next := managerSpawn(t, m, domain.HarnessCodex, "")
	route, managed, err = accounts.SessionAccount(context.Background(), next.ID)
	if err != nil || !managed || route.AccountID != bob {
		t.Fatalf("new session did not use new primary=%+v %v", route, err)
	}
	nextEnv, err := accounts.LaunchAccountEnv(context.Background(), next.ID)
	if err != nil {
		t.Fatal(err)
	}
	if nextEnv["AO_PROXY_TICKET"] == firstEnv["AO_PROXY_TICKET"] {
		t.Fatal("two sessions share their route identity")
	}
	if len(launcher.paused) != 0 {
		t.Fatal("changing only defaults paused existing sessions")
	}
}
func TestManagedChatManualSwitchKeepsProviderConversationAndTicket(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			m, sessions, runtime, launcher, accounts, proxy := managedChatFixture(t)
			alice := managerLogin(t, accounts, provider, "alice@example.test")
			bob := managerLogin(t, accounts, provider, "bob@example.test")
			harness := domain.HarnessCodex
			if provider == "claude" {
				harness = domain.HarnessClaudeCode
			}
			rec := managerSpawn(t, m, harness, alice)
			stored := sessions.sessions[rec.ID]
			stored.Activity.State = domain.ActivityIdle
			sessions.sessions[rec.ID] = stored
			owner := stored.ControllerOwner()
			before, err := accounts.LaunchAccountEnv(context.Background(), rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = accounts.Switch(context.Background(), rec.ID, bob); err != nil {
				t.Fatal(err)
			}
			after, err := accounts.LaunchAccountEnv(context.Background(), rec.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("manual account switch changed native launch identity")
			}
			if len(launcher.started) != 1 || len(launcher.stopped) != 0 || runtime.destroyed != 0 {
				t.Fatal("account switch replaced or stopped native controller")
			}
			if sessions.sessions[rec.ID].ControllerOwner() != owner {
				t.Fatal("account switch changed native conversation or generation")
			}
			if !reflect.DeepEqual(launcher.paused, []domain.SessionID{rec.ID}) || !reflect.DeepEqual(launcher.released, launcher.paused) {
				t.Fatal("manual switch did not fence and release input exactly once")
			}
			route, managed, err := accounts.SessionAccount(context.Background(), rec.ID)
			if err != nil || !managed || route.AccountID != bob {
				t.Fatalf("manual assignment=%+v %v", route, err)
			}
			for _, r := range proxy.snapshot.Routes {
				if r.SessionID == rec.ID && r.AuthID != "auth-bob@example.test" {
					t.Fatal("helper snapshot still points to old account")
				}
			}
			catalogue, err := accounts.State(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range catalogue.Primaries {
				if p.Provider == provider && p.PrimaryID != alice {
					t.Fatal("manual session switch changed primary")
				}
			}
		})
	}
}
func TestManagedChatBusySwitchDoesNotQueueOrRestart(t *testing.T) {
	m, sessions, _, launcher, accounts, proxy := managedChatFixture(t)
	alice := managerLogin(t, accounts, "codex", "alice@example.test")
	bob := managerLogin(t, accounts, "codex", "bob@example.test")
	rec := managerSpawn(t, m, domain.HarnessCodex, alice)
	stored := sessions.sessions[rec.ID]
	stored.Activity.State = domain.ActivityActive
	sessions.sessions[rec.ID] = stored
	before, err := accounts.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	helperBefore := proxy.snapshot
	if err = accounts.Switch(context.Background(), rec.ID, bob); !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("busy switch=%v", err)
	}
	if len(launcher.paused) != 0 || len(launcher.stopped) != 0 {
		t.Fatal("busy refusal touched active native controller")
	}
	stored.Activity.State = domain.ActivityIdle
	sessions.sessions[rec.ID] = stored
	if err = accounts.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := accounts.State(context.Background())
	if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(helperBefore, proxy.snapshot) {
		t.Fatal("refused busy switch was automatically queued")
	}
	if err = accounts.Switch(context.Background(), rec.ID, bob); err != nil {
		t.Fatal(err)
	}
	if len(launcher.paused) != 1 || len(launcher.released) != 1 {
		t.Fatal("explicit idle retry failed to release admission")
	}
}
func TestManagedChatRestoreUsesSavedNativeConversationAndSameTicket(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			m, sessions, runtime, launcher, accounts, _ := managedChatFixture(t)
			account := managerLogin(t, accounts, provider, "alice@example.test")
			harness := domain.HarnessCodex
			if provider == "claude" {
				harness = domain.HarnessClaudeCode
			}
			rec := managerSpawn(t, m, harness, account)
			before, err := accounts.LaunchAccountEnv(context.Background(), rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Kill(context.Background(), rec.ID); err != nil {
				t.Fatal(err)
			}
			native := sessions.sessions[rec.ID].Metadata.ProviderConversationID
			if native == "" {
				t.Fatal("terminating lost provider history handle")
			}
			result, err := m.RestoreWithMode(context.Background(), rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if result.Mode != RestoreModeNative || len(launcher.started) != 2 || runtime.created != 0 {
				t.Fatal("restore used a different surface or replay mode")
			}
			resumed := launcher.started[1]
			if resumed.ProviderConversationID != native || resumed.SessionID != rec.ID || resumed.Harness != harness {
				t.Fatal("restore lost saved native conversation")
			}
			for name, value := range before {
				if resumed.Env[name] != value {
					t.Fatalf("restore changed managed field %s", name)
				}
			}
			route, managed, err := accounts.SessionAccount(context.Background(), rec.ID)
			if err != nil || !managed || route.AccountID != account {
				t.Fatal("restore resolved a new default instead of saved account")
			}
			if len(launcher.turns) != 0 {
				t.Fatal("restore resent the previous prompt")
			}
		})
	}
}
func TestManagedChatLastLogoutAndReloginReuseRunningSession(t *testing.T) {
	m, sessions, runtime, launcher, accounts, proxy := managedChatFixture(t)
	alice := managerLogin(t, accounts, "codex", "alice@example.test")
	rec := managerSpawn(t, m, domain.HarnessCodex, alice)
	stored := sessions.sessions[rec.ID]
	stored.Activity.State = domain.ActivityIdle
	sessions.sessions[rec.ID] = stored
	before, err := accounts.LaunchAccountEnv(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner := stored.ControllerOwner()
	if err = accounts.Remove(context.Background(), alice, "", true); err != nil {
		t.Fatal(err)
	}
	waiting, managed, err := accounts.SessionAccount(context.Background(), rec.ID)
	if err != nil || !managed || waiting.AccountID != "" {
		t.Fatal("last logout did not leave managed session waiting")
	}
	if _, _, err = accounts.ResolveAccount(context.Background(), domain.HarnessCodex, ""); !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatal("last logout allowed new native fallback")
	}
	if len(launcher.stopped) != 0 || runtime.destroyed != 0 {
		t.Fatal("last logout stopped existing session")
	}
	renewed, err := accounts.RecordCredential(context.Background(), ports.VerifiedProviderLogin{Provider: "codex", Email: "alice@example.test", CredentialRef: "renewed.json", AuthID: "renewed-auth"}, alice)
	if err != nil || renewed != alice {
		t.Fatalf("relogin=%s %v", renewed, err)
	}
	restored, managed, err := accounts.SessionAccount(context.Background(), rec.ID)
	if err != nil || !managed || restored.AccountID != alice {
		t.Fatal("relogin did not restore waiting session")
	}
	after, err := accounts.LaunchAccountEnv(context.Background(), rec.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("relogin changed live native endpoint or ticket")
	}
	if sessions.sessions[rec.ID].ControllerOwner() != owner || len(launcher.started) != 1 || len(launcher.stopped) != 0 {
		t.Fatal("relogin restarted native conversation")
	}
	if len(launcher.turns) != 0 || len(launcher.relayed) != 0 || len(launcher.queued) != 0 {
		t.Fatal("relogin automatically resent a user request")
	}
	if !reflect.DeepEqual(proxy.deleted, []string{"alice@example.test.json"}) {
		t.Fatalf("logout cleanup=%v", proxy.deleted)
	}
}
func TestManagedSessionCannotSwitchProviderBeforeStoppingSource(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeTUI, domain.SessionModeChat} {
		t.Run(string(mode), func(t *testing.T) {
			runtime := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{}}
			m, sessions, _ := newSwitchTestManager(t, runtime)
			rec := sessions.sessions["proj-1"]
			rec.Mode = mode
			rec.Metadata.ProviderConversationID = "source-native-chat"
			rec.Metadata.ControllerGeneration = "source-generation"
			sessions.sessions[rec.ID] = rec
			routes := &accountRoutingFake{managed: true, route: domain.ProviderSessionRoute{Provider: "claude", AccountID: "alice"}}
			m.SetProviderAccounts(routes)
			before := sessions.sessions[rec.ID]
			_, err := m.SwitchAgent(context.Background(), rec.ID, SwitchAgentConfig{TargetHarness: domain.HarnessCodex, IdempotencyKey: "managed-harness-switch"})
			if !errors.Is(err, ports.ErrProviderAccountIncompatible) {
				t.Fatalf("provider switch=%v", err)
			}
			if !reflect.DeepEqual(sessions.sessions[rec.ID], before) {
				t.Fatal("refused provider change mutated source session")
			}
			if len(runtime.destroyedIDs) != 0 || runtime.created != 0 {
				t.Fatal("refused provider change stopped or replaced native process")
			}
			if len(routes.assigned) != 0 || len(routes.launches) != 0 || len(routes.forgotten) != 0 {
				t.Fatal("refused provider switch changed account ownership")
			}
		})
	}
}
