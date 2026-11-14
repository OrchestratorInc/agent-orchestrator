package modelcatalog

import (
	"context"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var browseProviderCredentials = map[string]func(workingDir string, env map[string]string) []string{}

func markConfiguredProviders(ctx context.Context, catalog *ports.AgentModelCatalog, request ports.AgentModelDiscoveryRequest) {
	check := browseProviderCredentials[request.AgentID]
	if check == nil {
		if len(catalog.ConfiguredProviders) == 0 {
			catalog.ConfiguredProviders = configuredProviders(ctx, request.AgentID, request.Env)
		}
		return
	}
	if request.AgentID == "cline" {
		groupClineModelsByProvider(catalog.Models)
	}
	configured := uniqueProviders(append(catalog.ConfiguredProviders, check(request.WorkingDir, request.Env)...))
	sort.Strings(configured)
	catalog.ConfiguredProviders = configured
}

func groupClineModelsByProvider(models []ports.AgentModelInfo) {
	for i := range models {
		if strings.Contains(models[i].ID, "/") {
			models[i].Provider = "cline"
		} else if models[i].Provider == "" {
			models[i].Provider = models[i].ID
		}
	}
}
