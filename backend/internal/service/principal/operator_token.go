package principal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// 旧 Operator token helper 仅供状态迁移与历史 fixture 兼容。
// 同用户进程可读取其文件，因此正式 daemon 不创建、读取、采用或用它授权。
// 正式 Operator 权限只能来自 operator_consent.go 的逐操作 OS 验证和一次性审批。

// HeaderOperatorToken carries the operator proof. It is a header, never a body field, so a
// request body can never assert an identity.
const HeaderOperatorToken = "X-AO-Operator-Token" // #nosec G101 -- 这是旧兼容协议 Header 名，不是 token 值，正式授权不读取它。

// OperatorTokenFileName is the file, under the daemon's data dir, that holds the operator
// secret. It is 0600 and never served over HTTP.
const OperatorTokenFileName = "operator.token"

// operatorTokenBytes is the minted secret's entropy. 32 bytes hex-encoded is 64 characters,
// the same shape as every other AO capability.
const operatorTokenBytes = 32

// ErrOperatorTokenUnavailable reports that no operator credential could be established.
// Callers must treat it as "operator requests are refused", never as "skip the check".
var ErrOperatorTokenUnavailable = errors.New("operator token is unavailable")

// TokenVerifier reports whether a presented token is this daemon's operator credential.
// A nil verifier is a refusal: the zero TokenOperatorTrust permits nothing.
type TokenVerifier func(token string) bool

// TokenOperatorTrust is the production operator proof: a loopback peer AND a token the
// daemon accepts.
//
// The transport requirement is kept, not relaxed. The token is minted for the loopback
// listener, so accepting it from a LAN peer would turn an unauthenticated-by-design
// local credential into a network-visible one. Both facts must hold:
//
//  1. the peer address is loopback, and
//  2. HeaderOperatorToken carries a token the verifier accepts.
type TokenOperatorTrust struct {
	// Verifier decides whether a presented token is the operator credential. Nil refuses
	// every request, so an unwired trust fails closed.
	Verifier TokenVerifier
}

// PermitsOperator reports whether req proves operator identity.
func (t TokenOperatorTrust) PermitsOperator(req *http.Request) bool {
	if req == nil || !LoopbackPeer(req) {
		return false
	}
	if t.Verifier == nil {
		return false
	}
	token := strings.TrimSpace(req.Header.Get(HeaderOperatorToken))
	if token == "" {
		return false
	}
	return t.Verifier(token)
}

// NewOperatorTokenTrust loads (or mints) this data dir's operator secret and returns the
// trust that checks it.
//
// NO PRODUCTION PATH CALLS THIS EITHER. The daemon no longer reads or mints a token file, and
// this constructor remains only so the file-lifecycle tests can exercise the primitive they
// describe. Minting happens only when the file is absent, and the write is O_EXCL so two
// booting processes cannot diverge: the loser adopts the winner's file.
//
// An empty data dir is an error rather than a fallback to a process-local secret, because a
// secret no other process can read would lock every operator surface with no recovery path.
func NewOperatorTokenTrust(dataDir string) (TokenOperatorTrust, error) {
	token, err := LoadOrCreateOperatorToken(dataDir)
	if err != nil {
		return TokenOperatorTrust{}, err
	}
	return NewTokenTrust(token), nil
}

// NewTokenTrust builds a TokenOperatorTrust that accepts exactly token, comparing in constant
// time.
//
// It is the constructor for callers that already hold the secret: a TEST FIXTURE only. No
// production path mints or reads an operator token any more — see
// daemon/workflow_auth_wiring.go, which builds an ApprovalOperatorTrust over a per-boot
// ApprovalStore and a native consent verifier and never touches a token file.
func NewTokenTrust(token string) TokenOperatorTrust {
	token = strings.TrimSpace(token)
	if token == "" {
		return TokenOperatorTrust{}
	}
	return TokenOperatorTrust{Verifier: func(presented string) bool {
		return subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1
	}}
}

