package browser

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/approval"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestClassifyRemoteArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want remoteGate
	}{
		{[]string{"click", "@e2"}, remoteGate{kind: remoteDescribe, action: "click", selector: "@e2"}},
		{[]string{"--session", "x", "dblclick", "#buy"}, remoteGate{kind: remoteDescribe, action: "dblclick", selector: "#buy"}},
		{[]string{"press", "Enter"}, remoteGate{kind: remoteDescribe, action: "press", key: "Enter"}},
		{[]string{"key", "enter"}, remoteGate{kind: remoteDescribe, action: "press", key: "enter"}},
		{[]string{"keyboard", "press", "Enter"}, remoteGate{kind: remoteDescribe, action: "press", key: "Enter"}},
		{[]string{"find", "text", "Place order", "click"}, remoteGate{kind: remoteDescribe, action: "click", name: "Place order"}},
		{[]string{"find", "role", "button", "click", "--name", "Pay now"}, remoteGate{kind: remoteDescribe, action: "click", role: "button", name: "Pay now"}},
		{[]string{"eval", "document.querySelector('form').submit()"}, remoteGate{kind: remoteAsk, action: "eval"}},
		{[]string{"mouse", "click", "400", "300"}, remoteGate{kind: remoteAsk, action: "mouse"}},
		{[]string{"frobnicate"}, remoteGate{kind: remoteAsk, action: "frobnicate"}},
		{[]string{"press", "Control+Enter"}, remoteGate{kind: remoteDescribe, action: "press", key: "Control+Enter"}},
		{[]string{"press", "Tab"}, remoteGate{}},
		{[]string{"type", "@e1", "4111\n"}, remoteGate{kind: remoteDescribe, action: "type", key: "Enter", selector: "@e1", typed: true}},
		{[]string{"type", "@e1", "4111"}, remoteGate{}},
		{[]string{"keyboard", "type", "hi\r"}, remoteGate{kind: remoteDescribe, action: "press", key: "Enter", typed: true}},
		{[]string{"find", "label", "Card", "type", "4111\n"}, remoteGate{kind: remoteDescribe, action: "type", key: "Enter", name: "Card", typed: true}},
		{[]string{"find", "label", "Card", "fill", "4111\n"}, remoteGate{}},
		{[]string{"snapshot", "--interactive"}, remoteGate{}},
		{[]string{"fill", "@e3", "hello"}, remoteGate{}},
		{[]string{"open", "https://example.com"}, remoteGate{}},
		{[]string{"get", "url"}, remoteGate{}},
		{[]string{"find", "text", "Docs", "hover"}, remoteGate{}},
		{[]string{"--help"}, remoteGate{}},
		{nil, remoteGate{}},
	} {
		if got := classifyRemoteArgs(tc.args); got != tc.want {
			t.Errorf("%q = %+v, want %+v", tc.args, got, tc.want)
		}
	}
}

// fakeRemote answers the describe calls the daemon makes and records every
// command it forwards.
type fakeRemote struct {
	mu        sync.Mutex
	forwarded [][]string
	refs      string
	focused   string
	failAll   bool
	// hang makes every describe call block until its context ends.
	hang bool
	// url overrides the page address the fake reports.
	url string
	// onCall runs, under the lock, before each describe call is answered,
	// so a test can change the page at an exact point.
	onCall func(f *fakeRemote, args []string)
	// hangOn makes the calls it selects block until their context ends.
	hangOn func(args []string) bool
	// sessionRefs holds other agent-browser sessions' pages: a describe read
	// prefixed with --session <name> sees that session's refs.
	sessionRefs map[string]string
	// unscopedReads counts describe reads that named no session.
	unscopedReads int
}

func (f *fakeRemote) Forward(ctx context.Context, args []string, _ map[string]string) (RemoteResponse, error) {
	if f.hangOn != nil && f.hangOn(args) {
		<-ctx.Done()
		return RemoteResponse{}, ctx.Err()
	}
	if f.hang && len(args) > 0 && (args[len(args)-1] == "--json") {
		<-ctx.Done()
		return RemoteResponse{}, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(args) > 0 && args[len(args)-1] == "--json" && f.sessionRefs != nil {
		// A describe read: answer from the session it names.
		session, rest := "", args
		if len(args) > 2 && args[0] == "--session" {
			session, rest = args[1], args[2:]
		} else if name, ok := strings.CutPrefix(args[0], "--session="); ok {
			session, rest = name, args[1:]
		}
		if session != "" {
			if slices.Equal(rest, []string{"snapshot", "--interactive", "--json"}) {
				return RemoteResponse{Output: f.sessionRefs[session]}, nil
			}
			args = rest
		} else {
			f.unscopedReads++
		}
	}
	if f.onCall != nil && len(args) > 0 && args[len(args)-1] == "--json" {
		f.onCall(f, args)
	}
	if f.failAll {
		return RemoteResponse{}, errors.New("browser service down")
	}
	switch {
	case slices.Equal(args, []string{"snapshot", "--interactive", "--json"}):
		return RemoteResponse{Output: f.refs}, nil
	case slices.Equal(args, []string{"get", "url", "--json"}):
		url := f.url
		if url == "" {
			url = "https://shop.example/checkout"
		}
		return RemoteResponse{Output: `{"success":true,"data":{"url":"` + url + `"}}`}, nil
	case slices.Equal(args, []string{"get", "title", "--json"}):
		return RemoteResponse{Output: `{"success":true,"data":{"title":"Checkout"}}`}, nil
	case len(args) == 3 && args[0] == "eval" && args[2] == "--json":
		return RemoteResponse{Output: f.focused}, nil
	}
	f.forwarded = append(f.forwarded, slices.Clone(args))
	return RemoteResponse{Output: "done"}, nil
}

func (f *fakeRemote) sent() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.forwarded)
}

