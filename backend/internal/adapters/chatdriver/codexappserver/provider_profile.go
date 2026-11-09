package codexappserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SetProviderProfiles 在 daemon 启动前注入可信解析器；Driver 不读取原生配置或接受任意路由。
func (d *Driver) SetProviderProfiles(profiles ports.ProviderProfiles) { d.profiles = profiles }

// profileLaunch 保留权限边界，只注入明确的供应商配置及启动瞬间的密钥环境。
func (d *Driver) profileLaunch(ctx context.Context, ref, model, effort string, env map[string]string, prepare func(context.Context) (map[string]string, error)) (map[string]any, func(context.Context) (map[string]string, error), string, error) {
	if ref == "" {
		return nil, prepare, "", nil
	}
	if d.profiles == nil {
		return nil, nil, "", fmt.Errorf("provider profiles unavailable")
	}
	p, err := d.profiles.Resolve(ctx, ref, domain.HarnessCodex, model, effort)
	if err != nil {
		return nil, nil, "", err
	}
	verified, err := d.profiles.Environment(ctx, ref, domain.HarnessCodex, model, effort)
	if err != nil {
		return nil, nil, "", err
	}
	identity := sha256.Sum256([]byte(ref + "\x00" + verified[ports.ProviderProfileKeyEnv]))
	_, digest, ok := strings.Cut(ref, "@")
	if !ok || len(digest) != 64 {
		return nil, nil, "", fmt.Errorf("provider reference is invalid")
	}
	providerID := "ao_workflow_" + digest
	config := map[string]any{
		"model_provider": providerID,
		"model_providers": map[string]any{providerID: map[string]any{
			"name": p.Name, "base_url": p.BaseURL, "wire_api": "responses",
			"env_key": ports.ProviderProfileKeyEnv, "requires_openai_auth": false,
		}},
	}
	if p.ContextWindow > 0 {
		config["model_context_window"] = p.ContextWindow
	}
	launch := func(launchCtx context.Context) (map[string]string, error) {
		base := env
		if prepare != nil {
			var err error
			base, err = prepare(launchCtx)
			if err != nil {
				return nil, err
			}
		}
		secret, err := d.profiles.Environment(launchCtx, ref, domain.HarnessCodex, model, effort)
		if err != nil {
			return nil, err
		}
		if sha256.Sum256([]byte(ref+"\x00"+secret[ports.ProviderProfileKeyEnv])) != identity {
			return nil, fmt.Errorf("provider credential changed before launch")
		}
		result := make(map[string]string, len(base)+1)
		for key, value := range base {
			result[key] = value
		}
		result[ports.ProviderProfileKeyEnv] = secret[ports.ProviderProfileKeyEnv]
		return result, nil
	}
	return config, launch, hex.EncodeToString(identity[:]), nil
}

// applyProfileConfig 合并经过白名单验证的路由，权限/沙箱/MCP 仍由原有启动逻辑独立决定。
func applyProfileConfig(params, profile map[string]any) {
	if len(profile) == 0 {
		return
	}
	config, ok := params["config"].(map[string]any)
	if !ok {
		config = map[string]any{}
	}
	for key, value := range profile {
		config[key] = value
	}
	params["config"] = config
	params["modelProvider"] = profile["model_provider"]
}
