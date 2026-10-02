package httpd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

type accountControlReadFixture struct {
	controllers.AccountsManagerControls
	read domain.SessionID
}

func (f *accountControlReadFixture) SessionAccount(_ context.Context, id domain.SessionID) (domain.AccountsManagerSessionRoute, *domain.AccountsManagerSwitch, error) {
	f.read = id
	return domain.AccountsManagerSessionRoute{SessionID: id, Mode: domain.AccountsManagerManaged, AccountID: "amc_a", Revision: 7}, nil, nil
}

func TestAccountsManagerControlsDependencyWiring(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "unavailable"
		deps := APIDeps{}
		fixture := &accountControlReadFixture{}
		want := http.StatusNotImplemented
		if configured {
			name, want = "configured", http.StatusOK
			deps.AccountsManagerControls = fixture
		}
		t.Run(name, func(t *testing.T) {
			router := chi.NewRouter()
			NewAPI(config.Config{}, deps).Register(router)
			res := httptest.NewRecorder()
			router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/session-a/account", nil))
			if res.Code != want || configured && fixture.read != "session-a" {
				t.Fatalf("production API construction: status=%d read=%q body=%s", res.Code, fixture.read, res.Body.String())
			}
		})
	}
}
