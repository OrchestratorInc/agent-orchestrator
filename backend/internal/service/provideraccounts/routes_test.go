package provideraccounts

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (h *harness) launch(session string, agent domain.AgentHarness, related bool) map[string]string {
	h.t.Helper()
	env, err := h.svc.LaunchAccountEnv(h.ctx, domain.SessionID(session), agent, related)
	if err != nil {
		h.t.Fatalf("launch %s on %s: %v", session, agent, err)
	}
	return env
}

// A switch moves the session's route to the target's provider, onto its account
// there, and a switch back finds the account it had before. Each provider keeps
// its ticket throughout, so an agent still running on the one the session left,
// such as a reviewer or a side chat, keeps its account.
func TestASwitchMovesTheRouteAndASwitchBackRestoresTheAccount(t *testing.T) {
	h := setup(t)
	maxPlan := h.signIn("claude", "max@example.com")
	work := h.signIn("claude", "work@example.com")
	codex := h.signIn("codex", "pro@example.com")
	h.assign("s1", domain.HarnessClaudeCode, work)
	claudeEnv := h.launch("s1", domain.HarnessClaudeCode, false)
	planned, err := h.svc.PlannedAccountEnv(h.ctx, "s1", domain.HarnessCodex)
	if err != nil || len(planned["AO_PROXY_TICKET"]) != 64 || planned["AO_PROXY_TICKET"] == claudeEnv["ANTHROPIC_AUTH_TOKEN"] {
		t.Fatalf("planned=%v err=%v", planned, err)
	}
	h.route("s1", work) // planning moved nothing
	if env := h.launch("s1", domain.HarnessCodex, false); !reflect.DeepEqual(env, planned) {
		t.Fatalf("the target got %v, planned %v", env, planned)
	}
	h.route("s1", codex)
	if route, _, _ := h.svc.SessionAccount(h.ctx, "s1"); route.Provider != "codex" {
		t.Fatalf("route=%+v", route)
	}
	if auth, _ := h.helperAuth("s1", "claude"); auth != "work@example.com-auth" {
		t.Fatalf("the Claude ticket now reaches %q", auth)
	}
	if env := h.launch("s1", domain.HarnessClaudeCode, true); !reflect.DeepEqual(env, claudeEnv) {
		t.Fatalf("a side chat on the provider the session left got %v", env)
	}
	h.route("s1", codex) // and moved nothing
	// The account page lists the session under work, and moving it from there moves its Claude route.
	if err := h.act(maxPlan, "assign-session", "s1"); err != nil {
		t.Fatal(err)
	}
	h.route("s1", codex)
	if auth, _ := h.helperAuth("s1", "claude"); auth != "max@example.com-auth" {
		t.Fatalf("the Claude ticket reaches %q", auth)
	}
	if err := h.act(work, "assign-session", "s1"); err != nil {
		t.Fatal(err)
	}
	if env := h.launch("s1", domain.HarnessClaudeCode, false); !reflect.DeepEqual(env, claudeEnv) {
		t.Fatalf("switch back env=%v", env)
	}
	h.route("s1", work)
	if auth, _ := h.helperAuth("s1", "codex"); auth != "pro@example.com-auth" {
		t.Fatalf("the Codex ticket now reaches %q", auth)
	}
	// The account page lists the session once under each account it has a route on.
	views, _ := h.svc.Accounts(h.ctx, false, false)
	for _, view := range views {
		if want := view.ID == work || view.ID == codex; want != reflect.DeepEqual(view.Sessions, []string{"s1"}) {
			t.Fatalf("account %s lists %v", view.ID, view.Sessions)
		}
	}
	if err = h.svc.ForgetAccount(h.ctx, "s1"); err != nil || len(h.store.get().Routes) != 0 {
		t.Fatalf("a deleted session kept routes: %+v err=%v", h.store.get().Routes, err)
	}
}

// A switch to a provider AO holds no account for leaves the agent its own setup,
// and keeps the session's account for a switch back.
func TestASwitchToAProviderWithoutAccountsKeepsTheAgentsOwnSetup(t *testing.T) {
	h := setup(t)
	work := h.signIn("claude", "work@example.com")
	h.assign("s1", domain.HarnessClaudeCode, work)
	if env := h.launch("s1", domain.HarnessCodex, false); env != nil {
		t.Fatalf("env=%v", env)
	}
	if _, managed, _ := h.svc.SessionAccount(h.ctx, "s1"); managed {
		t.Fatal("the session still has a route")
	}
	h.launch("s1", domain.HarnessClaudeCode, false)
	h.route("s1", work)
}

// A reviewer on another provider runs on the worker's account for that
// provider, which starts as its default; the worker's own route is left alone.
func TestAReviewerOnAnotherProviderGetsAnAccountOfItsOwn(t *testing.T) {
	h := setup(t)
	work := h.signIn("claude", "work@example.com")
	h.assign("w1", domain.HarnessClaudeCode, work)
	worker := h.launch("w1", domain.HarnessClaudeCode, false)
	if env := h.launch("w1", domain.HarnessCodex, true); env != nil {
		t.Fatalf("no Codex account yet, env=%v", env)
	}
	h.signIn("codex", "pro@example.com")
	reviewer := h.launch("w1", domain.HarnessCodex, true)
	if reviewer["AO_PROXY_TICKET"] == "" || reviewer["AO_PROXY_TICKET"] == worker["ANTHROPIC_AUTH_TOKEN"] {
		t.Fatalf("reviewer env=%v", reviewer)
	}
	if same := h.launch("w1", domain.HarnessClaudeCode, true); !reflect.DeepEqual(same, worker) {
		t.Fatalf("a reviewer on the worker's provider got %v", same)
	}
	h.route("w1", work)
	// A related launch waiting for a sign-in is refused rather than run on this computer's login.
	h.assign("w2", domain.HarnessClaudeCode, work)
	if err := h.act(work, "sign-out"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.LaunchAccountEnv(h.ctx, "w2", domain.HarnessClaudeCode, true); !errors.Is(err, ports.ErrProviderLoginRequired) {
		t.Fatalf("err=%v", err)
	}
}

// A side chat of a session that still runs on the computer's own sign-in goes
// through an account, but does not put the session on it; moving its own agent does.
func TestASideChatDoesNotPutASessionWithoutAnAccountOnOne(t *testing.T) {
	h := setup(t)
	claude := h.signIn("claude", "max@example.com")
	if env := h.launch("old", domain.HarnessClaudeCode, true); len(env) == 0 {
		t.Fatal("the side chat got no account")
	}
	if got := h.view(claude).Sessions; len(got) != 0 {
		t.Fatalf("the account lists %v", got)
	}
	h.launch("old", domain.HarnessClaudeCode, false)
	if got := h.view(claude).Sessions; !reflect.DeepEqual(got, []string{"old"}) {
		t.Fatalf("after its own agent moved, the account lists %v", got)
	}
}
