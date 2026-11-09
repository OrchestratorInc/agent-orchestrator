package controllers_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// TestM2OperatorConsentRefusalsAreDistinct proves the two native-verification failures fail closed
// with STABLE, DIFFERENT reasons, and that neither touches the durable state.
//
// A client has to tell "the human said no" from "this host cannot ask a human": the first means
// re-ask, the second means stop trying. They must also be distinguishable in a log line, so a
// Windows-host refusal is not reported as a policy decision.
func TestM2OperatorConsentRefusalsAreDistinct(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		err    error
		reason string
	}{
		{"denied", principal.ErrConsentDenied, "consent_denied"},
		{"unavailable", principal.ErrConsentUnavailable, "consent_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := newApprovalEnv(t)
			env.consent.set(tc.err)
			path := env.bindPath()
			body := `{"idempotencyKey":"consent-refused"}`
			digest := principal.PayloadDigest([]byte(body))
			approval := env.createApproval(t, http.MethodPost, path, body)

			status, raw := doJSONWithHeaders(t, env.srv, http.MethodPost,
				"/api/v1/operator/approvals/"+approval.Approval.ApprovalID+"/confirm",
				`{"code":"`+approval.Approval.Code+`","payloadDigest":"`+digest+`"}`, nil)
			if status != http.StatusForbidden {
				t.Fatalf("confirm under %s = %d, want 403; body=%s", tc.name, status, raw)
			}
			var envelope struct {
				Code    string `json:"code"`
				Details struct {
					Reason string `json:"reason"`
				} `json:"details"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatalf("decode refusal: %v (%s)", err, raw)
			}
			if envelope.Code != principal.CodeApprovalRequired || envelope.Details.Reason != tc.reason {
				t.Fatalf("%s refusal = %q/%q, want %q/%q",
					tc.name, envelope.Code, envelope.Details.Reason,
					principal.CodeApprovalRequired, tc.reason)
			}
			// The verifier really ran; it was not skipped by an earlier guard.
			if len(env.consent.seen()) != 1 {
				t.Fatalf("the OS verifier ran %d times, want 1", len(env.consent.seen()))
			}
			// Refused means refused: the approval is still unusable and nothing happened.
			if status, raw := env.submitAsApproval(t, http.MethodPost, path, body,
				approval.Approval.ApprovalID); status != http.StatusForbidden ||
				!strings.Contains(string(raw), principal.ReasonApprovalUnconfirmed) {
				t.Fatalf("refused confirmation still authorised: %d %s", status, raw)
			}
			if env.gateway.SpawnCalls() != 0 {
				t.Fatalf("a refused confirmation reached the gateway: %d spawns", env.gateway.SpawnCalls())
			}
			if rows, err := env.store.ListWorkflowAudits(t.Context(), env.workflowID, 100, 0); err != nil {
				t.Fatalf("list audits: %v", err)
			} else if len(rows) != 0 {
				t.Fatalf("a refused confirmation wrote %d audit rows", len(rows))
			}
		})
	}
}

// TestM2OperatorUnavailableConsentRefusesWithoutAConsentField proves a daemon wired without any
// verifier refuses instead of silently confirming (the non-Windows wiring).
func TestM2OperatorUnavailableConsentRefusesWithoutAConsentField(t *testing.T) {
	t.Parallel()
	env := newApprovalEnv(t)
	env.deps.OperatorConsent = nil
	env.srv = env.serverWith(t, env.deps.WorkflowPrincipals)
	body := `{"idempotencyKey":"no-consent"}`
	digest := principal.PayloadDigest([]byte(body))
	approval := env.createApproval(t, http.MethodPost, env.bindPath(), body)
	status, raw := doJSONWithHeaders(t, env.srv, http.MethodPost,
		"/api/v1/operator/approvals/"+approval.Approval.ApprovalID+"/confirm",
		`{"code":"`+approval.Approval.Code+`","payloadDigest":"`+digest+`"}`, nil)
	if status != http.StatusForbidden ||
		!strings.Contains(string(raw), "consent_unavailable") {
		t.Fatalf("unwired verifier = %d; body=%s", status, raw)
	}
}

// TestM2OperatorAgentCannotReachAnyApprovalVerb proves existing operator routes refuse Agent headers and the removed list remains unavailable,
// so the approval surface is not an agent-reachable channel in either direction.
func TestM2OperatorAgentCannotReachAnyApprovalVerb(t *testing.T) {
	t.Parallel()
	env := newApprovalEnv(t)
	body := `{"idempotencyKey":"agent-verbs"}`
	digest := principal.PayloadDigest([]byte(body))
	approval := env.createApproval(t, http.MethodPost, env.bindPath(), body)
	agentHeaders := map[string]string{
		principal.HeaderAgentCapability: "agent-capability",
		principal.HeaderAttemptLease:    "lease-1",
	}

	createBody, _ := json.Marshal(map[string]string{
		"operationMethod": http.MethodPost, "operationPath": env.bindPath(),
		"payloadDigest": digest, "payload": body,
	})
	for name, call := range map[string]struct {
		method, path, body string
		want               int
	}{
		"create": {
			http.MethodPost, "/api/v1/operator/approvals", string(createBody), http.StatusForbidden,
		},
		"list": {http.MethodGet, "/api/v1/operator/approvals", "", http.StatusMethodNotAllowed},
		"confirm": {
			http.MethodPost, "/api/v1/operator/approvals/" + approval.Approval.ApprovalID + "/confirm",
			`{"code":"` + approval.Approval.Code + `","payloadDigest":"` + digest + `"}`,
			http.StatusForbidden,
		},
		"status": {
			http.MethodPost, "/api/v1/operator/approvals/" + approval.Approval.ApprovalID + "/status",
			`{"code":"` + approval.Approval.Code + `"}`, http.StatusForbidden,
		},
	} {
		status, raw := doJSONWithHeaders(t, env.srv, call.method, call.path, call.body, agentHeaders)
		if status != call.want {
			t.Fatalf("%s as agent = %d, want %d; body=%s", name, status, call.want, raw)
		}
	}
	// An agent that tried to confirm must not have triggered native verification at all.
	if len(env.consent.seen()) != 0 {
		t.Fatalf("an agent triggered %d native verifications", len(env.consent.seen()))
	}
	// And the approval the human did not confirm is still unusable.
	if _, found := env.approvals.Get(approval.Approval.ApprovalID); !found {
		t.Fatal("the approval vanished")
	}
	if state, _, _ := env.approvals.State(approval.Approval.ApprovalID); state != principal.ApprovalStatePending {
		t.Fatalf("state = %q, want pending: nothing an agent did may advance it", state)
	}
}

// TestM2OperatorApprovalRestartDropsTheTable proves approvals are per boot: the same id, code and
// digest cannot be consumed after the table is reset, and no credential survives either.
func TestM2OperatorApprovalRestartDropsTheTable(t *testing.T) {
	t.Parallel()
	env := newApprovalEnv(t)
	body := `{"idempotencyKey":"restart-table"}`
	env.approvedAction(t, http.MethodPost, env.bindPath(), `{"idempotencyKey":"bind-restart"}`)
	approval := env.createApproval(t, http.MethodPost, env.resolvePath(), body)
	digest := principal.PayloadDigest([]byte(body))

	// Reset is a restart within one process: the new daemon has an empty table.
	env.approvals.ResetApprovals()

	status, raw := doJSONWithHeaders(t, env.srv, http.MethodPost,
		"/api/v1/operator/approvals/"+approval.Approval.ApprovalID+"/confirm",
		`{"code":"`+approval.Approval.Code+`","payloadDigest":"`+digest+`"}`, nil)
	if status != http.StatusForbidden || !strings.Contains(string(raw), principal.ReasonApprovalUnknown) {
		t.Fatalf("post-restart confirm = %d; body=%s", status, raw)
	}
	if status, raw := env.submitAsApproval(t, http.MethodPost, env.resolvePath(), body,
		approval.Approval.ApprovalID); status != http.StatusForbidden ||
		!strings.Contains(string(raw), principal.CodeApprovalInvalid) {
		t.Fatalf("post-restart submit = %d; body=%s", status, raw)
	}
	if env.gateway.SpawnCalls() != 1 || env.gateway.ResumeCalls() != 0 {
		t.Fatalf("restart changed durable effects: spawns=%d resumes=%d",
			env.gateway.SpawnCalls(), env.gateway.ResumeCalls())
	}
}

// TestM2OperatorOperationsAllowlistIsClosed pins the closed list of approval-gated operations:
// adding a route here is a security edit, so a wildcard entry must fail
// loudly, and the five mutating verbs the design names must all be present.
func TestM2OperatorOperationsAllowlistIsClosed(t *testing.T) {
	t.Parallel()
	want := []string{
		"/api/v1/workflows/{workflowId}/attempts/{attemptId}/bind",
		"/api/v1/workflows/{workflowId}/attempts/{attemptId}/resolve",
		"/api/v1/workflows/{workflowId}/attempts/{attemptId}/reconcile",
		"/api/v1/workflows/{workflowId}/attempts/{attemptId}/resume",
		"/api/v1/workflows/{workflowId}/task-requests/{requestId}/decide",
	}
	listed := map[string]bool{}
	for _, op := range principal.OperatorOperations {
		listed[op.Method+" "+op.Path] = true
		if op.Method != http.MethodPost && op.Method != http.MethodGet && op.Method != http.MethodPut {
			t.Fatalf("allowlisted operation %+v has an unsupported method", op)
		}
		if strings.ContainsAny(op.Path, "*") {
			t.Fatalf("allowlisted operation %q contains a wildcard", op.Path)
		}
		// A concrete path in this shape IS approvable, and the match resolves back to the pattern.
		// A parameterless entry (the capability matrix) is its own concrete path.
		concrete := op.Path
		if strings.Contains(op.Path, "{") {
			if !strings.Contains(op.Path, "}") {
				t.Fatalf("allowlisted operation %q has an unterminated parameter", op.Path)
			}
			concrete = strings.NewReplacer("{workflowId}", "w1", "{attemptId}", "a1",
				"{requestId}", "r1", "{taskId}", "t1", "{planId}", "p1", "{artifactId}", "g1", "{reviewId}", "v1", "{operationId}", "o1", "{candidateId}", "c1").Replace(op.Path)
		} else if !principal.IsOperatorOperation(op.Method, op.Path) {
			t.Fatalf("static allowlisted operation %+v does not match itself", op)
		}
		matched, ok := principal.MatchOperatorOperation(op.Method, concrete)
		if !ok || matched.Path != op.Path {
			t.Fatalf("concrete %s %s matched %+v (ok=%v), want %s", op.Method, concrete, matched, ok, op.Path)
		}
		// The method is part of the binding: the same path under another verb is not approvable.
		if principal.IsOperatorOperation(http.MethodDelete, concrete) {
			t.Fatalf("a DELETE on %q is approvable", concrete)
		}
		// A path that names no allowlisted route shape is not approvable, so the list cannot be a
		// standing grant. (Appending a segment is NOT a valid probe: for a pattern ending in a
		// parameter the extra segment simply fills that parameter. A segment under a different
		// resource is.)
		if principal.IsOperatorOperation(op.Method, "/api/v1/not-a-workflow-route/"+concrete[len(concrete)-1:]) {
			t.Fatalf("%s on an unrelated path is approvable", op.Method)
		}
	}
	for _, path := range want {
		if !listed[http.MethodPost+" "+path] {
			t.Fatalf("the allowlist is missing the mutating verb %s", path)
		}
	}
}
