package principal_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// fakeSessions is a session reader over a fixed map.
type fakeSessions struct {
	sessions map[domain.SessionID]domain.SessionRecord
	err      error
}

func (f fakeSessions) GetSession(
	_ context.Context,
	id domain.SessionID,
) (domain.SessionRecord, bool, error) {
	if f.err != nil {
		return domain.SessionRecord{}, false, f.err
	}
	rec, ok := f.sessions[id]
	return rec, ok, nil
}

// fakeAttempts is an attempt reader over a fixed map.
type fakeAttempts struct {
	attempts map[domain.WorkflowAttemptID]domain.WorkflowAttempt
}

func (f fakeAttempts) GetWorkflowAttempt(
	_ context.Context,
	id domain.WorkflowAttemptID,
) (domain.WorkflowAttempt, bool, error) {
	attempt, ok := f.attempts[id]
	return attempt, ok, nil
}

const agentToken = "agent-capability-token"

// browserStyleVerifier reproduces service/browser.Authority's digest, which is the digest the
// production verifier is checked against: the worker capability and the agent capability are the
// same credential, so this test pins the shared contract rather than a private variant of it.
func browserStyleVerifier(sessionID domain.SessionID, token string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("ao-browser-capability-verifier-v1\x00"))
	_, _ = h.Write([]byte(sessionID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(token))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func sessionWithVerifier(id domain.SessionID, projectID domain.ProjectID, token string) domain.SessionRecord {
	return domain.SessionRecord{
		ID:        id,
		ProjectID: projectID,
		Metadata: domain.SessionMetadata{
			BrowserCapabilityVerifier: browserStyleVerifier(id, token),
		},
	}
}

// testAuthority is the digest comparison the production resolver takes from service/browser.
// It reproduces browser.Authority exactly so these tests pin the shared contract rather than a
// private reimplementation of it.
type testAuthority struct{}

func (testAuthority) Valid(sessionID domain.SessionID, token, verifier string) bool {
	if sessionID == "" || token == "" || verifier == "" {
		return false
	}
	return browserStyleVerifier(sessionID, token) == verifier
}

// resolverFixture builds a resolver whose session `session-1` accepts agentToken and whose
// attempt `attempt-1` (in `workflow-1`) holds leaseToken.
func resolverFixture() *principal.Resolver {
	sessions := fakeSessions{sessions: map[domain.SessionID]domain.SessionRecord{
		"session-1": sessionWithVerifier("session-1", "project-1", agentToken),
	}}
	leaseUntil := time.Now().Add(time.Hour)
	attempts := fakeAttempts{attempts: map[domain.WorkflowAttemptID]domain.WorkflowAttempt{
		"attempt-1": {
			ID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1",
			LeaseToken: "lease-token-1", LeaseUntil: &leaseUntil,
		},
		"attempt-2": {
			ID: "attempt-2", WorkflowID: "workflow-1", TaskID: "task-2",
			LeaseToken: "lease-token-2", LeaseUntil: &leaseUntil,
		},
	}}
	return principal.NewResolver(
		sessions,
		principal.CapabilityVerifier{Sessions: sessions, Authority: testAuthority{}},
		operatorTrustFixture(),
	).WithLeaseProver(principal.StoreLeaseProver{Attempts: attempts})
}

// operatorToken is the operator credential the fixtures accept. It is a fixed test value, never a
// minted one: the production trust is built from <dataDir>/operator.token, which these tests must
// not touch.
const operatorToken = "operator-token-for-principal-tests"

// operatorTrustFixture is the production operator proof, minus the filesystem: a loopback peer AND
// this exact token.
func operatorTrustFixture() principal.OperatorTrust {
	return principal.NewTokenTrust(operatorToken)
}

// operatorRequest builds a loopback request carrying the operator credential, which is what every
// operator-path test must now present.
func operatorRequest() *http.Request {
	req := loopbackRequest()
	req.Header.Set(principal.HeaderOperatorToken, operatorToken)
	return req
}

func loopbackRequest() *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	return req
}

