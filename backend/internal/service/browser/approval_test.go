package browser

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
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
	// dialogs answer successive dialog status reads (the last one repeats);
	// operations records each dialog call's operation.
	dialogs    []map[string]interface{}
	dialogErr  error
	operations []string
	// describeArgs is what the last describe was asked about; describeCalls
	// records each describe's args.
	describeArgs  map[string]interface{}
	describeCalls []map[string]interface{}
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
	if action == "dialog" {
		op, _ := args["operation"].(string)
		r.operations = append(r.operations, op)
		if op == "status" {
			if r.dialogErr != nil {
				return browserruntime.Result{}, r.dialogErr
			}
			var status map[string]interface{}
			if len(r.dialogs) > 0 {
				status = r.dialogs[0]
				if len(r.dialogs) > 1 {
					r.dialogs = r.dialogs[1:]
				}
			}
			return browserruntime.Result{Value: status}, nil
		}
	}
	r.actions = append(r.actions, action)
	if action != describeAction {
		r.lastArgs = args
	}
	if action == describeAction {
		r.describeArgs = args
		r.describeCalls = append(r.describeCalls, args)
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
	// Facts first, then the element's picture once the user will be asked.
	if got := rt.received(); !slices.Equal(got, []string{describeAction, describeAction}) {
		t.Fatalf("runtime received %v; the click must not be dispatched", got)
	}
	pending := <-asked
	if pending.Action != `Click "Place order" on shop.example` || pending.Category != approval.CategoryPayment ||
		pending.Site != "https://shop.example" || pending.Image == "" || pending.Reason == "" || pending.SessionID != "s1" {
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
	if got := rt.received(); !slices.Equal(got, []string{describeAction, describeAction, "click"}) {
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
	if got := chrome.received(); !slices.Equal(got, []string{describeAction, describeAction}) {
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

func cardField() map[string]interface{} {
	return map[string]interface{}{
		"target": map[string]interface{}{
			"role": "textbox", "name": "Card", "tag": "input", "inputType": "text",
			"formMethod": "get", "formHasPayment": true, "submitName": "Place order",
			"pageUrl": "https://shop.example/checkout", "pageTitle": "Checkout",
		},
		"binding": map[string]interface{}{"tabId": "t1", "document": float64(1), "ref": "e1"},
	}
}

func TestTypedLineBreakIsGatedLikeEnter(t *testing.T) {
	// A trailing newline in typed text is a real Enter keypress, which
	// submits the field's form.
	for _, tc := range []struct {
		action string
		args   map[string]interface{}
	}{
		{"type", map[string]interface{}{"ref": "e1", "text": "4111\n"}},
		{"type", map[string]interface{}{"ref": "@e1", "text": "4111\r"}},
		{"act", map[string]interface{}{"instruction": "Card", "action": "type", "value": "4111\n"}},
	} {
		rt := &scriptedRuntime{describe: cardField()}
		svc, gate, token := approvalService(t, rt)
		asked := answer(t, gate, approval.DecisionDeny)
		_, _, err := svc.Execute(context.Background(), "s1", token, tc.action, tc.args)
		if apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
			t.Fatalf("%s %v: err = %v", tc.action, tc.args, err)
		}
		if slices.Contains(rt.received(), tc.action) {
			t.Fatalf("%s dispatched after deny", tc.action)
		}
		if p := <-asked; p.Category != approval.CategoryPayment || p.Action != `Press Enter in "Card" on shop.example` {
			t.Fatalf("prompt = %+v", p)
		}
	}
}

func TestTypedLineBreakApprovedCarriesTheBinding(t *testing.T) {
	rt := &scriptedRuntime{describe: cardField()}
	svc, gate, token := approvalService(t, rt)
	answer(t, gate, approval.DecisionAllowOnce)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "type", map[string]interface{}{"ref": "e1", "text": "4111\n"}); err != nil {
		t.Fatal(err)
	}
	if binding, _ := rt.lastArgs["approvalBinding"].(map[string]interface{}); binding["ref"] != "e1" {
		t.Fatalf("dispatched args = %v", rt.lastArgs)
	}
}

func TestTypedLineBreakInEverySubmitModeAsks(t *testing.T) {
	plain := cardField()
	target := plain["target"].(map[string]interface{})
	delete(target, "formHasPayment")
	target["name"], target["submitName"] = "Search", "Search"
	rt := &scriptedRuntime{describe: plain}
	svc, gate, token := approvalService(t, rt)
	_ = gate.SetSessionMode("s1", approval.ModeEverySubmit)
	answer(t, gate, approval.DecisionDeny)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "type", map[string]interface{}{"ref": "e1", "text": "shoes\n"}); apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
}

func TestTypingWithoutLineBreakSkipsDescribe(t *testing.T) {
	rt := &scriptedRuntime{describe: cardField()}
	svc, _, token := approvalService(t, rt)
	for _, tc := range []struct {
		action string
		args   map[string]interface{}
	}{
		{"type", map[string]interface{}{"ref": "e1", "text": "4111"}},
		{"act", map[string]interface{}{"instruction": "Card", "action": "type", "value": "4111"}},
		{"act", map[string]interface{}{"instruction": "Card", "action": "fill", "value": "4111\n"}},
	} {
		if _, _, err := svc.Execute(context.Background(), "s1", token, tc.action, tc.args); err != nil {
			t.Fatalf("%s: %v", tc.action, err)
		}
	}
	if slices.Contains(rt.received(), describeAction) {
		t.Fatalf("describe ran: %v", rt.received())
	}
}

func TestAllowSiteIsScopedToTheOrigin(t *testing.T) {
	at := func(pageURL string) map[string]interface{} {
		d := placeOrder()
		d["target"].(map[string]interface{})["pageUrl"] = pageURL
		return d
	}
	rt := &scriptedRuntime{describe: at("http://localhost:4173/checkout")}
	svc, gate, token := approvalService(t, rt)
	asked := answer(t, gate, approval.DecisionAllowSite)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e4"}); err != nil {
		t.Fatal(err)
	}
	if p := <-asked; p.Site != "http://localhost:4173" {
		t.Fatalf("site = %q", p.Site)
	}
	// Another dev server on the same host, and the same host over another
	// scheme, are other sites: they ask again.
	for _, other := range []string{"http://localhost:5173/checkout", "https://localhost:4173/checkout"} {
		rt.mu.Lock()
		rt.describe = at(other)
		rt.mu.Unlock()
		denied := answer(t, gate, approval.DecisionDeny)
		if _, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e4"}); apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
			t.Fatalf("%s: err = %v", other, err)
		}
		<-denied
	}
	// The approved origin itself is remembered.
	rt.mu.Lock()
	rt.describe = at("http://localhost:4173/other")
	rt.mu.Unlock()
	if _, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e4"}); err != nil {
		t.Fatalf("same origin: %v", err)
	}
}

