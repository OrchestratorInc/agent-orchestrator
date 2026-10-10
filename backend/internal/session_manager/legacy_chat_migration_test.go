package sessionmanager

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

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

type switchingStore struct{ *switchTestStore }

func (switchingStore) GetActiveAgentSwitch(context.Context, domain.SessionID) (domain.AgentSwitch, bool, error) {
	return domain.AgentSwitch{}, true, nil
}

func TestALegacyChatGetsItsAccountAsItsProviderIsLaunched(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	accounts := &accountRoutingFake{env: managedLaunch}
	m.accounts = accounts
	rec := legacyChat("mer-1")
	// A second launch finds the account and changes nothing.
	for range 2 {
		st.sessions[rec.ID] = rec
		if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(accounts.adopted, []domain.SessionID{rec.ID}) || len(launcher.started) != 2 || launcher.started[0].Env["AO_PROXY_TICKET"] != "session-ticket" {
		t.Fatalf("adopted=%v started=%#v", accounts.adopted, launcher.started)
	}
}

func TestALegacyChatKeepsThisComputersSignInWhenItCannotBeAdopted(t *testing.T) {
	for name, prepare := range map[string]func(*Manager, *accountRoutingFake, *domain.SessionRecord){
		"another agent": func(_ *Manager, _ *accountRoutingFake, rec *domain.SessionRecord) { rec.Harness = domain.HarnessCursor },
		"changing agent": func(m *Manager, _ *accountRoutingFake, _ *domain.SessionRecord) {
			m.store = switchingStore{newSwitchTestStore()}
		},
		"helper refuses": func(_ *Manager, f *accountRoutingFake, _ *domain.SessionRecord) { f.err = errors.New("helper refused") },
	} {
		m, _, _ := newChatManager(&recordingLauncher{})
		accounts := &accountRoutingFake{env: managedLaunch}
		m.accounts = accounts
		rec := legacyChat("mer-1")
		prepare(m, accounts, &rec)
		m.adoptLegacyChat(context.Background(), rec)
		if len(accounts.adopted) != 0 {
			t.Errorf("%s: adopted=%v", name, accounts.adopted)
		}
	}
}

// restartingLauncher is a Chat service whose providers can be stopped and
// started again; starting one is what gives the chat its account.
type restartingLauncher struct {
	*recordingLauncher
	store    *fakeStore
	accounts *accountRoutingFake
	running  map[domain.SessionID]bool
	busy     map[domain.SessionID]bool
	restarts []domain.SessionID
	wakeErr  error
}

func (l *restartingLauncher) HasLiveChatController(id domain.SessionID) bool { return l.running[id] }

func (l *restartingLauncher) HibernateChatForRestart(_ context.Context, id domain.SessionID) (bool, error) {
	if l.busy[id] {
		return false, nil
	}
	rec := l.store.sessions[id]
	rec.HibernatedAt = &rec.CreatedAt
	l.store.sessions[id], l.running[id] = rec, false
	return true, nil
}

func (l *restartingLauncher) WakeChat(ctx context.Context, id domain.SessionID) error {
	if l.wakeErr != nil {
		return l.wakeErr
	}
	rec := l.store.sessions[id]
	rec.HibernatedAt = nil
	l.store.sessions[id], l.running[id] = rec, true
	l.restarts = append(l.restarts, id)
	_, err := l.accounts.AdoptSession(ctx, id, rec.Harness)
	return err
}

func TestMigrateLegacyChatsRestartsOnlyQuietChatsWithoutAnAccount(t *testing.T) {
	accounts := &accountRoutingFake{has: map[domain.SessionID]bool{"managed": true}}
	launcher := &restartingLauncher{
		recordingLauncher: &recordingLauncher{}, accounts: accounts, busy: map[domain.SessionID]bool{"busy": true},
		running: map[domain.SessionID]bool{"quiet-1": true, "quiet-2": true, "busy": true, "managed": true, "terminal": true, "other-agent": true, "ended": true},
	}
	m, st, _ := newChatManager(launcher)
	launcher.store, m.accounts = st, accounts
	for id, change := range map[domain.SessionID]func(*domain.SessionRecord){
		"quiet-1": nil, "busy": nil, "managed": nil,
		"quiet-2": func(rec *domain.SessionRecord) {
			rec.Harness, rec.Kind = domain.HarnessClaudeCode, domain.KindOrchestrator
		},
		"asleep":      func(rec *domain.SessionRecord) { rec.HibernatedAt = &rec.CreatedAt },
		"stopped":     func(rec *domain.SessionRecord) { rec.Activity.State = domain.ActivityExited },
		"terminal":    func(rec *domain.SessionRecord) { rec.Mode = domain.SessionModeTUI },
		"other-agent": func(rec *domain.SessionRecord) { rec.Harness = domain.HarnessCursor },
		"ended":       func(rec *domain.SessionRecord) { rec.IsTerminated = true },
	} {
		rec := legacyChat(id)
		rec.Activity.State = domain.ActivityIdle
		if change != nil {
			change(&rec)
		}
		st.sessions[id] = rec
	}
	// Only the busy chat keeps running without an account: the sleeping and the
	// stopped one have no process and are adopted when they next start.
	remaining, err := m.MigrateLegacyChats(context.Background())
	if err != nil || remaining != 1 || len(launcher.restarts) != 2 || !accounts.has["quiet-1"] || !accounts.has["quiet-2"] {
		t.Fatalf("remaining=%d err=%v restarts=%v", remaining, err, launcher.restarts)
	}
	// The busy chat finishes its turn; a later pass has nothing left to do.
	launcher.busy["busy"] = false
	for range 2 {
		if remaining, err = m.MigrateLegacyChats(context.Background()); err != nil || remaining != 0 || len(launcher.restarts) != 3 {
			t.Fatalf("later pass: remaining=%d err=%v restarts=%v", remaining, err, launcher.restarts)
		}
	}
}

func TestMigrateLegacyChatsLeavesAChatAsleepWhenItCannotBeStartedAgain(t *testing.T) {
	failure := errors.New("provider did not start")
	accounts := &accountRoutingFake{}
	launcher := &restartingLauncher{recordingLauncher: &recordingLauncher{}, accounts: accounts, running: map[domain.SessionID]bool{"quiet": true}, wakeErr: failure}
	m, st, _ := newChatManager(launcher)
	launcher.store, m.accounts = st, accounts
	rec := legacyChat("quiet")
	rec.Activity.State = domain.ActivityIdle
	st.sessions[rec.ID] = rec
	if remaining, err := m.MigrateLegacyChats(context.Background()); !errors.Is(err, failure) || remaining != 1 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
	// It is asleep now, with nothing left to stop. It is adopted when it wakes.
	if remaining, err := m.MigrateLegacyChats(context.Background()); err != nil || remaining != 0 {
		t.Fatalf("next pass: remaining=%d err=%v", remaining, err)
	}
}