// TestOperatorWithTokenIsPermitted proves the operator path: a loopback peer presenting the
// operator credential resolves as the operator.
func TestOperatorWithTokenIsPermitted(t *testing.T) {
	t.Parallel()
	caller, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{
		Request: operatorRequest(),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if caller.Kind != principal.KindOperator {
		t.Fatalf("kind = %q, want operator", caller.Kind)
	}
	if caller.LeaseProven {
		t.Fatal("an operator resolved through the loopback path must not claim a proven lease")
	}
}

// TestNonLoopbackPeerIsRefused proves the resolver does not fall back to "anonymous" for a peer
// it cannot attribute: a LAN or remote peer with no capability is refused outright.
func TestNonLoopbackPeerIsRefused(t *testing.T) {
	t.Parallel()
	for _, remote := range []string{"10.0.0.7:1234", "203.0.113.9:80", "192.168.1.20:5555"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
		req.RemoteAddr = remote
		_, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{Request: req})
		if !errors.Is(err, principal.ErrNotAuthorized) {
			t.Fatalf("%s: err = %v, want ErrNotAuthorized", remote, err)
		}
	}
}

// TestValidCapabilityResolvesAsAgent proves the agent path with a lease.
func TestValidCapabilityResolvesAsAgent(t *testing.T) {
	t.Parallel()
	req := loopbackRequest()
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	caller, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: "session-1",
		WorkflowID: "workflow-1", AttemptID: "attempt-1", LeaseToken: "lease-token-1",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if caller.Kind != principal.KindAgent {
		t.Fatalf("kind = %q, want agent", caller.Kind)
	}
	if caller.SessionID != "session-1" || caller.ProjectID != "project-1" {
		t.Fatalf("caller = %+v, want the session's own identity", caller)
	}
	if caller.AttemptID != "attempt-1" || !caller.LeaseProven {
		t.Fatalf("caller = %+v, want a proven attempt-1 lease", caller)
	}
}

// TestWrongCapabilityIsRefusedWithoutDowngrade is the load-bearing one: a bad capability must not
// quietly become an operator request. Loopback would otherwise pass the operator check.
func TestWrongCapabilityIsRefusedWithoutDowngrade(t *testing.T) {
	t.Parallel()
	req := loopbackRequest()
	req.Header.Set(principal.HeaderAgentCapability, "not-the-token")
	_, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: "session-1",
		WorkflowID: "workflow-1", AttemptID: "attempt-1", LeaseToken: "lease-token-1",
	})
	if !errors.Is(err, principal.ErrCapabilityInvalid) {
		t.Fatalf("err = %v, want ErrCapabilityInvalid", err)
	}
}

// TestCapabilityForAnotherSessionIsRefused proves the verifier is bound to the session id, so a
// token minted for one session cannot speak for another.
func TestCapabilityForAnotherSessionIsRefused(t *testing.T) {
	t.Parallel()
	sessions := fakeSessions{sessions: map[domain.SessionID]domain.SessionRecord{
		"session-1": sessionWithVerifier("session-1", "project-1", agentToken),
		"session-2": sessionWithVerifier("session-2", "project-2", "other-token"),
	}}
	resolver := principal.NewResolver(
		sessions, principal.CapabilityVerifier{Sessions: sessions, Authority: testAuthority{}}, operatorTrustFixture(),
	)
	req := loopbackRequest()
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	_, err := resolver.Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: "session-2",
	})
	if !errors.Is(err, principal.ErrCapabilityInvalid) {
		t.Fatalf("err = %v, want ErrCapabilityInvalid", err)
	}
}

// TestCapabilityWithoutLeaseIsRefused proves holding a capability is not holding an attempt.
func TestCapabilityWithoutLeaseIsRefused(t *testing.T) {
	t.Parallel()
	req := loopbackRequest()
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	_, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: "session-1",
		WorkflowID: "workflow-1", AttemptID: "attempt-1",
	})
	if !errors.Is(err, principal.ErrLeaseUnproven) {
		t.Fatalf("err = %v, want ErrLeaseUnproven", err)
	}
}

