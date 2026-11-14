package browser

import (
	"context"
	"testing"

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

func TestCredentialFillsCarryTheSessionsOwnProject(t *testing.T) {
	for _, action := range []string{"fill-secret", "login"} {
		service, runtime, token := vaultService(t, "proj-a")
		// An agent cannot claim another project's credentials.
		args := map[string]interface{}{"itemId": "i1", "projectId": "proj-b"}
		if _, got, err := service.Execute(context.Background(), "s1", token, action, args); err != nil || got != action {
			t.Fatalf("%s: action=%q err=%v", action, got, err)
		}
		if runtime.action != action || runtime.args["projectId"] != "proj-a" || runtime.args["itemId"] != "i1" {
			t.Fatalf("%s dispatched %q with %v", action, runtime.action, runtime.args)
		}
	}
}

func TestCredentialFillsStampProjectEvenWithoutArgs(t *testing.T) {
	service, runtime, token := vaultService(t, "proj-a")
	if _, _, err := service.Execute(context.Background(), "s1", token, "login", nil); err != nil {
		t.Fatal(err)
	}
	if runtime.args["projectId"] != "proj-a" {
		t.Fatalf("args = %v", runtime.args)
	}
}

func TestCredentialFillsNeedAProject(t *testing.T) {
	service, runtime, token := vaultService(t, "")
	if _, _, err := service.Execute(context.Background(), "s1", token, "fill-secret", map[string]interface{}{"projectId": "x"}); apiErrorCode(err) != "BROWSER_VAULT_UNAVAILABLE" {
		t.Fatalf("err = %v", err)
	}
	if runtime.action != "" {
		t.Fatalf("dispatched %q", runtime.action)
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
