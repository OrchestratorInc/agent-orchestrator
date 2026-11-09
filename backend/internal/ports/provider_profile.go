package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ProviderProfile 只携带宿主登记的非秘密路由；权限、命令、hook 和 MCP 不属于此契约。
type ProviderProfile struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Harness       domain.AgentHarness `json:"harness"`
	BaseURL       string              `json:"baseUrl"`
	Model         string              `json:"model"`
	Effort        string              `json:"effort,omitempty"`
	ContextWindow int64               `json:"contextWindow,omitempty"`
	CredentialEnv string              `json:"credentialEnv"`
}

// ProviderProfileChoice 是可公开目录，不暴露宿主路径、凭据值或凭据环境变量名。
type ProviderProfileChoice struct {
	Reference     string              `json:"reference"`
	Name          string              `json:"name"`
	Harness       domain.AgentHarness `json:"harness"`
	Model         string              `json:"model"`
	Effort        string              `json:"effort,omitempty"`
	ContextWindow int64               `json:"contextWindow,omitempty"`
}

// ProviderProfiles 从可信宿主登记表解析固定摘要；只有启动环境方法读取凭据。
type ProviderProfiles interface {
	List(context.Context) ([]ProviderProfileChoice, error)
	Resolve(context.Context, string, domain.AgentHarness, string, string) (ProviderProfile, error)
	Environment(context.Context, string, domain.AgentHarness, string, string) (map[string]string, error)
	Authentication(context.Context, string, domain.AgentHarness, string, string) (domain.AgentAuthenticationState, error)
}

// ProviderProfileKeyEnv 固定进程内密钥入口，避免登记的环境变量名变成任意进程环境覆盖。
const ProviderProfileKeyEnv = "AO_WORKFLOW_PROVIDER_KEY"

// ProviderProfileCredentialPrefix 保留给宿主登记凭据，所有 Agent 环境均清除此命名空间。
const ProviderProfileCredentialPrefix = "AO_WORKFLOW_CREDENTIAL_" // #nosec G101 -- 环境变量名称前缀，不包含凭据值。
