package accountsmanager

import (
	"context"
	"testing"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type scopedModelClient struct {
	removalClient
	ref string
}

func (c *scopedModelClient) ListCredentialModels(_ context.Context, ref string) ([]core.CredentialModel, error) {
	c.ref = ref
	return []core.CredentialModel{{ID: "selected-model", DisplayName: "Selected model", Efforts: []string{"low", "high"}}}, nil
}

func TestAgentAccountModelsPreservesExplicitAccountAndCapabilities(t *testing.T) {
	for _, scenario := range []string{"selected", "missing", "wrong provider", "unavailable", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			client := &scopedModelClient{removalClient: removalClient{credentials: []core.CredentialSummary{{Ref: "a", Provider: core.ProviderCodex, Status: core.CredentialActive}, {Ref: "b", Provider: core.ProviderCodex, Status: core.CredentialActive}}}}
			id := "safe-b"
			switch scenario {
			case "missing":
				id = "safe-missing"
			case "wrong provider":
				client.credentials[1].Provider = core.ProviderClaude
			case "unavailable":
				client.credentials[1].Unavailable = true
			case "disabled":
				client.credentials[1].Disabled = true
			}
			catalog, err := New(client).AgentAccountModels(t.Context(), domain.AccountsManagerProviderCodex, id)
			if scenario != "selected" {
				if err == nil || client.ref != "" {
					t.Fatalf("rejected account discovered another model catalog: %v %q", err, client.ref)
				}
				return
			}
			if err != nil || client.ref != "b" || len(catalog.Models) != 1 || len(catalog.Models[0].Efforts) != 2 || catalog.Models[0].Efforts[1] != "high" {
				t.Fatalf("selected account capabilities lost: %+v %q %v", catalog, client.ref, err)
			}
		})
	}
}
