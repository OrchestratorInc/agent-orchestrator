package controllers_test

import (
	"context"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestM2OperatorApprovalRefusals is the negative matrix: every unusable approval is refused with a
// stable code and no effect.
func TestM2OperatorApprovalRefusals(t *testing.T) {
	t.Parallel()
	env := newApprovalEnv(t)
	bindBody := `{"idempotencyKey":"bind-refused"}`
	digest := principal.PayloadDigest([]byte(bindBody))

	// 1. No proof at all.
	status, raw := doJSONWithHeaders(t, env.srv, http.MethodPost, env.bindPath(), bindBody, nil)
	if status != http.StatusForbidden || !strings.Contains(string(raw), "COLLAB_NOT_AUTHORIZED") {
		t.Fatalf("no credential = %d; body=%s", status, raw)
	}

	// 2. A wrong token, and an unknown approval id.
	status, _ = doJSONWithHeaders(t, env.srv, http.MethodPost, env.bindPath(), bindBody,
		map[string]string{principal.HeaderOperatorToken: "not-the-token"})
	if status != http.StatusForbidden {
		t.Fatalf("wrong token = %d, want 403", status)
	}
	status, raw = env.submitAsApproval(t, http.MethodPost, env.bindPath(), bindBody, "no-such-approval")
	if status != http.StatusForbidden || !strings.Contains(string(raw), principal.CodeApprovalInvalid) {
		t.Fatalf("unknown approval = %d; body=%s", status, raw)
	}

	// 3. An unconfirmed approval.
	pending := env.createApproval(t, http.MethodPost, env.bindPath(), bindBody)
	status, raw = env.submitAsApproval(t, http.MethodPost, env.bindPath(), bindBody, pending.Approval.ApprovalID)
	if status != http.StatusForbidden || !strings.Contains(string(raw), principal.ReasonApprovalUnconfirmed) {
		t.Fatalf("unconfirmed = %d; body=%s", status, raw)
	}

	// 4. A confirmed approval used for a DIFFERENT operation.
	other := env.createApproval(t, http.MethodPost, env.resolvePath(), bindBody)
	env.confirmApproval(t, other, digest)
	status, raw = env.submitAsApproval(t, http.MethodPost, env.bindPath(), bindBody, other.Approval.ApprovalID)
	if status != http.StatusForbidden || !strings.Contains(string(raw), principal.ReasonApprovalOperationMismatch) {
		t.Fatalf("wrong operation = %d; body=%s", status, raw)
	}

	// 5. An expired approval.
	expired := env.createApproval(t, http.MethodPost, env.bindPath(), bindBody)
	env.confirmApproval(t, expired, digest)
	env.clock.Advance(principal.DefaultApprovalTTL + time.Second)
	status, raw = env.submitAsApproval(t, http.MethodPost, env.bindPath(), bindBody, expired.Approval.ApprovalID)
	if status != http.StatusForbidden || !strings.Contains(string(raw), principal.ReasonApprovalExpired) {
		t.Fatalf("expired = %d; body=%s", status, raw)
	}

	// 6. A replayed approval.
	replay := env.createApproval(t, http.MethodPost, env.bindPath(), bindBody)
	env.confirmApproval(t, replay, digest)
	if status, raw := env.submitAsApproval(t, http.MethodPost, env.bindPath(), bindBody,
		replay.Approval.ApprovalID); status != http.StatusOK {
		t.Fatalf("first use = %d; body=%s", status, raw)
	}
	status, raw = env.submitAsApproval(t, http.MethodPost, env.bindPath(), bindBody, replay.Approval.ApprovalID)
	if status != http.StatusForbidden || !strings.Contains(string(raw), principal.ReasonApprovalConsumed) {
		t.Fatalf("replay = %d; body=%s", status, raw)
	}

	// Nothing above may have bound anything beyond the one deliberate replay use.
	if env.gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls = %d, want 1: a refused caller reached the gateway", env.gateway.SpawnCalls())
	}
}

