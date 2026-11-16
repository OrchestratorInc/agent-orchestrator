package browser

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/approval"
	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// scriptedRuntime answers describe with a fixed element and records every
// action it receives.
type scriptedRuntime struct {
	mu          sync.Mutex
	actions     []string
	lastArgs    map[string]interface{}
	describe    map[string]interface{}
	describeErr error
	// describeHang makes describe block until its context ends.
	describeHang bool
}

func (r *scriptedRuntime) Status() browserruntime.Status {
	return browserruntime.Status{Connected: true}
}

func (r *scriptedRuntime) Execute(ctx context.Context, _ domain.SessionID, action string, args map[string]interface{}) (browserruntime.Result, error) {
	if action == describeAction && r.describeHang {
		<-ctx.Done()
		return browserruntime.Result{}, ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions = append(r.actions, action)
	if action != describeAction {
		r.lastArgs = args
	}
	if action == describeAction {
		if r.describeErr != nil {
			return browserruntime.Result{}, r.describeErr
		}
		return browserruntime.Result{Value: r.describe}, nil
	}
	return browserruntime.Result{RequestID: "r1"}, nil
}

func (r *scriptedRuntime) received() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.actions)
}

func placeOrder() map[string]interface{} {
	return map[string]interface{}{
		"target": map[string]interface{}{
			"role": "button", "name": "Place order", "tag": "button", "isSubmit": true,
			"formMethod": "post", "pageUrl": "https://shop.example/checkout", "pageTitle": "Checkout",
		},
		"image":   "iVBORw0KGgo=",
		"binding": map[string]interface{}{"tabId": "t1", "document": float64(1), "ref": "e4"},
	}
}

func approvalService(t *testing.T, rt *scriptedRuntime) (*Service, *approval.Gate, string) {
	t.Helper()
	authority := NewAuthority()
	token, verifier, err := authority.Issue("s1")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{
		ID: "s1", Metadata: domain.SessionMetadata{BrowserCapabilityVerifier: verifier},
	}}}, rt, authority)
	gate, err := approval.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc.UseApprovals(gate, 2*time.Second)
	return svc, gate, token
}

// answer decides the next pending request and returns it.
func answer(t *testing.T, gate *approval.Gate, decision approval.Decision) <-chan approval.Pending {
	t.Helper()
	out := make(chan approval.Pending, 1)
	go func() {
		for range 400 {
			if list := gate.Pending(); len(list) > 0 {
				_ = gate.Decide(list[0].ID, decision)
				out <- list[0]
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		close(out)
	}()
	return out
}

func TestRiskyClickWaitsAndDenyBlocksDispatch(t *testing.T) {
	rt := &scriptedRuntime{describe: placeOrder()}
	svc, gate, token := approvalService(t, rt)
	asked := answer(t, gate, approval.DecisionDeny)
	_, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e4"})
	if apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	if got := rt.received(); !slices.Equal(got, []string{describeAction}) {
		t.Fatalf("runtime received %v; the click must not be dispatched", got)
	}
	pending := <-asked
	if pending.Action != `Click "Place order" on shop.example` || pending.Category != approval.CategoryPayment ||
		pending.Site != "shop.example" || pending.Image == "" || pending.Reason == "" || pending.SessionID != "s1" {
		t.Fatalf("prompt = %+v", pending)
	}
}

func TestRiskyClickAllowedDispatches(t *testing.T) {
	rt := &scriptedRuntime{describe: placeOrder()}
	svc, gate, token := approvalService(t, rt)
	answer(t, gate, approval.DecisionAllowOnce)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e4"}); err != nil {
		t.Fatal(err)
	}
	if got := rt.received(); !slices.Equal(got, []string{describeAction, "click"}) {
		t.Fatalf("runtime received %v", got)
	}
}

func TestBenignClickNeverAsks(t *testing.T) {
	rt := &scriptedRuntime{describe: map[string]interface{}{"target": map[string]interface{}{
		"role": "link", "name": "Home", "pageUrl": "https://shop.example/",
	}}}
	svc, gate, token := approvalService(t, rt)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e1"}); err != nil {
		t.Fatal(err)
	}
	if len(gate.Pending()) != 0 {
		t.Fatal("benign click asked")
	}
	if entries, _ := gate.Audit(10); len(entries) != 0 {
		t.Fatalf("benign click audited: %+v", entries)
	}
}

