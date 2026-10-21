package testing

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type controlProvider struct {
	ports.TestingTargetEnvironment
	calls int
}

func (p *controlProvider) Control(_ context.Context, target domain.TestTargetIdentity, action string) (ports.TestingControlResult, error) {
	p.calls++
	result := ports.TestingControlResult{Detail: action}
	if action == "admit_terminal" {
		target.ID += "-terminal"
		target.ElectronPID = 200
		target.WindowID = "terminal"
		result.Window = &target
	}
	if action == "select_app" {
		result.Window = &target
	}
	return result, nil
}

type controlDesktop struct{ *fakeProviders }

func (p controlDesktop) BindWindow(ctx context.Context, target domain.TestTargetIdentity) (domain.TestTargetIdentity, error) {
	if target.WindowID != "" {
		return target, ctx.Err()
	}
	return p.fakeProviders.BindWindow(ctx, target)
}

func TestTargetControlJournalsBeforeDispatchAndRefusesReplay(t *testing.T) {
	var p *controlProvider
	f := newFixture(t, func(d *Deps) { p = &controlProvider{TestingTargetEnvironment: d.Target}; d.Target = p })
	f.evidence.failState = "dispatching"
	if _, err := f.call("blocked-control", "target_control", domain.TestTargetControlRequest{Action: "stop_daemon"}); err == nil || p.calls != 0 {
		t.Fatal("control bypassed journal", err, p.calls)
	}
	f.evidence.failState = ""
	if _, err := f.call("stop", "target_control", domain.TestTargetControlRequest{Action: "stop_daemon"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.call("stop", "target_control", domain.TestTargetControlRequest{Action: "stop_daemon"}); code(err) != "DUPLICATE_TEST_REQUEST" || p.calls != 1 {
		t.Fatal("control replayed", err, p.calls)
	}
}

func TestTargetControlSelectsOnlyControllerResolvedWindow(t *testing.T) {
	f := newFixture(t, func(d *Deps) {
		d.Target = &controlProvider{TestingTargetEnvironment: d.Target}
		d.Desktop = controlDesktop{d.Desktop.(*fakeProviders)}
	})
	before, err := f.call("app-shot", "observe", domain.TestObserveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.call("admit", "target_control", domain.TestTargetControlRequest{Action: "admit_terminal"}); err != nil {
		t.Fatal(err)
	}
	shot, err := f.call("terminal-shot", "observe", domain.TestObserveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if shot.Screenshot.Frame.Target.WindowID != "terminal" || shot.Screenshot.Frame.Target.ElectronPID != 200 {
		t.Fatal("did not capture admitted window")
	}
	if _, err := f.call("old-app-input", "key", domain.TestKeyRequest{ScreenshotID: before.Screenshot.Frame.ScreenshotID, Keys: []string{"Enter"}}); err == nil {
		t.Fatal("old app receipt admitted into terminal")
	}
	if _, err := f.call("select", "target_control", domain.TestTargetControlRequest{Action: "select_app"}); err != nil {
		t.Fatal(err)
	}
	shot, err = f.call("new-app-shot", "observe", domain.TestObserveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if shot.Screenshot.Frame.Target.WindowID != "window" {
		t.Fatal("did not return to owned app")
	}
}

func TestTargetControlRejectsInjectedIdentifiers(t *testing.T) {
	for _, raw := range []string{`{"action":"kill"}`, `{"action":"stop_daemon","pid":3001}`, `{"action":"admit_terminal","windowId":"foreign"}`, `{"action":"read_terminal","path":"/private"}`} {
		if _, _, err := decodeInput([]byte(raw), "target_control"); err == nil {
			t.Fatal("accepted injected control", raw)
		}
	}
}
