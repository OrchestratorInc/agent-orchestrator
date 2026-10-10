package sessionmanager

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// adoptingAccounts is an Account Manager that takes in sessions it did not
// create and remembers, per session, whether it has one.
type adoptingAccounts struct {
	accountRoutingFake
	mu         sync.Mutex
	has        map[domain.SessionID]bool
	adopted    []domain.SessionID
	adoptErr   error
	unfinished bool
}

func (f *adoptingAccounts) SessionAccount(_ context.Context, id domain.SessionID) (domain.ProviderSessionRoute, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return domain.ProviderSessionRoute{SessionID: id, Provider: "codex", AccountID: "default"}, f.has[id], nil
}

func (f *adoptingAccounts) AdoptSession(_ context.Context, id domain.SessionID, _ domain.AgentHarness) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.adoptErr != nil {
		return false, f.adoptErr
	}
	if f.has[id] {
		return false, nil
	}
	if f.has == nil {
		f.has = make(map[domain.SessionID]bool)
	}
	f.has[id] = true
	f.adopted = append(f.adopted, id)
	return true, nil
}

func (f *adoptingAccounts) RecoveryRequired(context.Context) (bool, error) {
	return f.unfinished, nil
}

func legacyChat(id domain.SessionID) domain.SessionRecord {
	return domain.SessionRecord{
		ID: id, ProjectID: chatTestProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityExited},
		Metadata: domain.SessionMetadata{
			Branch: "ao/" + string(id) + "/root", WorkspacePath: "/ws/" + string(id),
			ProviderConversationID: "conversation-" + string(id),
		},
	}
}

var managedLaunch = map[string]string{"AO_PROXY_ENDPOINT": "http://127.0.0.1:4567", "AO_PROXY_TICKET": "session-ticket"}

func TestALegacyChatGetsItsAccountAsItsProviderIsLaunched(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	accounts := &adoptingAccounts{accountRoutingFake: accountRoutingFake{env: managedLaunch}}
	m.SetProviderAccounts(accounts)
	m.SetLegacyChatMigration(true)
	rec := legacyChat("mer-1")
	st.sessions[rec.ID] = rec

	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(accounts.adopted, []domain.SessionID{rec.ID}) {
		t.Fatalf("adopted=%v", accounts.adopted)
	}
	if len(launcher.started) != 1 || launcher.started[0].Env["AO_PROXY_TICKET"] != "session-ticket" {
		t.Fatalf("the provider was not launched on the account: %#v", launcher.started)
	}
	// A second launch finds the account and changes nothing.
	st.sessions[rec.ID] = legacyChat(rec.ID)
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatal(err)
	}
	if len(accounts.adopted) != 1 {
		t.Fatalf("adopted again: %v", accounts.adopted)
	}
}

func TestALegacyChatKeepsThisComputersSignInWhenTheMoveIsOff(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	accounts := &adoptingAccounts{accountRoutingFake: accountRoutingFake{env: managedLaunch}}
	m.SetProviderAccounts(accounts)
	rec := legacyChat("mer-1")
	st.sessions[rec.ID] = rec

	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatal(err)
	}
	if len(accounts.adopted) != 0 || launcher.started[0].Env["AO_PROXY_TICKET"] != "" {
		t.Fatalf("adopted=%v env=%v", accounts.adopted, launcher.started[0].Env)
	}
}

func TestALegacyChatIsNotAdoptedByAnotherAgentOrWhileChangingAgent(t *testing.T) {
	t.Run("another agent", func(t *testing.T) {
		launcher := &recordingLauncher{}
		m, st, _ := newChatManager(launcher)
		accounts := &adoptingAccounts{}
		m.SetProviderAccounts(accounts)
		m.SetLegacyChatMigration(true)
		rec := legacyChat("mer-1")
		rec.Harness = domain.HarnessCursor
		st.sessions[rec.ID] = rec
		if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
			t.Fatal(err)
		}
		if len(accounts.adopted) != 0 {
			t.Fatalf("adopted=%v", accounts.adopted)
		}
	})
	t.Run("changing agent", func(t *testing.T) {
		m, _, _ := newChatManager(&recordingLauncher{})
		accounts := &adoptingAccounts{}
		m.SetProviderAccounts(accounts)
		m.SetLegacyChatMigration(true)
		m.store = switchingStore{Store: m.store}
		// The account would belong to the provider the session is leaving.
		if err := m.adoptLegacyChat(context.Background(), legacyChat("mer-1")); err != nil || len(accounts.adopted) != 0 {
			t.Fatalf("err=%v adopted=%v", err, accounts.adopted)
		}
	})
}

