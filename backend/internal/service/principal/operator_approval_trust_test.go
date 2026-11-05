package principal_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// testConsent is the injected OS boundary: it records what it was asked and returns a configured
// outcome. Production uses operatorconsent.New() (Windows Hello); tests never show a prompt.
type testConsent struct {
	err  error
	seen []principal.Approval
}

func (c *testConsent) Verify(_ context.Context, approval principal.Approval) error {
	c.seen = append(c.seen, approval)
	return c.err
}

// approveAndConfirm runs the real path: the store asks the OS verifier, then records the gesture.
func approveAndConfirm(t *testing.T, f *approvalFixture, id, code, digest string) principal.Approval {
	t.Helper()
	consent := &testConsent{}
	approval, err := f.store.VerifyAndConfirm(context.Background(), id, code, digest, consent)
	if err != nil {
		t.Fatalf("native consent: %v", err)
	}
	if len(consent.seen) != 1 || consent.seen[0].ID != id {
		t.Fatalf("the OS verifier saw %+v, want the approval under test", consent.seen)
	}
	return approval
}

// TestOperatorTrustRequiresApproval 证明 token 无法跳过逐操作审批。
func TestOperatorTrustRequiresApproval(t *testing.T) {
	t.Parallel()
	f := newApprovalFixture(t)
	digest := principal.PayloadDigest(nil)

	// The legacy token shape the old wiring wrote to <dataDir>/operator.token. It is not part of
	// the trust any more, so presenting it must not produce an operator.
	tokenOnly := httptest.NewRequest(http.MethodPost, approvalBindPath, nil)
	tokenOnly.RemoteAddr = "127.0.0.1:5555"
	tokenOnly.Header.Set(principal.HeaderOperatorToken, strings.Repeat("a", 64))
	if f.trust.PermitsOperator(tokenOnly) {
		t.Fatal("token alone bypassed per-operation human consent")
	}

	// A loopback peer with nothing else proves nothing, at either layer.
	bare := httptest.NewRequest(http.MethodPost, approvalBindPath, nil)
	bare.RemoteAddr = "127.0.0.1:5555"
	if f.trust.PermitsOperator(bare) {
		t.Fatal("a bare loopback peer proved the operator")
	}
	if _, err := f.trust.ProveApproval(bare); err == nil {
		t.Fatal("a bare loopback peer proved an approval")
	}

	// An approval presented without a binding is refused rather than trusted: the operation and
	// payload must be known, or the approval covers nothing.
	approval, err := f.store.CreateWithPayload(http.MethodPost, approvalBindPath, digest, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.store.VerifyAndConfirm(
		context.Background(), approval.ID, approval.Code, digest, &testConsent{}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	unbound := httptest.NewRequest(http.MethodPost, approvalBindPath, nil)
	unbound.RemoteAddr = "127.0.0.1:5555"
	unbound.Header.Set(principal.HeaderOperatorApproval, approval.ID)
	if _, err := f.trust.ProveApproval(unbound); err == nil {
		t.Fatal("an unbound approval was accepted")
	} else if refusal, ok := principal.AsApprovalRefusal(err); !ok || refusal.Reason != principal.ReasonApprovalUnbound {
		t.Fatalf("unbound refusal = %v, want reason %q", err, principal.ReasonApprovalUnbound)
	}

	// A non-loopback peer is refused even with a perfect approval: it is a local proof.
	remote := approvalRequest(approval.ID, http.MethodPost, approvalBindPath, "")
	remote.RemoteAddr = "10.1.2.3:4000"
	if _, err := f.trust.ProveApproval(remote); err == nil {
		t.Fatal("an approval was accepted from a non-loopback peer")
	}
}

// TestOperatorConsentFailuresFailClosedWithDistinctReasons proves the two OS outcomes are distinct
// and both refuse: neither a denial nor an unavailable verifier can confirm an approval.
func TestOperatorConsentFailuresFailClosedWithDistinctReasons(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		err    error
		reason string
	}{
		{"denied", principal.ErrConsentDenied, "consent_denied"},
		{"unavailable", principal.ErrConsentUnavailable, "consent_unavailable"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newApprovalFixture(t)
			digest := principal.PayloadDigest(nil)
			approval, err := f.store.CreateWithPayload(http.MethodPost, approvalBindPath, digest, "")
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			_, err = f.store.VerifyAndConfirm(
				context.Background(), approval.ID, approval.Code, digest, &testConsent{err: tc.err})
			if err == nil {
				t.Fatal("a failed native verification confirmed the approval")
			}
			refusal, ok := principal.AsApprovalRefusal(err)
			if !ok || refusal.Reason != tc.reason {
				t.Fatalf("refusal = %v, want reason %q", err, tc.reason)
			}
			// The approval stays pending: a refusal is not a confirm, and it is not a consumption.
			if state, _, found := f.store.State(approval.ID); !found || state != principal.ApprovalStatePending {
				t.Fatalf("state after refusal = %q (found=%v), want pending", state, found)
			}
			// A nil verifier is the "no OS integration" wiring and refuses the same way.
			if _, err := f.store.VerifyAndConfirm(
				context.Background(), approval.ID, approval.Code, digest, nil); err == nil {
				t.Fatal("a nil verifier confirmed an approval")
			}
		})
	}
}