// TestLeaseForAnotherAttemptIsRefused proves an attempt id is not identity: naming attempt-1
// while holding attempt-2's lease is refused.
func TestLeaseForAnotherAttemptIsRefused(t *testing.T) {
	t.Parallel()
	req := loopbackRequest()
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	_, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: "session-1",
		WorkflowID: "workflow-1", AttemptID: "attempt-1", LeaseToken: "lease-token-2",
	})
	if !errors.Is(err, principal.ErrLeaseUnproven) {
		t.Fatalf("err = %v, want ErrLeaseUnproven", err)
	}
}

// TestLeaseForAnotherWorkflowIsRefused proves a lease is scoped to its workflow.
func TestLeaseForAnotherWorkflowIsRefused(t *testing.T) {
	t.Parallel()
	req := loopbackRequest()
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	_, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: "session-1",
		WorkflowID: "workflow-2", AttemptID: "attempt-1", LeaseToken: "lease-token-1",
	})
	if !errors.Is(err, principal.ErrLeaseUnproven) {
		t.Fatalf("err = %v, want ErrLeaseUnproven", err)
	}
}

// TestExpiredLeaseIsRefused proves a settled or expired attempt cannot be claimed.
func TestExpiredLeaseIsRefused(t *testing.T) {
	t.Parallel()
	sessions := fakeSessions{sessions: map[domain.SessionID]domain.SessionRecord{
		"session-1": sessionWithVerifier("session-1", "project-1", agentToken),
	}}
	expired := time.Now().Add(-time.Minute)
	attempts := fakeAttempts{attempts: map[domain.WorkflowAttemptID]domain.WorkflowAttempt{
		"attempt-1": {
			ID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1",
			LeaseToken: "lease-token-1", LeaseUntil: &expired,
		},
	}}
	resolver := principal.NewResolver(
		sessions, principal.CapabilityVerifier{Sessions: sessions, Authority: testAuthority{}}, operatorTrustFixture(),
	).WithLeaseProver(principal.StoreLeaseProver{Attempts: attempts})

	req := loopbackRequest()
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	_, err := resolver.Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: "session-1",
		WorkflowID: "workflow-1", AttemptID: "attempt-1", LeaseToken: "lease-token-1",
	})
	if !errors.Is(err, principal.ErrLeaseUnproven) {
		t.Fatalf("err = %v, want ErrLeaseUnproven", err)
	}
}

// TestAttemptIDWithoutAnyProofIsRefused proves an operator request that merely names an attempt
// does not inherit that attempt's identity.
func TestAttemptIDWithoutAnyProofIsRefused(t *testing.T) {
	t.Parallel()
	_, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{
		Request: operatorRequest(), AttemptID: "attempt-1",
	})
	if !errors.Is(err, principal.ErrLeaseUnproven) {
		t.Fatalf("err = %v, want ErrLeaseUnproven", err)
	}

	// The same on the agent route: a session id in the URL without a capability is not a
	// downgrade into an attempt identity either.
	req := operatorRequest()
	_, err = resolverFixture().Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: "session-1", AttemptID: "attempt-1",
	})
	if !errors.Is(err, principal.ErrLeaseUnproven) {
		t.Fatalf("session-scoped err = %v, want ErrLeaseUnproven", err)
	}
}

// TestCapabilityHeaderWithoutSessionIsRefused proves a capability with nothing to verify it
// against is not treated as an operator request.
func TestCapabilityHeaderWithoutSessionIsRefused(t *testing.T) {
	t.Parallel()
	req := loopbackRequest()
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	_, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{Request: req})
	if !errors.Is(err, principal.ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
}