const checkoutRefs = `{"success":true,"data":{"refs":{"e1":{"role":"link","name":"Home"},"e2":{"role":"button","name":"Place order"}}}}`

func remoteService(t *testing.T, remote *fakeRemote) (*Service, *approval.Gate, string) {
	t.Helper()
	svc, gate, token := approvalService(t, &scriptedRuntime{})
	svc.UseRemoteBrowser(remote)
	return svc, gate, token
}

func TestRemoteRiskyClickDeniedIsNeverForwarded(t *testing.T) {
	remote := &fakeRemote{refs: checkoutRefs}
	svc, gate, token := remoteService(t, remote)
	asked := answer(t, gate, approval.DecisionDeny)
	_, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"click", "@e2"}, nil)
	if apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	if len(remote.sent()) != 0 {
		t.Fatalf("forwarded %v after deny", remote.sent())
	}
	if p := <-asked; p.Action != `Click "Place order" on shop.example` || p.Category != approval.CategoryPayment || p.PageTitle != "Checkout" {
		t.Fatalf("prompt = %+v", p)
	}
}

func TestRemoteRiskyClickAllowedIsForwardedOnce(t *testing.T) {
	remote := &fakeRemote{refs: checkoutRefs}
	svc, gate, token := remoteService(t, remote)
	answer(t, gate, approval.DecisionAllowOnce)
	out, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"click", "@e2"}, nil)
	if err != nil || out.Output != "done" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if got := remote.sent(); len(got) != 1 || !slices.Equal(got[0], []string{"click", "@e2"}) {
		t.Fatalf("forwarded %v", got)
	}
}

func TestRemoteBenignCommandsPassWithoutAsking(t *testing.T) {
	remote := &fakeRemote{refs: checkoutRefs, focused: `{"success":true,"data":{"result":{"role":"link","name":"Home"}}}`}
	svc, gate, token := remoteService(t, remote)
	for _, args := range [][]string{{"click", "@e1"}, {"snapshot", "--interactive"}, {"fill", "@e3", "x"}, {"press", "Tab"}} {
		if _, err := svc.ExecuteRemote(context.Background(), "s1", token, args, nil); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if len(gate.Pending()) != 0 || len(remote.sent()) != 4 {
		t.Fatalf("pending=%d forwarded=%v", len(gate.Pending()), remote.sent())
	}
}

func TestRemoteEnterInPaymentFieldAsks(t *testing.T) {
	remote := &fakeRemote{focused: `{"success":true,"data":{"result":{"role":"textbox","name":"Card number","isSubmit":true,"formMethod":"post","formHasPayment":true}}}`}
	svc, gate, token := remoteService(t, remote)
	asked := answer(t, gate, approval.DecisionDeny)
	_, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"press", "Enter"}, nil)
	if apiErrorCode(err) != "BROWSER_ACTION_DENIED" || len(remote.sent()) != 0 {
		t.Fatalf("err=%v forwarded=%v", err, remote.sent())
	}
	if p := <-asked; !strings.Contains(p.Reason, "payment card") {
		t.Fatalf("reason = %q", p.Reason)
	}
}

func TestRemoteEvalAlwaysAsksInRiskyMode(t *testing.T) {
	remote := &fakeRemote{}
	svc, gate, token := remoteService(t, remote)
	asked := answer(t, gate, approval.DecisionAllowOnce)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"eval", "1+1"}, nil); err != nil {
		t.Fatal(err)
	}
	if p := <-asked; p.Category != approval.CategoryScript || !strings.Contains(p.Action, "1+1") {
		t.Fatalf("prompt = %+v", p)
	}
}

