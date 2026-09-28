package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

func TestSpawnInitialAccountProductionConstruction(t *testing.T) {
	f, runtime, agent := newPublicExecutionFixture(t)
	capability := f.request(t, http.MethodGet, "/sessions/account-selection", "", http.StatusOK)
	var support controllers.InitialAccountSelectionResponse
	if err := json.Unmarshal(capability.Body.Bytes(), &support); err != nil || !support.InitialSelection {
		t.Fatal("production dependency construction hides initial selection support", err)
	}
	if err := f.store.UpsertProject(t.Context(), domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch, Path: t.TempDir(), RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.PutAccountsManagerRoutingPolicy(t.Context(), domain.AccountsManagerRoutingPolicy{Provider: domain.AccountsManagerProviderCodex, Enabled: true, AccountIDs: []string{"account-b"}}); err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"account-a", "account-b", ""} {
		mode := domain.AccountsManagerManaged
		if account == "" {
			mode = domain.AccountsManagerNative
		}
		body := fmt.Sprintf(`{"projectId":"scratch","kind":"worker","harness":"codex","mode":"tui","account":{"mode":%q,"accountId":%q}}`, mode, account)
		response := f.request(t, http.MethodPost, "/sessions", body, http.StatusCreated)
		var result controllers.SpawnSessionResponse
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		binding, found, err := f.store.GetAccountsManagerSessionRoute(t.Context(), result.Session.ID, domain.AccountsManagerProviderCodex)
		if err != nil || !found || binding.Mode != mode || binding.AccountID != account {
			t.Fatal("production spawn replaced explicit choice with saved default", err)
		}
		agent.mu.Lock()
		launch := agent.launches[len(agent.launches)-1]
		agent.mu.Unlock()
		if (launch.Route != nil) != (mode == domain.AccountsManagerManaged) {
			t.Fatal("production launch lost explicit routing mode")
		}
		runtime.mu.Lock()
		child := runtime.launches[len(runtime.launches)-1]
		runtime.mu.Unlock()
		if launch.Route != nil && child.Env[launch.Route.TokenEnv] != fmt.Sprintf("synthetic-route-%s-%d", account, binding.Revision) {
			t.Fatal("first production launch did not use the selected account revision")
		}
	}
	policy, err := f.store.GetAccountsManagerRoutingPolicy(t.Context(), domain.AccountsManagerProviderCodex)
	if err != nil || len(policy.AccountIDs) != 1 || policy.AccountIDs[0] != "account-b" {
		t.Fatal("initial choice rewrote the saved default", err)
	}
}
