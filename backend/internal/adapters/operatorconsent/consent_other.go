//go:build !windows

// Package operatorconsent 在不支持的 OS 上明确拒绝人工验证。
package operatorconsent

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// Verifier 不为尚无原生实现的平台提供替代授权。
type Verifier struct{}

func New() principal.ConsentVerifier { return Verifier{} }

// Verify 缺少原生认证时始终失败闭锁。
func (Verifier) Verify(context.Context, principal.Approval) error {
	return principal.ErrConsentUnavailable
}
