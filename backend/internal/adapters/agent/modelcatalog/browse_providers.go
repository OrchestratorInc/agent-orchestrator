package modelcatalog

import (
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// browseProviderCredentials reports, per harness, the providers it holds a
// credential for, read from that harness's own configuration, credential
// store and environment variables. Presence is the evidence: whether the
// provider accepts the credential is not proven. Each harness registers its
// check from its own file. Harnesses with their own browsing path (OpenCode,
// Kilo, MiMo, Pi, OMP and FX) are not listed here.
var browseProviderCredentials = map[string]func(workingDir string, env map[string]string) []string{}

// markBrowsedProviders marks the providers a Harness Settings catalog's
// credential check finds as configured.
func markBrowsedProviders(catalog *ports.AgentModelCatalog, request ports.AgentModelDiscoveryRequest) {
	check := browseProviderCredentials[request.AgentID]
	if check == nil {
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
