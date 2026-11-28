package browser

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/approval"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type argsRuntime struct {
	action string
	args   map[string]interface{}
}

func (f *argsRuntime) Status() browserruntime.Status { return browserruntime.Status{Connected: true} }

func (f *argsRuntime) Execute(_ context.Context, _ domain.SessionID, action string, args map[string]interface{}) (browserruntime.Result, error) {
	f.action, f.args = action, args
	return browserruntime.Result{}, nil
}

func vaultService(t *testing.T, projectID domain.ProjectID) (*Service, *argsRuntime, string) {
	t.Helper()
	authority := NewAuthority()
	token, verifier, err := authority.Issue("s1")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &argsRuntime{}
	service := New(fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{
		ID:        "s1",
		ProjectID: projectID,
		Metadata:  domain.SessionMetadata{BrowserCapabilityVerifier: verifier},
	}}}, runtime, authority)
	return service, runtime, token
}

func TestStandaloneSessionsFillWithAnEmptyProject(t *testing.T) {
	rt := &vaultRuntime{describe: describedDemo()}
	svc, gate, token := gatedVaultServiceFor(t, rt, "")
	answerNext(t, gate, approval.DecisionAllowOnce)
	args := map[string]interface{}{"projectId": "proj-a"}
	if _, _, err := svc.Execute(context.Background(), "s1", token, "login", args); err != nil {
		t.Fatal(err)
	}
	// A standalone session claims no project, whatever the caller sends;
	// the panel then allows only items scoped to all projects.
	if projectID, ok := rt.last["projectId"]; !ok || projectID != "" {
		t.Fatalf("dispatched args = %v", rt.last)
	}
}

func TestOtherActionsDoNotGainAProject(t *testing.T) {
	service, runtime, token := vaultService(t, "proj-a")
	if _, _, err := service.Execute(context.Background(), "s1", token, "snapshot", map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.args["projectId"]; ok {
		t.Fatalf("snapshot args = %v", runtime.args)
	}
}

func TestCredentialFillsAreRefusedInChromeMode(t *testing.T) {
	service, _, chrome, store, token := chromeService(t)
	if err := store.Set("s1", TargetChrome); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Execute(context.Background(), "s1", token, "login", nil); apiErrorCode(err) != "BROWSER_VAULT_CHROME_UNSUPPORTED" {
		t.Fatalf("err = %v", err)
	}
	if len(chrome.actions) != 0 {
		t.Fatalf("Chrome extension received %v", chrome.actions)
	}
}

// vaultRuntime answers vault-describe like the Electron panel and records the
// arguments of every other action.
type vaultRuntime struct {
	mu       sync.Mutex
	describe map[string]interface{}
	err      error
	calls    []string
	last     map[string]interface{}
}

func (r *vaultRuntime) Status() browserruntime.Status { return browserruntime.Status{Connected: true} }

func (r *vaultRuntime) Execute(_ context.Context, _ domain.SessionID, action string, args map[string]interface{}) (browserruntime.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, action)
	if action == vaultDescribeAction {
		if r.err != nil {
			return browserruntime.Result{}, r.err
		}
		return browserruntime.Result{Value: r.describe}, nil
	}
	r.last = args
	return browserruntime.Result{Value: map[string]interface{}{"filled": true, "value": "••••"}}, nil
}

func describedDemo() map[string]interface{} {
	return map[string]interface{}{
		"origin": "https://app.example.test", "pageUrl": "https://app.example.test/login", "pageTitle": "Sign in",
		"item": map[string]interface{}{"id": "i1", "label": "Demo app", "username": "alice@example.test"},
	}
}

func gatedVaultService(t *testing.T, rt *vaultRuntime) (*Service, *approval.Gate, string) {
	t.Helper()
	return gatedVaultServiceFor(t, rt, "proj-a")
}

func gatedVaultServiceFor(t *testing.T, rt *vaultRuntime, projectID domain.ProjectID) (*Service, *approval.Gate, string) {
	t.Helper()
	authority := NewAuthority()
	token, verifier, err := authority.Issue("s1")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{
		ID: "s1", ProjectID: projectID, Metadata: domain.SessionMetadata{BrowserCapabilityVerifier: verifier},
	}}}, rt, authority)
	gate, err := approval.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc.UseApprovals(gate, 5*time.Second)
	return svc, gate, token
}

