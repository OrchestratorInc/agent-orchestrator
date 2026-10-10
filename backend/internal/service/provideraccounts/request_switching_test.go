package provideraccounts

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRequestBoundaryPrimaryMovesOnlyPreviousCodexAccount(t *testing.T) {
	h := setupAccounts(t)

	a := h.login(t, "codex", "a@test.example")
	b := h.login(t, "codex", "b@test.example")
	c := h.login(t, "codex", "c@test.example")
	claude := h.login(t, "claude", "claude@test.example")
	for _, id := range []string{"busy", "idle", "approval", "review-owner"} {
		h.assign(t, id, domain.HarnessCodex, a)
		h.guard.busy[domain.SessionID(id)] = true
	}
	h.assign(t, "other", domain.HarnessCodex, c)
	h.assign(t, "claude", domain.HarnessClaudeCode, claude)
	before, _ := h.svc.State(h.ctx)
	if err := h.svc.SetPrimaryWithOptions(h.ctx, b, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"busy", "idle", "approval", "review-owner"} {
		h.route(t, id, b)
	}
	h.route(t, "other", c)
	h.route(t, "claude", claude)
	after, _ := h.svc.State(h.ctx)
	for i, r := range before.Routes {
		if r.SessionID != after.Routes[i].SessionID || r.TicketHash != after.Routes[i].TicketHash {
			t.Fatal("primary change replaced session identity/ticket")
		}
	}
	if len(h.guard.acquired) != 0 || len(h.proxy.deleted) != 0 {
		t.Fatal("non-destructive Codex rebind drained/stopped work or deleted a login")
	}
	selected, _, err := h.svc.ResolveAccount(h.ctx, domain.HarnessCodex, "")
	if err != nil || selected != b {
		t.Fatalf("new primary=%s err=%v", selected, err)
	}
	if err := h.svc.SetPrimaryWithOptions(h.ctx, c, true); err != nil {
		t.Fatal(err)
	}
	h.route(t, "busy", c)
	if err := h.svc.SetPrimaryWithOptions(h.ctx, a, true); err != nil {
		t.Fatal(err)
	}
	h.route(t, "busy", a)
	h.route(t, "other", a) // Explicit C was also on the previous primary: requested bulk semantics.
	h.route(t, "claude", claude)
}

func TestRequestBoundaryIndividualSwitchRetainsPrimaryForBothProviders(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			h := setupAccounts(t)
			a := h.login(t, provider, "a@test.example")
			b := h.login(t, provider, "b@test.example")
			harness := domain.HarnessCodex
			if provider == "claude" {
				harness = domain.HarnessClaudeCode
			}
			h.assign(t, "working", harness, a)
			h.assign(t, "other", harness, a)
			h.guard.busy["working"] = true
			env, _ := h.svc.LaunchAccountEnv(h.ctx, "working")
			err := h.svc.Switch(h.ctx, "working", b)
			if provider == "codex" {
				if err != nil {
					t.Fatal(err)
				}
				h.route(t, "working", b)
				nextEnv, _ := h.svc.LaunchAccountEnv(h.ctx, "working")
				if !reflect.DeepEqual(env, nextEnv) {
					t.Fatal("switch requires native restart")
				}
				if err = h.svc.Remove(h.ctx, b, "", true); !errors.Is(err, ports.ErrProviderAccountBusy) {
					t.Fatalf("sign-out bypassed native busy protection: %v", err)
				}
				h.route(t, "working", b)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				h.route(t, "working", b)
			}
			h.route(t, "other", a)
			selected, _, err := h.svc.ResolveAccount(h.ctx, harness, "")
			if err != nil || selected != a {
				t.Fatal("individual switch changed default")
			}
		})
	}
}

func TestRequestBoundaryIntentRecoveryRetainsAdmissionAfterRestart(t *testing.T) {
	for _, operation := range []string{"primary", "session"} {
		for _, failure := range []string{"save", "apply", "commit", "finish"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				h := setupAccounts(t)

				a := h.login(t, "codex", "a@test.example")
				b := h.login(t, "codex", "b@test.example")
				h.assign(t, "working", domain.HarnessCodex, a)
				h.guard.busy["working"] = true
				before, _ := h.svc.State(h.ctx)
				if failure == "apply" {
					h.proxy.fail = "apply"
				} else {
					h.store.fail = failure
				}
				var err error
				if operation == "primary" {
					err = h.svc.SetPrimaryWithOptions(h.ctx, b, true)
				} else {
					err = h.svc.Switch(h.ctx, "working", b)
				}
				if !errors.Is(err, errTestFailure) {
					t.Fatalf("failure not surfaced: %v", err)
				}
				h.store.fail = ""
				h.proxy.fail = ""
				state, pending, err := h.store.LoadProviderAccountState(h.ctx)
				if err != nil {
					t.Fatal(err)
				}
				if failure == "save" {
					if pending != nil || !reflect.DeepEqual(state, before) {
						t.Fatal("failed save published routing")
					}
					h.route(t, "working", a)
					return
				}
				if pending == nil || !pending.RequestBoundary {
					t.Fatal("admitted mode missing from durable journal")
				}
				// Restart without the experiment flag; an admitted operation must still recover.
				h.svc = New(h.store, h.proxy, h.guard, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:1234", h.svc.newID)
				if err = h.svc.Recover(context.Background()); err != nil {
					t.Fatalf("busy recovery=%v", err)
				}
				h.route(t, "working", b)
				if err = h.svc.RestoreHost(h.ctx); err != nil {
					t.Fatalf("same-revision replay=%v", err)
				}
				_, pending, err = h.store.LoadProviderAccountState(h.ctx)
				if err != nil || pending != nil || len(h.guard.acquired) != 0 {
					t.Fatal("recovery retained intent or interrupted work")
				}
			})
		}
	}
}

func TestRequestBoundaryRetirementBusyRefusalKeepsAccountAndClearsIntent(t *testing.T) {
	for _, signOut := range []bool{false, true} {
		h := setupAccounts(t)

		a := h.login(t, "codex", "a@test.example")
		b := h.login(t, "codex", "b@test.example")
		h.assign(t, "working", domain.HarnessCodex, a)
		if err := h.svc.SetPrimary(h.ctx, b); err != nil {
			t.Fatal(err)
		}
		before, _ := h.svc.State(h.ctx)
		proxy := &busyProxy{fakeProxy: h.proxy, refuse: true}
		h.svc.proxy = proxy
		if err := h.svc.Remove(h.ctx, a, "", signOut); !errors.Is(err, ports.ErrProviderAccountBusy) {
			t.Fatalf("old outstanding account removal=%v", err)
		}
		after, pending, err := h.store.LoadProviderAccountState(h.ctx)
		if err != nil || pending != nil || !reflect.DeepEqual(before, after) || len(h.proxy.deleted) != 0 {
			t.Fatal("refused retirement changed catalogue or credentials")
		}
		proxy.refuse = false
		if err = h.svc.Remove(h.ctx, a, "", signOut); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(h.proxy.deleted, []string{"a@test.example.json"}) {
			t.Fatal("idle retirement did not clean up original login")
		}
		h.route(t, "working", b)
	}
}