// TestNilTrustRefusesEverything proves a resolver built without an operator trust source refuses
// rather than permitting: a missing dependency is not permission.
func TestNilTrustRefusesEverything(t *testing.T) {
	t.Parallel()
	resolver := principal.NewResolver(fakeSessions{}, principal.CapabilityVerifier{Authority: testAuthority{}}, nil)
	_, err := resolver.Resolve(context.Background(), principal.RequestFacts{Request: operatorRequest()})
	if !errors.Is(err, principal.ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
}

// TestTerminatedSessionCapabilityIsRefused proves a surviving token from a dead session is dead.
func TestTerminatedSessionCapabilityIsRefused(t *testing.T) {
	t.Parallel()
	rec := sessionWithVerifier("session-1", "project-1", agentToken)
	rec.IsTerminated = true
	sessions := fakeSessions{sessions: map[domain.SessionID]domain.SessionRecord{"session-1": rec}}
	resolver := principal.NewResolver(
		sessions, principal.CapabilityVerifier{Sessions: sessions, Authority: testAuthority{}}, operatorTrustFixture(),
	)
	req := loopbackRequest()
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	_, err := resolver.Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: "session-1",
	})
	if !errors.Is(err, principal.ErrCapabilityInvalid) {
		t.Fatalf("err = %v, want ErrCapabilityInvalid", err)
	}
}

// TestCapabilityVerifierRequiresAnAuthority proves a verifier without a comparison function
// refuses rather than passing: a missing dependency is not permission.
func TestCapabilityVerifierRequiresAnAuthority(t *testing.T) {
	t.Parallel()
	sessions := fakeSessions{sessions: map[domain.SessionID]domain.SessionRecord{
		"session-1": sessionWithVerifier("session-1", "project-1", agentToken),
	}}
	withoutAuthority := principal.NewResolver(
		sessions, principal.CapabilityVerifier{Sessions: sessions}, operatorTrustFixture(),
	)
	req := loopbackRequest()
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	if _, err := withoutAuthority.Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: "session-1",
	}); !errors.Is(err, principal.ErrCapabilityInvalid) {
		t.Fatalf("err = %v, want ErrCapabilityInvalid", err)
	}
}

// TestStoreResolverWiresEveryCheck proves the production constructor answers both questions.
func TestStoreResolverWiresEveryCheck(t *testing.T) {
	t.Parallel()
	sessions := fakeSessions{sessions: map[domain.SessionID]domain.SessionRecord{
		"session-1": sessionWithVerifier("session-1", "project-1", agentToken),
	}}
	leaseUntil := time.Now().Add(time.Hour)
	attempts := fakeAttempts{attempts: map[domain.WorkflowAttemptID]domain.WorkflowAttempt{
		"attempt-1": {
			ID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1",
			LeaseToken: "lease-token-1", LeaseUntil: &leaseUntil,
		},
	}}
	resolver := principal.StoreResolverWithOperator(sessions, attempts, testAuthority{}, operatorTrustFixture())

	req := loopbackRequest()
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	caller, err := resolver.Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: "session-1",
		WorkflowID: "workflow-1", AttemptID: "attempt-1", LeaseToken: "lease-token-1",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !caller.LeaseProven || caller.AttemptID != "attempt-1" {
		t.Fatalf("caller = %+v, want a proven attempt", caller)
	}

	// An operator presenting the credential still resolves, with no attempt identity.
	operator, err := resolver.Resolve(context.Background(), principal.RequestFacts{
		Request: operatorRequest(),
	})
	if err != nil {
		t.Fatalf("operator resolve: %v", err)
	}
	if !operator.IsOperator() || operator.LeaseProven {
		t.Fatalf("operator = %+v", operator)
	}
}

// TestTheWorkerCapabilityIsTheAgentCapability pins the deliberate sharing.
//
// AO persists one verifier per session, so the capability a worker receives at launch is the one
// it presents here. This test states that as a fact rather than leaving it implicit, and pairs it
// with the rule that matters: the capability alone is NOT attempt identity — the lease below is.
func TestTheWorkerCapabilityIsTheAgentCapability(t *testing.T) {
	t.Parallel()
	resolver := resolverFixture()

	// The capability alone resolves a session-scoped agent.
	req := loopbackRequest()
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	caller, err := resolver.Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if caller.Kind != principal.KindAgent || caller.SessionID != "session-1" {
		t.Fatalf("caller = %+v, want the session's own agent identity", caller)
	}
	if caller.LeaseProven || caller.AttemptID != "" {
		t.Fatalf("a bare capability was treated as an attempt identity: %+v", caller)
	}

	// And a capability minted for one session cannot speak for another, because the digest is
	// bound to the session id.
	otherReq := loopbackRequest()
	otherReq.Header.Set(principal.HeaderAgentCapability, agentToken)
	if _, err := resolver.Resolve(context.Background(), principal.RequestFacts{
		Request: otherReq, AgentSessionID: "session-9",
	}); !errors.Is(err, principal.ErrCapabilityInvalid) {
		t.Fatalf("err = %v, want ErrCapabilityInvalid for a foreign session", err)
	}
}

