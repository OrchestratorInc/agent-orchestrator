package provideraccounts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type modelsProxy struct {
	*fakeProxy
	authIDs []string
	efforts []string
}

func (p *modelsProxy) FetchAccountModels(_ context.Context, _ string, authID string) (ports.AgentModelCatalog, error) {
	p.authIDs = append(p.authIDs, authID)
	return ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: authID + "-model", Efforts: p.efforts}}}, nil
}

// A stored model list is refreshed when its fingerprint changes. Reasoning
// levels are part of the list, so their arrival must change it.
func TestModelsFingerprintChangesWhenReasoningLevelsDo(t *testing.T) {
	proxy := &modelsProxy{fakeProxy: &fakeProxy{}}
	svc := New(&memoryStore{}, proxy, &fakeGuard{busy: make(map[domain.SessionID]bool)}, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:1234", func() string { return "account-1" })
	ctx := context.Background()
	if _, err := recordLogin(ctx, svc, "codex", "alice@example.test", "alice.json", "alice-auth", ""); err != nil {
		t.Fatal(err)
	}
	without, handled, err := svc.ModelsFingerprint(ctx, "codex", "")
	if err != nil || !handled {
		t.Fatalf("fingerprint=%q handled=%v err=%v", without, handled, err)
	}
	proxy.efforts = []string{"low", "high"}
	with, _, err := svc.ModelsFingerprint(ctx, "codex", "")
	if err != nil || with == without {
		t.Fatalf("fingerprint did not change with reasoning levels: %q err=%v", with, err)
	}
	again, _, _ := svc.ModelsFingerprint(ctx, "codex", "")
	if again != with {
		t.Fatalf("fingerprint is not stable: %q then %q", with, again)
	}
}

func TestDiscoverModelsUsesTheChosenAccountForAnAccountScope(t *testing.T) {
	proxy := &modelsProxy{fakeProxy: &fakeProxy{}}
	n := 0
	svc := New(&memoryStore{}, proxy, &fakeGuard{busy: make(map[domain.SessionID]bool)}, []byte(strings.Repeat("k", 32)), "http://127.0.0.1:1234", func() string { n++; return "account-" + string(rune('0'+n)) })
	ctx := context.Background()
	if _, err := recordLogin(ctx, svc, "codex", "alice@example.test", "alice.json", "alice-auth", ""); err != nil {
		t.Fatal(err)
	}
	bob, err := recordLogin(ctx, svc, "codex", "bob@example.test", "bob.json", "bob-auth", "")
	if err != nil {
		t.Fatal(err)
	}
	clara, err := recordLogin(ctx, svc, "claude", "clara@example.test", "clara.json", "clara-auth", "")
	if err != nil {
		t.Fatal(err)
	}
	// A project scope shows the provider default; an account scope shows that account.
	for scope, want := range map[string]string{"project-1": "alice-auth-model", ports.ModelCatalogAccountScope(bob): "bob-auth-model"} {
		catalog, handled, err := svc.DiscoverModels(ctx, domain.HarnessCodex, scope)
		if err != nil || !handled || len(catalog.Models) != 1 || catalog.Models[0].ID != want {
			t.Fatalf("scope %q: catalog=%+v handled=%t err=%v", scope, catalog.Models, handled, err)
		}
	}
	for _, scope := range []string{ports.ModelCatalogAccountScope("missing"), ports.ModelCatalogAccountScope(clara)} {
		if _, handled, err := svc.DiscoverModels(ctx, domain.HarnessCodex, scope); !handled || !errors.Is(err, ports.ErrProviderAccountUnknown) {
			t.Fatalf("scope %q: handled=%t err=%v", scope, handled, err)
		}
	}
	if len(proxy.authIDs) != 2 {
		t.Fatalf("helper was asked for the wrong accounts: %v", proxy.authIDs)
	}
}