func (r *scriptedRuntime) dialogOps() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.operations)
}

func confirmDialog(message string) map[string]interface{} {
	return confirmDialogOn("t1", message)
}

func confirmDialogOn(tab, message string) map[string]interface{} {
	return map[string]interface{}{"open": true, "type": "confirm", "message": message, "target": map[string]interface{}{"tabId": tab}}
}

func TestDialogAcceptAsksAndDenyLeavesTheDialogOpen(t *testing.T) {
	// A benign Continue opened confirm("Delete account?"): accepting it is
	// the destructive step.
	rt := &scriptedRuntime{dialogs: []map[string]interface{}{confirmDialog("Delete account?")}}
	svc, gate, token := approvalService(t, rt)
	asked := answer(t, gate, approval.DecisionDeny)
	_, _, err := svc.Execute(context.Background(), "s1", token, "dialog", map[string]interface{}{"operation": "accept"})
	if apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(rt.dialogOps(), "accept") {
		t.Fatalf("accepted after deny: %v", rt.dialogOps())
	}
	if p := <-asked; p.Category != approval.CategoryDelete || p.Action != `Accept the page's confirm dialog "Delete account?"` {
		t.Fatalf("prompt = %+v", p)
	}
}

func TestDialogAcceptApprovedRunsAndPromptTextStaysOut(t *testing.T) {
	prompt := map[string]interface{}{"open": true, "type": "prompt", "message": "Type the repository name to confirm"}
	rt := &scriptedRuntime{dialogs: []map[string]interface{}{prompt}}
	svc, gate, token := approvalService(t, rt)
	asked := answer(t, gate, approval.DecisionAllowOnce)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "dialog", map[string]interface{}{"operation": "accept", "text": "secret-repo"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(rt.dialogOps(), "accept") {
		t.Fatalf("not accepted: %v", rt.dialogOps())
	}
	if p := <-asked; strings.Contains(p.Action, "secret-repo") || strings.Contains(p.Reason, "secret-repo") {
		t.Fatalf("prompt answer in the summary: %+v", p)
	}
}

func TestDialogStatusDismissAndAlertNeverAsk(t *testing.T) {
	rt := &scriptedRuntime{dialogs: []map[string]interface{}{{"open": true, "type": "alert", "message": "Saved"}}}
	svc, gate, token := approvalService(t, rt)
	for _, op := range []string{"status", "dismiss", "accept"} {
		if _, _, err := svc.Execute(context.Background(), "s1", token, "dialog", map[string]interface{}{"operation": op}); err != nil {
			t.Fatalf("%s: %v", op, err)
		}
	}
	if len(gate.Pending()) != 0 {
		t.Fatal("asked")
	}
}

func TestDialogAcceptAsksWhenTheDialogCannotBeRead(t *testing.T) {
	rt := &scriptedRuntime{dialogErr: errors.New("page blocked")}
	svc, gate, token := approvalService(t, rt)
	asked := answer(t, gate, approval.DecisionDeny)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "dialog", map[string]interface{}{"operation": "accept"}); apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	if p := <-asked; p.Action != "Accept the page's dialog" {
		t.Fatalf("prompt = %+v", p)
	}
}