func TestEveryGatedActionPathIsChecked(t *testing.T) {
	for _, tc := range []struct {
		action string
		args   map[string]interface{}
	}{
		{"click", map[string]interface{}{"ref": "e4"}},
		{"dblclick", map[string]interface{}{"ref": "e4"}},
		{"act", map[string]interface{}{"instruction": "place order button"}},
		{"press", map[string]interface{}{"key": "Enter"}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			rt := &scriptedRuntime{describe: placeOrder()}
			svc, gate, token := approvalService(t, rt)
			answer(t, gate, approval.DecisionDeny)
			_, _, err := svc.Execute(context.Background(), "s1", token, tc.action, tc.args)
			if apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
				t.Fatalf("err = %v", err)
			}
			if slices.Contains(rt.received(), tc.action) {
				t.Fatalf("%s dispatched after deny", tc.action)
			}
		})
	}
}

func TestNonActingActionsSkipDescribe(t *testing.T) {
	rt := &scriptedRuntime{describe: placeOrder()}
	svc, _, token := approvalService(t, rt)
	for _, tc := range []struct {
		action string
		args   map[string]interface{}
	}{
		{"snapshot", nil},
		{"fill", map[string]interface{}{"ref": "e2", "text": "hi"}},
		{"press", map[string]interface{}{"key": "Tab"}},
		{"act", map[string]interface{}{"instruction": "card number", "action": "fill", "value": "4242"}},
	} {
		if _, _, err := svc.Execute(context.Background(), "s1", token, tc.action, tc.args); err != nil {
			t.Fatalf("%s: %v", tc.action, err)
		}
	}
	if slices.Contains(rt.received(), describeAction) {
		t.Fatalf("describe ran for a non-acting action: %v", rt.received())
	}
}

