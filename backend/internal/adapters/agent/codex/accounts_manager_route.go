package codex

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func accountsManagerRouteFlags(route *ports.AgentProviderRoute) ([]string, error) {
	if route == nil {
		return nil, nil
	}
	base := strings.TrimSpace(route.BaseURL)
	endpoint, err := url.Parse(base)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || strings.Contains(base, "#") ||
		(endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawPath != "" ||
		strings.TrimSpace(route.TokenEnv) != "AO_ACCOUNTS_MANAGER_SESSION_TOKEN" {
		return nil, errors.New("invalid managed Codex route configuration")
	}
	port, err := strconv.ParseUint(endpoint.Port(), 10, 16)
	if err != nil || port == 0 {
		return nil, errors.New("invalid managed Codex route configuration")
	}
	values := []string{
		"cli_auth_credentials_store=" + codexTOMLConfigString("ephemeral"),
		"model_provider=" + codexTOMLConfigString("ao_accounts_manager"),
		"model_providers.ao_accounts_manager.name=" + codexTOMLConfigString("AO Accounts Manager"),
		"model_providers.ao_accounts_manager.base_url=" + codexTOMLConfigString(strings.TrimRight(base, "/")+"/v1"),
		"model_providers.ao_accounts_manager.env_key=" + codexTOMLConfigString(strings.TrimSpace(route.TokenEnv)),
		"model_providers.ao_accounts_manager.wire_api=" + codexTOMLConfigString("responses"),
		"model_providers.ao_accounts_manager.requires_openai_auth=false",
	}
	args := make([]string, 0, len(values)*2)
	for _, value := range values {
		args = append(args, "-c", value)
	}
	return args, nil
}