// answer decides the next prompt and returns what it asked.
func answerNext(t *testing.T, gate *approval.Gate, decision approval.Decision) <-chan approval.Pending {
	t.Helper()
	events, stop := gate.Subscribe()
	asked := make(chan approval.Pending, 1)
	go func() {
		defer stop()
		for ev := range events {
			if ev.Type == approval.EventRequested && ev.Pending != nil {
				asked <- *ev.Pending
				_ = gate.Decide(ev.Pending.ID, decision)
				return
			}
		}
	}()
	return asked
}

func TestCredentialFillAsksTheApprovalGateWithoutTheSecret(t *testing.T) {
	rt := &vaultRuntime{describe: describedDemo()}
	svc, gate, token := gatedVaultService(t, rt)
	asked := answerNext(t, gate, approval.DecisionAllowSite)

	args := map[string]interface{}{"ref": "e3", "itemId": "i1", "field": "password", "approvedOrigin": "https://evil.test"}
	if _, _, err := svc.Execute(context.Background(), "s1", token, "fill-secret", args); err != nil {
		t.Fatal(err)
	}
	prompt := <-asked
	if prompt.Kind != approval.KindSavedLogin || prompt.Category != approval.CategoryCredential || prompt.Site != "https://app.example.test · alice@example.test (i1)" {
		t.Fatalf("prompt = %+v", prompt.Request)
	}
	for _, want := range []string{"alice@example.test", "Demo app", "https://app.example.test"} {
		if !strings.Contains(prompt.Action, want) {
			t.Fatalf("prompt action %q missing %q", prompt.Action, want)
		}
	}
	if rt.last["approvedOrigin"] != "https://app.example.test" || rt.last["projectId"] != "proj-a" {
		t.Fatalf("dispatched args = %v", rt.last)
	}
	if got := strings.Join(rt.calls, ","); got != "vault-describe,fill-secret" {
		t.Fatalf("calls = %s", got)
	}

	// The site is remembered: a second fill does not ask again.
	if _, _, err := svc.Execute(context.Background(), "s1", token, "fill-secret", args); err != nil {
		t.Fatal(err)
	}
	entries, err := gate.Audit(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Decision != approval.DecisionSiteAllowed {
		t.Fatalf("audit = %+v", entries)
	}
}

func TestCredentialFillRefusedWhenDeniedEvenWithApprovalsOff(t *testing.T) {
	rt := &vaultRuntime{describe: describedDemo()}
	svc, gate, token := gatedVaultService(t, rt)
	if err := gate.SetSessionMode("s1", approval.ModeOff); err != nil {
		t.Fatal(err)
	}
	answerNext(t, gate, approval.DecisionDeny)
	_, _, err := svc.Execute(context.Background(), "s1", token, "login", nil)
	if apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	if got := strings.Join(rt.calls, ","); got != "vault-describe" {
		t.Fatalf("calls = %s", got)
	}
}

func TestCredentialLoginListsCandidatesWithoutAsking(t *testing.T) {
	rt := &vaultRuntime{describe: map[string]interface{}{
		"origin":     "https://app.example.test",
		"candidates": []interface{}{map[string]interface{}{"id": "i1", "label": "A", "username": "a"}},
	}}
	svc, _, token := gatedVaultService(t, rt)
	result, _, err := svc.Execute(context.Background(), "s1", token, "login", nil)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := result.Value.(map[string]interface{})
	if _, ok := value["candidates"]; !ok {
		t.Fatalf("result = %v", result.Value)
	}
	if got := strings.Join(rt.calls, ","); got != "vault-describe" {
		t.Fatalf("calls = %s", got)
	}
}

func TestCredentialDescribeFailureNeverPrompts(t *testing.T) {
	rt := &vaultRuntime{err: apierr.Invalid("BROWSER_VAULT_NO_MATCH", "No saved credential for https://evil.test", nil)}
	svc, gate, token := gatedVaultService(t, rt)
	_, _, err := svc.Execute(context.Background(), "s1", token, "login", nil)
	if apiErrorCode(err) != "BROWSER_VAULT_NO_MATCH" {
		t.Fatalf("err = %v", err)
	}
	if len(gate.Pending()) != 0 {
		t.Fatal("prompted")
	}
}

func TestCredentialFillsNeedTheApprovalGate(t *testing.T) {
	service, runtime, token := vaultService(t, "proj-a")
	_, _, err := service.Execute(context.Background(), "s1", token, "login", nil)
	if apiErrorCode(err) != "BROWSER_VAULT_UNAVAILABLE" {
		t.Fatalf("err = %v", err)
	}
	if runtime.action != "" {
		t.Fatalf("dispatched %q", runtime.action)
	}
}

func TestCredentialFillRefusedWhenTheApprovalCannotBeAudited(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	rt := &vaultRuntime{describe: describedDemo()}
	authority := NewAuthority()
	token, verifier, err := authority.Issue("s1")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{
		ID: "s1", ProjectID: "proj-a", Metadata: domain.SessionMetadata{BrowserCapabilityVerifier: verifier},
	}}}, rt, authority)
	dir := t.TempDir()
	gate, err := approval.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc.UseApprovals(gate, 5*time.Second)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	answerNext(t, gate, approval.DecisionAllowOnce)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "login", nil); err == nil {
		t.Fatal("login succeeded although the approval could not be audited")
	}
	if got := strings.Join(rt.calls, ","); got != "vault-describe" {
		t.Fatalf("calls = %s", got)
	}
}

