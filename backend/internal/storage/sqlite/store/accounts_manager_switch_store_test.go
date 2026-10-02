package store_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func switchFixture(t *testing.T) (*sqlite.Store, domain.SessionRecord, domain.AccountsManagerSwitch) {
	t.Helper()
	store := newTestStore(t)
	return switchFixtureInStore(t, store)
}

func switchFixtureInStore(t *testing.T, store *sqlite.Store) (*sqlite.Store, domain.SessionRecord, domain.AccountsManagerSwitch) {
	t.Helper()
	seedProject(t, store, "switches")
	record := sampleRecord("switches")
	record.Harness, record.Mode = domain.HarnessCodex, domain.SessionModeTUI
	record.Metadata.RuntimeLaunchID, record.Metadata.RuntimeHandleID = "source-generation", "source-handle"
	session, err := store.CreateSession(t.Context(), record)
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err := store.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: session.ID, Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerManaged, AccountID: "account-a"})
	if err != nil {
		t.Fatal(err)
	}
	op := domain.AccountsManagerSwitch{ID: "switch-1", SessionID: session.ID, Provider: binding.Provider, SourceMode: binding.Mode, SourceAccountID: binding.AccountID, SourceRevision: binding.Revision, SourceOwner: session.ControllerOwner(), SourceRuntimeHandleID: session.Metadata.RuntimeHandleID, TargetMode: domain.AccountsManagerManaged, TargetAccountID: "account-b", TargetGeneration: "target-generation", Policy: domain.SessionInterfaceTransitionDrain}
	return store, session, op
}