// TestM2OperatorApprovalCannotPromoteAnAgent proves an agent stays an agent with a valid approval,
// and — separately — that presenting the operator token alongside an agent capability does not
// downgrade it either.
func TestM2OperatorApprovalCannotPromoteAnAgent(t *testing.T) {
	t.Parallel()
	env := newApprovalEnv(t)
	ctx := context.Background()
	bindBody := `{"idempotencyKey":"agent-bind"}`
	digest := principal.PayloadDigest([]byte(bindBody))
	approval := env.createApproval(t, http.MethodPost, env.bindPath(), bindBody)
	env.confirmApproval(t, approval, digest)

	// A session whose stored verifier accepts this capability, ALREADY BOUND to the attempt. That
	// binding is what lets the caller derivation find the session the capability is checked
	// against, so the request genuinely resolves as an agent rather than being refused for want of
	// an identity — the two refusals are different facts and the assertion below must be about the
	// first one.
	const agentToken = "agent-capability-for-approval"
	now := time.Now().UTC().Truncate(time.Second)
	created, err := env.store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "project-1", Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata:  domain.SessionMetadata{WorkspacePath: t.TempDir()},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	sessionID := created.ID
	attemptID := domain.WorkflowAttemptID(env.attemptID)
	if _, _, err := env.store.CreateWorkflowAttemptBinding(ctx, domain.WorkflowAttemptBinding{
		AttemptID: attemptID, WorkflowID: domain.WorkflowID(env.workflowID),
		TaskID: domain.WorkflowTaskID(env.taskID), SpawnRequestID: "spawn-agent",
		SpawnHash: strings.Repeat("d", 64), State: domain.WorkflowBindingStateIntentPersisted,
	}, nil, now); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if _, _, err := env.store.BindWorkflowAttemptSession(
		ctx, attemptID, sessionID, "gen-agent",
		domain.WorkflowBindingStateBoundReady, 1, now,
	); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	// The capability is checked against the session the attempt is BOUND to. The store mints the
	// session id, so the verifier for that id is supplied by a reader wrapper rather than by
	// rewriting the row: the resolver's SessionReader is the production interface, and overriding
	// one stored field is exactly what a rotation would do.
	resolver := principal.NewResolver(
		verifierOverride{Sessions: env.store, verifier: approvalVerifier(sessionID, agentToken)},
		principal.CapabilityVerifier{
			Sessions:  verifierOverride{Sessions: env.store, verifier: approvalVerifier(sessionID, agentToken)},
			Authority: approvalAuthority{},
		},
		principal.ApprovalOperatorTrust{Approvals: env.approvals},
	).WithLeaseProver(principal.StoreLeaseProver{Attempts: env.store, Clock: env.clock.Now})
	srv := env.serverWith(t, resolver)

	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{
			name: "approval only",
			headers: map[string]string{
				principal.HeaderOperatorApproval: approval.Approval.ApprovalID,
				principal.HeaderAgentCapability:  agentToken,
				principal.HeaderAttemptID:        env.attemptID,
				principal.HeaderAttemptLease:     "lease-1",
			},
		},
		{
			name: "approval plus operator token",
			headers: map[string]string{
				principal.HeaderOperatorApproval: approval.Approval.ApprovalID,
				principal.HeaderOperatorToken:    env.token,
				principal.HeaderAgentCapability:  agentToken,
				principal.HeaderAttemptID:        env.attemptID,
				principal.HeaderAttemptLease:     "lease-1",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := doJSONWithHeaders(t, srv, http.MethodPost, env.bindPath(), bindBody, tc.headers)
			if status != http.StatusForbidden {
				t.Fatalf("agent bind = %d, want 403; body=%s", status, raw)
			}
			if !strings.Contains(string(raw), "COLLAB_NOT_AUTHORIZED") {
				t.Fatalf("body does not carry the authorization code: %s", raw)
			}
		})
	}
	if env.gateway.SpawnCalls() != 0 {
		t.Fatalf("an agent bound an attempt: spawn calls = %d", env.gateway.SpawnCalls())
	}
}