// TestForbiddenRequestWithoutRemoteAddrIsRefused covers the degenerate transport case.
func TestForbiddenRequestWithoutRemoteAddrIsRefused(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
	req.RemoteAddr = ""
	_, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{Request: req})
	if !errors.Is(err, principal.ErrNotAuthorized) {
		t.Fatalf("err = %v, want ErrNotAuthorized", err)
	}
}

// ---------------------------------------------------------------------------
// R-04: the operator is a credential, not an address
// ---------------------------------------------------------------------------

// TestLoopbackPeerAloneIsNotTheOperator is the core R-04 regression: a loopback peer with no
// operator credential must NOT resolve as the operator. Every worker AO launches shares that
// address, so treating it as the human would let a worker act as its own owner.
func TestLoopbackPeerAloneIsNotTheOperator(t *testing.T) {
	t.Parallel()
	_, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{
		Request: loopbackRequest(),
	})
	if !errors.Is(err, principal.ErrNotAuthorized) {
		t.Fatalf("a bare loopback peer resolved with err = %v, want ErrNotAuthorized", err)
	}
}

// TestWrongOperatorTokenIsRefused proves the comparison is exact: a token of the right shape that
// is not the credential is refused, even over loopback.
func TestWrongOperatorTokenIsRefused(t *testing.T) {
	t.Parallel()
	for _, presented := range []string{
		"not-the-operator-token",
		operatorToken + "x",
		strings.ToUpper(operatorToken),
	} {
		req := loopbackRequest()
		req.Header.Set(principal.HeaderOperatorToken, presented)
		if _, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{
			Request: req,
		}); !errors.Is(err, principal.ErrNotAuthorized) {
			t.Fatalf("%q resolved with err = %v, want ErrNotAuthorized", presented, err)
		}
	}
}

// TestNonLoopbackPeerWithAValidTokenIsRefused proves the transport requirement is not relaxed by
// the credential: the operator token is minted for the loopback listener, so accepting it from a
// LAN address would turn a local secret into a network password.
func TestNonLoopbackPeerWithAValidTokenIsRefused(t *testing.T) {
	t.Parallel()
	for _, remote := range []string{"10.0.0.7:1234", "203.0.113.9:80", "192.168.1.20:5555"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
		req.RemoteAddr = remote
		req.Header.Set(principal.HeaderOperatorToken, operatorToken)
		if _, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{
			Request: req,
		}); !errors.Is(err, principal.ErrNotAuthorized) {
			t.Fatalf("%s resolved with err = %v, want ErrNotAuthorized", remote, err)
		}
	}
}

// TestUnconfiguredTokenTrustRefusesEverything proves the fail-closed default: a trust with no
// verifier — the shape a daemon gets when the credential file could not be read — permits nothing,
// so every sensitive route refuses rather than falling back to the peer address.
func TestUnconfiguredTokenTrustRefusesEverything(t *testing.T) {
	t.Parallel()
	for name, trust := range map[string]principal.OperatorTrust{
		"nil verifier":       principal.TokenOperatorTrust{},
		"empty token":        principal.NewTokenTrust(""),
		"entirely nil trust": nil,
	} {
		trust := trust
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resolver := principal.NewResolver(
				fakeSessions{},
				principal.CapabilityVerifier{Authority: testAuthority{}},
				trust,
			)
			req := loopbackRequest()
			req.Header.Set(principal.HeaderOperatorToken, operatorToken)
			if _, err := resolver.Resolve(context.Background(), principal.RequestFacts{
				Request: req,
			}); !errors.Is(err, principal.ErrNotAuthorized) {
				t.Fatalf("err = %v, want ErrNotAuthorized", err)
			}
		})
	}
}

