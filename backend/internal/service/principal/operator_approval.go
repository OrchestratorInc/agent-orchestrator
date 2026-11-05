package principal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Operator 审批只在内存存活，绑定逐操作、目标和精确正文，必须通过 OS 人工验证后单次消费。
// 同用户可读取的 token 永远不能代替人工验证；旧 token helper 仅供兼容测试使用。

// HeaderOperatorApproval carries the approval id. It is a header, never a body field: a body
// can never assert an identity or a proof.
const HeaderOperatorApproval = "X-AO-Operator-Approval"

// Stable wire codes for approval refusals. They are distinct from COLLAB_NOT_AUTHORIZED on
// purpose: "you presented no proof at all" (the token path's refusal) and "the approval you
// presented cannot authorise this" are different facts, and a client that retries needs to tell
// them apart. Both are 403 and both fail closed.
const (
	// CodeApprovalRequired refuses an operation that demands a human gesture when the caller
	// proved no human credential at all — in practice: confirming an approval without the
	// desktop app's token.
	CodeApprovalRequired = "OPERATOR_APPROVAL_REQUIRED"
	// CodeApprovalInvalid refuses an approval that is missing, unknown, unconfirmed, expired,
	// already consumed, bound to another operation, or bound to another payload.
	CodeApprovalInvalid = "OPERATOR_APPROVAL_INVALID"
)

// Stable machine-readable reasons carried alongside CodeApprovalInvalid. They are audit-safe
// (they describe a state, never a secret) and stable so a client can branch without parsing
// prose.
const (
	ReasonApprovalMissing           = "approval_missing"
	ReasonApprovalUnbound           = "approval_context_unbound"
	ReasonApprovalUnknown           = "approval_unknown"
	ReasonApprovalExpired           = "approval_expired"
	ReasonApprovalUnconfirmed       = "approval_unconfirmed"
	ReasonApprovalConsumed          = "approval_consumed"
	ReasonApprovalOperationMismatch = "approval_operation_mismatch"
	ReasonApprovalPayloadMismatch   = "approval_payload_mismatch"
	ReasonApprovalCodeMismatch      = "approval_code_mismatch"
	ReasonApprovalLimit             = "approval_capacity_exhausted"
)

// ApprovalRefusal is the typed refusal for the approval proof.
//
// It is an error so the resolver can return it unchanged and the handler can map it onto a 403
// with the stable code and reason. It never carries a code, a digest, or a token.
type ApprovalRefusal struct {
	// Code is CodeApprovalRequired or CodeApprovalInvalid.
	Code string
	// Reason is one of the stable ReasonApproval* values.
	Reason string
	// Message is the human-facing sentence the API envelope shows.
	Message string
}

// Error implements error.
func (e *ApprovalRefusal) Error() string {
	if e == nil {
		return ""
	}
	reason := e.Reason
	if reason == "" {
		reason = "refused"
	}
	return fmt.Sprintf("operator approval %s: %s", e.Code, reason)
}

// ApprovalState is the lifecycle state of one approval.
type ApprovalState string

const (
	// ApprovalStatePending is created and waiting for a human gesture.
	ApprovalStatePending ApprovalState = "pending"
	// ApprovalStateConfirmed 表示 OS 已验证当前操作。
	ApprovalStateConfirmed ApprovalState = "confirmed"
	// ApprovalStateConsumed authorised exactly one request and is spent.
	ApprovalStateConsumed ApprovalState = "consumed"
	// ApprovalStateExpired passed its deadline before being consumed.
	ApprovalStateExpired ApprovalState = "expired"
)

// OperatorOperation is one operation an approval may authorise.
//
// Method and Path are matched EXACTLY against the request's method and its chi route pattern,
// so an approval for one route can never authorise a sibling route with the same shape.
type OperatorOperation struct {
	Method string
	Path   string
}