func TestDialogAcceptRefusedWhenTheDialogChangedWhileWaiting(t *testing.T) {
	rt := &scriptedRuntime{dialogs: []map[string]interface{}{confirmDialog("Leave this page?"), confirmDialog("Delete account?")}}
	svc, gate, token := approvalService(t, rt)
	answer(t, gate, approval.DecisionAllowOnce)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "dialog", map[string]interface{}{"operation": "accept"}); apiErrorCode(err) != "BROWSER_TARGET_CHANGED" {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(rt.dialogOps(), "accept") {
		t.Fatalf("accepted a dialog the user never saw: %v", rt.dialogOps())
	}
}

func TestDialogAcceptRefusedWhenTheSameTextIsOnAnotherTab(t *testing.T) {
	// The active tab changed while the user decided, and the new tab shows
	// the same "Are you sure?": accepting would answer a dialog the user
	// never saw.
	rt := &scriptedRuntime{dialogs: []map[string]interface{}{confirmDialogOn("t1", "Are you sure?"), confirmDialogOn("t2", "Are you sure?")}}
	svc, gate, token := approvalService(t, rt)
	answer(t, gate, approval.DecisionAllowOnce)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "dialog", map[string]interface{}{"operation": "accept"}); apiErrorCode(err) != "BROWSER_TARGET_CHANGED" {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(rt.dialogOps(), "accept") {
		t.Fatalf("accepted another tab's dialog: %v", rt.dialogOps())
	}
}

func TestDialogAcceptCarriesTheApprovedTab(t *testing.T) {
	// The runtime acts on its active tab when the accept arrives: it gets the
	// approved tab to check against.
	rt := &scriptedRuntime{dialogs: []map[string]interface{}{confirmDialogOn("t1", "Are you sure?")}}
	svc, gate, token := approvalService(t, rt)
	answer(t, gate, approval.DecisionAllowOnce)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "dialog", map[string]interface{}{"operation": "accept"}); err != nil {
		t.Fatal(err)
	}
	if rt.lastArgs["dialogTab"] != "t1" {
		t.Fatalf("accept args = %v", rt.lastArgs)
	}
}

