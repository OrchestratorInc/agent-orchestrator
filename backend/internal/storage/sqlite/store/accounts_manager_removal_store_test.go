package store_test

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestAccountsManagerRemovalRequiresCurrentInUseConsent(t *testing.T) {
	st, session, _ := switchFixture(t)
	impact, err := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
	if err != nil || len(impact.Sessions) != 1 || impact.Sessions[0].SessionID != session.ID {
		t.Fatalf("impact=%+v err=%v", impact, err)
	}
	if _, _, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-1", "account-a", impact.Revision, false); !errors.Is(err, domain.ErrAccountsManagerAccountInUse) {
		t.Fatal("in-use removal lacked confirmation", err)
	}
	if _, _, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-1", "account-a", impact.Revision-1, true); !errors.Is(err, domain.ErrAccountsManagerRemovalConflict) {
		t.Fatal("stale impact accepted", err)
	}
	op, created, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-1", "account-a", impact.Revision, true)
	if err != nil || !created {
		t.Fatal(err)
	}
	if _, created, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-1", "account-a", impact.Revision, true); err != nil || created {
		t.Fatal("removal retry was not idempotent", err)
	}
	if _, _, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-1", "account-b", impact.Revision, true); !errors.Is(err, domain.ErrAccountsManagerRemovalConflict) {
		t.Fatal("same operation id used for another account")
	}
	snapshot, err := st.AccountsManagerBindings(t.Context())
	if err != nil || !snapshot.Bindings[0].Blocked || snapshot.Revision <= impact.Revision {
		t.Fatal("removal did not durably revoke requests")
	}
	if err := st.CompleteAccountsManagerRemoval(t.Context(), op.ID); !errors.Is(err, domain.ErrAccountsManagerRemovalConflict) {
		t.Fatal("completed without credential tombstone acknowledgement")
	}
	if err := st.RecordAccountsManagerRemovalRevoked(t.Context(), op.ID); !errors.Is(err, domain.ErrAccountsManagerRemovalConflict) {
		t.Fatal("credential removal acknowledged before affected stop")
	}
	if err := st.BeginAccountsManagerRemovalStop(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAccountsManagerRemovalBindingsRevoked(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAccountsManagerRemovalStopped(t.Context(), op.ID, session.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAccountsManagerRemovalRevoked(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteAccountsManagerRemoval(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
	if deleting, err := st.AccountsManagerAccountDeleting(t.Context(), op.AccountID); err != nil || !deleting {
		t.Fatal("completed removal dropped the tombstone")
	}
}

func TestAccountsManagerRemovalPreventsNewSelectionsAndPendingTargets(t *testing.T) {
	st, session, request := switchFixture(t)
	if _, _, err := st.CreateAccountsManagerSwitch(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-target", request.TargetAccountID, 0, false); !errors.Is(err, domain.ErrAccountsManagerSwitchConflict) {
		t.Fatal("pending switch target removed", err)
	}
	if _, err := st.AdvanceAccountsManagerSwitch(t.Context(), request.ID, domain.AccountsManagerSwitchRequested, domain.AccountsManagerSwitchCancelled, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-target", request.TargetAccountID, 0, false); err != nil {
		t.Fatal(err)
	}
	request.ID = "second-switch"
	if _, _, err := st.CreateAccountsManagerSwitch(t.Context(), request); !errors.Is(err, domain.ErrAccountsManagerAccountDeleting) {
		t.Fatal("deleting target accepted", err)
	}
	route := domain.AccountsManagerSessionRoute{SessionID: session.ID, Provider: request.Provider, Mode: domain.AccountsManagerManaged, AccountID: request.TargetAccountID}
	if _, err := st.CompareAndSwapAccountsManagerSessionRoute(t.Context(), route, request.SourceRevision); !errors.Is(err, domain.ErrAccountsManagerAccountDeleting) {
		t.Fatal("direct CAS selected deleting account", err)
	}
	if err := st.PutAccountsManagerRoutingPolicy(t.Context(), domain.AccountsManagerRoutingPolicy{Provider: request.Provider, Enabled: true, AccountIDs: []string{request.TargetAccountID}}); !errors.Is(err, domain.ErrAccountsManagerAccountDeleting) {
		t.Fatal("deleting default accepted", err)
	}
	next, err := st.CreateSession(t.Context(), sampleRecord(string(session.ProjectID)))
	if err != nil {
		t.Fatal(err)
	}
	route.SessionID = next.ID
	if _, _, err := st.GetOrCreateAccountsManagerSessionRoute(t.Context(), route); !errors.Is(err, domain.ErrAccountsManagerAccountDeleting) {
		t.Fatal("first binding selected deleting account", err)
	}
}

func TestAccountsManagerRemovalRecoveryRetainsStopsAndClearsOnlyMatchingDefault(t *testing.T) {
	directory := t.TempDir()
	st, err := sqlitetest.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, session, request := switchFixtureInStore(t, st)
	if err := st.PutAccountsManagerRoutingPolicy(t.Context(), domain.AccountsManagerRoutingPolicy{Provider: request.Provider, Enabled: true, AccountIDs: []string{"account-a"}}); err != nil {
		t.Fatal(err)
	}
	other := domain.AccountsManagerRoutingPolicy{Provider: domain.AccountsManagerProviderClaude, Enabled: true, AccountIDs: []string{"unrelated"}}
	if err := st.PutAccountsManagerRoutingPolicy(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	impact, err := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-a", "account-a", impact.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BeginAccountsManagerRemovalStop(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAccountsManagerRemovalBindingsRevoked(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAccountsManagerRemovalStopped(t.Context(), op.ID, session.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAccountsManagerRemovalFailure(t.Context(), op.ID, "RUNNER_UNAVAILABLE"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = sqlite.OpenPreMigrated(directory)
	if err != nil {
		t.Fatal(err)
	}
	recovered, found, err := st.GetAccountsManagerRemoval(t.Context(), op.ID)
	if err != nil || !found || !recovered.Impact.Sessions[0].Stopped || recovered.Phase != domain.AccountsManagerRemovalRecovery {
		t.Fatal("restart lost cleanup obligation", err)
	}
	if err := st.RecordAccountsManagerRemovalRevoked(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAccountsManagerRemovalFailure(t.Context(), op.ID, "CLEANUP_RETRY"); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteAccountsManagerRemoval(t.Context(), op.ID); err != nil {
		t.Fatal(err)
	}
	policy, err := st.GetAccountsManagerRoutingPolicy(t.Context(), request.Provider)
	if err != nil || policy.Enabled || len(policy.AccountIDs) != 0 {
		t.Fatal("deleted default retained", err)
	}
	policy, err = st.GetAccountsManagerRoutingPolicy(t.Context(), other.Provider)
	if err != nil || !policy.Enabled || len(policy.AccountIDs) != 1 || policy.AccountIDs[0] != "unrelated" {
		t.Fatal("unrelated default changed", err)
	}
	binding, found, err := st.GetAccountsManagerSessionRoute(t.Context(), session.ID, request.Provider)
	if err != nil || !found || binding.AccountID != "account-a" || binding.Mode != domain.AccountsManagerManaged {
		t.Fatal("removal silently changed session choice", err)
	}
}