// OperatorOperations is the closed allowlist of approval-gated operations.
//
// A closed list at the source, not a wildcard: an approval that named an arbitrary path would
// be an approval for "anything", and the whole point is that the human approved a specific
// operation. Adding a route here is a security-relevant edit and belongs with the route.
var OperatorOperations = []OperatorOperation{
	{Method: http.MethodPost, Path: "/api/v1/workflows"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/recover-expired"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/activate"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/tasks/{taskId}/claim"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/tasks/{taskId}/retry"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/start"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/settle"},
	{Method: http.MethodPut, Path: "/api/v1/scheduler/policy"},
	{Method: http.MethodPut, Path: "/api/v1/scheduler/budget"},
	{Method: http.MethodPut, Path: "/api/v1/workflows/{workflowId}/budget"},
	{Method: http.MethodPut, Path: "/api/v1/workflows/{workflowId}/tasks/{taskId}/budget"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/pause"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/resume-scheduling"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/task-requests/{requestId}/materialize"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/bind"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/resolve"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/reconcile"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/resume"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/task-requests/{requestId}/decide"},
	{Method: http.MethodPost, Path: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/interventions"},
	{Method: http.MethodGet, Path: "/api/v1/workflows/capabilities"},
	{Method: http.MethodGet, Path: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/binding"},
	{Method: http.MethodGet, Path: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/observe"},
	{Method: http.MethodGet, Path: "/api/v1/workflows/{workflowId}/messages"},
	{Method: http.MethodGet, Path: "/api/v1/workflows/{workflowId}/task-requests"},
	{Method: http.MethodGet, Path: "/api/v1/workflows/{workflowId}/task-requests/{requestId}"},
}

// IsOperatorOperation 验证具体本地 RequestURI 是否属于审批闭集，包含 query 时也绑定其精确字节。
func IsOperatorOperation(method, path string) bool {
	_, ok := MatchOperatorOperation(method, path)
	return ok
}

// MatchOperatorOperation resolves a concrete request path to the allowlisted route pattern it
// belongs to.
//
// The allowlist is matched segment by segment, with "{name}" matching exactly one non-empty
// segment. Two consequences matter:
//
//   - an approval always names a REAL route (a made-up path matches nothing), so an approval
//     cannot be a standing grant for an arbitrary URL; and
//   - the pattern is only half of the binding. An approval also records the CONCRETE path, so
//     approving "bind attempt A" cannot authorise "bind attempt B" even though both belong to the
//     same pattern.
func MatchOperatorOperation(method, path string) (OperatorOperation, bool) {
	method = strings.ToUpper(strings.TrimSpace(method))
	path = canonicalRoutePath(path)
	if path == "" {
		return OperatorOperation{}, false
	}
	requestURL, err := url.ParseRequestURI(path)
	if err != nil {
		return OperatorOperation{}, false
	}
	for _, op := range OperatorOperations {
		if op.Method != method {
			continue
		}
		if op.Path == requestURL.Path || routePatternMatches(op.Path, requestURL.Path) {
			return op, true
		}
	}
	return OperatorOperation{}, false
}

// canonicalRoutePath 验证本地 RequestURI，保留 query 的完整字节，拒绝可改变路由解释的路径写法。
func canonicalRoutePath(path string) string {
	if len(path) > 1024 || strings.TrimSpace(path) != path || !strings.HasPrefix(path, "/") ||
		strings.HasPrefix(path, "//") || strings.ContainsAny(path, "#\r\n\t") {
		return ""
	}
	requestURL, err := url.ParseRequestURI(path)
	if err != nil || requestURL.IsAbs() || requestURL.Host != "" || requestURL.Fragment != "" ||
		strings.ContainsAny(requestURL.EscapedPath(), "%\\") {
		return ""
	}
	if _, err := url.ParseQuery(requestURL.RawQuery); err != nil {
		return ""
	}
	for _, segment := range strings.Split(strings.TrimPrefix(requestURL.Path, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ""
		}
	}
	return path
}

// routePatternMatches reports whether concrete matches pattern, where "{param}" stands for
// exactly one non-empty segment.
func routePatternMatches(pattern, concrete string) bool {
	patternParts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	concreteParts := strings.Split(strings.TrimPrefix(concrete, "/"), "/")
	if len(patternParts) != len(concreteParts) {
		return false
	}
	for i, part := range patternParts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			if concreteParts[i] == "" {
				return false
			}
			continue
		}
		if part != concreteParts[i] {
			return false
		}
	}
	return true
}

// Approval is one in-memory approval record.
//
// Code is the human-visible pairing value (device-code style): it binds the operation the
// terminal displayed to the operation the desktop app is about to approve. It is NOT a
// credential — 知道配对码仍必须通过本次 OS 验证。
//
// Exactly three things are bound, and all three are checked at consumption:
//
//	Method + Pattern  which allowlisted operation (a route pattern, never a wildcard);
//	Path              which concrete target ("/api/v1/workflows/w1/attempts/a1/bind"), so an
//	                  approval for one attempt cannot authorise another; and
//	PayloadDigest     the exact request body.
type Approval struct {
	ID     string
	Code   string
	Method string
	// Pattern is the allowlisted route pattern this approval belongs to.
	Pattern string
	// Path is the concrete request path the human approved.
	Path          string
	PayloadDigest string
	// Payload 只保留在内存，供 OS 验证前展示精确正文；不进入普通响应。
	Payload      string
	payloadBound bool
	CreatedAt    time.Time
	ExpiresAt    time.Time
	Confirmed    bool
	// ConfirmedByFingerprint 保留审计字段兼容；生产保存 OS 验证主体，不保存 token。
	ConfirmedByFingerprint string
	ConfirmedAt            *time.Time
	ConsumedAt             *time.Time
}

// State reports the approval's state at now. Expiry is derived, never stored, so a record can
// never disagree with the clock it is read with.
func (a Approval) State(now time.Time) ApprovalState {
	if a.ConsumedAt != nil {
		return ApprovalStateConsumed
	}
	if !a.ExpiresAt.After(now) {
		return ApprovalStateExpired
	}
	if a.Confirmed {
		return ApprovalStateConfirmed
	}
	return ApprovalStatePending
}

// DefaultApprovalTTL is how long an approval stays usable. It is short on purpose: an approval
// is a gesture about something happening NOW, and a long-lived approval is a standing grant.
const DefaultApprovalTTL = 120 * time.Second

// maxApprovals bounds the in-memory table. Creation is unauthenticated on loopback, so an
// unbounded map would be a local memory-exhaustion primitive. The cap is far above any human
// interaction rate (a person cannot confirm 128 approvals inside two minutes).
const maxApprovals = 128

// approvalRetention keeps a consumed or expired record a little past its expiry so a replay is
// refused with "consumed"/"expired" rather than "unknown". It bounds memory without letting the
// table grow: records older than this are dropped on the next mutation.
const approvalRetention = 10 * time.Minute

// ApprovalStoreOptions configures an ApprovalStore.
//
// Clock, NewID and NewCode exist so tests can drive expiry deterministically and assert the
// pairing flow; production values are the zero-value defaults.
type ApprovalStoreOptions struct {
	TTL     time.Duration
	Clock   func() time.Time
	NewID   func() string
	NewCode func() string
	// Max overrides maxApprovals; values <= 0 mean the default.
	Max int
}

// ApprovalStore is the in-memory, per-daemon-boot approval table.
//
// It is safe for concurrent use. Nothing in it is durable and nothing in it is logged: the
// records live for minutes in this process and die with it, which is what makes "a restart
// invalidates every outstanding approval" true by construction rather than by cleanup code.
type ApprovalStore struct {
	mu        sync.Mutex
	consentMu sync.Mutex
	ttl       time.Duration
	clock     func() time.Time
	newID     func() string
	newCode   func() string
	max       int
	entries   map[string]Approval
	order     []string
}

// NewApprovalStore builds an approval table.
func NewApprovalStore(opts ApprovalStoreOptions) *ApprovalStore {
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultApprovalTTL
	}
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	newID := opts.NewID
	if newID == nil {
		newID = randomApprovalID
	}
	newCode := opts.NewCode
	if newCode == nil {
		newCode = randomApprovalCode
	}
	max := opts.Max
	if max <= 0 {
		max = maxApprovals
	}
	return &ApprovalStore{
		ttl: ttl, clock: clock, newID: newID, newCode: newCode, max: max,
		entries: make(map[string]Approval),
	}
}