// TestOperatorCallerCarriesTheTokenFingerprint proves the audit identifier is derived from the
// presented credential and never carries the credential itself.
func TestOperatorCallerCarriesTheTokenFingerprint(t *testing.T) {
	t.Parallel()
	caller, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{
		Request: operatorRequest(),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := principal.OperatorTokenFingerprint(operatorToken)
	if caller.OperatorTokenFingerprint != want {
		t.Fatalf("fingerprint = %q, want %q", caller.OperatorTokenFingerprint, want)
	}
	if want == "" || len(want) != 8 {
		t.Fatalf("fingerprint %q is not the documented 8-hex shape", want)
	}
	if strings.Contains(want, operatorToken) || want == operatorToken {
		t.Fatal("the fingerprint carries the token")
	}
	if caller.Ref() != "operator:operator" {
		t.Fatalf("Ref() = %q, want operator:operator", caller.Ref())
	}
}

// TestOperatorTokenFingerprintIsStableAndTokenFree pins the two properties the audit trail needs:
// the same token always produces the same fingerprint, a different token produces a different one,
// and an absent token produces nothing at all.
func TestOperatorTokenFingerprintIsStableAndTokenFree(t *testing.T) {
	t.Parallel()
	first := principal.OperatorTokenFingerprint("credential-one")
	if first != principal.OperatorTokenFingerprint("credential-one") {
		t.Fatal("the same token produced two fingerprints")
	}
	if first == principal.OperatorTokenFingerprint("credential-two") {
		t.Fatal("two different tokens produced the same fingerprint")
	}
	if got := principal.OperatorTokenFingerprint(""); got != "" {
		t.Fatalf("an absent token produced fingerprint %q", got)
	}
	for _, value := range []string{"credential-one", "credential-two"} {
		if strings.Contains(first, value) {
			t.Fatal("the fingerprint contains the token")
		}
	}
}

// TestOperatorTokenIsMintedOnceReadableAndRotatable covers the file lifecycle: the first boot mints
// a 0600 secret, a restart reads the same one, and the value is a 64-character hex string.
func TestOperatorTokenIsMintedOnceReadableAndRotatable(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	minted, err := principal.LoadOrCreateOperatorToken(dataDir)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(minted) != 64 {
		t.Fatalf("minted token length = %d, want 64 hex characters", len(minted))
	}
	if _, err := hex.DecodeString(minted); err != nil {
		t.Fatalf("minted token is not hex: %v", err)
	}

	reloaded, err := principal.LoadOrCreateOperatorToken(dataDir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded != minted {
		t.Fatal("a second boot minted a different token, so a running desktop app would break")
	}

	info, err := os.Stat(filepath.Join(dataDir, principal.OperatorTokenFileName))
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	// The mode is only meaningful on a POSIX filesystem: Windows ignores the perm argument to
	// os.OpenFile, so asserting 0600 there would be asserting a property the platform does not
	// implement. The creation call still requests 0600, which is what the platform that enforces
	// it receives.
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("token file mode = %o, want 600", mode)
		}
	}

	// The trust built from that file accepts exactly that token.
	trust, err := principal.NewOperatorTokenTrust(dataDir)
	if err != nil {
		t.Fatalf("build trust: %v", err)
	}
	req := loopbackRequest()
	req.Header.Set(principal.HeaderOperatorToken, minted)
	if !trust.PermitsOperator(req) {
		t.Fatal("the trust rejected the token it minted")
	}

	// Rotating the file rotates the credential: the old token stops working.
	if err := os.WriteFile(filepath.Join(dataDir, principal.OperatorTokenFileName),
		[]byte(strings.Repeat("a", 64)+"\n"), 0o600); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	rotated, err := principal.NewOperatorTokenTrust(dataDir)
	if err != nil {
		t.Fatalf("rebuild trust: %v", err)
	}
	if rotated.PermitsOperator(req) {
		t.Fatal("the rotated credential still accepted the old token")
	}
}