// TestResolverDerivesOperatorFromApproval proves the resolver, not just the store: an approval
// produces an operator caller whose recorded identity is the OS verification, not a credential.
func TestResolverDerivesOperatorFromApproval(t *testing.T) {
	t.Parallel()
	f := newApprovalFixture(t)
	digest := principal.PayloadDigest(nil)
	approval, err := f.store.CreateWithPayload(http.MethodPost, approvalBindPath, digest, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	confirmed := approveAndConfirm(t, f, approval.ID, approval.Code, digest)
	if confirmed.ConfirmedByFingerprint != principal.NativeConsentIdentity {
		t.Fatalf("confirming identity = %q, want %q",
			confirmed.ConfirmedByFingerprint, principal.NativeConsentIdentity)
	}

	resolver := principal.NewResolver(nil, nil, f.trust)
	req := approvalRequest(approval.ID, http.MethodPost, approvalBindPath, "")
	caller, err := resolver.FromRequest(req, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !caller.IsOperator() {
		t.Fatalf("caller kind = %q, want operator", caller.Kind)
	}
	if caller.OperatorTokenFingerprint != principal.NativeConsentIdentity {
		t.Fatalf("fingerprint = %q, want the native consent identity", caller.OperatorTokenFingerprint)
	}

	// The approval is spent: the same request is refused the second time.
	if _, err := resolver.FromRequest(req, ""); err == nil {
		t.Fatal("a resolver accepted a replayed approval")
	} else if refusal, ok := principal.AsApprovalRefusal(err); !ok || refusal.Reason != principal.ReasonApprovalConsumed {
		t.Fatalf("replay refusal = %v, want reason %q", err, principal.ReasonApprovalConsumed)
	}

	// No proof of any kind still answers principal.ErrNotAuthorized, not an approval code: the CLI's
	// "you proved nothing" case must stay distinguishable from "your approval is unusable".
	bare := httptest.NewRequest(http.MethodPost, approvalBindPath, nil)
	bare.RemoteAddr = "127.0.0.1:5555"
	if _, err := resolver.FromRequest(bare, ""); !errors.Is(err, principal.ErrNotAuthorized) {
		t.Fatalf("bare request error = %v, want principal.ErrNotAuthorized", err)
	}
}

// TestAgentCallerStaysAnAgentWithAnApproval proves the approval cannot promote an agent: a request
// that proves a capability is resolved as an agent even when it also carries a usable approval,
// so the operator-only checks downstream keep refusing it.
func TestAgentCallerStaysAnAgentWithAnApproval(t *testing.T) {
	t.Parallel()
	f := newApprovalFixture(t)
	digest := principal.PayloadDigest(nil)
	approval, err := f.store.CreateWithPayload(http.MethodPost, approvalBindPath, digest, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	approveAndConfirm(t, f, approval.ID, approval.Code, digest)

	s := identityFixture()
	resolver := principal.StoreResolverWithOperator(s, s, testAuthority{}, f.trust)

	req := approvalRequest(approval.ID, http.MethodPost, approvalBindPath, "")
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	caller, err := resolver.Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: s.session.ID,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !caller.IsAgent() {
		t.Fatalf("caller kind = %q, want agent despite the approval", caller.Kind)
	}
	// The approval was never spent: an agent request does not reach the operator path at all,
	// which is what keeps a valid approval from becoming an agent's authority.
	if _, err := f.store.Consume(approval.ID, http.MethodPost, approvalBindPath, digest); err != nil {
		t.Fatalf("the agent request spent the grant: %v", err)
	}
}