func TestRemoteFailsClosedWhenDescribeFails(t *testing.T) {
	// The service cannot answer describe calls; a click still goes through
	// the gate with what the arguments say, and an unidentifiable target asks.
	remote := &fakeRemote{failAll: true}
	svc, gate, token := remoteService(t, remote)
	asked := answer(t, gate, approval.DecisionDeny)
	_, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"click", "@e2"}, nil)
	if apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	if p := <-asked; p.Category != approval.CategoryScript {
		t.Fatalf("prompt = %+v", p)
	}
}

func TestRemoteFailsClosedWithoutAGate(t *testing.T) {
	remote := &fakeRemote{refs: checkoutRefs}
	svc, _, token := remoteService(t, remote)
	svc.approvals = nil
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"click", "@e2"}, nil); apiErrorCode(err) != "BROWSER_APPROVAL_UNAVAILABLE" {
		t.Fatalf("err = %v", err)
	}
	if len(remote.sent()) != 0 {
		t.Fatal("forwarded without a gate")
	}
	// Commands that never need approval still work.
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"snapshot"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteRequiresCapabilityAndService(t *testing.T) {
	remote := &fakeRemote{}
	svc, _, _ := remoteService(t, remote)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", "wrong", []string{"snapshot"}, nil); apiErrorCode(err) != "BROWSER_CAPABILITY_INVALID" {
		t.Fatalf("err = %v", err)
	}
	plain, _, token := approvalService(t, &scriptedRuntime{})
	if _, err := plain.ExecuteRemote(context.Background(), "s1", token, []string{"snapshot"}, nil); apiErrorCode(err) != "BROWSER_REMOTE_UNAVAILABLE" {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoteModeOffSkipsDescribe(t *testing.T) {
	remote := &fakeRemote{refs: checkoutRefs}
	svc, gate, token := remoteService(t, remote)
	_ = gate.SetSessionMode("s1", approval.ModeOff)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"eval", "1"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := remote.sent(); len(got) != 1 {
		t.Fatalf("forwarded %v", got)
	}
}

var _ = time.Second
var _ domain.SessionID

func TestRemoteBareAndAtRefsAreDescribedAlike(t *testing.T) {
	// Agents use refs as the snapshot prints them ("e2"); "@e2" is the same.
	for _, ref := range []string{"e2", "@e2"} {
		remote := &fakeRemote{refs: checkoutRefs}
		svc, gate, token := remoteService(t, remote)
		asked := answer(t, gate, approval.DecisionDeny)
		_, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"click", ref}, nil)
		if apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
			t.Fatalf("%s: err = %v", ref, err)
		}
		if p := <-asked; p.Category != approval.CategoryPayment || p.Action != `Click "Place order" on shop.example` {
			t.Fatalf("%s: prompt = %+v", ref, p)
		}
	}
}

func TestRemoteSpaceOnFocusedControlAsks(t *testing.T) {
	for _, args := range [][]string{{"press", "Space"}, {"key", "NumpadEnter"}, {"keyboard", "press", "Space"}} {
		remote := &fakeRemote{focused: `{"success":true,"data":{"result":{"role":"button","name":"Delete repository"}}}`}
		svc, gate, token := remoteService(t, remote)
		answer(t, gate, approval.DecisionDeny)
		if _, err := svc.ExecuteRemote(context.Background(), "s1", token, args, nil); apiErrorCode(err) != "BROWSER_ACTION_DENIED" || len(remote.sent()) != 0 {
			t.Fatalf("%v: err=%v forwarded=%v", args, err, remote.sent())
		}
	}
}

func TestRemoteRefClickReadsTheFormBeforeAllowing(t *testing.T) {
	// The snapshot only says "button Continue"; the element's form is a
	// checkout form, so every-submit mode must ask.
	refs := `{"success":true,"data":{"refs":{"e2":{"role":"button","name":"Continue"}}}}`
	facts := `{"success":true,"data":{"result":{"role":"button","name":"Continue","isSubmit":true,"formMethod":"post","formHasPayment":true}}}`
	for _, args := range [][]string{{"click", "@e2"}, {"find", "role", "button", "click", "--name", "Continue"}, {"find", "text", "Continue", "click"}} {
		remote := &fakeRemote{refs: refs, focused: facts}
		svc, gate, token := remoteService(t, remote)
		_ = gate.SetSessionMode("s1", approval.ModeEverySubmit)
		answer(t, gate, approval.DecisionDeny)
		if _, err := svc.ExecuteRemote(context.Background(), "s1", token, args, nil); apiErrorCode(err) != "BROWSER_ACTION_DENIED" || len(remote.sent()) != 0 {
			t.Fatalf("%v: err=%v forwarded=%v", args, err, remote.sent())
		}
	}
}

