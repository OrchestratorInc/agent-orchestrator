package controllers_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/approval"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

func approvalServer(t *testing.T) (*httptest.Server, *approval.Gate) {
	t.Helper()
	gate, err := approval.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Sessions:  newFakeSessionService(),
		Approvals: gate,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv, gate
}

func pendingRequest(gate *approval.Gate, sessionID string) <-chan approval.Outcome {
	out := make(chan approval.Outcome, 1)
	go func() {
		outcome, _ := gate.Request(context.Background(), approval.Request{
			Kind: approval.KindBrowserAction, SessionID: sessionID, Site: "shop.example",
			Action: `Click "Place order" on shop.example`, Category: approval.CategoryPayment,
			Reason: "reads like a payment", Image: "iVBORw0KGgo=", Timeout: 5 * time.Second,
		})
		out <- outcome
	}()
	return out
}

func onlyPending(t *testing.T, srv *httptest.Server) map[string]any {
	t.Helper()
	for range 200 {
		resp, err := http.Get(srv.URL + "/api/v1/approvals")
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Pending []map[string]any `json:"pending"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if len(body.Pending) == 1 {
			return body.Pending[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no pending approval")
	return nil
}

func post(t *testing.T, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestApprovalDecisionFromTheApp(t *testing.T) {
	srv, gate := approvalServer(t)
	out := pendingRequest(gate, "s1")
	pending := onlyPending(t, srv)
	if pending["action"] != `Click "Place order" on shop.example` || pending["image"] != "iVBORw0KGgo=" || pending["expiresAt"] == nil {
		t.Fatalf("pending = %v", pending)
	}
	url := srv.URL + "/api/v1/approvals/" + pending["id"].(string) + "/decision"
	resp := post(t, url, `{"decision":"allow-once"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if outcome := <-out; outcome.Decision != approval.DecisionAllowOnce {
		t.Fatalf("outcome = %+v", outcome)
	}
	// The request has ended; a second answer conflicts.
	resp = post(t, url, `{"decision":"deny"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("late status = %d", resp.StatusCode)
	}
}

func TestApprovalRefusesAgentCallers(t *testing.T) {
	srv, gate := approvalServer(t)
	out := pendingRequest(gate, "s1")
	id := onlyPending(t, srv)["id"].(string)
	for _, headers := range []map[string]string{
		{"X-AO-Caller-Session": "s1"},
		{"X-AO-Browser-Capability": "token"},
	} {
		resp := post(t, srv.URL+"/api/v1/approvals/"+id+"/decision", `{"decision":"allow-once"}`, headers)
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(toJSON(body), "APPROVAL_REQUIRES_USER") {
			t.Fatalf("headers %v: status %d body %v", headers, resp.StatusCode, body)
		}
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/approvals/policy", strings.NewReader(`{"sessionId":"s1","mode":"off"}`))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		policy, _ := http.DefaultClient.Do(req)
		policy.Body.Close()
		if policy.StatusCode != http.StatusForbidden {
			t.Fatalf("agent changed policy: %d", policy.StatusCode)
		}
	}
	if gate.SessionMode("s1") != approval.ModeRisky {
		t.Fatal("agent turned the gate off")
	}
	_ = gate.Decide(id, approval.DecisionDeny)
	<-out
}

func TestApprovalRejectsUnknownDecision(t *testing.T) {
	srv, gate := approvalServer(t)
	out := pendingRequest(gate, "s1")
	id := onlyPending(t, srv)["id"].(string)
	resp := post(t, srv.URL+"/api/v1/approvals/"+id+"/decision", `{"decision":"site-allowed"}`, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	_ = gate.Decide(id, approval.DecisionDeny)
	<-out
}

func TestApprovalAuditAndPolicy(t *testing.T) {
	srv, gate := approvalServer(t)
	for _, session := range []string{"s1", "s2"} {
		out := pendingRequest(gate, session)
		_ = gate.Decide(onlyPending(t, srv)["id"].(string), approval.DecisionDeny)
		<-out
	}
	resp, _ := http.Get(srv.URL + "/api/v1/approvals/audit?sessionId=s2")
	var audit struct {
		Entries []map[string]any `json:"entries"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&audit)
	resp.Body.Close()
	if len(audit.Entries) != 1 || audit.Entries[0]["sessionId"] != "s2" || audit.Entries[0]["decision"] != "deny" || audit.Entries[0]["site"] != "shop.example" {
		t.Fatalf("audit = %v", audit.Entries)
	}
	if resp, _ := http.Get(srv.URL + "/api/v1/approvals/audit?limit=0"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("limit=0 status = %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/approvals/policy", strings.NewReader(`{"sessionId":"s1","mode":"every-submit"}`))
	put, _ := http.DefaultClient.Do(req)
	var policy map[string]any
	_ = json.NewDecoder(put.Body).Decode(&policy)
	put.Body.Close()
	if put.StatusCode != http.StatusOK || policy["mode"] != "every-submit" || gate.SessionMode("s1") != approval.ModeEverySubmit {
		t.Fatalf("policy = %d %v", put.StatusCode, policy)
	}
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/api/v1/approvals/policy", strings.NewReader(`{"mode":"off"}`))
	put, _ = http.DefaultClient.Do(req)
	policy = map[string]any{}
	_ = json.NewDecoder(put.Body).Decode(&policy)
	put.Body.Close()
	if put.StatusCode != http.StatusOK || policy["defaultMode"] != "off" || gate.SessionMode("s3") != approval.ModeOff || gate.SessionMode("s1") != approval.ModeEverySubmit {
		t.Fatalf("default policy = %d %v", put.StatusCode, policy)
	}
}

func TestApprovalStream(t *testing.T) {
	srv, gate := approvalServer(t)
	resp, err := http.Get(srv.URL + "/api/v1/approvals/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)
	next := func() string {
		for lines.Scan() {
			if line := lines.Text(); strings.HasPrefix(line, "event: ") && line != "event: heartbeat" {
				return strings.TrimPrefix(line, "event: ")
			}
		}
		return ""
	}
	if ev := next(); ev != "ready" {
		t.Fatalf("first event = %q", ev)
	}
	out := pendingRequest(gate, "s1")
	if ev := next(); ev != "approval_requested" {
		t.Fatalf("event = %q", ev)
	}
	_ = gate.Decide(gate.Pending()[0].ID, approval.DecisionDeny)
	if ev := next(); ev != "approval_resolved" {
		t.Fatalf("event = %q", ev)
	}
	<-out
}

func toJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