// TTL reports how long this store keeps an approval usable.
func (s *ApprovalStore) TTL() time.Duration {
	if s == nil {
		return 0
	}
	return s.ttl
}

// Create records a new pending approval for one concrete request: the operation (method plus
// concrete path, which must match an allowlisted route) and a digest of the exact body.
func (s *ApprovalStore) Create(method, path, payloadDigest string) (Approval, error) {
	if s == nil {
		return Approval{}, &ApprovalRefusal{
			Code: CodeApprovalRequired, Reason: ReasonApprovalMissing,
			Message: "This daemon cannot issue operator approvals",
		}
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	concrete := canonicalRoutePath(path)
	payloadDigest = strings.TrimSpace(payloadDigest)
	operation, ok := MatchOperatorOperation(method, concrete)
	if !ok {
		return Approval{}, fmt.Errorf("%w: %s %s", errNotAnOperatorOperation, method, path)
	}
	if payloadDigest == "" {
		return Approval{}, fmt.Errorf("%w: empty payload digest", errNotAnOperatorOperation)
	}
	now := s.clock().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	pending := s.liveCountLocked(now)
	if pending >= s.max {
		return Approval{}, &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalLimit,
			Message: "Too many outstanding approvals; confirm or wait for them to expire",
		}
	}
	approval := Approval{
		ID:            s.newID(),
		Code:          s.newCode(),
		Method:        method,
		Pattern:       operation.Path,
		Path:          concrete,
		PayloadDigest: payloadDigest,
		CreatedAt:     now,
		ExpiresAt:     now.Add(s.ttl),
	}
	if _, exists := s.entries[approval.ID]; exists {
		// A collision would overwrite a live approval. With 128 random bits this cannot
		// happen in practice; refusing is the only safe answer if it somehow does.
		return Approval{}, &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalUnknown,
			Message: "Could not allocate an approval identifier",
		}
	}
	s.entries[approval.ID] = approval
	s.order = append(s.order, approval.ID)
	return approval, nil
}