func TestRemoteRefWithoutFormFactsAsks(t *testing.T) {
	// The page could not resolve the ref to one element: its form is
	// unknown, so even a benign-looking label asks.
	refs := `{"success":true,"data":{"refs":{"e2":{"role":"button","name":"Continue"}}}}`
	remote := &fakeRemote{refs: refs, focused: `{"success":true,"data":{"result":{"partial":true}}}`}
	svc, gate, token := remoteService(t, remote)
	answer(t, gate, approval.DecisionDeny)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"click", "@e2"}, nil); apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoteTypedLineBreakAsks(t *testing.T) {
	refs := `{"success":true,"data":{"refs":{"e1":{"role":"textbox","name":"Card"}}}}`
	facts := `{"success":true,"data":{"result":{"role":"textbox","name":"Card","tag":"input","inputType":"text","formMethod":"get","formHasPayment":true}}}`
	for _, args := range [][]string{
		{"type", "@e1", "4111\n"},
		{"type", "#card", "4111\r"},
		{"keyboard", "type", "4111\n"},
		{"find", "label", "Card", "type", "4111\n"},
	} {
		remote := &fakeRemote{refs: refs, focused: facts}
		svc, gate, token := remoteService(t, remote)
		answer(t, gate, approval.DecisionDeny)
		if _, err := svc.ExecuteRemote(context.Background(), "s1", token, args, nil); apiErrorCode(err) != "BROWSER_ACTION_DENIED" || len(remote.sent()) != 0 {
			t.Fatalf("%q: err=%v forwarded=%v", args, err, remote.sent())
		}
	}
	// Without a line break, typing is still not gated.
	remote := &fakeRemote{refs: refs, focused: facts}
	svc, gate, token := remoteService(t, remote)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"type", "@e1", "4111"}, nil); err != nil || len(gate.Pending()) != 0 {
		t.Fatalf("err=%v", err)
	}
}

func TestRemoteSummaryNeverCarriesTypedText(t *testing.T) {
	// Discovery fails, so the prompt falls back to describing the command.
	// The summary goes to the prompt, the audit log and push notifications,
	// so typed values must never appear in it.
	const secret = "4111111111111111"
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"type", "@e1", secret + "\n"}, `Type a line break into @e1`},
		{[]string{"keyboard", "type", secret + "\n"}, `Type a line break into the focused element`},
		{[]string{"find", "label", "Card", "type", secret + "\n"}, `Type a line break into "Card"`},
		{[]string{"batch", `[["fill","@e1","` + secret + `"],["click","@e2"]]`}, `Run "batch"`},
		{[]string{"frobnicate", secret}, `Run "frobnicate"`},
	} {
		remote := &fakeRemote{failAll: true}
		svc, gate, token := remoteService(t, remote)
		asked := answer(t, gate, approval.DecisionDeny)
		if _, err := svc.ExecuteRemote(context.Background(), "s1", token, tc.args, nil); apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
			t.Fatalf("%q: err = %v", tc.args, err)
		}
		p := <-asked
		if strings.Contains(p.Action, secret) || strings.Contains(p.Reason, secret) {
			t.Fatalf("%q: prompt carries the typed value: %+v", tc.args, p)
		}
		if p.Action != tc.want {
			t.Fatalf("%q: summary = %q, want %q", tc.args, p.Action, tc.want)
		}
		entries, _ := gate.Audit(10)
		for _, entry := range entries {
			if strings.Contains(entry.Action, secret) || strings.Contains(entry.Reason, secret) {
				t.Fatalf("%q: audit carries the typed value: %+v", tc.args, entry)
			}
		}
	}
}

func TestRemoteDialogAcceptAsks(t *testing.T) {
	for _, args := range [][]string{{"dialog", "accept"}, {"dialog", "accept", "my answer"}} {
		remote := &fakeRemote{}
		svc, gate, token := remoteService(t, remote)
		asked := answer(t, gate, approval.DecisionDeny)
		if _, err := svc.ExecuteRemote(context.Background(), "s1", token, args, nil); apiErrorCode(err) != "BROWSER_ACTION_DENIED" || len(remote.sent()) != 0 {
			t.Fatalf("%q: err=%v forwarded=%v", args, err, remote.sent())
		}
		if p := <-asked; strings.Contains(p.Action, "my answer") || p.Action != "Accept the page's dialog on shop.example" {
			t.Fatalf("%q: summary = %q", args, p.Action)
		}
	}
	// Reading or dismissing a dialog never asks.
	remote := &fakeRemote{}
	svc, gate, token := remoteService(t, remote)
	for _, args := range [][]string{{"dialog", "status"}, {"dialog", "dismiss"}} {
		if _, err := svc.ExecuteRemote(context.Background(), "s1", token, args, nil); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
	}
	if len(gate.Pending()) != 0 || len(remote.sent()) != 2 {
		t.Fatalf("pending=%d forwarded=%v", len(gate.Pending()), remote.sent())
	}
}

