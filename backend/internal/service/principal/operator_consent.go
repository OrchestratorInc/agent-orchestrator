package principal

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const (
	// MaxApprovalPayloadBytes 限制每份内存审批的正文，防止本地匿名创建耗尽内存。
	MaxApprovalPayloadBytes = 64 << 10
	// NativeConsentIdentity 是 OS 验证成功后的审计主体，不是凭证。
	NativeConsentIdentity = "windows-hello"
)

// ConsentVerifier 是 daemon 到操作系统人工验证服务的边界；nil error 仅代表本次 OS 验证成功。
type ConsentVerifier interface {
	Verify(ctx context.Context, approval Approval) error
}

// ErrConsentUnavailable 表示操作系统原生人工验证不可用。
var (
	ErrConsentUnavailable = errors.New("native operator verification is unavailable")
	ErrConsentDenied      = errors.New("native operator verification was not approved")
)

// CreateWithPayload 绑定收到的精确正文，不能用调用方提供的哈希代替人工可读内容。
func (s *ApprovalStore) CreateWithPayload(method, path, digest, payload string) (Approval, error) {
	if len(payload) > MaxApprovalPayloadBytes || !utf8.ValidString(payload) || (payload != "" && !json.Valid([]byte(payload))) {
		return Approval{}, errors.New("operator payload must be JSON within 64 KiB, or empty")
	}
	if PayloadDigest([]byte(payload)) != digest {
		return Approval{}, &ApprovalRefusal{Code: CodeApprovalInvalid, Reason: ReasonApprovalPayloadMismatch,
			Message: "The exact payload does not match its digest"}
	}
	approval, err := s.Create(method, path, digest)
	if err != nil {
		return Approval{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	approval.Payload = payload
	approval.payloadBound = true
	s.entries[approval.ID] = approval
	return approval, nil
}

// VerifyAndConfirm 在锁外调用 OS；前后都验证绑定和状态，并拒绝并发弹窗。
func (s *ApprovalStore) VerifyAndConfirm(ctx context.Context, id, code, digest string, verifier ConsentVerifier) (Approval, error) {
	if verifier == nil || s == nil {
		return Approval{}, &ApprovalRefusal{Code: CodeApprovalRequired, Reason: "consent_unavailable",
			Message: "Native operator verification is unavailable on this daemon"}
	}
	if !s.consentMu.TryLock() {
		return Approval{}, &ApprovalRefusal{Code: CodeApprovalRequired, Reason: "consent_busy",
			Message: "Another native operator verification is in progress"}
	}
	defer s.consentMu.Unlock()
	s.mu.Lock()
	approval, err := s.pendingLocked(id, code, digest)
	s.mu.Unlock()
	if err != nil {
		return Approval{}, err
	}
	if !approval.payloadBound || PayloadDigest([]byte(approval.Payload)) != approval.PayloadDigest {
		return Approval{}, &ApprovalRefusal{Code: CodeApprovalInvalid, Reason: ReasonApprovalPayloadMismatch,
			Message: "This approval has no verified payload to display"}
	}
	// 使用 store 时钟的剩余时长，测试时钟与系统时钟不同也不会产生额外授权窗口。
	remaining := approval.ExpiresAt.Sub(s.Now())
	verificationCtx, cancel := context.WithTimeout(ctx, min(remaining, DefaultApprovalTTL))
	defer cancel()
	if err := verifier.Verify(verificationCtx, approval); err != nil {
		reason := "consent_denied"
		if errors.Is(err, ErrConsentUnavailable) {
			reason = "consent_unavailable"
		}
		return Approval{}, &ApprovalRefusal{Code: CodeApprovalRequired, Reason: reason,
			Message: "Native operator verification did not approve this operation"}
	}
	if verificationCtx.Err() != nil {
		return Approval{}, &ApprovalRefusal{Code: CodeApprovalRequired, Reason: "consent_cancelled",
			Message: "Native operator verification was cancelled or timed out"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.pendingLocked(id, code, digest)
	if err != nil {
		return Approval{}, err
	}
	if verificationCtx.Err() != nil {
		return Approval{}, &ApprovalRefusal{Code: CodeApprovalRequired, Reason: "consent_cancelled",
			Message: "Native operator verification was cancelled or timed out"}
	}
	current.Confirmed = true
	current.ConfirmedByFingerprint = NativeConsentIdentity
	confirmedAt := s.now()
	current.ConfirmedAt = &confirmedAt
	s.entries[id] = current
	return current, nil
}

// pendingLocked 必须持有条目锁；人工验证前后复用相同状态与绑定校验。
func (s *ApprovalStore) pendingLocked(id, code, digest string) (Approval, error) {
	current, found := s.entries[id]
	if !found {
		return Approval{}, &ApprovalRefusal{Code: CodeApprovalInvalid, Reason: ReasonApprovalUnknown, Message: "No such approval"}
	}
	switch current.State(s.now()) {
	case ApprovalStateExpired:
		return Approval{}, &ApprovalRefusal{Code: CodeApprovalInvalid, Reason: ReasonApprovalExpired, Message: "That approval has expired"}
	case ApprovalStateConsumed, ApprovalStateConfirmed:
		return Approval{}, &ApprovalRefusal{Code: CodeApprovalInvalid, Reason: ReasonApprovalConsumed, Message: "That approval was already confirmed or used"}
	}
	if !ApprovalCodeMatches(current, code) {
		return Approval{}, &ApprovalRefusal{Code: CodeApprovalInvalid, Reason: ReasonApprovalCodeMismatch, Message: "The approval code does not match"}
	}
	if !constantTimeEqual(current.PayloadDigest, digest) {
		return Approval{}, &ApprovalRefusal{Code: CodeApprovalInvalid, Reason: ReasonApprovalPayloadMismatch, Message: "The payload digest does not match this approval"}
	}
	return current, nil
}
