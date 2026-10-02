package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type existingAccountCommander struct {
	*sessionmanager.Manager
	launches int
}

func (m *existingAccountCommander) Spawn(context.Context, ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	m.launches++
	return domain.SessionRecord{}, 0, 0, errors.New("unexpected launch in reuse fixture")
}

func TestSpawnExistingAccountPublicContract(t *testing.T) {
	for _, scenario := range []string{"other account", "native requested", "managed requested", "managed match", "native match", "removal pending", "switch pending"} {
		t.Run(scenario, func(t *testing.T) {
			store := sqlitetest.MustOpen(t)
			now := time.Now().UTC()
			if err := store.UpsertProject(t.Context(), domain.ProjectRecord{ID: "reuse", Path: t.TempDir(), RegisteredAt: now}); err != nil {
				t.Fatal(err)
			}
			choice := domain.AccountsManagerAccountChoice{Mode: domain.AccountsManagerManaged, AccountID: "account-a"}
			if scenario == "native match" || scenario == "managed requested" {
				choice.Mode, choice.AccountID = domain.AccountsManagerNative, ""
			}
			rec, fresh, err := store.CreateSessionWithAccount(t.Context(), domain.SessionRecord{
				ProjectID: "reuse", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
				Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}, CreatedAt: now, UpdatedAt: now,
			}, choice)
			if err != nil || !fresh {
				t.Fatal("seed existing account-bound session", err)
			}
			if scenario == "removal pending" {
				impact, err := store.AccountsManagerRemovalImpact(t.Context(), "account-a")
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := store.CreateAccountsManagerRemoval(t.Context(), "remove-existing", "account-a", impact.Revision, true); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "switch pending" {
				binding, found, err := store.GetAccountsManagerSessionRoute(t.Context(), rec.ID, domain.AccountsManagerProviderCodex)
				if err != nil || !found {
					t.Fatal("read switch source", err)
				}
				if _, _, err := store.CreateAccountsManagerSwitch(t.Context(), domain.AccountsManagerSwitch{
					ID: "switch-existing", SessionID: rec.ID, Provider: binding.Provider, SourceMode: binding.Mode,
					SourceAccountID: binding.AccountID, SourceRevision: binding.Revision, SourceOwner: rec.ControllerOwner(),
					SourceRuntimeHandleID: rec.Metadata.RuntimeHandleID, TargetMode: domain.AccountsManagerManaged,
					TargetAccountID: "account-b", TargetGeneration: "pending-target", Policy: domain.SessionInterfaceTransitionDrain,
				}); err != nil {
					t.Fatal(err)
				}
			}
			before, err := store.AccountsManagerBindings(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			manager := &existingAccountCommander{Manager: sessionmanager.New(sessionmanager.Deps{Store: store})}
			service := sessionsvc.NewWithDeps(sessionsvc.Deps{Manager: manager, Store: store})
			router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Sessions: service}, httpd.ControlDeps{})
			account := `{"mode":"managed","accountId":"account-a"}`
			switch scenario {
			case "other account":
				account = `{"mode":"managed","accountId":"account-b"}`
			case "native requested", "native match":
				account = `{"mode":"native"}`
			}
			for range 2 {
				request := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"projectId":"reuse","kind":"orchestrator","harness":"codex","account":`+account+`}`))
				request.Header.Set("X-Request-Id", "reuse-account-request")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if scenario == "managed match" || scenario == "native match" {
					var body struct {
						Session struct{ ID string } `json:"session"`
					}
					if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Session.ID != string(rec.ID) {
						t.Fatalf("same-choice public replay status=%d did not retain the existing session", response.Code)
					}
				} else if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"ACCOUNT_BINDING_CHANGED"`) || !strings.Contains(response.Body.String(), `"requestId":"reuse-account-request"`) {
					t.Fatalf("conflicting public replay status=%d did not preserve rejection and request ID", response.Code)
				}
			}
			after, err := store.AccountsManagerBindings(t.Context())
			if err != nil || !reflect.DeepEqual(before, after) || manager.launches != 0 {
				t.Fatal("public account replay launched or changed durable routing", err)
			}
			records, err := store.ListSessions(t.Context(), "reuse")
			if err != nil || len(records) != 1 || records[0].ID != rec.ID {
				t.Fatal("public account replay created or removed a session", err)
			}
		})
	}
}
