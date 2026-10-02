package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	accountsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/accountsmanager"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type controlServiceCatalog struct{ *core.ManagementClient }

func (controlServiceCatalog) ListCredentials(context.Context) ([]core.CredentialSummary, error) {
	return []core.CredentialSummary{
		{Ref: "secret-token-a", Provider: core.ProviderCodex, Kind: core.CredentialOAuth, Status: core.CredentialActive},
		{Ref: "secret-token-b", Provider: core.ProviderCodex, Kind: core.CredentialOAuth, Status: core.CredentialActive},
	}, nil
}
func (controlServiceCatalog) CredentialPublicID(ref string) (string, error) {
	switch ref {
	case "secret-token-a":
		return "amc_a", nil
	case "secret-token-b":
		return "amc_b", nil
	default:
		return "", core.ErrCredentialNotFound
	}
}

// Only admission and durable cancellation are exercised here, never controller launch.
type persistedControlService struct {
	store       *sqlite.Store
	service     *accountsvc.Service
	manager     *sessionmanager.Manager
	cancelCalls int
	retryCalls  int
}

func newPersistedControlService(t *testing.T) *persistedControlService {
	t.Helper()
	store := sqlitetest.MustOpen(t)
	service := accountsvc.New(controlServiceCatalog{}, store)
	return &persistedControlService{store: store, service: service, manager: sessionmanager.New(sessionmanager.Deps{Store: store, AccountsManager: service})}
}

func (s *persistedControlService) SessionAccount(ctx context.Context, id domain.SessionID) (domain.AccountsManagerSessionRoute, *domain.AccountsManagerSwitch, error) {
	binding, found, err := s.store.GetAccountsManagerSessionRoute(ctx, id, domain.AccountsManagerProviderCodex)
	if err != nil || !found {
		return binding, nil, apierr.NotFound("not_found", "binding not found")
	}
	op, pending, err := s.store.GetLatestAccountsManagerSwitch(ctx, id)
	if pending {
		return binding, &op, err
	}
	return binding, nil, err
}
func (s *persistedControlService) StartAccountSwitch(ctx context.Context, op domain.AccountsManagerSwitch) (domain.AccountsManagerSwitch, error) {
	binding, _, err := s.SessionAccount(ctx, op.SessionID)
	if err != nil {
		return op, err
	}
	rec, found, err := s.store.GetSession(ctx, op.SessionID)
	if err != nil || !found {
		return op, apierr.NotFound("not_found", "session not found")
	}
	op.Provider, op.SourceMode, op.SourceAccountID = binding.Provider, binding.Mode, binding.AccountID
	op.SourceOwner, op.SourceRuntimeHandleID = rec.ControllerOwner(), rec.Metadata.RuntimeHandleID
	op.TargetGeneration = "private-generation-" + op.ID
	op, _, err = s.service.AdmitAgentAccountSwitch(ctx, op, "")
	return op, err
}
func (s *persistedControlService) AccountSwitch(ctx context.Context, id domain.SessionID, operationID string) (domain.AccountsManagerSwitch, error) {
	op, found, err := s.store.GetAccountsManagerSwitch(ctx, operationID)
	if err != nil {
		return op, err
	}
	if !found || op.SessionID != id {
		return domain.AccountsManagerSwitch{}, apierr.NotFound("not_found", "switch not found")
	}
	return op, nil
}
func (s *persistedControlService) RetryAccountSwitch(ctx context.Context, id domain.SessionID, operationID string) (domain.AccountsManagerSwitch, error) {
	s.retryCalls++
	return s.manager.RetryAccountsManagerSwitch(ctx, id, operationID)
}
func (s *persistedControlService) CancelAccountSwitch(ctx context.Context, id domain.SessionID, operationID string) (domain.AccountsManagerSwitch, error) {
	s.cancelCalls++
	return s.manager.CancelAccountsManagerSwitch(ctx, id, operationID)
}
func (s *persistedControlService) AccountRemovalImpact(ctx context.Context, id string) (domain.AccountsManagerRemovalImpact, error) {
	return s.store.AccountsManagerRemovalImpact(ctx, id)
}
func (s *persistedControlService) StartAccountRemoval(ctx context.Context, op, id string, revision int64, confirmed bool) (domain.AccountsManagerRemoval, error) {
	result, _, err := s.service.PrepareAccountRemoval(ctx, op, id, revision, confirmed)
	return result, err
}
func (s *persistedControlService) AccountRemoval(ctx context.Context, id string) (domain.AccountsManagerRemoval, error) {
	op, found, err := s.store.GetAccountsManagerRemoval(ctx, id)
	if err != nil {
		return op, err
	}
	if !found {
		return domain.AccountsManagerRemoval{}, apierr.NotFound("not_found", "removal not found")
	}
	return op, nil
}
func (s *persistedControlService) RetryAccountRemoval(ctx context.Context, id string) (domain.AccountsManagerRemoval, error) {
	return s.manager.RetryAccountsManagerRemoval(ctx, id)
}
func (s *persistedControlService) CancelAccountRemoval(ctx context.Context, id string) (domain.AccountsManagerRemoval, error) {
	return s.manager.CancelAccountsManagerRemoval(ctx, id)
}

