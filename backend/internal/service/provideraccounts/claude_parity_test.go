package provideraccounts

import (
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestClaudeDefaultScopePreservesOtherAccountsAndProcessEnvironment(t *testing.T) {
	for _, move := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-only", true: "move-existing"}[move], func(t *testing.T) {
			h := setupAccounts(t)
			a := h.login(t, "claude", "a@example.test")
			b := h.login(t, "claude", "b@example.test")
			c := h.login(t, "claude", "c@example.test")
			codex := h.login(t, "codex", "codex@example.test")
			for _, session := range []string{"busy", "idle", "explicit-a"} {
				h.assign(t, session, domain.HarnessClaudeCode, a)
			}
			h.assign(t, "explicit-c", domain.HarnessClaudeCode, c)
			h.assign(t, "codex", domain.HarnessCodex, codex)
			h.guard.busy["busy"] = true
			env, err := h.svc.LaunchAccountEnv(h.ctx, "busy")
			if err != nil {
				t.Fatal(err)
			}
			if err = h.svc.SetPrimaryWithOptions(h.ctx, b, move); err != nil {
				t.Fatal(err)
			}
			want := a
			if move {
				want = b
			}
			for _, session := range []string{"busy", "idle", "explicit-a"} {
				h.route(t, session, want)
			}
			h.route(t, "explicit-c", c)
			h.route(t, "codex", codex)
			chosen, managed, err := h.svc.ResolveAccount(h.ctx, domain.HarnessClaudeCode, "")
			if err != nil || !managed || chosen != b {
				t.Fatalf("new session=%s managed=%v err=%v", chosen, managed, err)
			}
			nextEnv, err := h.svc.LaunchAccountEnv(h.ctx, "busy")
			if err != nil || !reflect.DeepEqual(env, nextEnv) {
				t.Fatalf("process config changed: %v", err)
			}
			if len(h.guard.acquired) != 0 || len(h.proxy.deleted) != 0 {
				t.Fatal("live rebind interrupted work or deleted credentials")
			}
		})
	}
}
