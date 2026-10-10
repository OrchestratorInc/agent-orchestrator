package provideraccounts

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAccountHostRestoreDoesNotStartHelperForUnmanagedDevice(t *testing.T) {
	h := setupAccounts(t)
	h.proxy.fail = "apply"
	for attempt := 0; attempt < 3; attempt++ {
		if err := h.svc.RestoreHost(h.ctx); err != nil {
			t.Fatalf("native-only device unnecessarily required helper: %v", err)
		}
	}
	if len(h.proxy.applied) != 0 || len(h.guard.acquired) != 0 {
		t.Fatal("native-only device published managed routes or paused native sessions")
	}
	state, pending, err := h.store.LoadProviderAccountState(h.ctx)
	if err != nil || pending != nil || state.Revision != 0 || len(state.Primaries) != 0 {
		t.Fatal("host maintenance adopted account management without a user sign-in")
	}
}

func TestAccountHostRestoreReplaysEffectiveSnapshotWithoutMovingBusySessions(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	bob := h.login(t, "codex", "bob@example.test")
	clara := h.login(t, "claude", "clara@example.test")
	h.assign(t, "alice", domain.HarnessCodex, alice)
	h.assign(t, "bob", domain.HarnessCodex, bob)
	h.assign(t, "clara", domain.HarnessClaudeCode, clara)
	for _, session := range []domain.SessionID{"alice", "bob", "clara"} {
		h.guard.busy[session] = true
	}
	before, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	remote := clone(h.proxy.snapshot)
	guardsBefore := len(h.guard.acquired)
	applicationsBefore := len(h.proxy.applied)
	for attempt := 0; attempt < 3; attempt++ {
		if err := h.svc.RestoreHost(h.ctx); err != nil {
			t.Fatal(err)
		}
	}
	after, pending, err := h.store.LoadProviderAccountState(h.ctx)
	if err != nil || pending != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(remote, h.proxy.snapshot) {
		t.Fatal("host readiness maintenance changed effective account routing")
	}
	if len(h.guard.acquired) != guardsBefore || len(h.proxy.applied) != applicationsBefore+3 {
		t.Fatal("exact snapshot replay unnecessarily required idle worker proof")
	}
	if len(h.proxy.deleted) != 0 {
		t.Fatal("host readiness maintenance removed credentials")
	}
	h.route(t, "alice", alice)
	h.route(t, "bob", bob)
	h.route(t, "clara", clara)
}

func TestAccountHostRestoreRefillsHelperThatLostItsCache(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	clara := h.login(t, "claude", "clara@example.test")
	h.assign(t, "alice", domain.HarnessCodex, alice)
	h.assign(t, "clara", domain.HarnessClaudeCode, clara)
	h.guard.busy["alice"], h.guard.busy["clara"] = true, true
	before, err := h.svc.State(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	remote := clone(h.proxy.snapshot)
	if remote.Revision < 2 {
		t.Fatal("test needs a revision gap")
	}
	guardsBefore := len(h.guard.acquired)
	// A restarted helper whose routing cache was lost or discarded knows nothing.
	h.proxy.snapshot = ports.ProviderRouteSnapshot{}
	if err := h.svc.RestoreHost(h.ctx); err != nil {
		t.Fatalf("AO could not refill an empty helper: %v", err)
	}
	after, pending, err := h.store.LoadProviderAccountState(h.ctx)
	if err != nil || pending != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("refilling the helper changed AO's durable account facts")
	}
	if !reflect.DeepEqual(remote, h.proxy.snapshot) {
		t.Fatal("helper was not refilled with AO's effective routes")
	}
	if len(h.guard.acquired) != guardsBefore || len(h.proxy.deleted) != 0 {
		t.Fatal("refilling the helper paused sessions or removed credentials")
	}
	h.route(t, "alice", alice)
	h.route(t, "clara", clara)
}

func TestAccountHostRestoreRetainsWaitingTicketsAndManagedAdoptionAfterLastRemoval(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "claude", "alice@example.test")
	h.assign(t, "waiting", domain.HarnessClaudeCode, alice)
	env, err := h.svc.LaunchAccountEnv(h.ctx, "waiting")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Remove(h.ctx, alice, "", false); err != nil {
		t.Fatal(err)
	}
	before := clone(h.proxy.snapshot)
	if err := h.svc.RestoreHost(h.ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, h.proxy.snapshot) || h.proxy.snapshot.Routes[0].AuthID != "" {
		t.Fatal("host maintenance reauthenticated a waiting session")
	}
	afterEnv, err := h.svc.LaunchAccountEnv(h.ctx, "waiting")
	if err != nil || !reflect.DeepEqual(env, afterEnv) {
		t.Fatal("host maintenance changed a waiting session's stable capability")
	}
	if _, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessClaudeCode, ""); !managed || !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatal("host maintenance allowed native fallback after last account removal")
	}
}

func TestAccountHostRestorePreservesInterruptedMutationUntilIdleRecoveryIsPossible(t *testing.T) {
	h := setupAccounts(t)
	alice := h.login(t, "codex", "alice@example.test")
	bob := h.login(t, "codex", "bob@example.test")
	h.assign(t, "s", domain.HarnessCodex, alice)
	h.proxy.fail = "apply"
	if err := h.svc.Switch(h.ctx, "s", bob); err == nil {
		t.Fatal("failed helper acknowledgement was hidden")
	}
	h.proxy.fail = ""
	h.guard.busy["s"] = true
	if err := h.svc.RestoreHost(h.ctx); err != nil {
		t.Fatalf("request-boundary recovery error=%v", err)
	}
	h.route(t, "s", bob)
	required, err := h.svc.RecoveryRequired(h.ctx)
	if err != nil || required {
		t.Fatal("request-boundary recovery left an unresolved account operation")
	}
	h.guard.busy["s"] = false
	if err := h.svc.RestoreHost(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.route(t, "s", bob)
	required, err = h.svc.RecoveryRequired(h.ctx)
	if err != nil || required {
		t.Fatal("idle maintenance did not finish durable recovery")
	}
}

func TestAccountHostRestoreFailureNeverChangesDurableFactsOrFallsBack(t *testing.T) {
	for _, boundary := range []string{"load", "apply", "cancel"} {
		t.Run(boundary, func(t *testing.T) {
			h := setupAccounts(t)
			alice := h.login(t, "codex", "alice@example.test")
			h.assign(t, "s", domain.HarnessCodex, alice)
			before, err := h.svc.State(h.ctx)
			if err != nil {
				t.Fatal(err)
			}
			remote := clone(h.proxy.snapshot)
			ctx := h.ctx
			switch boundary {
			case "load":
				h.store.fail = "load"
			case "apply":
				h.proxy.fail = "apply"
			default:
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if err := h.svc.RestoreHost(ctx); err == nil {
				t.Fatal("maintenance failure was reported as ready")
			}
			h.store.fail = ""
			h.proxy.fail = ""
			after, pending, err := h.store.LoadProviderAccountState(h.ctx)
			if err != nil || pending != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(remote, h.proxy.snapshot) {
				t.Fatal("failed maintenance changed local or helper assignments")
			}
			h.route(t, "s", alice)
			if err := h.svc.RestoreHost(h.ctx); err != nil {
				t.Fatalf("maintenance could not recover after transient failure: %v", err)
			}
		})
	}
}
