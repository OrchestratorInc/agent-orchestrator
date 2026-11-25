package approval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestGate(t *testing.T) *Gate {
	t.Helper()
	g, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func sampleRequest() Request {
	return Request{
		Kind: KindBrowserAction, SessionID: "s-1", Site: "shop.example", URL: "https://shop.example/checkout",
		Action: `Click "Place order" on shop.example`, Category: CategoryPayment, Reason: "reads like a payment",
		Timeout: 5 * time.Second,
	}
}

// waitPending blocks until the gate shows one pending request.
func waitPending(t *testing.T, g *Gate) Pending {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if list := g.Pending(); len(list) == 1 {
			return list[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no pending request")
	return Pending{}
}

func requestAsync(g *Gate, req Request) <-chan Outcome {
	out := make(chan Outcome, 1)
	go func() {
		outcome, _ := g.Request(context.Background(), req)
		out <- outcome
	}()
	return out
}

func TestGateAllowOnce(t *testing.T) {
	g := newTestGate(t)
	events, stop := g.Subscribe()
	defer stop()
	out := requestAsync(g, sampleRequest())
	pending := waitPending(t, g)
	if ev := <-events; ev.Type != EventRequested || ev.Pending == nil || ev.Pending.ID != pending.ID {
		t.Fatalf("requested event = %+v", ev)
	}
	if err := g.Decide(pending.ID, DecisionAllowOnce); err != nil {
		t.Fatal(err)
	}
	outcome := <-out
	if !outcome.Allowed() || outcome.Decision != DecisionAllowOnce {
		t.Fatalf("outcome = %+v", outcome)
	}
	if ev := <-events; ev.Type != EventResolved || ev.ID != pending.ID || ev.Decision != DecisionAllowOnce {
		t.Fatalf("resolved event = %+v", ev)
	}
	// Allow once does not remember the site: the next request asks again.
	second := requestAsync(g, sampleRequest())
	again := waitPending(t, g)
	_ = g.Decide(again.ID, DecisionDeny)
	if outcome := <-second; outcome.Allowed() {
		t.Fatal("deny allowed")
	}
}

func TestGateDeny(t *testing.T) {
	g := newTestGate(t)
	out := requestAsync(g, sampleRequest())
	_ = g.Decide(waitPending(t, g).ID, DecisionDeny)
	if outcome := <-out; outcome.Allowed() || outcome.Decision != DecisionDeny {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestGateTimeoutDenies(t *testing.T) {
	g := newTestGate(t)
	req := sampleRequest()
	req.Timeout = 30 * time.Millisecond
	outcome, err := g.Request(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Allowed() || outcome.Decision != DecisionTimeout {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(g.Pending()) != 0 {
		t.Fatal("timed-out request still pending")
	}
	if err := g.Decide(outcome.ID, DecisionAllowOnce); !errors.Is(err, ErrNotPending) {
		t.Fatalf("late decision err = %v", err)
	}
}

func TestGateCancelledContextDenies(t *testing.T) {
	g := newTestGate(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitPending(t, g)
		cancel()
	}()
	outcome, _ := g.Request(ctx, sampleRequest())
	if outcome.Allowed() || outcome.Decision != DecisionCancelled {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestGateAllowSiteRemembersSiteAndCategory(t *testing.T) {
	dir := t.TempDir()
	g, _ := Open(dir)
	out := requestAsync(g, sampleRequest())
	_ = g.Decide(waitPending(t, g).ID, DecisionAllowSite)
	<-out
	outcome, err := g.Request(context.Background(), sampleRequest())
	if err != nil || outcome.Decision != DecisionSiteAllowed {
		t.Fatalf("second request = %+v, %v", outcome, err)
	}
	// Survives a restart.
	reopened, _ := Open(dir)
	if outcome, _ := reopened.Request(context.Background(), sampleRequest()); outcome.Decision != DecisionSiteAllowed {
		t.Fatalf("after reopen = %+v", outcome)
	}
	// Another category on the same site still asks.
	other := sampleRequest()
	other.Category = CategoryDelete
	other.Timeout = 20 * time.Millisecond
	if outcome, _ := reopened.Request(context.Background(), other); outcome.Decision != DecisionTimeout {
		t.Fatalf("other category = %+v", outcome)
	}
	if err := reopened.RemoveSiteRule("shop.example", CategoryPayment); err != nil {
		t.Fatal(err)
	}
	other.Category = CategoryPayment
	if outcome, _ := reopened.Request(context.Background(), other); outcome.Decision != DecisionTimeout {
		t.Fatalf("after removing rule = %+v", outcome)
	}
}

func TestGateAuditRecordsEveryGatedAction(t *testing.T) {
	dir := t.TempDir()
	g, _ := Open(dir)
	out := requestAsync(g, sampleRequest())
	_ = g.Decide(waitPending(t, g).ID, DecisionAllowSite)
	<-out
	_, _ = g.Request(context.Background(), sampleRequest())
	req := sampleRequest()
	req.Timeout = 10 * time.Millisecond
	req.Image = "iVBORw0KGgo="
	req.Category = CategoryDelete
	_, _ = g.Request(context.Background(), req)

	entries, err := g.Audit(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d", len(entries))
	}
	want := []Decision{DecisionTimeout, DecisionSiteAllowed, DecisionAllowSite} // newest first
	for i, entry := range entries {
		if entry.Decision != want[i] || entry.Site != "shop.example" || entry.SessionID != "s-1" || entry.Action == "" || entry.Time.IsZero() {
			t.Fatalf("entry %d = %+v", i, entry)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if len(raw) == 0 || containsBytes(raw, "iVBORw0KGgo=") {
		t.Fatal("audit log missing, or it stored the screenshot")
	}
	if info, _ := os.Stat(filepath.Join(dir, "audit.jsonl")); info.Mode().Perm() != 0o600 {
		t.Fatalf("audit mode = %v", info.Mode().Perm())
	}
}

func TestGateSessionMode(t *testing.T) {
	dir := t.TempDir()
	g, _ := Open(dir)
	if g.SessionMode("s-1") != ModeRisky {
		t.Fatal("default mode is not risky")
	}
	if err := g.SetSessionMode("s-1", ModeEverySubmit); err != nil {
		t.Fatal(err)
	}
	if err := g.SetSessionMode("s-1", "bogus"); err == nil {
		t.Fatal("bogus mode accepted")
	}
	reopened, _ := Open(dir)
	if reopened.SessionMode("s-1") != ModeEverySubmit || reopened.SessionMode("s-2") != ModeRisky {
		t.Fatal("mode not persisted")
	}
}

func TestGateRejectsUnknownDecision(t *testing.T) {
	g := newTestGate(t)
	out := requestAsync(g, sampleRequest())
	id := waitPending(t, g).ID
	if err := g.Decide(id, DecisionSiteAllowed); err == nil {
		t.Fatal("site-allowed is not a user decision")
	}
	_ = g.Decide(id, DecisionDeny)
	<-out
}

func containsBytes(haystack []byte, needle string) bool {
	return strings.Contains(string(haystack), needle)
}

func TestGateDefaultModeAppliesToSessionsWithoutTheirOwn(t *testing.T) {
	dir := t.TempDir()
	g, _ := Open(dir)
	if g.DefaultMode() != ModeRisky {
		t.Fatal("default is not risky")
	}
	_ = g.SetSessionMode("s-1", ModeRisky)
	if err := g.SetDefaultMode(ModeEverySubmit); err != nil {
		t.Fatal(err)
	}
	if g.SessionMode("s-2") != ModeEverySubmit || g.SessionMode("s-1") != ModeRisky {
		t.Fatalf("s-2=%s s-1=%s", g.SessionMode("s-2"), g.SessionMode("s-1"))
	}
	reopened, _ := Open(dir)
	if reopened.DefaultMode() != ModeEverySubmit {
		t.Fatal("default not persisted")
	}
	if err := g.SetDefaultMode("nope"); err == nil {
		t.Fatal("bad default accepted")
	}
}

func TestGatePolicyChangesAreAtomic(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	g, _ := Open(dir)
	out := requestAsync(g, sampleRequest())
	id := waitPending(t, g).ID
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := g.Decide(id, DecisionAllowSite); err == nil {
		t.Fatal("allow-site saved into a read-only dir")
	}
	if rules := g.SiteRules(); len(rules) != 0 {
		t.Fatalf("unsaved rule kept in memory: %+v", rules)
	}
	if err := g.SetSessionMode("s-1", ModeOff); err == nil || g.SessionMode("s-1") != ModeRisky {
		t.Fatalf("unsaved session mode applied: err=%v mode=%s", err, g.SessionMode("s-1"))
	}
	if err := g.SetDefaultMode(ModeOff); err == nil || g.DefaultMode() != ModeRisky {
		t.Fatalf("unsaved default mode applied: err=%v mode=%s", err, g.DefaultMode())
	}
	_ = os.Chmod(dir, 0o700)
	_ = g.Decide(id, DecisionDeny)
	<-out
	// The failed allow-site left nothing behind: the next request asks again.
	req := sampleRequest()
	req.Timeout = 20 * time.Millisecond
	if outcome, _ := g.Request(context.Background(), req); outcome.Decision != DecisionTimeout {
		t.Fatalf("next request = %+v", outcome)
	}
}

func TestGateRefusesWhenTheAuditLogCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	g, _ := Open(dir)
	// A directory where the log file should be makes every append fail.
	if err := os.Mkdir(filepath.Join(dir, "audit.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	out := make(chan struct {
		outcome Outcome
		err     error
	}, 1)
	go func() {
		outcome, err := g.Request(context.Background(), sampleRequest())
		out <- struct {
			outcome Outcome
			err     error
		}{outcome, err}
	}()
	_ = g.Decide(waitPending(t, g).ID, DecisionAllowOnce)
	got := <-out
	if got.err == nil || got.outcome.Allowed() {
		t.Fatalf("allowed without an audit record: %+v %v", got.outcome, got.err)
	}
}

// auditLines checks that every line in the audit file is one whole record.
func auditLines(t *testing.T, dir string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		t.Fatalf("audit ends mid-record: %q", raw)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	for _, line := range lines {
		var entry AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("audit line is not a whole record: %q", line)
		}
	}
	return len(lines)
}

func TestAuditRecoversFromAPartialWrite(t *testing.T) {
	dir := t.TempDir()
	g, _ := Open(dir)
	// Storage takes half a record, then fails.
	g.writeAudit = func(f *os.File, b []byte) (int, error) {
		n, _ := f.Write(b[:len(b)/2])
		return n, errors.New("disk full")
	}
	req := sampleRequest()
	if _, err := g.Request(context.Background(), allowedBySite(t, g, req)); err == nil {
		t.Fatal("an unrecorded outcome was allowed")
	}
	// Storage recovers: the next record is whole, and readable.
	g.writeAudit = nil
	outcome, err := g.Request(context.Background(), req)
	if err != nil || outcome.Decision != DecisionSiteAllowed {
		t.Fatalf("after recovery = %+v, %v", outcome, err)
	}
	auditLines(t, dir)
	entries, _ := g.Audit(10)
	if len(entries) == 0 || entries[0].Decision != DecisionSiteAllowed {
		t.Fatalf("allowed outcome missing from the audit: %+v", entries)
	}
}

func TestAuditRepairsAnIncompleteTailLeftAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	g, _ := Open(dir)
	req := sampleRequest()
	if _, err := g.Request(context.Background(), allowedBySite(t, g, req)); err != nil {
		t.Fatal(err)
	}
	// A crash mid-append leaves half a record.
	f, _ := os.OpenFile(filepath.Join(dir, "audit.jsonl"), os.O_WRONLY|os.O_APPEND, 0o600)
	_, _ = f.WriteString(`{"id":"apr_half","action":"Cli`)
	_ = f.Close()
	reopened, _ := Open(dir)
	if outcome, err := reopened.Request(context.Background(), req); err != nil || outcome.Decision != DecisionSiteAllowed {
		t.Fatalf("after restart = %+v, %v", outcome, err)
	}
	if n := auditLines(t, dir); n != 3 {
		t.Fatalf("audit lines = %d, want the allow-site, the first site-allowed and the new one", n)
	}
}

// allowedBySite saves an allow-site rule for req so the next request is
// decided (and audited) without waiting for a person.
func allowedBySite(t *testing.T, g *Gate, req Request) Request {
	t.Helper()
	if len(g.SiteRules()) > 0 {
		return req
	}
	writer := g.writeAudit
	g.writeAudit = nil
	out := requestAsync(g, req)
	_ = g.Decide(waitPending(t, g).ID, DecisionAllowSite)
	<-out
	g.writeAudit = writer
	return req
}
