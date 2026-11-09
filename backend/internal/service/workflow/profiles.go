package workflow

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SetProviderProfiles 在发布服务前绑定唯一宿主目录，普通请求不能替换解析器。
func (s *Service) SetProviderProfiles(profiles ports.ProviderProfiles) { s.profiles = profiles }

// ListProviderProfiles 仅暴露固定选择；未装配时保留空目录及原生路径。
func (s *Service) ListProviderProfiles(ctx context.Context) ([]ports.ProviderProfileChoice, error) {
	if s.profiles == nil {
		return []ports.ProviderProfileChoice{}, nil
	}
	return s.profiles.List(ctx)
}
