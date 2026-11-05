package sessionmanager

import (
	"context"

	claudeagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// nativeOwnershipClaudeAgent 保留真实 Claude 启动与历史适配器，只隔离外部认证 I/O。
// Runtime 和 Chat Driver 同样由历史归属用例控制，因此无需用户的 Provider 凭证。
type nativeOwnershipClaudeAgent struct {
	*claudeagent.Plugin
}

// ValidateLaunchAuth 模拟已授权的 Provider；历史归属断言仍经过真实 Manager 与存储链路。
func (nativeOwnershipClaudeAgent) ValidateLaunchAuth(context.Context, string, map[string]string) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusAuthorized, nil
}