// errNotAnOperatorOperation marks a create request that names an operation no approval can
// authorise. It is unexported because the HTTP layer reports it as a 400 with its own code.
var errNotAnOperatorOperation = errors.New("not an approval-gated operator operation")

// ErrNotAnOperatorOperation is the exported form for callers that classify create failures.
var ErrNotAnOperatorOperation = errNotAnOperatorOperation

// Confirm 仅保留原有内存状态测试的兼容入口；HTTP 必须使用 VerifyAndConfirm。
func (s *ApprovalStore) Confirm(id, code, payloadDigest string) (Approval, error) {
	if s == nil {
		return Approval{}, &ApprovalRefusal{
			Code: CodeApprovalRequired, Reason: ReasonApprovalMissing,
			Message: "This daemon cannot confirm operator approvals",
		}
	}
	return s.confirm(id, code, payloadDigest, "")
}

// ConfirmWithFingerprint 仅供旧状态机与审计 fixture 设定主体；生产授权不调用该入口。
func (s *ApprovalStore) ConfirmWithFingerprint(
	id, code, payloadDigest, fingerprint string,
) (Approval, error) {
	return s.confirm(id, code, payloadDigest, strings.TrimSpace(fingerprint))
}

func (s *ApprovalStore) confirm(id, code, payloadDigest, fingerprint string) (Approval, error) {
	id = strings.TrimSpace(id)
	now := s.clock().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	current, found := s.entries[id]
	if !found {
		return Approval{}, &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalUnknown,
			Message: "No such approval",
		}
	}
	switch current.State(now) {
	case ApprovalStateExpired:
		return Approval{}, &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalExpired,
			Message: "That approval has expired; request a new one",
		}
	case ApprovalStateConsumed:
		return Approval{}, &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalConsumed,
			Message: "That approval was already used",
		}
	case ApprovalStateConfirmed:
		return Approval{}, &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalConsumed,
			Message: "That approval was already confirmed",
		}
	}
	if !constantTimeEqual(current.Code, strings.TrimSpace(code)) {
		return Approval{}, &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalCodeMismatch,
			Message: "The approval code does not match",
		}
	}
	if !constantTimeEqual(current.PayloadDigest, strings.TrimSpace(payloadDigest)) {
		return Approval{}, &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalPayloadMismatch,
			Message: "The payload digest does not match this approval",
		}
	}
	current.Confirmed = true
	current.ConfirmedByFingerprint = fingerprint
	at := now
	current.ConfirmedAt = &at
	s.entries[id] = current
	return current, nil
}

