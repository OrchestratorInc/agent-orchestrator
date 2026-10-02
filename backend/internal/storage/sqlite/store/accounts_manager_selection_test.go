package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type initialAccountStore interface {
	CreateSessionWithAccount(context.Context, domain.SessionRecord, domain.AccountsManagerAccountChoice) (domain.SessionRecord, bool, error)
	PromoteTaskPreparationWithAccount(context.Context, domain.SessionID, domain.SessionRecord, domain.AccountsManagerAccountChoice) (bool, error)
}

func initialAccountCapability(t *testing.T, st *sqlite.Store) initialAccountStore {
	t.Helper()
	creator, ok := any(st).(initialAccountStore)
	if !ok {
		t.Fatal("atomic initial account selection is unavailable")
	}
	return creator
}

func initialAccountRecord() domain.SessionRecord {
	rec := sampleRecord("initial-choice")
	rec.Harness, rec.Mode = domain.HarnessCodex, domain.SessionModeTUI
	rec.Metadata = domain.SessionMetadata{}
	return rec
}

func TestInitialAccountCreationAtomicAndReopen(t *testing.T) {
	dir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dir)
	seedProject(t, st, "initial-choice")
	creator := initialAccountCapability(t, st)
	selections := []domain.AccountsManagerAccountChoice{
		{Mode: domain.AccountsManagerManaged, AccountID: "account-a"},
		{Mode: domain.AccountsManagerManaged, AccountID: "account-b"},
		{Mode: domain.AccountsManagerNative},
	}
	var sessions []domain.SessionRecord
	for _, choice := range selections {
		rec, fresh, err := creator.CreateSessionWithAccount(t.Context(), initialAccountRecord(), choice)
		if err != nil || !fresh || rec.ID == "" {
			t.Fatal("initial selection did not create a session", err)
		}
		sessions = append(sessions, rec)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.OpenPreMigrated(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for i, rec := range sessions {
		binding, found, err := reopened.GetAccountsManagerSessionRoute(t.Context(), rec.ID, domain.AccountsManagerProviderCodex)
		if err != nil || !found || binding.Mode != selections[i].Mode || binding.AccountID != selections[i].AccountID || binding.Blocked {
			t.Fatal("restart lost or replaced initial account intent", err)
		}
	}
}

func TestInitialAccountCreationRollsBackBindingFailure(t *testing.T) {
	dir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dir)
	seedProject(t, st, "initial-choice")
	creator := initialAccountCapability(t, st)
	db, err := sql.Open("sqlite", filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER refuse_initial_account BEFORE INSERT ON accounts_manager_session_bindings BEGIN SELECT RAISE(ABORT, 'synthetic selection failure'); END`); err != nil {
		t.Fatal(err)
	}
	before, err := st.AccountsManagerBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = creator.CreateSessionWithAccount(t.Context(), initialAccountRecord(), domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-a"})
	if err == nil {
		t.Fatal("failed binding insert was ignored")
	}
	sessions, err := st.ListSessions(t.Context(), "initial-choice")
	if err != nil || len(sessions) != 0 {
		t.Fatal("failed choice left a session that could launch without its binding", err)
	}
	after, err := st.AccountsManagerBindings(t.Context())
	if err != nil || after.Revision != before.Revision || len(after.Bindings) != 0 {
		t.Fatal("rolled-back selection changed the binding ledger", err)
	}
}

func TestInitialAccountCreationRejectsDeletionAndInvalidChoice(t *testing.T) {
	st := newTestStore(t)
	seedProject(t, st, "initial-choice")
	creator := initialAccountCapability(t, st)
	if _, _, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-initial", "account-a", 0, false); err != nil {
		t.Fatal(err)
	}
	for _, choice := range []domain.AccountsManagerAccountChoice{
		{Mode: domain.AccountsManagerManaged, AccountID: "account-a"},
		{Mode: domain.AccountsManagerManaged},
		{Mode: domain.AccountsManagerNative, AccountID: "account-b"},
		{Mode: "fallback", AccountID: "account-b"},
	} {
		if _, _, err := creator.CreateSessionWithAccount(t.Context(), initialAccountRecord(), choice); err == nil {
			t.Fatal("invalid or deleting account created a session")
		}
	}
	if rows, err := st.ListSessions(t.Context(), "initial-choice"); err != nil || len(rows) != 0 {
		t.Fatal("rejected account left a session", err)
	}
	if _, fresh, err := creator.CreateSessionWithAccount(t.Context(), initialAccountRecord(), domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerNative}); err != nil || !fresh {
		t.Fatal("unrelated native choice was blocked", err)
	}
}

func TestInitialAccountPreparedPromotionIsAtomic(t *testing.T) {
	st := newTestStore(t)
	seedProject(t, st, "initial-choice")
	creator := initialAccountCapability(t, st)
	seed := initialAccountRecord()
	seed.IsTaskPreparation = true
	hidden, err := st.CreateSession(t.Context(), seed)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-prepared", "account-a", 0, false); err != nil {
		t.Fatal(err)
	}
	choice := domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-a"}
	if promoted, err := creator.PromoteTaskPreparationWithAccount(t.Context(), hidden.ID, initialAccountRecord(), choice); promoted || !errors.Is(err, domain.ErrAccountsManagerAccountDeleting) {
		t.Fatal("prepared choice bypassed deletion admission", err)
	}
	preserved, found, err := st.GetSession(t.Context(), hidden.ID)
	if err != nil || !found || !preserved.IsTaskPreparation {
		t.Fatal("failed selection exposed the hidden session", err)
	}
	choice.AccountID = "account-b"
	if promoted, err := creator.PromoteTaskPreparationWithAccount(t.Context(), hidden.ID, initialAccountRecord(), choice); err != nil || !promoted {
		t.Fatal("valid prepared choice did not commit", err)
	}
	binding, found, err := st.GetAccountsManagerSessionRoute(t.Context(), hidden.ID, domain.AccountsManagerProviderCodex)
	if err != nil || !found || binding.AccountID != choice.AccountID {
		t.Fatal("promotion lost initial binding", err)
	}
	if promoted, err := creator.PromoteTaskPreparationWithAccount(t.Context(), hidden.ID, initialAccountRecord(), domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerNative}); promoted || err == nil {
		t.Fatal("repeated promotion changed committed account intent")
	}
}

func TestInitialAccountCreationDeletionRace(t *testing.T) {
	for range 12 {
		st := newTestStore(t)
		seedProject(t, st, "initial-choice")
		creator := initialAccountCapability(t, st)
		var spawnErr, deleteErr error
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Go(func() {
			<-start
			_, _, spawnErr = creator.CreateSessionWithAccount(t.Context(), initialAccountRecord(), domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-a"})
		})
		workers.Go(func() {
			<-start
			_, _, deleteErr = st.CreateAccountsManagerRemoval(t.Context(), "race-delete", "account-a", 0, false)
		})
		close(start)
		workers.Wait()
		rows, err := st.ListSessions(t.Context(), "initial-choice")
		if err != nil {
			t.Fatal(err)
		}
		if spawnErr == nil {
			if deleteErr == nil || len(rows) != 1 {
				t.Fatal("creation and unconfirmed deletion both won")
			}
		} else if !errors.Is(spawnErr, domain.ErrAccountsManagerAccountDeleting) || deleteErr != nil || len(rows) != 0 {
			t.Fatal("deletion winner left an orphan session", spawnErr, deleteErr)
		}
	}
}

func TestInitialAccountAutomationAdoptionPreservesChoice(t *testing.T) {
	st := newTestStore(t)
	seedProject(t, st, "initial-choice")
	creator := initialAccountCapability(t, st)
	now := time.Now().UTC()
	if _, err := st.CreateAutomation(t.Context(), domain.Automation{ID: "initial-automation", ProjectID: "initial-choice", DisplayName: "Initial selection", Prompt: "Test", Kind: domain.KindWorker, RRuleText: "FREQ=DAILY", Timezone: "UTC", Enabled: true, NextRunAt: now, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	runID := domain.AutomationRunID("initial-run")
	if _, _, err := st.CreateAutomationRun(t.Context(), domain.AutomationRun{ID: runID, AutomationID: "initial-automation", ScheduledFor: now, Status: domain.AutomationRunSpawning, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	seed := initialAccountRecord()
	seed.AutomationRunID = &runID
	choice := domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-a"}
	created, fresh, err := creator.CreateSessionWithAccount(t.Context(), seed, choice)
	if err != nil || !fresh {
		t.Fatal("first automation selection failed", err)
	}
	before, err := st.AccountsManagerBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	adopted, fresh, err := creator.CreateSessionWithAccount(t.Context(), seed, choice)
	if err != nil || fresh || adopted.ID != created.ID {
		t.Fatal("same choice did not adopt the existing session", err)
	}
	for _, different := range []domain.AccountsManagerAccountChoice{
		{Mode: domain.AccountsManagerManaged, AccountID: "account-b"},
		{Mode: domain.AccountsManagerNative},
	} {
		if _, _, err := creator.CreateSessionWithAccount(t.Context(), seed, different); !errors.Is(err, domain.ErrAccountsManagerBindingConflict) {
			t.Fatal("retry replaced the original choice", err)
		}
	}
	after, err := st.AccountsManagerBindings(t.Context())
	if err != nil || after.Revision != before.Revision || len(after.Bindings) != 1 || after.Bindings[0].AccountID != "account-a" {
		t.Fatal("repeated creation changed the binding ledger", err)
	}
}

func TestInitialAccountPreparedBindingFailureRollsBackPromotion(t *testing.T) {
	dir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dir)
	seedProject(t, st, "initial-choice")
	creator := initialAccountCapability(t, st)
	seed := initialAccountRecord()
	seed.IsTaskPreparation = true
	hidden, err := st.CreateSession(t.Context(), seed)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER refuse_initial_promotion BEFORE INSERT ON accounts_manager_session_bindings BEGIN SELECT RAISE(ABORT, 'synthetic promotion failure'); END`); err != nil {
		t.Fatal(err)
	}
	if promoted, err := creator.PromoteTaskPreparationWithAccount(t.Context(), hidden.ID, initialAccountRecord(), domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-a"}); err == nil || promoted {
		t.Fatal("failed binding insert promoted the session")
	}
	preserved, found, err := st.GetSession(t.Context(), hidden.ID)
	if err != nil || !found || !preserved.IsTaskPreparation {
		t.Fatal("binding failure exposed the prepared session", err)
	}
	if bindings, err := st.AccountsManagerBindings(t.Context()); err != nil || len(bindings.Bindings) != 0 {
		t.Fatal("failed promotion left a binding", err)
	}
}