func TestRemoteStuckDiscoveryStillLeavesTimeToDecide(t *testing.T) {
	// The page never answers describe calls. Discovery gives up on its own
	// short deadline and the user still gets the whole approval wait.
	remote := &fakeRemote{hang: true}
	svc, gate, token := remoteService(t, remote)
	svc.describeTimeout = 50 * time.Millisecond
	started := time.Now()
	asked := answer(t, gate, approval.DecisionDeny)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"click", "@e2"}, nil); apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	p := <-asked
	if waited := p.CreatedAt.Sub(started); waited > time.Second {
		t.Fatalf("prompt appeared after %s", waited)
	}
	if p.Category != approval.CategoryScript || p.Action != "Click @e2" {
		t.Fatalf("prompt = %+v", p)
	}
}

func TestRemoteApprovalIsRefusedWhenThePageChangedWhileWaiting(t *testing.T) {
	for name, change := range map[string]func(*fakeRemote){
		"navigated": func(f *fakeRemote) { f.url = "https://shop.example/other" },
		"target changed": func(f *fakeRemote) {
			f.refs = `{"success":true,"data":{"refs":{"e2":{"role":"button","name":"Delete account"}}}}`
		},
	} {
		t.Run(name, func(t *testing.T) {
			remote := &fakeRemote{refs: checkoutRefs}
			svc, gate, token := remoteService(t, remote)
			go func() {
				for range 400 {
					if list := gate.Pending(); len(list) > 0 {
						remote.mu.Lock()
						change(remote)
						remote.mu.Unlock()
						_ = gate.Decide(list[0].ID, approval.DecisionAllowOnce)
						return
					}
					time.Sleep(5 * time.Millisecond)
				}
			}()
			_, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"click", "@e2"}, nil)
			if apiErrorCode(err) != "BROWSER_TARGET_CHANGED" {
				t.Fatalf("err = %v", err)
			}
			if len(remote.sent()) != 0 {
				t.Fatalf("forwarded %v after the page changed", remote.sent())
			}
		})
	}
}

// countCalls returns an onCall hook that runs change on the nth call of verb.
func countCalls(verb string, nth int, change func(*fakeRemote)) func(*fakeRemote, []string) {
	seen := 0
	return func(f *fakeRemote, args []string) {
		if args[0] != verb {
			return
		}
		seen++
		if seen == nth {
			change(f)
		}
	}
}

func TestRemoteRecheckReadsTheURLAfterTheTarget(t *testing.T) {
	// The page navigates right after the recheck reads the target, to a page
	// with the same control: the URL read that follows must catch it.
	remote := &fakeRemote{refs: checkoutRefs}
	remote.onCall = countCalls("snapshot", 2, func(f *fakeRemote) { f.url = "https://shop.example/other" })
	svc, gate, token := remoteService(t, remote)
	answer(t, gate, approval.DecisionAllowOnce)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"click", "@e2"}, nil); apiErrorCode(err) != "BROWSER_TARGET_CHANGED" {
		t.Fatalf("err = %v", err)
	}
	if len(remote.sent()) != 0 {
		t.Fatalf("forwarded %v", remote.sent())
	}
}

func TestRemoteRecheckCatchesAReloadOfTheSamePage(t *testing.T) {
	// Same address and same control, but a new document: what the user
	// approved is gone.
	field := func(origin string) string {
		return `{"success":true,"data":{"result":{"role":"textbox","name":"Card number","isSubmit":true,"formMethod":"post","formHasPayment":true,"documentOrigin":` + origin + `}}}`
	}
	remote := &fakeRemote{focused: field("1000.5")}
	remote.onCall = countCalls("eval", 2, func(f *fakeRemote) { f.focused = field("2000.25") })
	svc, gate, token := remoteService(t, remote)
	answer(t, gate, approval.DecisionAllowOnce)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"press", "Enter"}, nil); apiErrorCode(err) != "BROWSER_TARGET_CHANGED" {
		t.Fatalf("err = %v", err)
	}
	if len(remote.sent()) != 0 {
		t.Fatalf("forwarded %v", remote.sent())
	}
	// The same document passes the recheck.
	steady := &fakeRemote{focused: field("1000.5")}
	svc2, gate2, token2 := remoteService(t, steady)
	answer(t, gate2, approval.DecisionAllowOnce)
	if _, err := svc2.ExecuteRemote(context.Background(), "s1", token2, []string{"press", "Enter"}, nil); err != nil || len(steady.sent()) != 1 {
		t.Fatalf("err=%v forwarded=%v", err, steady.sent())
	}
}