// Consume spends an approval for exactly one request and reports the fingerprint of the
// credential that confirmed it.
//
// Every check is a refusal rather than a fallback, and the order is the order in which a caller
// can fix the problem: unknown → expired → unconfirmed → consumed → wrong operation → wrong
// payload. A consumed approval is reported as consumed even if the operation also disagrees, so
// a replay sees the truth about the replay.
//
// The consumption is recorded BEFORE the handler runs, so a request that is refused later still
// burns the approval. That is the fail-closed choice: "single use" must mean one attempt, not
// "one success", or a caller could retry a refused operation until it succeeds.
func (s *ApprovalStore) Consume(id, method, path, payloadDigest string) (string, error) {
	if s == nil {
		return "", &ApprovalRefusal{
			Code: CodeApprovalRequired, Reason: ReasonApprovalMissing,
			Message: "This daemon cannot verify operator approvals",
		}
	}
	id = strings.TrimSpace(id)
	now := s.clock().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	current, found := s.entries[id]
	if !found {
		return "", &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalUnknown,
			Message: "No such approval",
		}
	}
	switch current.State(now) {
	case ApprovalStateExpired:
		return "", &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalExpired,
			Message: "That approval has expired; request a new one",
		}
	case ApprovalStateConsumed:
		return "", &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalConsumed,
			Message: "That approval was already used",
		}
	case ApprovalStatePending:
		return "", &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalUnconfirmed,
			Message: "That approval has not passed native user verification",
		}
	}
	if !constantTimeEqual(current.Method, strings.ToUpper(strings.TrimSpace(method))) ||
		!constantTimeEqual(current.Path, canonicalRoutePath(path)) {
		return "", &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalOperationMismatch,
			Message: "That approval is for a different operation",
		}
	}
	if !constantTimeEqual(current.PayloadDigest, strings.TrimSpace(payloadDigest)) {
		return "", &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalPayloadMismatch,
			Message: "That approval is for a different payload",
		}
	}
	at := now
	current.ConsumedAt = &at
	s.entries[id] = current
	return current.ConfirmedByFingerprint, nil
}