// TestOperatorTokenRequiresADataDir refuses to invent a process-local secret: a secret the desktop
// app cannot read would lock every operator surface with no way to recover.
func TestOperatorTokenRequiresADataDir(t *testing.T) {
	t.Parallel()
	if _, err := principal.LoadOrCreateOperatorToken(""); err == nil {
		t.Fatal("an empty data dir minted a token")
	}
	if _, err := principal.NewOperatorTokenTrust(""); err == nil {
		t.Fatal("an empty data dir produced a trust")
	}
}

// TestAgentWithAValidOperatorTokenIsStillAnAgent is the "no silent downgrade" rule read the other
// way: presenting both credentials does not let an agent become the operator, and the resolver does
// not treat the presence of an operator token as a reason to drop the agent check.
func TestAgentWithAValidOperatorTokenIsStillAnAgent(t *testing.T) {
	t.Parallel()
	req := operatorRequest()
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	caller, err := resolverFixture().Resolve(context.Background(), principal.RequestFacts{
		Request: req, AgentSessionID: "session-1",
		WorkflowID: "workflow-1", AttemptID: "attempt-1", LeaseToken: "lease-token-1",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if caller.Kind != principal.KindAgent {
		t.Fatalf("kind = %q, want agent: a capability header decides the agent path", caller.Kind)
	}
	if caller.OperatorTokenFingerprint != "" {
		t.Fatal("an agent identity carried an operator fingerprint")
	}
	if caller.Ref() != "agent:session-1" {
		t.Fatalf("Ref() = %q, want agent:session-1", caller.Ref())
	}
}

// stubAuditRecorder records what a handler decided, so the record shape can be asserted without a
// database.
type stubAuditRecorder struct {
	mu      sync.Mutex
	records []principal.AuditRecord
	err     error
}

func (s *stubAuditRecorder) RecordSensitiveDecision(
	_ context.Context,
	record principal.AuditRecord,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.records = append(s.records, record)
	return nil
}

func (s *stubAuditRecorder) all() []principal.AuditRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]principal.AuditRecord(nil), s.records...)
}

// TestAuditRecordValidation pins what a record must carry. An audit row missing its actor, its
// operation, or its subject could not be read back, so it is rejected rather than stored.
func TestAuditRecordValidation(t *testing.T) {
	t.Parallel()
	valid := principal.AuditRecord{
		ActorKind: principal.KindOperator, ActorRef: "operator:operator",
		WorkflowID: "workflow-1", AttemptID: "attempt-1",
		Decision: "resolve_attempt", Outcome: "succeeded",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
	for name, mutate := range map[string]func(*principal.AuditRecord){
		"no actor kind": func(r *principal.AuditRecord) { r.ActorKind = "" },
		"unknown kind":  func(r *principal.AuditRecord) { r.ActorKind = "robot" },
		"no actor ref":  func(r *principal.AuditRecord) { r.ActorRef = "" },
		"no operation":  func(r *principal.AuditRecord) { r.Decision = "" },
		"no workflow":   func(r *principal.AuditRecord) { r.WorkflowID = "" },
		"no subject":    func(r *principal.AuditRecord) { r.AttemptID = "" },
	} {
		mutate := mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			record := valid
			mutate(&record)
			if err := record.Validate(); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

// TestStubAuditRecorderIsNotANoOp proves the injected interface is a real contract: the record a
// caller appends is readable afterwards, so a handler test can assert exactly one decision row.
func TestStubAuditRecorderIsNotANoOp(t *testing.T) {
	t.Parallel()
	recorder := &stubAuditRecorder{}
	record := principal.AuditRecord{
		ActorKind: principal.KindOperator, ActorRef: "operator:operator",
		WorkflowID: "workflow-1", RequestID: "req-1",
		Decision: "decide_task_request", Outcome: "approved",
		TokenFingerprint: principal.OperatorTokenFingerprint("credential"),
	}
	if err := recorder.RecordSensitiveDecision(context.Background(), record); err != nil {
		t.Fatalf("record: %v", err)
	}
	got := recorder.all()
	if len(got) != 1 || got[0].Decision != "decide_task_request" {
		t.Fatalf("recorded = %+v, want the appended decision", got)
	}
	if got[0].TokenFingerprint != principal.OperatorTokenFingerprint("credential") {
		t.Fatal("the fingerprint was not preserved")
	}
}