// switchingStore reports every session as changing agent.
type switchingStore struct{ Store }

func (switchingStore) GetActiveAgentSwitch(context.Context, domain.SessionID) (domain.AgentSwitch, bool, error) {
	return domain.AgentSwitch{}, true, nil
}

func TestAnAdoptionThatWillStillTakeEffectHoldsTheLaunch(t *testing.T) {
	failure := errors.New("the helper did not answer")
	for name, tc := range map[string]struct {
		unfinished bool
		launched   bool
	}{
		// Written down, not yet sent: launching on this computer's own sign-in
		// would leave a chat that reads as managed and is not.
		"written down": {unfinished: true, launched: false},
		// Nothing changed: the chat starts as it always has.
		"not written down": {unfinished: false, launched: true},
	} {
		t.Run(name, func(t *testing.T) {
			launcher := &recordingLauncher{}
			m, st, _ := newChatManager(launcher)
			accounts := &adoptingAccounts{adoptErr: failure, unfinished: tc.unfinished}
			m.SetProviderAccounts(accounts)
			m.SetLegacyChatMigration(true)
			rec := legacyChat("mer-1")
			st.sessions[rec.ID] = rec

			_, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
			if launched := err == nil && len(launcher.started) == 1; launched != tc.launched {
				t.Fatalf("launched=%v err=%v started=%d", launched, err, len(launcher.started))
			}
			if !tc.launched && !errors.Is(err, failure) {
				t.Fatalf("err=%v, want the adoption failure", err)
			}
			if tc.launched && launcher.started[0].Env["AO_PROXY_TICKET"] != "" {
				t.Fatalf("an unadopted chat was pointed at the helper: %v", launcher.started[0].Env)
			}
		})
	}
}

// restartingLauncher is a Chat service whose quiet providers can be stopped and
// started again.
type restartingLauncher struct {
	*recordingLauncher
	mu       sync.Mutex
	store    *fakeStore
	running  map[domain.SessionID]bool
	busy     map[domain.SessionID]bool
	restarts []domain.SessionID
	wake     func(domain.SessionID) error
}

func (l *restartingLauncher) HasLiveChatController(id domain.SessionID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running[id]
}

func (l *restartingLauncher) HibernateChatForRestart(_ context.Context, id domain.SessionID) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.busy[id] {
		return false, nil
	}
	l.running[id] = false
	l.sleeping(id, true)
	return true, nil
}

// sleeping records, as the Chat service does, that a stopped chat can be woken.
func (l *restartingLauncher) sleeping(id domain.SessionID, asleep bool) {
	rec := l.store.sessions[id]
	rec.HibernatedAt = nil
	if asleep {
		at := rec.CreatedAt
		rec.HibernatedAt = &at
	}
	l.store.sessions[id] = rec
}

func (l *restartingLauncher) WakeChat(_ context.Context, id domain.SessionID) error {
	if err := l.wake(id); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.running[id] = true
	l.sleeping(id, false)
	l.restarts = append(l.restarts, id)
	return nil
}

