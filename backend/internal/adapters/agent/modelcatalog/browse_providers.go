package modelcatalog

import (
	"context"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// browseProviderCredentials reports, per harness, the providers it holds a
// credential for, read from that harness's own configuration, credential
// store and environment variables. Presence is the evidence: whether the
// provider accepts the credential is not proven. Each harness registers its
// check from its own file. OpenCode, Kilo, MiMo, Pi and OMP use
// configuredProviders instead.
var browseProviderCredentials = map[string]func(workingDir string, env map[string]string) []string{}

// markConfiguredProviders marks the providers a catalog's harness holds a
// credential for, so Settings can tell them apart from the rest.
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

// groupClineModelsByProvider groups Cline's ACP model choices by the Cline
// provider that serves them. Cline's model option mixes provider choices
// ("cline", "cline-pass", "openai-codex") with the Cline account's catalog,
// whose IDs carry the model vendor ("anthropic/...", "~anthropic/..."): that
// vendor is not a provider configured in Cline, so those models belong to the
// "cline" provider and its credential.
func groupClineModelsByProvider(models []ports.AgentModelInfo) {
	for i := range models {
		if strings.Contains(models[i].ID, "/") {
			models[i].Provider = "cline"
		} else if models[i].Provider == "" {
			models[i].Provider = models[i].ID
		}
	}
}