func TestAccountsManagerSwitchRollbackAndRestartRetainFence(t *testing.T) {
	directory := t.TempDir()
	store, err := sqlitetest.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	_, session, request := switchFixtureInStore(t, store)
	op, _, err := store.CreateAccountsManagerSwitch(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchWaiting, domain.AccountsManagerSwitchStopping, domain.AccountsManagerSwitchStopped} {
		op, err = store.AdvanceAccountsManagerSwitch(t.Context(), op.ID, op.Phase, next, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(directory, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER refuse_account_switch_commit BEFORE UPDATE OF phase ON accounts_manager_switches WHEN NEW.phase = 'committed' BEGIN SELECT RAISE(ABORT, 'synthetic commit failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.AccountsManagerBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitAccountsManagerSwitch(t.Context(), op.ID); err == nil {
		t.Fatal("injected journal failure was ignored")
	}
	after, err := store.AccountsManagerBindings(t.Context())
	if err != nil || after.Revision != before.Revision || after.Bindings[0].AccountID != "account-a" || !after.Bindings[0].Blocked {
		t.Fatal("failed journal write partially committed the binding")
	}
	if _, err := db.Exec(`DROP TRIGGER refuse_account_switch_commit`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.OpenPreMigrated(directory)
	if err != nil {
		t.Fatal(err)
	}
	recovered, found, err := store.GetLatestAccountsManagerSwitch(t.Context(), session.ID)
	if err != nil || !found || recovered.Phase != domain.AccountsManagerSwitchStopped || recovered.SourceOwner != request.SourceOwner || recovered.TargetGeneration != request.TargetGeneration {
		t.Fatal("restart lost switch ownership or phase")
	}
	op, err = store.CommitAccountsManagerSwitch(t.Context(), recovered.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.OpenPreMigrated(directory)
	if err != nil {
		t.Fatal(err)
	}
	recovered, found, err = store.GetAccountsManagerSwitch(t.Context(), op.ID)
	if err != nil || !found || recovered.Phase != domain.AccountsManagerSwitchCommitted || recovered.TargetRevision != op.TargetRevision {
		t.Fatal("restart lost committed target")
	}
	binding, _, err := store.GetAccountsManagerSessionRoute(t.Context(), session.ID, op.Provider)
	if err != nil || binding.Revision != recovered.TargetRevision || binding.AccountID != request.TargetAccountID {
		t.Fatal("restart disagrees on committed binding")
	}
}

func TestAccountsManagerSwitchJournalCommitAndAcknowledgement(t *testing.T) {
	store, session, request := switchFixture(t)
	ctx := t.Context()
	created, inserted, err := store.CreateAccountsManagerSwitch(ctx, request)
	if err != nil || !inserted || created.Phase != domain.AccountsManagerSwitchRequested {
		t.Fatalf("create=%+v inserted=%v err=%v", created, inserted, err)
	}
	replayed, inserted, err := store.CreateAccountsManagerSwitch(ctx, request)
	if err != nil || inserted || replayed.ID != created.ID {
		t.Fatal("retry was not idempotent")
	}
	changed := request
	changed.TargetAccountID = "account-c"
	if _, _, err := store.CreateAccountsManagerSwitch(ctx, changed); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
		t.Fatal("operation id reused for another target")
	}
	changed.ID = "switch-2"
	if _, _, err := store.CreateAccountsManagerSwitch(ctx, changed); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
		t.Fatal("second concurrent switch admitted")
	}
	if _, err := store.CommitAccountsManagerSwitch(ctx, request.ID); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
		t.Fatal("binding committed before confirmed stop")
	}
	op, err := store.AdvanceAccountsManagerSwitch(ctx, request.ID, domain.AccountsManagerSwitchRequested, domain.AccountsManagerSwitchWaiting, "")
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.AccountsManagerBindings(ctx)
	if err != nil || len(before.Bindings) != 1 || before.Bindings[0].Blocked {
		t.Fatal("drain prematurely revoked the current response")
	}
	op, err = store.AdvanceAccountsManagerSwitch(ctx, op.ID, op.Phase, domain.AccountsManagerSwitchStopping, "")
	if err != nil {
		t.Fatal(err)
	}
	fenced, err := store.AccountsManagerBindings(ctx)
	if err != nil || !fenced.Bindings[0].Blocked || fenced.Revision <= before.Revision || fenced.Bindings[0].Revision != request.SourceRevision {
		t.Fatal("stop boundary did not durably fence the old binding")
	}
	if _, err := store.AdvanceAccountsManagerSwitch(ctx, op.ID, op.Phase, domain.AccountsManagerSwitchCancelled, ""); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
		t.Fatal("cancellation crossed source stop")
	}
	op, err = store.AdvanceAccountsManagerSwitch(ctx, op.ID, op.Phase, domain.AccountsManagerSwitchStopped, "")
	if err != nil {
		t.Fatal(err)
	}
	op, err = store.CommitAccountsManagerSwitch(ctx, op.ID)
	if err != nil || op.Phase != domain.AccountsManagerSwitchCommitted || op.TargetRevision <= request.SourceRevision {
		t.Fatalf("commit=%+v err=%v", op, err)
	}
	binding, found, err := store.GetAccountsManagerSessionRoute(ctx, session.ID, request.Provider)
	if err != nil || !found || binding.Revision != op.TargetRevision || binding.AccountID != "account-b" {
		t.Fatal("binding and journal did not commit together")
	}
	committed, err := store.AccountsManagerBindings(ctx)
	if err != nil || committed.Bindings[0].Blocked {
		t.Fatal("committed target could not be authorized")
	}
	op, err = store.AdvanceAccountsManagerSwitch(ctx, op.ID, op.Phase, domain.AccountsManagerSwitchStarting, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcknowledgeAccountsManagerSwitch(ctx, op.ID, op.TargetGeneration); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
		t.Fatal("source generation acknowledged target readiness")
	}
	session.Metadata.RuntimeLaunchID, session.Metadata.RuntimeHandleID = op.TargetGeneration, "target-handle"
	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	op, err = store.AcknowledgeAccountsManagerSwitch(ctx, op.ID, op.TargetGeneration)
	if err != nil || op.Phase != domain.AccountsManagerSwitchReady {
		t.Fatalf("acknowledge=%+v err=%v", op, err)
	}
	active, err := store.ListActiveAccountsManagerSwitches(ctx)
	if err != nil || len(active) != 0 {
		t.Fatal("ready operation retained its active slot")
	}
	latest, found, err := store.GetLatestAccountsManagerSwitch(ctx, session.ID)
	if err != nil || !found || latest.TargetAccountID != "account-b" || latest.Phase != domain.AccountsManagerSwitchReady {
		t.Fatal("latest durable operation lost committed intent")
	}
	if _, inserted, err := store.CreateAccountsManagerSwitch(ctx, request); err != nil || inserted {
		t.Fatal("completed retry was not idempotent")
	}
}

func TestAccountsManagerSwitchCancellationRacesStop(t *testing.T) {
	store, _, request := switchFixture(t)
	if _, _, err := store.CreateAccountsManagerSwitch(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AdvanceAccountsManagerSwitch(t.Context(), request.ID, domain.AccountsManagerSwitchRequested, domain.AccountsManagerSwitchWaiting, ""); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, phase := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchCancelled, domain.AccountsManagerSwitchStopping} {
		workers.Go(func() {
			_, err := store.AdvanceAccountsManagerSwitch(t.Context(), request.ID, domain.AccountsManagerSwitchWaiting, phase, "")
			results <- err
		})
	}
	workers.Wait()
	close(results)
	won := 0
	for err := range results {
		if err == nil {
			won++
		} else if !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
			t.Fatal(err)
		}
	}
	if won != 1 {
		t.Fatal("cancellation and stopping both won")
	}
	op, found, err := store.GetAccountsManagerSwitch(t.Context(), request.ID)
	if err != nil || !found {
		t.Fatal(err)
	}
	snapshot, err := store.AccountsManagerBindings(t.Context())
	if err != nil || snapshot.Bindings[0].Blocked != (op.Phase == domain.AccountsManagerSwitchStopping) {
		t.Fatal("authorization fence disagrees with durable winner")
	}
}

