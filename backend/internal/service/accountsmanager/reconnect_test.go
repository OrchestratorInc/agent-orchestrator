package accountsmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type reconnectClient struct {
	fakeClient
	ref        string
	generation uint64
}

func (f *reconnectClient) ReconnectOAuth(_ context.Context, provider core.Provider, mode core.OAuthMode, ref string, generation uint64) (core.OAuthSession, error) {
	f.ref, f.generation = ref, generation
	return core.OAuthSession{Provider: provider, Mode: mode, State: "operation", TargetRef: ref, AuthorizationURL: "https://provider.example/login", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func TestReconnectUsesExplicitGenerationWithoutChangingChoices(t *testing.T) {
	for _, scenario := range []string{"success", "stale", "unverified", "wrong provider", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			client := &reconnectClient{fakeClient: fakeClient{credentials: []core.CredentialSummary{{Ref: "private-a", Provider: core.ProviderCodex, Generation: 2, ReconnectSupported: true}}}}
			store := newFakeRoutingStore()
			store.policies[domain.AccountsManagerProviderCodex] = domain.AccountsManagerRoutingPolicy{Provider: domain.AccountsManagerProviderCodex, Enabled: true, AccountIDs: []string{"safe-b"}}
			store.routes["pinned"] = domain.AccountsManagerSessionRoute{AccountID: "safe-a"}
			service := New(client, store)
			id, _ := client.CredentialPublicID("private-a")
			generation, provider := uint64(2), core.ProviderCodex
			var want error
			switch scenario {
			case "stale":
				generation, want = 1, core.ErrCredentialConflict
			case "unverified":
				client.credentials[0].ReconnectSupported, want = false, core.ErrOperationUnsupported
			case "wrong provider":
				provider, want = core.ProviderClaude, core.ErrCredentialNotFound
			case "missing":
				id, want = "missing", core.ErrCredentialNotFound
			}
			session, err := service.ReconnectOAuth(t.Context(), provider, core.OAuthModeCallback, id, generation)
			if !errors.Is(err, want) {
				t.Fatalf("reconnect=%v want=%v", err, want)
			}
			if want != nil && client.ref != "" {
				t.Fatal("invalid target reached sign-in")
			}
			if want == nil && (client.ref != "private-a" || client.generation != 2 || session.AccountID != id) {
				t.Fatal("selected target or generation lost")
			}
			if store.policies[domain.AccountsManagerProviderCodex].AccountIDs[0] != "safe-b" || store.routes["pinned"].AccountID != "safe-a" {
				t.Fatal("reconnect changed user choices")
			}
		})
	}
}