// Get reads one approval without changing it. It exists for status polling by the process that
// requested the approval: the id is 128 random bits and the record carries no authority.
func (s *ApprovalStore) Get(id string) (Approval, bool) {
	if s == nil {
		return Approval{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, found := s.entries[strings.TrimSpace(id)]
	if !found {
		return Approval{}, false
	}
	// State is derived from the clock, so the caller sees "expired" without the store having to
	// rewrite the record.
	approval.CreatedAt = approval.CreatedAt.UTC()
	approval.ExpiresAt = approval.ExpiresAt.UTC()
	return approval, true
}

// State reports one approval's state at the store's clock, or false when it is unknown.
func (s *ApprovalStore) State(id string) (ApprovalState, Approval, bool) {
	approval, found := s.Get(id)
	if !found {
		return "", Approval{}, false
	}
	return approval.State(s.now()), approval, true
}

// List returns every approval that is still actionable (pending or confirmed, unexpired),
// oldest first, so the desktop UI can render exactly what a human may approve.
func (s *ApprovalStore) List() []Approval {
	if s == nil {
		return nil
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Approval, 0, len(s.order))
	for _, id := range s.order {
		approval, found := s.entries[id]
		if !found {
			continue
		}
		state := approval.State(now)
		if state != ApprovalStatePending && state != ApprovalStateConfirmed {
			continue
		}
		out = append(out, approval)
	}
	return out
}

func (s *ApprovalStore) now() time.Time { return s.clock().UTC() }

// pruneLocked drops records that are past their retention window. Bounds memory without making
// the "expired"/"consumed" refusals unavailable to a caller that retries soon after.
func (s *ApprovalStore) pruneLocked(now time.Time) {
	if len(s.order) == 0 {
		return
	}
	kept := s.order[:0]
	for _, id := range s.order {
		approval, found := s.entries[id]
		if !found {
			continue
		}
		if now.Sub(approval.ExpiresAt) > approvalRetention {
			delete(s.entries, id)
			continue
		}
		kept = append(kept, id)
	}
	s.order = kept
}

// liveCountLocked counts the approvals that could still be confirmed or consumed.
func (s *ApprovalStore) liveCountLocked(now time.Time) int {
	count := 0
	for _, approval := range s.entries {
		if state := approval.State(now); state == ApprovalStatePending || state == ApprovalStateConfirmed {
			count++
		}
	}
	return count
}

// randomApprovalID mints a 128-bit identifier. It is not a credential (the approval only works
// once confirmed by native user verification) but it must still be unguessable so a local process cannot
// enumerate other processes' pending approvals.
func randomApprovalID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand failing is unrecoverable for a credential-shaped value. Returning an
		// empty id makes the create fail validation rather than minting a predictable one.
		return ""
	}
	return hex.EncodeToString(raw)
}

// AsApprovalRefusal extracts the typed refusal from an error chain.
func AsApprovalRefusal(err error) (*ApprovalRefusal, bool) {
	var refusal *ApprovalRefusal
	if errors.As(err, &refusal) {
		return refusal, true
	}
	return nil, false
}

// ApprovalCodeMatches reports whether code is the approval's pairing code.
func ApprovalCodeMatches(approval Approval, code string) bool {
	code = strings.TrimSpace(code)
	if code == "" || approval.Code == "" {
		return false
	}
	return constantTimeEqual(approval.Code, code)
}

// Now reports the store's current time, so a caller rendering derived state uses the same clock
// the store checks against.
func (s *ApprovalStore) Now() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.now()
}

// ResetApprovals drops every record. It exists for tests that must simulate a daemon restart
// within one process; production never calls it, because a restart builds a new store.
func (s *ApprovalStore) ResetApprovals() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]Approval)
	s.order = nil
}

// randomApprovalCode mints the 8-hex pairing code the human compares between the terminal and
// the desktop app.
func randomApprovalCode() string {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	return strings.ToUpper(hex.EncodeToString(raw))
}

// constantTimeEqual compares two short strings without an early exit.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ApprovalDigestPrefix names the digest algorithm in PayloadDigest so a mismatch caused by a
// different algorithm is diagnosable rather than mysterious.
const ApprovalDigestPrefix = "sha256:"

// PayloadDigest returns the approval-binding digest of a request body: "sha256:" followed by
// the lowercase hex SHA-256 of the EXACT bytes.
//
// Exact bytes, not canonical JSON: the client digests what it is about to send, and the server
// digests what it received. Anything that re-serialises in between (a proxy, a middleware that
// rewrites the body) breaks the binding — which is the point: the approval is for those bytes.
func PayloadDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return ApprovalDigestPrefix + hex.EncodeToString(sum[:])
}

// approvalBinding is the transport fact set by the HTTP seam for the approval proof: the
// operation the request is executing (method + route pattern) and the digest of its body.
//
// It is put on the request context by the handler that owns the body, because only the handler
// can digest the bytes it is about to decode. A trust asked to prove an approval with no
// binding refuses: a missing binding is not permission, and it cannot be supplied by a client.
type approvalBinding struct {
	method string
	path   string
	digest string
}

