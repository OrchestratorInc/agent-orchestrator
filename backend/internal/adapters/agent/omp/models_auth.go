package omp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// ompTokenRunner 只读取凭据命令的 stdout，避免把 stderr 警告拼进密钥；原始输出不进入诊断。
var ompTokenRunner = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
	return aoprocess.CommandContext(ctx, binary, args...).Output()
}

// ompAuthRoute 只描述认证目的地；自定义请求头和禁用 Bearer 认证的路由不能套用目录探测。
type ompAuthRoute struct {
	BaseURL    string            `yaml:"baseUrl"`
	Headers    map[string]string `yaml:"headers"`
	AuthHeader *bool             `yaml:"authHeader"`
}

// ompModelsAuthStatus 按原生默认模型验证 models.yml 路由，凭据解析交给 OMP，避免自行执行配置中的命令。
func ompModelsAuthStatus(ctx context.Context, binary, dir string) (ports.AgentAuthStatus, bool, error) {
	path := filepath.Join(dir, "models.yml")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	var config struct {
		Providers map[string]struct {
			ompAuthRoute   `yaml:",inline"`
			ModelOverrides map[string]ompAuthRoute `yaml:"modelOverrides"`
			Models         []struct {
				ID           string `yaml:"id"`
				ompAuthRoute `yaml:",inline"`
			} `yaml:"models"`
		} `yaml:"providers"`
	}
	if yaml.Unmarshal(raw, &config) != nil {
		authprobe.RecordFileDiagnostic(ctx, path, "credential_file_invalid")
		return ports.AgentAuthStatusUnknown, false, errors.New("invalid OMP model configuration")
	}
	if len(config.Providers) == 0 {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	var settings struct {
		ModelRoles struct {
			Default string `yaml:"default"`
		} `yaml:"modelRoles"`
	}
	raw, err = os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil || yaml.Unmarshal(raw, &settings) != nil {
		ports.RecordAuthDiagnostic(ctx, domain.NewAgentAuthDiagnostic("model_selection", "model_selection_unresolved"))
		return ports.AgentAuthStatusConfigured, true, nil
	}
	provider, model, found := strings.Cut(settings.ModelRoles.Default, "/")
	selected, declared := config.Providers[provider]
	if !found || model == "" {
		ports.RecordAuthDiagnostic(ctx, domain.NewAgentAuthDiagnostic("model_selection", "model_selection_unresolved"))
		return ports.AgentAuthStatusConfigured, true, nil
	}
	if !declared {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	if colon := strings.LastIndex(model, ":"); colon >= 0 {
		switch model[colon+1:] {
		case "off", "minimal", "low", "medium", "high", "xhigh", "max":
			model = model[:colon]
		}
	}
	route := selected.ompAuthRoute
	overrides := []ompAuthRoute{selected.ModelOverrides[model]}
	for _, entry := range selected.Models {
		if entry.ID == model {
			overrides = append(overrides, entry.ompAuthRoute)
			break
		}
	}
	for _, override := range overrides {
		if override.BaseURL != "" {
			route.BaseURL = override.BaseURL
		}
		if len(override.Headers) > 0 {
			route.Headers = override.Headers
		}
		if override.AuthHeader != nil {
			route.AuthHeader = override.AuthHeader
		}
	}
	identity := authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAPIKey)
	identity.Provider = provider
	ports.RecordAuthEvidence(ctx, identity)
	if len(route.Headers) > 0 || route.AuthHeader != nil && !*route.AuthHeader {
		ports.RecordAuthDiagnostic(ctx, domain.NewAgentAuthDiagnostic("provider_validation", "provider_unsupported"))
		return ports.AgentAuthStatusConfigured, true, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	started := time.Now()
	token, err := ompTokenRunner(probeCtx, binary, "token", provider)
	key := strings.TrimSpace(string(token))
	if err != nil || key == "" || strings.ContainsAny(key, "\r\n") {
		ports.RecordAuthDiagnostic(ctx, authprobe.CommandStep(binary, []string{"token", provider}, ports.AgentAuthStatusUnknown, err, probeCtx.Err(), started))
		return ports.AgentAuthStatusConfigured, true, nil
	}
	status, step := authprobe.VerifyModelAccess(probeCtx, authprobe.Connection{
		Provider: provider, BaseURL: route.BaseURL, Model: model, APIKey: key,
	}, nil)
	ports.RecordAuthDiagnostic(ctx, step)
	return status, true, nil
}