func TestRemoteDiscoveryAcrossANavigationIsRefused(t *testing.T) {
	// The page moves on between the first URL read and the target read: the
	// prompt would pair one page's address with another's control.
	remote := &fakeRemote{refs: checkoutRefs}
	remote.onCall = countCalls("snapshot", 1, func(f *fakeRemote) { f.url = "https://shop.example/other" })
	svc, gate, token := remoteService(t, remote)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"click", "@e2"}, nil); apiErrorCode(err) != "BROWSER_TARGET_CHANGED" {
		t.Fatalf("err = %v", err)
	}
	if len(gate.Pending()) != 0 || len(remote.sent()) != 0 {
		t.Fatalf("pending=%d forwarded=%v", len(gate.Pending()), remote.sent())
	}
}

// nthURLRead selects the nth "get url" call.
func nthURLRead(nth int) func([]string) bool {
	var mu sync.Mutex
	seen := 0
	return func(args []string) bool {
		if !slices.Equal(args, []string{"get", "url", "--json"}) {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		seen++
		return seen == nth
	}
}

func TestRemoteStalledURLReReadStillAsks(t *testing.T) {
	// Discovery runs out of time on the address re-read. That says nothing
	// about a navigation: drop the facts and ask about an unknown target.
	remote := &fakeRemote{refs: checkoutRefs, hangOn: nthURLRead(2)}
	svc, gate, token := remoteService(t, remote)
	svc.describeTimeout = 100 * time.Millisecond
	asked := answer(t, gate, approval.DecisionDeny)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"click", "@e2"}, nil); apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	if p := <-asked; p.Category != approval.CategoryScript || p.Action != "Click @e2 on shop.example" {
		t.Fatalf("prompt = %+v", p)
	}
}

func TestRemoteFailedRecheckReadRefuses(t *testing.T) {
	// After approval a failed address read is not trusted: refuse.
	remote := &fakeRemote{refs: checkoutRefs, hangOn: nthURLRead(3)}
	svc, gate, token := remoteService(t, remote)
	svc.describeTimeout = 100 * time.Millisecond
	answer(t, gate, approval.DecisionAllowOnce)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"click", "@e2"}, nil); apiErrorCode(err) != "BROWSER_TARGET_CHANGED" {
		t.Fatalf("err = %v", err)
	}
	if len(remote.sent()) != 0 {
		t.Fatalf("forwarded %v", remote.sent())
	}
}

func TestRemoteApprovedUnknownTargetRunsOnTheSamePage(t *testing.T) {
	// The user approved without seeing the target (the address re-read
	// stalled), so the recheck has no target to compare: only the page.
	remote := &fakeRemote{refs: checkoutRefs, hangOn: nthURLRead(2)}
	svc, gate, token := remoteService(t, remote)
	svc.describeTimeout = 100 * time.Millisecond
	answer(t, gate, approval.DecisionAllowOnce)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"click", "@e2"}, nil); err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := remote.sent(); len(got) != 1 {
		t.Fatalf("forwarded %v", got)
	}
}

func TestRemoteDownloadClicksItsElementAndAsksLikeAClick(t *testing.T) {
	// agent-browser's download clicks the element to start the download: on a
	// "Place order" button that is a payment click.
	if got := classifyRemoteArgs([]string{"download", "@e2", "/tmp/x"}); got != (remoteGate{kind: remoteDescribe, action: "click", selector: "@e2"}) {
		t.Fatalf("gate = %+v", got)
	}
	remote := &fakeRemote{refs: checkoutRefs}
	svc, gate, token := remoteService(t, remote)
	asked := answer(t, gate, approval.DecisionDeny)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"download", "@e2", "/tmp/x"}, nil); apiErrorCode(err) != "BROWSER_ACTION_DENIED" || len(remote.sent()) != 0 {
		t.Fatalf("err=%v forwarded=%v", err, remote.sent())
	}
	if p := <-asked; p.Category != approval.CategoryPayment {
		t.Fatalf("prompt = %+v", p)
	}
}

func TestRemoteDragIsJudgedByWhereItDrops(t *testing.T) {
	refs := `{"success":true,"data":{"refs":{"e1":{"role":"listitem","name":"Invoice 42"},"e2":{"role":"button","name":"Delete forever"},"e3":{"role":"region","name":"Done"}}}}`
	if got := classifyRemoteArgs([]string{"drag", "@e1", "@e2", "--human"}); got != (remoteGate{kind: remoteDescribe, action: "drag", selector: "@e2", source: "@e1"}) {
		t.Fatalf("gate = %+v", got)
	}
	// Dropping onto "Delete forever" asks as a delete.
	remote := &fakeRemote{refs: refs, focused: `{"success":true,"data":{"result":{"role":"button","name":"Delete forever"}}}`}
	svc, gate, token := remoteService(t, remote)
	asked := answer(t, gate, approval.DecisionDeny)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"drag", "@e1", "@e2"}, nil); apiErrorCode(err) != "BROWSER_ACTION_DENIED" || len(remote.sent()) != 0 {
		t.Fatalf("err=%v forwarded=%v", err, remote.sent())
	}
	if p := <-asked; p.Category != approval.CategoryDelete || p.Action != `Drag @e1 onto "Delete forever" on shop.example` {
		t.Fatalf("prompt = %+v", p)
	}
	// Dropping onto an ordinary zone does not ask.
	calm := &fakeRemote{refs: refs, focused: `{"success":true,"data":{"result":{"role":"region","name":"Done"}}}`}
	svc2, gate2, token2 := remoteService(t, calm)
	if _, err := svc2.ExecuteRemote(context.Background(), "s1", token2, []string{"drag", "@e1", "@e3"}, nil); err != nil || len(gate2.Pending()) != 0 || len(calm.sent()) != 1 {
		t.Fatalf("err=%v pending=%d forwarded=%v", err, len(gate2.Pending()), calm.sent())
	}
}