type approvalBindingKey struct{}

// WithApprovalBinding records the operation and payload digest of the request being handled.
//
// The handler MUST call this before deriving the caller: an approval presented without a
// binding is refused (fail closed), so forgetting the call disables the approval path rather
// than weakening it.
func WithApprovalBinding(req *http.Request, method, path string, digest string) *http.Request {
	binding := approvalBinding{
		method: strings.ToUpper(strings.TrimSpace(method)),
		path:   strings.TrimSpace(path),
		digest: strings.TrimSpace(digest),
	}
	return req.WithContext(context.WithValue(req.Context(), approvalBindingKey{}, binding))
}

// ApprovalBindingFrom returns the approval binding recorded on the request, if any.
func ApprovalBindingFrom(req *http.Request) (approvalBinding, bool) {
	if req == nil {
		return approvalBinding{}, false
	}
	binding, ok := req.Context().Value(approvalBindingKey{}).(approvalBinding)
	if !ok || binding.path == "" || binding.digest == "" {
		return approvalBinding{}, false
	}
	return binding, true
}

// ApprovalProver is implemented by an operator trust that also accepts a single-use approval.
//
// It is separate from OperatorTrust because proving an approval CONSUMES it, returns the
// confirming credential's fingerprint, and can fail for reasons the caller must be told. A
// boolean PermitsOperator cannot express any of that.
type ApprovalProver interface {
	// ProveApproval checks, then spends, the approval named by HeaderOperatorApproval. It
	// 返回 OS 确认主体的非秘密标识。
	ProveApproval(req *http.Request) (fingerprint string, err error)
}

// ApprovalOperatorTrust 是生产身份边界，只接受绑定当前请求的一次性审批。
//
// 它不持有任何 token 证明：同用户进程可以复制任何本地文件/内存形状的值，所以 token 不能
// 证明"有人在场"。唯一被接受的证明是本次启动内由 OS 原生验证确认过的审批。
type ApprovalOperatorTrust struct {
	Approvals *ApprovalStore
}

var (
	_ OperatorTrust  = ApprovalOperatorTrust{}
	_ ApprovalProver = ApprovalOperatorTrust{}
)

// PermitsOperator 永远不授予无操作上下文的权限，避免 token 跳过审批。
func (t ApprovalOperatorTrust) PermitsOperator(_ *http.Request) bool {
	return false
}

// ProveApproval validates and spends the approval the request presents.
//
// Refusals, in order: no approval header (nothing to prove), no request binding, unknown id,
// expired, unconfirmed, consumed, wrong operation, wrong payload.
func (t ApprovalOperatorTrust) ProveApproval(req *http.Request) (string, error) {
	if req == nil {
		return "", &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalMissing,
			Message: "The caller presented no operator approval",
		}
	}
	id := strings.TrimSpace(req.Header.Get(HeaderOperatorApproval))
	if id == "" {
		return "", &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalMissing,
			Message: "The caller presented no operator approval",
		}
	}
	if t.Approvals == nil {
		return "", &ApprovalRefusal{
			Code: CodeApprovalRequired, Reason: ReasonApprovalMissing,
			Message: "This daemon cannot verify operator approvals",
		}
	}
	// The approval is a loopback-listener proof for the same reason the token is: it is minted
	// and confirmed on this machine, so accepting it over another transport would turn a local
	// gesture into a network credential. Both proofs keep the transport requirement.
	if !LoopbackPeer(req) {
		return "", &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalMissing,
			Message: "The caller presented no operator approval",
		}
	}
	binding, ok := ApprovalBindingFrom(req)
	if !ok {
		return "", &ApprovalRefusal{
			Code: CodeApprovalInvalid, Reason: ReasonApprovalUnbound,
			Message: "This route cannot be authorised by an approval",
		}
	}
	return t.Approvals.Consume(id, binding.method, binding.path, binding.digest)
}
