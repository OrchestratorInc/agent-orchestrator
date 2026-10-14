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
// check from its own file. Harnesses with their own browsing path (OpenCode,
// Kilo, MiMo, Pi, OMP and FX) are not listed here.
var browseProviderCredentials = map[string]func(workingDir string, env map[string]string) []string{}

// browseSupportedProviders reports, per harness, every provider the installed
// harness itself supports, read from its own bundle or catalog. Harness
// Settings lists them all so a provider the user has not set up yet is still
// visible. Each harness registers its list from its own file.
// shown holds the providers the catalog already groups models under.
var browseSupportedProviders = map[string]func(ctx context.Context, request ports.AgentModelDiscoveryRequest, shown []string) []string{}

// markBrowsedProviders lists every provider a Harness Settings catalog has
// models for, every provider the harness supports, and the ones the harness's
// credential check finds, and marks the latter as configured.
func markBrowsedProviders(ctx context.Context, catalog *ports.AgentModelCatalog, request ports.AgentModelDiscoveryRequest) {
	check := browseProviderCredentials[request.AgentID]
	supported := browseSupportedProviders[request.AgentID]
	if check == nil && supported == nil {
		return
	}
	if request.AgentID == "cline" {
		groupClineModelsByProvider(catalog.Models)
	}
	providers := append([]string(nil), catalog.Providers...)
	for _, model := range catalog.Models {
		if model.Provider != "" {
			providers = append(providers, model.Provider)
		}
	}
	if supported != nil {
		providers = append(providers, supported(ctx, request, uniqueProviders(append([]string(nil), providers...)))...)
	}
	configured := append([]string(nil), catalog.ConfiguredProviders...)
	if check != nil {
		configured = append(configured, check(request.WorkingDir, request.Env)...)
	}
	configured = uniqueProviders(configured)
	sort.Strings(configured)
	catalog.ConfiguredProviders = configured
	// A configured provider is listed even while it has no models.
	catalog.Providers = uniqueProviders(append(providers, configured...))
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
