package principal_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// TestOperatorApprovalLifecycle is the state machine: pending → confirmed → consumed, with each
// step refused out of order.
func TestOperatorApprovalLifecycle(t *testing.T) {
	t.Parallel()
	f := newApprovalFixture(t)
	body := `{"idempotencyKey":"bind-1"}`
	digest := principal.PayloadDigest([]byte(body))

	approval, err := f.store.Create(http.MethodPost, approvalBindPath, digest)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if approval.State(f.now) != principal.ApprovalStatePending {
		t.Fatalf("state = %q, want pending", approval.State(f.now))
	}
	if approval.Pattern != "/api/v1/workflows/{workflowId}/attempts/{attemptId}/bind" {
		t.Fatalf("pattern = %q, want the allowlisted bind pattern", approval.Pattern)
	}
	if approval.Path != approvalBindPath {
		t.Fatalf("path = %q, want the concrete path", approval.Path)
	}

	// An unconfirmed approval can never authorise a request.
	if _, err := f.store.Consume(approval.ID, http.MethodPost, approvalBindPath, digest); err == nil {
		t.Fatal("an unconfirmed approval authorised a request")
	} else if refusal, ok := principal.AsApprovalRefusal(err); !ok || refusal.Reason != principal.ReasonApprovalUnconfirmed {
		t.Fatalf("unconfirmed refusal = %v, want reason %q", err, principal.ReasonApprovalUnconfirmed)
	}

	// A wrong code does not confirm.
	if _, err := f.store.Confirm(approval.ID, "WRONGCODE", digest); err == nil {
		t.Fatal("a wrong code confirmed an approval")
	} else if refusal, ok := principal.AsApprovalRefusal(err); !ok || refusal.Reason != principal.ReasonApprovalCodeMismatch {
		t.Fatalf("wrong-code refusal = %v, want reason %q", err, principal.ReasonApprovalCodeMismatch)
	}
	// A wrong digest does not confirm either, so a confirmation cannot be pointed at a payload
	// the human never saw.
	if _, err := f.store.Confirm(approval.ID, approval.Code, principal.PayloadDigest([]byte("{}"))); err == nil {
		t.Fatal("a wrong digest confirmed an approval")
	} else if refusal, ok := principal.AsApprovalRefusal(err); !ok || refusal.Reason != principal.ReasonApprovalPayloadMismatch {
		t.Fatalf("wrong-digest refusal = %v, want reason %q", err, principal.ReasonApprovalPayloadMismatch)
	}

	confirmed, err := f.store.ConfirmWithFingerprint(approval.ID, approval.Code, digest, "fingerprint1")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if !confirmed.Confirmed || confirmed.ConfirmedByFingerprint != "fingerprint1" {
		t.Fatalf("confirmed record = %+v", confirmed)
	}
	if _, err := f.store.Confirm(approval.ID, approval.Code, digest); err == nil {
		t.Fatal("a second confirmation was accepted")
	}

	fingerprint, err := f.store.Consume(approval.ID, http.MethodPost, approvalBindPath, digest)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	// The fingerprint returned is the CONFIRMING credential's, which is what an audit row must
	// record: the human gesture, not the requesting process.
	if fingerprint != "fingerprint1" {
		t.Fatalf("fingerprint = %q, want the confirming credential's", fingerprint)
	}
	if _, err := f.store.Consume(approval.ID, http.MethodPost, approvalBindPath, digest); err == nil {
		t.Fatal("an approval was consumed twice")
	} else if refusal, ok := principal.AsApprovalRefusal(err); !ok || refusal.Reason != principal.ReasonApprovalConsumed {
		t.Fatalf("replay refusal = %v, want reason %q", err, principal.ReasonApprovalConsumed)
	}
}

// TestOperatorApprovalExpires proves the TTL is enforced from the store's clock and that a
// consumed approval reports consumed rather than expired.
func TestOperatorApprovalExpires(t *testing.T) {
	t.Parallel()
	f := newApprovalFixture(t)
	digest := principal.PayloadDigest(nil)
	approval, err := f.store.Create(http.MethodPost, approvalBindPath, digest)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f.tick(11 * time.Second)
	if _, err := f.store.Consume(approval.ID, http.MethodPost, approvalBindPath, digest); err == nil {
		t.Fatal("an expired approval authorised a request")
	} else if refusal, ok := principal.AsApprovalRefusal(err); !ok || refusal.Reason != principal.ReasonApprovalExpired {
		t.Fatalf("expired refusal = %v, want reason %q", err, principal.ReasonApprovalExpired)
	}
	// Confirmation of an expired approval is refused too: the human gesture must still be
	// possible when the record is live.
	if _, err := f.store.Confirm(approval.ID, approval.Code, digest); err == nil {
		t.Fatal("an expired approval was confirmed")
	}
}