// LoadOrCreateOperatorToken returns the operator secret stored at
// <dataDir>/operator.token, minting and persisting one when the file does not exist.
//
// NO PRODUCTION PATH CALLS THIS. It survives because it is the primitive the file-lifecycle
// tests exercise, and because it states the residual risk plainly: on Windows a 0600 file is
// advisory, so any same-user process can read what this mints. That is exactly why the daemon
// no longer mints one — an operator proof that a sibling process can copy does not prove a human.
//
// It never logs the value and never returns it anywhere but to its caller.
func LoadOrCreateOperatorToken(dataDir string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", fmt.Errorf("%w: no data dir", ErrOperatorTokenUnavailable)
	}
	path := filepath.Join(dataDir, OperatorTokenFileName)
	if token, ok := readOperatorToken(path); ok {
		return token, nil
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return "", fmt.Errorf("%w: create data dir: %w", ErrOperatorTokenUnavailable, err)
	}
	raw := make([]byte, operatorTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("%w: generate token: %w", ErrOperatorTokenUnavailable, err)
	}
	token := hex.EncodeToString(raw)
	// O_EXCL is the whole concurrency story: a second daemon booting at the same moment
	// loses the race and adopts the file the winner wrote, so there is exactly one secret
	// per data dir rather than one per process.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			if existing, ok := readOperatorToken(path); ok {
				return existing, nil
			}
			return "", fmt.Errorf("%w: %s exists but is unreadable", ErrOperatorTokenUnavailable, path)
		}
		return "", fmt.Errorf("%w: write token: %w", ErrOperatorTokenUnavailable, err)
	}
	if _, err := file.WriteString(token + "\n"); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("%w: write token: %w", ErrOperatorTokenUnavailable, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("%w: close token file: %w", ErrOperatorTokenUnavailable, err)
	}
	return token, nil
}

// readOperatorToken reads and validates a stored token. A file whose contents are not a
// well-formed 64-hex secret is treated as absent rather than trusted: a truncated or
// hand-edited file must not become a weaker credential nobody minted.
func readOperatorToken(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(string(raw))
	if len(token) != operatorTokenBytes*2 {
		return "", false
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", false
	}
	return token, true
}

// OperatorTokenFingerprint is the audit-safe identifier of a presented token: the first 8
// hex characters of its SHA-256. The token itself never leaves this package.
func OperatorTokenFingerprint(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:8]
}

// LoopbackPeer reports whether req's peer address is a loopback IP.
func LoopbackPeer(req *http.Request) bool {
	if req == nil {
		return false
	}
	host := req.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// AuditRecord is one append-only row describing a sensitive decision.
//
// It carries identifiers and a credential FINGERPRINT, never a token, never a message body,
// and never a prompt: an audit trail that leaks what it audits is a second disclosure.
type AuditRecord struct {
	// ActorKind is the derived caller kind (operator or agent).
	ActorKind Kind
	// ActorRef is the derived reference, for example "operator:operator". It is never a
	// body-supplied value.
	ActorRef   string
	WorkflowID domain.WorkflowID
	// AttemptID or RequestID names the subject of the decision; exactly one is set for the
	// surfaces this package's callers record.
	AttemptID domain.WorkflowAttemptID
	RequestID string
	// Decision is the stable operation name ("resolve_attempt", "decide_task_request", ...).
	Decision string
	// Outcome is the recorded result ("succeeded", "failed", "approved", "rejected", ...).
	Outcome string
	// DaemonRequestID is the HTTP request id when the request context carries one.
	DaemonRequestID string
	// TokenFingerprint identifies the credential used, never the credential.
	TokenFingerprint string
	At               time.Time
}

// Validate rejects an audit row that could not be read back meaningfully.
func (r AuditRecord) Validate() error {
	if r.ActorKind != KindOperator && r.ActorKind != KindAgent {
		return fmt.Errorf("audit record: invalid actor kind %q", r.ActorKind)
	}
	if strings.TrimSpace(r.ActorRef) == "" ||
		strings.TrimSpace(r.Decision) == "" ||
		strings.TrimSpace(string(r.WorkflowID)) == "" ||
		(r.AttemptID == "" && strings.TrimSpace(r.RequestID) == "") {
		return fmt.Errorf("audit record: incomplete")
	}
	return nil
}

// AuditRecorder appends sensitive decisions to an audit trail.
//
// The contract is deliberately narrow and deliberately NOT satisfiable by a no-op: a
// recorder must persist (or otherwise make durable) every record it accepts, must be
// append-only, and must return an error rather than silently dropping one. A caller with no
// recorder records nothing and says so — there is no built-in implementation that pretends
// to record.
//
// Durable persistence is blocked on the storage owner: the SQLite store exposes no generic
// audit table today, and this task must not add a migration. The interface exists so the
// handlers, their refusal paths, and their idempotency rules are testable now, and so
// wiring a store-backed recorder later is a one-line change at the call site.
type AuditRecorder interface {
	RecordSensitiveDecision(ctx context.Context, record AuditRecord) error
}

var _ OperatorTrust = TokenOperatorTrust{}