func TestChromeModeIsGated(t *testing.T) {
	panel := &scriptedRuntime{}
	chrome := &scriptedRuntime{describe: placeOrder()}
	svc, gate, token := approvalService(t, panel)
	targets, err := OpenTargetStore(filepath.Join(t.TempDir(), "targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc.UseChrome(chrome, targets, nil)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "target", map[string]interface{}{"target": "chrome"}); err != nil {
		t.Fatal(err)
	}
	answer(t, gate, approval.DecisionDeny)
	_, _, err = svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e4"})
	if apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	if got := chrome.received(); !slices.Equal(got, []string{describeAction}) {
		t.Fatalf("chrome received %v", got)
	}
	if len(panel.received()) != 0 {
		t.Fatalf("panel received %v", panel.received())
	}
}

func TestApprovalTimeoutDenies(t *testing.T) {
	rt := &scriptedRuntime{describe: placeOrder()}
	svc, gate, token := approvalService(t, rt)
	svc.UseApprovals(gate, 30*time.Millisecond)
	_, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e4"})
	if apiErrorCode(err) != "BROWSER_APPROVAL_TIMEOUT" {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(rt.received(), "click") {
		t.Fatal("click dispatched after timeout")
	}
	entries, _ := gate.Audit(1)
	if len(entries) != 1 || entries[0].Decision != approval.DecisionTimeout {
		t.Fatalf("audit = %+v", entries)
	}
}

func TestDescribeFailureFallsBackToArguments(t *testing.T) {
	// An older Chrome extension does not know describe; the instruction text
	// alone still flags an obviously risky act.
	rt := &scriptedRuntime{describeErr: browserruntime.CommandError{Code: "INVALID_ARGUMENT", Message: "Unsupported browser action: describe"}}
	svc, gate, token := approvalService(t, rt)
	answer(t, gate, approval.DecisionDeny)
	_, _, err := svc.Execute(context.Background(), "s1", token, "act", map[string]interface{}{"instruction": "click the place order button"})
	if apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
}

func TestDescribeIsNotAgentCallable(t *testing.T) {
	rt := &scriptedRuntime{describe: placeOrder()}
	svc, _, token := approvalService(t, rt)
	if _, _, err := svc.Execute(context.Background(), "s1", token, describeAction, nil); apiErrorCode(err) != "BROWSER_ACTION_UNSUPPORTED" {
		t.Fatalf("err = %v", err)
	}
}

func TestModeOffSkipsTheGate(t *testing.T) {
	rt := &scriptedRuntime{describe: placeOrder()}
	svc, gate, token := approvalService(t, rt)
	if err := gate.SetSessionMode("s1", approval.ModeOff); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e4"}); err != nil {
		t.Fatal(err)
	}
	if got := rt.received(); !slices.Equal(got, []string{"click"}) {
		t.Fatalf("runtime received %v", got)
	}
}

func TestPreflightForBatchSteps(t *testing.T) {
	rt := &scriptedRuntime{describe: placeOrder()}
	svc, gate, _ := approvalService(t, rt)
	answer(t, gate, approval.DecisionDeny)
	_, err := svc.Preflight(context.Background(), "s1", "click", map[string]interface{}{"ref": "e4"})
	var denied *ApprovalError
	if !errors.As(err, &denied) || denied.Decision != approval.DecisionDeny {
		t.Fatalf("err = %v", err)
	}
	// An allowed step comes back with the binding the batch must dispatch.
	answer(t, gate, approval.DecisionAllowOnce)
	bound, err := svc.Preflight(context.Background(), "s1", "click", map[string]interface{}{"ref": "e4"})
	if err != nil || bound["approvalBinding"] == nil || bound["ref"] != "e4" {
		t.Fatalf("bound=%v err=%v", bound, err)
	}
}

func TestApprovedActionWithoutBindingIsRefused(t *testing.T) {
	// An older runtime describes nothing; the user still approves, but the
	// runtime could not confirm the target at dispatch, so it is refused.
	rt := &scriptedRuntime{describeErr: errors.New("unknown action describe")}
	svc, gate, token := approvalService(t, rt)
	answer(t, gate, approval.DecisionAllowOnce)
	_, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e4"})
	if apiErrorCode(err) != "BROWSER_APPROVAL_UNBOUND" || slices.Contains(rt.received(), "click") {
		t.Fatalf("err=%v received=%v", err, rt.received())
	}
}

func TestUndescribableTargetAsksInsteadOfPassing(t *testing.T) {
	for name, rt := range map[string]*scriptedRuntime{
		"describe fails":  {describeErr: errors.New("tab closed")},
		"no target facts": {describe: map[string]interface{}{"target": map[string]interface{}{"pageUrl": "https://shop.example/"}}},
	} {
		t.Run(name, func(t *testing.T) {
			svc, gate, token := approvalService(t, rt)
			asked := answer(t, gate, approval.DecisionDeny)
			_, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e4"})
			if apiErrorCode(err) != "BROWSER_ACTION_DENIED" || slices.Contains(rt.received(), "click") {
				t.Fatalf("err=%v received=%v", err, rt.received())
			}
			if p := <-asked; p.Category != approval.CategoryScript || p.Reason == "" {
				t.Fatalf("prompt = %+v", p)
			}
		})
	}
}

func TestApprovalStorageFailureFailsClosed(t *testing.T) {
	rt := &scriptedRuntime{describe: placeOrder()}
	svc, _, token := approvalService(t, rt)
	svc.approvals = nil
	svc.ApprovalsUnavailable(errors.New("policy.json: unexpected end of JSON input"))
	_, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e4"})
	if apiErrorCode(err) != "BROWSER_APPROVAL_UNAVAILABLE" || slices.Contains(rt.received(), "click") {
		t.Fatalf("err=%v received=%v", err, rt.received())
	}
	if _, _, err := svc.Execute(context.Background(), "s1", token, "snapshot", nil); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
}

func TestChordedEnterIsGated(t *testing.T) {
	for _, key := range []string{"Control+Enter", "Return", "Shift+Enter"} {
		rt := &scriptedRuntime{describe: placeOrder()}
		svc, gate, token := approvalService(t, rt)
		answer(t, gate, approval.DecisionDeny)
		if _, _, err := svc.Execute(context.Background(), "s1", token, "press", map[string]interface{}{"key": key}); apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
			t.Fatalf("%s: err = %v", key, err)
		}
	}
}