func TestMigrateLegacyChatsRestartsOnlyQuietChatsWithoutAnAccount(t *testing.T) {
	accounts := &adoptingAccounts{has: map[domain.SessionID]bool{"managed": true}}
	launcher := &restartingLauncher{
		recordingLauncher: &recordingLauncher{},
		running:           map[domain.SessionID]bool{"quiet-1": true, "quiet-2": true, "busy": true, "managed": true, "terminal": true, "other-agent": true, "ended": true},
		busy:              map[domain.SessionID]bool{"busy": true},
	}
	// Starting a chat's provider is what gives it its account.
	launcher.wake = func(id domain.SessionID) error {
		_, err := accounts.AdoptSession(context.Background(), id, domain.HarnessCodex)
		return err
	}
	m, st, _ := newChatManager(launcher)
	launcher.store = st
	m.SetProviderAccounts(accounts)
	for _, id := range []domain.SessionID{"quiet-1", "quiet-2", "busy", "asleep", "stopped", "managed", "terminal", "other-agent", "ended"} {
		rec := legacyChat(id)
		rec.Activity.State = domain.ActivityIdle
		st.sessions[id] = rec
	}
	edit := func(id domain.SessionID, change func(*domain.SessionRecord)) {
		rec := st.sessions[id]
		change(&rec)
		st.sessions[id] = rec
	}
	edit("quiet-2", func(rec *domain.SessionRecord) {
		rec.Harness, rec.Kind = domain.HarnessClaudeCode, domain.KindOrchestrator
	})
	edit("asleep", func(rec *domain.SessionRecord) { at := rec.CreatedAt; rec.HibernatedAt = &at })
	edit("stopped", func(rec *domain.SessionRecord) { rec.Activity.State = domain.ActivityExited })
	edit("terminal", func(rec *domain.SessionRecord) { rec.Mode = domain.SessionModeTUI })
	edit("other-agent", func(rec *domain.SessionRecord) { rec.Harness = domain.HarnessCursor })
	edit("ended", func(rec *domain.SessionRecord) { rec.IsTerminated = true })

	// Off, nothing is touched.
	if remaining, err := m.MigrateLegacyChats(context.Background()); err != nil || remaining != 0 || len(launcher.restarts) != 0 {
		t.Fatalf("off: remaining=%d err=%v restarts=%v", remaining, err, launcher.restarts)
	}
	m.SetLegacyChatMigration(true)

	remaining, err := m.MigrateLegacyChats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	restarted := append([]domain.SessionID(nil), launcher.restarts...)
	sort.Slice(restarted, func(i, j int) bool { return restarted[i] < restarted[j] })
	if !reflect.DeepEqual(restarted, []domain.SessionID{"quiet-1", "quiet-2"}) {
		t.Fatalf("restarted=%v", restarted)
	}
	// Only the busy chat is still running without an account. The sleeping and
	// the stopped one have no process; they are adopted when they next start.
	if remaining != 1 {
		t.Fatalf("remaining=%d, want only the busy chat", remaining)
	}

	// The busy chat finishes its turn.
	launcher.busy["busy"] = false
	remaining, err = m.MigrateLegacyChats(context.Background())
	if err != nil || remaining != 0 || len(launcher.restarts) != 3 {
		t.Fatalf("second pass: remaining=%d err=%v restarts=%v", remaining, err, launcher.restarts)
	}
	// A pass with nothing to do changes nothing.
	if remaining, err = m.MigrateLegacyChats(context.Background()); err != nil || remaining != 0 || len(launcher.restarts) != 3 {
		t.Fatalf("third pass: remaining=%d err=%v restarts=%v", remaining, err, launcher.restarts)
	}
}

func TestMigrateLegacyChatsLeavesAChatAsleepWhenItCannotBeStartedAgain(t *testing.T) {
	failure := errors.New("provider did not start")
	accounts := &adoptingAccounts{}
	launcher := &restartingLauncher{recordingLauncher: &recordingLauncher{}, running: map[domain.SessionID]bool{"quiet": true}, busy: map[domain.SessionID]bool{}}
	launcher.wake = func(domain.SessionID) error { return failure }
	m, st, _ := newChatManager(launcher)
	launcher.store = st
	m.SetProviderAccounts(accounts)
	m.SetLegacyChatMigration(true)
	rec := legacyChat("quiet")
	rec.Activity.State = domain.ActivityIdle
	st.sessions[rec.ID] = rec

	remaining, err := m.MigrateLegacyChats(context.Background())
	if !errors.Is(err, failure) || remaining != 1 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
	// It is asleep now, with nothing left to stop. It is adopted when it wakes.
	if remaining, err = m.MigrateLegacyChats(context.Background()); err != nil || remaining != 0 {
		t.Fatalf("next pass: remaining=%d err=%v", remaining, err)
	}
	var _ ports.ProviderAccountRouting = accounts
}