func TestAlwaysAllowCoversOnlyTheApprovedSavedLogin(t *testing.T) {
	rt := &vaultRuntime{describe: describedDemo()}
	svc, gate, token := gatedVaultService(t, rt)
	answerNext(t, gate, approval.DecisionAllowSite)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "login", nil); err != nil {
		t.Fatal(err)
	}

	// Another login saved for the same origin needs its own first yes.
	rt.describe = map[string]interface{}{
		"origin": "https://app.example.test", "pageUrl": "https://app.example.test/login",
		"item": map[string]interface{}{"id": "admin-9f8e7d6c5b", "label": "Admin", "username": "admin@example.test"},
	}
	asked := answerNext(t, gate, approval.DecisionDeny)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "login", map[string]interface{}{"itemId": "admin-9f8e7d6c5b"}); apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("admin login err = %v", err)
	}
	if prompt := <-asked; !strings.Contains(prompt.Site, "admin@example.test") || !strings.Contains(prompt.Site, "admin-9f") {
		t.Fatalf("admin prompt site = %q", prompt.Site)
	}

	// The approved login still fills without asking.
	rt.describe = describedDemo()
	if _, _, err := svc.Execute(context.Background(), "s1", token, "login", nil); err != nil {
		t.Fatal(err)
	}
	entries, err := gate.Audit(10)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []string
	for i := len(entries) - 1; i >= 0; i-- {
		decisions = append(decisions, string(entries[i].Decision))
	}
	if got := strings.Join(decisions, ","); got != "allow-site,deny,site-allowed" {
		t.Fatalf("audit decisions = %s", got)
	}
}

func TestLoopbackLoginsAskEveryTimeWithoutAlwaysAllow(t *testing.T) {
	for _, origin := range []string{"http://localhost:4173", "https://localhost:8443", "https://127.0.0.1:9443", "http://app.localhost"} {
		t.Run(origin, func(t *testing.T) {
			rt := &vaultRuntime{describe: map[string]interface{}{
				"origin": origin, "pageUrl": origin + "/login", "pageTitle": "Sign in",
				"item": map[string]interface{}{"id": "i1", "label": "Demo app", "username": "alice@example.test"},
			}}
			svc, gate, token := gatedVaultService(t, rt)
			for round := 0; round < 2; round++ {
				// Any local program (one an agent starts, even with a dev
				// certificate) can answer there: no "Always allow" is offered
				// (no site), so nothing can be remembered.
				asked := answerNext(t, gate, approval.DecisionAllowSite)
				if _, _, err := svc.Execute(context.Background(), "s1", token, "login", nil); err != nil {
					t.Fatal(err)
				}
				prompt := <-asked
				if prompt.Site != "" || !strings.Contains(prompt.Reason, "this computer") || prompt.URL != origin+"/login" {
					t.Fatalf("round %d prompt = %+v", round, prompt.Request)
				}
			}
			if rules := gate.SiteRules(); len(rules) != 0 {
				t.Fatalf("a loopback rule was stored: %+v", rules)
			}
		})
	}
}
