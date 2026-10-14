package modelcatalog

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func init() { browseSupportedProviders["omp"] = ompSupportedProviders }

const ompProviderListTimeout = 20 * time.Second

// ompNonModelProviders are entries of `omp auth-broker list` that sign in to
// something other than a model provider: web search services, and a second
// login flow for a provider already listed.
var ompNonModelProviders = map[string]bool{
	"exa": true, "kagi": true, "parallel": true, "perplexity": true, "tavily": true,
	"openai-codex-device": true,
}

// ompSupportedProviders lists the providers OMP can sign in to, from
// `omp auth-broker list --json`, which reads OMP's built-in provider registry
// without contacting a provider or a broker. Providers OMP reads only from
// environment variables are not in that list; the catalog's own model groups
// cover the ones with models.
func ompSupportedProviders(ctx context.Context, request ports.AgentModelDiscoveryRequest, _ []string) []string {
	if strings.TrimSpace(request.Binary) == "" {
		return nil
	}
	runCtx, cancel := context.WithTimeout(ctx, ompProviderListTimeout)
	defer cancel()
	output, err := modelCommand(runCtx, request.Binary, []string{"auth-broker", "list", "--json"}, "", request.Env).Output()
	if err != nil {
		return nil
	}
	return parseOMPProviderList(output)
}

func parseOMPProviderList(output []byte) []string {
	var entries []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(output, &entries) != nil {
		return nil
	}
	providers := make([]string, 0, len(entries))
	for _, entry := range entries {
		if id := strings.TrimSpace(entry.ID); id != "" && !ompNonModelProviders[id] {
			providers = append(providers, id)
		}
	}
	return providers
}
