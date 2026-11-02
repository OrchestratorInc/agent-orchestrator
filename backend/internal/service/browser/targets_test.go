package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type namedRuntime struct {
	transport string
	actions   []string
}

func (r *namedRuntime) Status() browserruntime.Status {
	return browserruntime.Status{Connected: true, Transport: r.transport}
}

func (r *namedRuntime) Execute(_ context.Context, _ domain.SessionID, action string, _ map[string]interface{}) (browserruntime.Result, error) {
	r.actions = append(r.actions, action)
	return browserruntime.Result{RequestID: r.transport}, nil
}

type destroyRecorder struct {
	destroyed []domain.SessionID
	err       error
}

func (d *destroyRecorder) DestroySession(_ context.Context, sessionID domain.SessionID) error {
	d.destroyed = append(d.destroyed, sessionID)
	return d.err
}

func chromeService(t *testing.T) (*Service, *namedRuntime, *namedRuntime, *TargetStore, string) {
	t.Helper()
	authority := NewAuthority()
	token, verifier, err := authority.Issue("s1")
	if err != nil {
		t.Fatal(err)
	}
	panel := &namedRuntime{transport: "panel"}
	chrome := &namedRuntime{transport: "chrome"}
	store, err := OpenTargetStore(filepath.Join(t.TempDir(), "browser-targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	service := New(fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{
		ID:       "s1",
		Metadata: domain.SessionMetadata{BrowserCapabilityVerifier: verifier},
	}}}, panel, authority)
	service.UseChrome(chrome, store, nil)
	return service, panel, chrome, store, token
}

func TestServiceRoutesBySessionTarget(t *testing.T) {
	service, panel, chrome, _, token := chromeService(t)
	ctx := context.Background()

	if _, _, err := service.Execute(ctx, "s1", token, "snapshot", nil); err != nil {
		t.Fatal(err)
	}
	result, _, err := service.Execute(ctx, "s1", token, "target", map[string]interface{}{"target": "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	if value := result.Value.(map[string]interface{}); value["target"] != TargetChrome || value["transport"] != "chrome" {
		t.Fatalf("target result = %#v", value)
	}
	if _, _, err := service.Execute(ctx, "s1", token, "click", nil); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(ctx, "s1", token)
	if err != nil {
		t.Fatal(err)
	}
	if status.Target != TargetChrome || status.Transport != "chrome" {
		t.Fatalf("status = %#v", status)
	}
	if len(panel.actions) != 1 || panel.actions[0] != "snapshot" || len(chrome.actions) != 1 || chrome.actions[0] != "click" {
		t.Fatalf("panel=%v chrome=%v", panel.actions, chrome.actions)
	}
	if _, _, err := service.Execute(ctx, "s1", token, "target", map[string]interface{}{"target": "safari"}); apiErrorCode(err) != "INVALID_ARGUMENT" {
		t.Fatalf("invalid target error = %v", err)
	}
}

func TestServiceWithoutChromeRejectsChromeTarget(t *testing.T) {
	authority := NewAuthority()
	token, verifier, err := authority.Issue("s1")
	if err != nil {
		t.Fatal(err)
	}
	service := New(fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{
		ID:       "s1",
		Metadata: domain.SessionMetadata{BrowserCapabilityVerifier: verifier},
	}}}, &namedRuntime{transport: "panel"}, authority)
	if _, _, err := service.Execute(context.Background(), "s1", token, "target", map[string]interface{}{"target": "chrome"}); apiErrorCode(err) != "BROWSER_CHROME_UNAVAILABLE" {
		t.Fatalf("error = %v", err)
	}
	result, _, err := service.Execute(context.Background(), "s1", token, "target", nil)
	if err != nil || result.Value.(map[string]interface{})["target"] != TargetPanel {
		t.Fatalf("target read = %#v, %v", result.Value, err)
	}
}

func TestTargetStorePersistsAndForgets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "browser-targets.json")
	store, err := OpenTargetStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Get("s1") != TargetPanel {
		t.Fatal("unknown session did not default to the panel")
	}
	if err := store.Set("s1", TargetChrome); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenTargetStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Get("s1") != TargetChrome {
		t.Fatal("chrome target did not persist")
	}
	if err := reopened.Delete("s1"); err != nil {
		t.Fatal(err)
	}
	again, err := OpenTargetStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Get("s1") != TargetPanel {
		t.Fatal("deleted session kept its target")
	}
	if err := store.Set("s1", "firefox"); err == nil {
		t.Fatal("store accepted an unknown target")
	}
}

func TestTargetStoreRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser-targets.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenTargetStore(path); err == nil {
		t.Fatal("corrupt target store opened")
	}
}

func TestLifecycleDestroysBothRuntimesAndForgetsTarget(t *testing.T) {
	store, err := OpenTargetStore(filepath.Join(t.TempDir(), "browser-targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("s1", TargetChrome); err != nil {
		t.Fatal(err)
	}
	panel := &destroyRecorder{}
	chrome := &destroyRecorder{err: errors.New("chrome gone")}
	err = Lifecycle{Panel: panel, Chrome: chrome, Targets: store}.DestroySession(context.Background(), "s1")
	if err == nil || len(panel.destroyed) != 1 || len(chrome.destroyed) != 1 {
		t.Fatalf("err=%v panel=%v chrome=%v", err, panel.destroyed, chrome.destroyed)
	}
	if store.Get("s1") != TargetPanel {
		t.Fatal("destroyed session kept its target")
	}
}