func TestClassifyRemoteScriptFlagsAndFindShapes(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want remoteGate
	}{
		// wait --fn evaluates its expression in the page, like eval.
		{[]string{"wait", "--fn", "document.forms[0].submit()"}, remoteGate{kind: remoteAsk, action: "wait"}},
		{[]string{"wait", "#spinner"}, remoteGate{}},
		{[]string{"wait", "--text", "Done"}, remoteGate{}},
		{[]string{"wait", "500"}, remoteGate{}},
		// A javascript: address runs script in the page.
		{[]string{"open", "javascript:document.forms[0].submit()"}, remoteGate{kind: remoteAsk, action: "open"}},
		{[]string{"goto", " JavaScript:alert(1)"}, remoteGate{kind: remoteAsk, action: "goto"}},
		{[]string{"open", "https://example.com"}, remoteGate{}},
		// find's action defaults to click.
		{[]string{"find", "text", "Place order"}, remoteGate{kind: remoteDescribe, action: "click", name: "Place order"}},
		{[]string{"find", "role", "button", "--name", "Pay now"}, remoteGate{kind: remoteDescribe, action: "click", role: "button", name: "Pay now"}},
		// nth takes an index and a selector before the action.
		{[]string{"find", "nth", "2", "button.pay", "click"}, remoteGate{kind: remoteDescribe, action: "click", selector: "button.pay", nth: "2"}},
		{[]string{"find", "nth", "2", ".card", "hover"}, remoteGate{}},
		{[]string{"find", "first", "li.item"}, remoteGate{kind: remoteDescribe, action: "click", selector: "li.item", nth: "0"}},
		{[]string{"find", "last", "li.item", "click"}, remoteGate{kind: remoteDescribe, action: "click", selector: "li.item", nth: "-1"}},
		{[]string{"find", "testid", "pay-btn"}, remoteGate{kind: remoteDescribe, action: "click", selector: `[data-testid="pay-btn"]`}},
		{[]string{"find", "role", "heading", "text", "--name", "Welcome"}, remoteGate{}},
		{[]string{"find", "label", "Email", "fill", "a@b.c"}, remoteGate{}},
		{[]string{"find", "label", "Agree", "check"}, remoteGate{}},
		// Shapes it does not recognise ask.
		{[]string{"find", "text", "Pay", "frobnicate"}, remoteGate{kind: remoteAsk, action: "find"}},
		{[]string{"find", "text"}, remoteGate{kind: remoteAsk, action: "find"}},
		{[]string{"find", "nth", "2"}, remoteGate{kind: remoteAsk, action: "find"}},
		{[]string{"find", "sideways", "x", "click"}, remoteGate{kind: remoteAsk, action: "find"}},
	} {
		if got := classifyRemoteArgs(tc.args); got != tc.want {
			t.Errorf("%q = %+v, want %+v", tc.args, got, tc.want)
		}
	}
}

func TestRemoteWaitFnAsksAndShowsTheScript(t *testing.T) {
	remote := &fakeRemote{}
	svc, gate, token := remoteService(t, remote)
	asked := answer(t, gate, approval.DecisionDeny)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"wait", "--fn", "document.forms[0].submit()"}, nil); apiErrorCode(err) != "BROWSER_ACTION_DENIED" || len(remote.sent()) != 0 {
		t.Fatalf("err=%v forwarded=%v", err, remote.sent())
	}
	if p := <-asked; p.Category != approval.CategoryScript || !strings.Contains(p.Action, "document.forms[0].submit()") || !strings.Contains(p.Reason, "JavaScript") {
		t.Fatalf("prompt = %+v", p)
	}
}

func TestRemoteLongScriptPromptSaysItIsCut(t *testing.T) {
	// A harmless-looking prefix must not hide what runs after it.
	script := strings.Repeat("/* harmless */ ", 12) + "document.forms.pay.submit()"
	remote := &fakeRemote{}
	svc, gate, token := remoteService(t, remote)
	asked := answer(t, gate, approval.DecisionDeny)
	if _, err := svc.ExecuteRemote(context.Background(), "s1", token, []string{"eval", script}, nil); apiErrorCode(err) != "BROWSER_ACTION_DENIED" {
		t.Fatalf("err = %v", err)
	}
	p := <-asked
	if !strings.Contains(p.Action, "more characters not shown; Allow runs all of it") {
		t.Fatalf("summary does not say the script is cut: %q", p.Action)
	}
}

func TestClassifyRemoteLeadingFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want remoteGate
	}{
		// A flag that takes no value must not swallow the command.
		{[]string{"--json", "wait", "--fn", "document.forms[0].submit()"}, remoteGate{kind: remoteAsk, action: "wait"}},
		{[]string{"--json", "keyboard", "type", "hello\n"}, remoteGate{kind: remoteDescribe, action: "press", key: "Enter", typed: true}},
		{[]string{"-q", "--annotate", "click", "@e2"}, remoteGate{kind: remoteDescribe, action: "click", selector: "@e2"}},
		{[]string{"--headed", "false", "click", "@e2"}, remoteGate{kind: remoteDescribe, action: "click", selector: "@e2"}},
		{[]string{"--headed", "click", "@e2"}, remoteGate{kind: remoteDescribe, action: "click", selector: "@e2"}},
		// Flags documented with a value still skip it.
		{[]string{"--session", "work", "click", "@e2"}, remoteGate{kind: remoteDescribe, action: "click", selector: "@e2"}},
		{[]string{"--max-output", "4000", "--json", "snapshot"}, remoteGate{}},
		{[]string{"--session=work", "snapshot"}, remoteGate{}},
		// A leading flag it does not know asks.
		{[]string{"--frobnicate", "click", "@e2"}, remoteGate{kind: remoteAsk, action: "--frobnicate"}},
		// Flags that run script or change what the browser trusts ask
		// wherever they appear.
		{[]string{"--init-script", "/tmp/x.js", "snapshot"}, remoteGate{kind: remoteAsk, action: "--init-script"}},
		{[]string{"snapshot", "--init-script", "/tmp/x.js"}, remoteGate{kind: remoteAsk, action: "--init-script"}},
		{[]string{"open", "https://example.com", "--restore-check-fn", "fetch('/x')"}, remoteGate{kind: remoteAsk, action: "--restore-check-fn"}},
		{[]string{"--allow-file-access", "open", "file:///etc/passwd"}, remoteGate{kind: remoteAsk, action: "--allow-file-access"}},
		{[]string{"--help"}, remoteGate{}},
	} {
		if got := classifyRemoteArgs(tc.args); got != tc.want {
			t.Errorf("%q = %+v, want %+v", tc.args, got, tc.want)
		}
	}
}

func TestRemoteJudgesTheSessionTheCommandRunsIn(t *testing.T) {
	// In the default session @e1 is "Home"; in session "pay" it is
	// "Place order". The command runs in "pay", so it must be judged there.
	remote := &fakeRemote{
		refs:        `{"success":true,"data":{"refs":{"e1":{"role":"link","name":"Home"}}}}`,
		sessionRefs: map[string]string{"pay": `{"success":true,"data":{"refs":{"e1":{"role":"button","name":"Place order"}}}}`},
	}
	svc, gate, token := remoteService(t, remote)
	asked := answer(t, gate, approval.DecisionDeny)
	for _, args := range [][]string{{"--session", "pay", "click", "@e1"}, {"--session=pay", "click", "@e1"}} {
		remote.mu.Lock()
		remote.unscopedReads = 0
		remote.mu.Unlock()
		_, err := svc.ExecuteRemote(context.Background(), "s1", token, args, nil)
		if apiErrorCode(err) != "BROWSER_ACTION_DENIED" || len(remote.sent()) != 0 {
			t.Fatalf("%q: err=%v forwarded=%v", args, err, remote.sent())
		}
		if p := <-asked; p.Category != approval.CategoryPayment {
			t.Fatalf("%q: prompt = %+v", args, p)
		}
		remote.mu.Lock()
		unscoped := remote.unscopedReads
		remote.mu.Unlock()
		if unscoped != 0 {
			t.Fatalf("%q: %d describe reads ignored the session", args, unscoped)
		}
		asked = answer(t, gate, approval.DecisionDeny)
	}
}

func TestRemoteScopeFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"--session", "pay", "click", "@e1"}, []string{"--session", "pay"}},
		{[]string{"--json", "--namespace", "ns", "--session=pay", "click", "@e1"}, []string{"--namespace", "ns", "--session=pay"}},
		{[]string{"click", "@e1", "--session", "late"}, nil},
		{[]string{"click", "@e1"}, nil},
	} {
		if got := remoteScope(tc.args); !slices.Equal(got, tc.want) {
			t.Errorf("%q = %q, want %q", tc.args, got, tc.want)
		}
	}
}