func TestApprovedActionIsBoundToTheApprovedPage(t *testing.T) {
	described := placeOrder()
	described["binding"] = map[string]interface{}{"ref": "e7", "tabId": "t1", "document": float64(3)}
	rt := &scriptedRuntime{describe: described}
	svc, gate, token := approvalService(t, rt)
	answer(t, gate, approval.DecisionAllowOnce)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "act", map[string]interface{}{"instruction": "place order"}); err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	binding, _ := rt.lastArgs["approvalBinding"].(map[string]interface{})
	if binding["ref"] != "e7" || binding["tabId"] != "t1" || binding["document"] != float64(3) || rt.lastArgs["instruction"] != "place order" {
		t.Fatalf("dispatched args = %v", rt.lastArgs)
	}
}

func TestPartialDescriptionAsks(t *testing.T) {
	// The panel could name the control but not read its form (duplicate
	// labels): "Save changes" alone reads as benign, so the gap must ask.
	rt := &scriptedRuntime{describe: map[string]interface{}{
		"target":  map[string]interface{}{"role": "button", "name": "Save changes", "pageUrl": "https://app.example/profile"},
		"partial": true,
	}}
	svc, gate, token := approvalService(t, rt)
	asked := answer(t, gate, approval.DecisionDeny)
	_, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e5"})
	if apiErrorCode(err) != "BROWSER_ACTION_DENIED" || slices.Contains(rt.received(), "click") {
		t.Fatalf("err=%v received=%v", err, rt.received())
	}
	if p := <-asked; p.Category != approval.CategoryScript {
		t.Fatalf("prompt = %+v", p)
	}
	// A complete description of the same plain control still passes.
	full := &scriptedRuntime{describe: map[string]interface{}{"target": map[string]interface{}{"role": "button", "name": "Save changes"}}}
	svc2, gate2, token2 := approvalService(t, full)
	if _, _, err := svc2.Execute(context.Background(), "s1", token2, "click", map[string]interface{}{"ref": "e5"}); err != nil || len(gate2.Pending()) != 0 {
		t.Fatalf("complete description asked: err=%v", err)
	}
}

func TestStuckDescribeStillShowsAPromptPromptly(t *testing.T) {
	// A runtime that never answers describe (a hidden tab whose capture never
	// finishes) must not hold the request back until the approval times out.
	rt := &scriptedRuntime{describeHang: true}
	svc, gate, token := approvalService(t, rt)
	svc.describeTimeout = 50 * time.Millisecond
	asked := make(chan time.Duration, 1)
	start := time.Now()
	go func() {
		for range 400 {
			if list := gate.Pending(); len(list) > 0 {
				asked <- time.Since(start)
				_ = gate.Decide(list[0].ID, approval.DecisionDeny)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		close(asked)
	}()
	_, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e4"})
	if apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	if waited, ok := <-asked; !ok || waited > time.Second {
		t.Fatalf("prompt listed after %v (ok=%v)", waited, ok)
	}
}

func TestSpaceOnFocusedControlIsGated(t *testing.T) {
	for _, key := range []string{"Space", " ", "NumpadEnter"} {
		rt := &scriptedRuntime{describe: placeOrder()}
		svc, gate, token := approvalService(t, rt)
		answer(t, gate, approval.DecisionDeny)
		if _, _, err := svc.Execute(context.Background(), "s1", token, "press", map[string]interface{}{"key": key}); apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
			t.Fatalf("%q: err = %v", key, err)
		}
	}
}