func dropTarget(name string) map[string]interface{} {
	return map[string]interface{}{
		"target":  map[string]interface{}{"role": "button", "name": name, "tag": "button", "pageUrl": "https://mail.example/", "pageTitle": "Mail"},
		"binding": map[string]interface{}{"tabId": "t1", "document": float64(1), "ref": "e2"},
	}
}

func TestDragIsJudgedByItsDropTarget(t *testing.T) {
	// Dropping a message onto "Delete forever" deletes it.
	rt := &scriptedRuntime{describe: dropTarget("Delete forever")}
	svc, gate, token := approvalService(t, rt)
	asked := answer(t, gate, approval.DecisionDeny)
	_, _, err := svc.Execute(context.Background(), "s1", token, "drag", map[string]interface{}{"ref": "e1", "targetRef": "e2"})
	if apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(rt.received(), "drag") {
		t.Fatal("dragged after deny")
	}
	if rt.describeArgs["ref"] != "e2" {
		t.Fatalf("described %v, not the drop target", rt.describeArgs)
	}
	if p := <-asked; p.Category != approval.CategoryDelete || p.Action != `Drag e1 onto "Delete forever" on mail.example` {
		t.Fatalf("prompt = %+v", p)
	}
}

func TestDragOntoAnOrdinaryZoneDoesNotAsk(t *testing.T) {
	rt := &scriptedRuntime{describe: dropTarget("Done")}
	svc, gate, token := approvalService(t, rt)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "drag", map[string]interface{}{"ref": "e1", "targetRef": "e2"}); err != nil {
		t.Fatal(err)
	}
	if len(gate.Pending()) != 0 || !slices.Contains(rt.received(), "drag") {
		t.Fatalf("pending=%d received=%v", len(gate.Pending()), rt.received())
	}
}

func TestDragOntoAnUnreadableTargetAsks(t *testing.T) {
	rt := &scriptedRuntime{describeErr: errors.New("no such ref")}
	svc, gate, token := approvalService(t, rt)
	asked := answer(t, gate, approval.DecisionDeny)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "drag", map[string]interface{}{"ref": "e1", "targetRef": "e2"}); apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	if p := <-asked; p.Category != approval.CategoryScript || p.Action != "Drag e1 onto element e2" {
		t.Fatalf("prompt = %+v", p)
	}
}

func (r *scriptedRuntime) describes() []map[string]interface{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.describeCalls)
}

func TestDescribeCapturesOnlyWhenTheUserWillBeAsked(t *testing.T) {
	// A benign click: one describe, no image asked for.
	benign := placeOrder()
	benign["target"].(map[string]interface{})["name"] = "Docs"
	delete(benign["target"].(map[string]interface{}), "isSubmit")
	delete(benign, "image")
	rt := &scriptedRuntime{describe: benign}
	svc, _, token := approvalService(t, rt)
	if _, _, err := svc.Execute(context.Background(), "s1", token, "click", map[string]interface{}{"ref": "e4"}); err != nil {
		t.Fatal(err)
	}
	if calls := rt.describes(); len(calls) != 1 || calls[0]["image"] == true {
		t.Fatalf("benign describes = %v", calls)
	}
	// A risky click: the image is asked for once the verdict needs approval.
	risky := &scriptedRuntime{describe: placeOrder()}
	svc2, gate2, token2 := approvalService(t, risky)
	asked := answer(t, gate2, approval.DecisionDeny)
	_, _, _ = svc2.Execute(context.Background(), "s1", token2, "click", map[string]interface{}{"ref": "e4"})
	calls := risky.describes()
	if len(calls) != 2 || calls[0]["image"] == true || calls[1]["image"] != true {
		t.Fatalf("risky describes = %v", calls)
	}
	if p := <-asked; p.Image != "iVBORw0KGgo=" {
		t.Fatalf("prompt image = %q", p.Image)
	}
}