func TestAccountsManagerSwitchRejectsChangedSourceAndRetainsRecoveryFence(t *testing.T) {
	store, session, request := switchFixture(t)
	wrong := request
	wrong.SourceOwner.RuntimeLaunchID = "different"
	if _, _, err := store.CreateAccountsManagerSwitch(t.Context(), wrong); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
		t.Fatal("stale source owner admitted")
	}
	wrong = request
	wrong.SourceRevision++
	if _, _, err := store.CreateAccountsManagerSwitch(t.Context(), wrong); !errors.Is(err, domain.ErrAccountsManagerBindingConflict) {
		t.Fatal("stale binding admitted")
	}
	op, _, err := store.CreateAccountsManagerSwitch(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchWaiting, domain.AccountsManagerSwitchStopping, domain.AccountsManagerSwitchStopped} {
		op, err = store.AdvanceAccountsManagerSwitch(t.Context(), op.ID, op.Phase, next, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	session.Metadata.RuntimeLaunchID = "replacement-generation"
	if err := store.UpdateSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitAccountsManagerSwitch(t.Context(), op.ID); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
		t.Fatal("commit crossed replacement ownership")
	}
	binding, _, err := store.GetAccountsManagerSessionRoute(t.Context(), session.ID, request.Provider)
	if err != nil || binding.AccountID != "account-a" || binding.Revision != request.SourceRevision {
		t.Fatal("failed commit changed binding")
	}
	_, err = store.AdvanceAccountsManagerSwitch(t.Context(), op.ID, op.Phase, domain.AccountsManagerSwitchRecoveryRequired, "SOURCE_CHANGED")
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.ListActiveAccountsManagerSwitches(t.Context())
	if err != nil || len(active) != 1 || active[0].Phase != domain.AccountsManagerSwitchRecoveryRequired {
		t.Fatal("restart cannot recover outstanding obligation")
	}
	snapshot, err := store.AccountsManagerBindings(t.Context())
	if err != nil || !snapshot.Bindings[0].Blocked {
		t.Fatal("uncertain recovery reopened authorization")
	}
}
