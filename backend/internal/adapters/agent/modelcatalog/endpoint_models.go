package modelcatalog

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// codexEndpoint returns the custom endpoint a Codex discovery request carries:
// the same variables its sessions receive, so the catalog lists exactly what
// those sessions can run.
func codexEndpoint(env map[string]string) (baseURL, key, model string, ok bool) {
	baseURL = strings.TrimSpace(env[harnessendpoint.CodexBaseURLEnv])
	key = strings.TrimSpace(env[harnessendpoint.CodexAPIKeyEnv])
	model = strings.TrimSpace(env[harnessendpoint.CodexModelEnv])
	return baseURL, key, model, baseURL != "" && key != ""
}

// codexEndpointFingerprint names the endpoint a Codex catalog came from, or ""
// when there is none, so catalogs cached without an endpoint stay valid. The
// key is reduced to a digest, and only the caller's hash of this is stored.
func codexEndpointFingerprint(env map[string]string) string {
	baseURL, key, model, ok := codexEndpoint(env)
	if !ok {
		return ""
	}
	keyDigest := sha256.Sum256([]byte(key))
	return fmt.Sprintf("endpoint-models-v1;url=%s;key=%x;model=%s", baseURL, keyDigest[:8], model)
}

// discoverCodexEndpointCatalog lists the models a custom endpoint serves. The
// client never follows redirects, so the key stays with the saved endpoint.
// Codex's app-server catalog describes OpenAI's models whatever provider a
// session uses, so it is never offered for an endpoint. The models keep the
// endpoint's order, and the endpoint's saved model is the default. A failure
// is returned for the service to keep the matching cached catalog or fall
// back to typed model IDs.
func discoverCodexEndpointCatalog(ctx context.Context, request ports.AgentModelDiscoveryRequest) (ports.AgentModelCatalog, error) {
	base := Base(request.AgentID)
	baseURL, key, model, _ := codexEndpoint(request.Env)
	ids, err := harnessendpoint.ListModels(ctx, harnessendpoint.ProbeClient, request.AgentID, baseURL, harnessendpoint.AuthBearer, key)
	if errors.Is(err, harnessendpoint.ErrKeyRejected) {
		return base, errors.New("codex endpoint model discovery: the endpoint rejected the saved key")
	}
	if err != nil {
		return base, fmt.Errorf("codex endpoint model discovery: %w", err)
	}
	models := make([]ports.AgentModelInfo, 0, len(ids))
	for _, id := range ids {
		models = append(models, ports.AgentModelInfo{ID: id, Label: id})
	}
	if len(models) == 0 {
		return base, errors.New("codex endpoint model discovery returned no models")
	}
	base.Models = applyConfiguredDefault(models, model)
	base.Source = "provider"
	base.FetchedAt = time.Now().UTC()
	return base, nil
}