func (s *persistedControlService) seedSession(t *testing.T, account string) domain.AccountsManagerSessionRoute {
	t.Helper()
	now := time.Now().UTC()
	if err := s.store.UpsertProject(t.Context(), domain.ProjectRecord{ID: "public-controls", Path: t.TempDir(), RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	record, err := s.store.CreateSession(t.Context(), domain.SessionRecord{ProjectID: "public-controls", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, Metadata: domain.SessionMetadata{RuntimeLaunchID: "private-generation", RuntimeHandleID: "private-runtime-http://127.0.0.1:54321", WorkspacePath: t.TempDir()}, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err := s.store.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: record.ID, Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerManaged, AccountID: account})
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func TestAccountControlServiceBackedSwitchAdmissionAndCancellation(t *testing.T) {
	s := newPersistedControlService(t)
	a, b := s.seedSession(t, "amc_a"), s.seedSession(t, "amc_b")
	path := "/sessions/" + string(a.SessionID) + "/account-switches"
	body := fmt.Sprintf(`{"operationId":"switch-persisted","expectedRevision":%d,"mode":"managed","accountId":"amc_b","policy":"drain"}`, a.Revision)
	for range 2 {
		res := accountControlRequest(t, s, http.MethodPost, path, body)
		if res.Code != http.StatusAccepted {
			t.Fatalf("admission=%d %s", res.Code, res.Body.String())
		}
	}
	ops, err := s.store.ListActiveAccountsManagerSwitches(t.Context())
	if err != nil || len(ops) != 1 || ops[0].Policy != domain.SessionInterfaceTransitionDrain || ops[0].TargetAccountID != "amc_b" || ops[0].SourceAccountID != "amc_a" {
		t.Fatalf("lost durable explicit choice: %+v err=%v", ops, err)
	}
	for _, request := range []struct{ path, body string }{
		{path, strings.Replace(body, `"drain"`, `"interrupt"`, 1)},
		{path, strings.Replace(body, `"amc_b"`, `"amc_a"`, 1)},
		{"/sessions/" + string(b.SessionID) + "/account-switches", body},
	} {
		res := accountControlRequest(t, s, http.MethodPost, request.path, request.body)
		if res.Code != http.StatusConflict {
			t.Fatalf("operation identity changed its intent: %d %s", res.Code, res.Body.String())
		}
	}
	for _, action := range []string{"retry", "cancel"} {
		res := accountControlRequest(t, s, http.MethodPost, "/sessions/"+string(b.SessionID)+"/account-switches/switch-persisted/"+action, "{}")
		if res.Code != http.StatusNotFound || s.retryCalls != 0 || s.cancelCalls != 0 {
			t.Fatalf("foreign session reached mutation: %d retries=%d cancellations=%d", res.Code, s.retryCalls, s.cancelCalls)
		}
	}
	for range 2 {
		res := accountControlRequest(t, s, http.MethodPost, path+"/switch-persisted/cancel", "{}")
		var cancelled AccountsManagerSwitchResponse
		if res.Code != http.StatusAccepted || json.Unmarshal(res.Body.Bytes(), &cancelled) != nil || cancelled.Phase != "cancelled" {
			t.Fatalf("cancellation=%d %s", res.Code, res.Body.String())
		}
		res = accountControlRequest(t, s, http.MethodPost, path+"/switch-persisted/retry", "{}")
		if res.Code != http.StatusConflict {
			t.Fatalf("cancelled retry was admitted: %d %s", res.Code, res.Body.String())
		}
		res = accountControlRequest(t, s, http.MethodGet, path+"/switch-persisted", "")
		if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &cancelled) != nil || cancelled.Phase != "cancelled" {
			t.Fatalf("retry changed durable cancellation: %d %s", res.Code, res.Body.String())
		}
	}
	for _, original := range []domain.AccountsManagerSessionRoute{a, b} {
		binding, _, err := s.store.GetAccountsManagerSessionRoute(t.Context(), original.SessionID, original.Provider)
		if err != nil || binding.AccountID != original.AccountID || binding.Revision != original.Revision {
			t.Fatalf("admission/cancellation changed a binding: %+v err=%v", binding, err)
		}
	}
}

func TestAccountControlServiceBackedStaleRevisionAndNoFallback(t *testing.T) {
	s := newPersistedControlService(t)
	a := s.seedSession(t, "amc_a")
	path := "/sessions/" + string(a.SessionID) + "/account-switches"
	for _, body := range []string{
		fmt.Sprintf(`{"operationId":"stale","expectedRevision":%d,"mode":"managed","accountId":"amc_b","policy":"interrupt"}`, a.Revision+1),
		fmt.Sprintf(`{"operationId":"missing-target","expectedRevision":%d,"mode":"managed","accountId":"amc_missing","policy":"interrupt"}`, a.Revision),
	} {
		res := accountControlRequest(t, s, http.MethodPost, path, body)
		if res.Code != http.StatusConflict {
			t.Fatalf("rejected choice=%d %s", res.Code, res.Body.String())
		}
	}
	ops, err := s.store.ListActiveAccountsManagerSwitches(t.Context())
	if err != nil || len(ops) != 0 {
		t.Fatalf("rejected choice persisted a fallback: %+v err=%v", ops, err)
	}
}

func TestAccountControlServiceBackedCancellationStopsAtDurableBoundary(t *testing.T) {
	s := newPersistedControlService(t)
	a := s.seedSession(t, "amc_a")
	path := "/sessions/" + string(a.SessionID) + "/account-switches"
	body := fmt.Sprintf(`{"operationId":"stop-wins","expectedRevision":%d,"mode":"managed","accountId":"amc_b","policy":"interrupt"}`, a.Revision)
	res := accountControlRequest(t, s, http.MethodPost, path, body)
	if res.Code != http.StatusAccepted {
		t.Fatalf("admission=%d %s", res.Code, res.Body.String())
	}
	phase := domain.AccountsManagerSwitchRequested
	for _, next := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchWaiting, domain.AccountsManagerSwitchStopping} {
		if _, err := s.store.AdvanceAccountsManagerSwitch(t.Context(), "stop-wins", phase, next, ""); err != nil {
			t.Fatal(err)
		}
		phase = next
	}
	res = accountControlRequest(t, s, http.MethodPost, path+"/stop-wins/cancel", "{}")
	if res.Code != http.StatusConflict {
		t.Fatalf("late cancellation=%d %s", res.Code, res.Body.String())
	}
	op, found, err := s.store.GetAccountsManagerSwitch(t.Context(), "stop-wins")
	if err != nil || !found || op.Phase != domain.AccountsManagerSwitchStopping || op.SourceRevision != a.Revision {
		t.Fatalf("late cancellation changed stopping: %+v err=%v", op, err)
	}
}

func TestAccountControlServiceBackedRemovalAdmissionAndCancellation(t *testing.T) {
	s := newPersistedControlService(t)
	res := accountControlRequest(t, s, http.MethodGet, "/accounts-manager/accounts/amc_a/removal-impact", "")
	var impact AccountsManagerRemovalImpactResponse
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &impact) != nil || len(impact.Sessions) != 0 {
		t.Fatalf("empty impact=%d %s", res.Code, res.Body.String())
	}
	stale := fmt.Sprintf(`{"operationId":"remove-stale","expectedRevision":%d,"confirmed":true}`, impact.Revision+1)
	res = accountControlRequest(t, s, http.MethodPost, "/accounts-manager/accounts/amc_a/removals", stale)
	if res.Code != http.StatusConflict {
		t.Fatalf("stale impact admitted: %d %s", res.Code, res.Body.String())
	}
	body := fmt.Sprintf(`{"operationId":"remove-observed","expectedRevision":%d,"confirmed":true}`, impact.Revision)
	for range 2 {
		res = accountControlRequest(t, s, http.MethodPost, "/accounts-manager/accounts/amc_a/removals", body)
		if res.Code != http.StatusAccepted {
			t.Fatalf("observed revision rejected: %d %s", res.Code, res.Body.String())
		}
	}
	res = accountControlRequest(t, s, http.MethodPost, "/accounts-manager/accounts/amc_b/removals", body)
	if res.Code != http.StatusConflict {
		t.Fatalf("removal operation was reused for another account: %d %s", res.Code, res.Body.String())
	}
	for range 2 {
		res = accountControlRequest(t, s, http.MethodPost, "/accounts-manager/removals/remove-observed/cancel", "{}")
		var operation AccountsManagerRemovalResponse
		if res.Code != http.StatusAccepted || json.Unmarshal(res.Body.Bytes(), &operation) != nil || operation.Phase != "cancelled" || operation.CanCancel {
			t.Fatalf("removal cancellation=%d %s", res.Code, res.Body.String())
		}
	}
	deleting, err := s.store.AccountsManagerAccountDeleting(t.Context(), "amc_a")
	if err != nil || deleting {
		t.Fatalf("cancelled deletion fence remains: %v %v", deleting, err)
	}
}