// TestOperatorApprovalIsBoundToTheExactOperation is the "approval for A used on B" proof, at both
// levels: a sibling route, and another attempt on the same route.
func TestOperatorApprovalIsBoundToTheExactOperation(t *testing.T) {
	t.Parallel()
	f := newApprovalFixture(t)
	digest := principal.PayloadDigest(nil)
	approval, err := f.store.Create(http.MethodPost, approvalBindPath, digest)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.store.Confirm(approval.ID, approval.Code, digest); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	// Another attempt on the same route: refused, because the human approved that target.
	otherTarget := "/api/v1/workflows/w1/attempts/a2/bind"
	if _, err := f.store.Consume(approval.ID, http.MethodPost, otherTarget, digest); err == nil {
		t.Fatal("an approval for one attempt authorised another")
	} else if refusal, ok := principal.AsApprovalRefusal(err); !ok || refusal.Reason != principal.ReasonApprovalOperationMismatch {
		t.Fatalf("other-target refusal = %v, want %q", err, principal.ReasonApprovalOperationMismatch)
	}

	// A different route: refused.
	if _, err := f.store.Consume(approval.ID, http.MethodPost,
		"/api/v1/workflows/w1/attempts/a1/resolve", digest); err == nil {
		t.Fatal("an approval for bind authorised resolve")
	}

	// A different method on the same path: refused.
	if _, err := f.store.Consume(approval.ID, http.MethodGet, approvalBindPath, digest); err == nil {
		t.Fatal("an approval for POST authorised GET")
	}

	// The right operation with the wrong payload: refused.
	if _, err := f.store.Consume(approval.ID, http.MethodPost, approvalBindPath,
		principal.PayloadDigest([]byte(`{"idempotencyKey":"other"}`))); err == nil {
		t.Fatal("an approval authorised a different payload")
	}

	// The exact operation and payload still works after every refusal above, because a refused
	// consumption does not spend the approval — the request it refused was never the approved one.
	if _, err := f.store.Consume(approval.ID, http.MethodPost, approvalBindPath, digest); err != nil {
		t.Fatalf("the exact approved operation was refused: %v", err)
	}
}

// TestOperatorApprovalRefusesAnUnlistedOperation proves the allowlist is closed: an approval can
// never name a route this daemon does not gate.
func TestOperatorApprovalRefusesAnUnlistedOperation(t *testing.T) {
	t.Parallel()
	f := newApprovalFixture(t)
	for _, path := range []string{
		"/api/v1/sessions",
		"/api/v1/workflows/w1/attempts/a1/renew-lease",
		"/api/v1/workflows/w1/attempts/a1/bind/extra",
		"/api/v1/operator/approvals",
	} {
		if _, err := f.store.Create(http.MethodPost, path, principal.PayloadDigest(nil)); err == nil {
			t.Fatalf("%s was approved, but it is not an approval-gated operation", path)
		}
	}
}

// TestOperatorApprovalsDieWithTheProcess proves the per-boot property by construction: a new store
// (a restart) knows nothing about the previous one's approvals.
func TestOperatorApprovalsDieWithTheProcess(t *testing.T) {
	t.Parallel()
	f := newApprovalFixture(t)
	digest := principal.PayloadDigest(nil)
	approval, err := f.store.Create(http.MethodPost, approvalBindPath, digest)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.store.Confirm(approval.ID, approval.Code, digest); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	restarted := principal.NewApprovalStore(principal.ApprovalStoreOptions{TTL: 10 * time.Second})
	if _, err := restarted.Consume(approval.ID, http.MethodPost, approvalBindPath, digest); err == nil {
		t.Fatal("an approval survived a daemon restart")
	} else if refusal, ok := principal.AsApprovalRefusal(err); !ok || refusal.Reason != principal.ReasonApprovalUnknown {
		t.Fatalf("post-restart refusal = %v, want reason %q", err, principal.ReasonApprovalUnknown)
	}
}

// TestOperatorApprovalCapacityIsBounded proves the unauthenticated create route cannot be used to
// grow the in-memory table without bound.
func TestOperatorApprovalCapacityIsBounded(t *testing.T) {
	t.Parallel()
	store := principal.NewApprovalStore(principal.ApprovalStoreOptions{
		TTL: time.Minute,
		Max: 2,
	})
	digest := principal.PayloadDigest(nil)
	for i := range 2 {
		if _, err := store.Create(http.MethodPost, approvalBindPath, digest); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := store.Create(http.MethodPost, approvalBindPath, digest); err == nil {
		t.Fatal("the approval table exceeded its cap")
	} else if refusal, ok := principal.AsApprovalRefusal(err); !ok || refusal.Reason != principal.ReasonApprovalLimit {
		t.Fatalf("capacity refusal = %v, want reason %q", err, principal.ReasonApprovalLimit)
	}
}
