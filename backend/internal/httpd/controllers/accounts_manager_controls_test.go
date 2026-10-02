package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type fakeAccountControls struct {
	binding     domain.AccountsManagerSessionRoute
	switchOp    domain.AccountsManagerSwitch
	removal     domain.AccountsManagerRemoval
	switchInput domain.AccountsManagerSwitch
	calls       []string
	err         error
}

func (f *fakeAccountControls) SessionAccount(_ context.Context, id domain.SessionID) (domain.AccountsManagerSessionRoute, *domain.AccountsManagerSwitch, error) {
	f.calls = append(f.calls, "binding:"+string(id))
	return f.binding, &f.switchOp, f.err
}
func (f *fakeAccountControls) StartAccountSwitch(_ context.Context, input domain.AccountsManagerSwitch) (domain.AccountsManagerSwitch, error) {
	f.calls = append(f.calls, "start-switch:"+string(input.SessionID))
	f.switchInput = input
	return f.switchOp, f.err
}
func (f *fakeAccountControls) AccountSwitch(_ context.Context, id domain.SessionID, op string) (domain.AccountsManagerSwitch, error) {
	f.calls = append(f.calls, "switch:"+string(id)+":"+op)
	return f.switchOp, f.err
}
func (f *fakeAccountControls) RetryAccountSwitch(_ context.Context, id domain.SessionID, op string) (domain.AccountsManagerSwitch, error) {
	f.calls = append(f.calls, "retry-switch:"+string(id)+":"+op)
	return f.switchOp, f.err
}
func (f *fakeAccountControls) CancelAccountSwitch(_ context.Context, id domain.SessionID, op string) (domain.AccountsManagerSwitch, error) {
	f.calls = append(f.calls, "cancel-switch:"+string(id)+":"+op)
	f.switchOp.Phase = domain.AccountsManagerSwitchCancelled
	return f.switchOp, f.err
}
func (f *fakeAccountControls) AccountRemovalImpact(_ context.Context, id string) (domain.AccountsManagerRemovalImpact, error) {
	f.calls = append(f.calls, "impact:"+id)
	return f.removal.Impact, f.err
}
func (f *fakeAccountControls) StartAccountRemoval(_ context.Context, op, id string, revision int64, confirmed bool) (domain.AccountsManagerRemoval, error) {
	f.calls = append(f.calls, fmt.Sprintf("start-removal:%s:%s:%d:%t", op, id, revision, confirmed))
	return f.removal, f.err
}
func (f *fakeAccountControls) AccountRemoval(_ context.Context, id string) (domain.AccountsManagerRemoval, error) {
	f.calls = append(f.calls, "removal:"+id)
	return f.removal, f.err
}
func (f *fakeAccountControls) RetryAccountRemoval(_ context.Context, id string) (domain.AccountsManagerRemoval, error) {
	f.calls = append(f.calls, "retry-removal:"+id)
	return f.removal, f.err
}
func (f *fakeAccountControls) CancelAccountRemoval(_ context.Context, id string) (domain.AccountsManagerRemoval, error) {
	f.calls = append(f.calls, "cancel-removal:"+id)
	f.removal.Phase = domain.AccountsManagerRemovalCancelled
	return f.removal, f.err
}

func accountControlsFixture() *fakeAccountControls {
	now := time.Unix(1700000000, 0).UTC()
	return &fakeAccountControls{
		binding:  domain.AccountsManagerSessionRoute{SessionID: "session-a", Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerManaged, AccountID: "amc_a", Revision: 7, Blocked: true},
		switchOp: domain.AccountsManagerSwitch{ID: "switch-a", SessionID: "session-a", Provider: domain.AccountsManagerProviderCodex, SourceMode: domain.AccountsManagerManaged, SourceAccountID: "amc_a", SourceRevision: 7, TargetMode: domain.AccountsManagerManaged, TargetAccountID: "amc_b", TargetRevision: 8, Policy: domain.SessionInterfaceTransitionInterrupt, Phase: domain.AccountsManagerSwitchWaiting, SourceRuntimeHandleID: "private-runtime", TargetGeneration: "private-generation", SourceNativeConversationID: "private-history", ErrorCode: "private-error", CreatedAt: now, UpdatedAt: now},
		removal:  domain.AccountsManagerRemoval{ID: "removal-a", AccountID: "amc_a", Phase: domain.AccountsManagerRemovalRequested, ErrorCode: "private-error", Impact: domain.AccountsManagerRemovalImpact{Revision: 9, Sessions: []domain.AccountsManagerRemovalSession{{SessionID: "session-a", Provider: domain.AccountsManagerProviderCodex, BindingRevision: 7, RuntimeHandleID: "private-runtime"}, {SessionID: "dormant-a", Provider: domain.AccountsManagerProviderCodex, BindingRevision: 3, Stopped: true}}}, CreatedAt: now, UpdatedAt: now},
	}
}

func accountControlRequest(t *testing.T, controls AccountsManagerControls, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	controller := AccountsManagerController{Controls: controls}
	router.Route("/api/v1", controller.Register)
	req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
	req.Header.Set(middleware.RequestIDHeader, "control-test-request")
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	for _, private := range []string{"private-runtime", "private-generation", "private-history", "private-error", "secret-token", "127.0.0.1:54321"} {
		if strings.Contains(res.Body.String(), private) {
			t.Errorf("private control state in public response: %s", private)
		}
	}
	return res
}

func TestAccountControlBindingSeparatesCurrentAndPending(t *testing.T) {
	f := accountControlsFixture()
	res := accountControlRequest(t, f, http.MethodGet, "/sessions/session-a/account", "")
	var body AccountsManagerSessionResponse
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &body) != nil {
		t.Fatalf("binding=%d %s", res.Code, res.Body.String())
	}
	if body.AccountID != "amc_a" || body.Revision != 7 || !body.Blocked || body.Switch == nil || body.Switch.TargetAccountID != "amc_b" || body.Switch.Phase != "waiting" {
		t.Fatalf("current account confused with pending: %+v", body)
	}
}

func TestAccountControlSwitchDelegatesExplicitChoice(t *testing.T) {
	for _, policy := range []string{"drain", "interrupt"} {
		t.Run(policy, func(t *testing.T) {
			f := accountControlsFixture()
			f.switchOp.Policy = domain.SessionInterfaceTransitionPolicy(policy)
			body := `{"operationId":"switch-a","expectedRevision":7,"mode":"managed","accountId":"amc_b","policy":"` + policy + `","newConversation":true}`
			res := accountControlRequest(t, f, http.MethodPost, "/sessions/session-a/account-switches", body)
			if res.Code != http.StatusAccepted || len(f.calls) != 1 || f.calls[0] != "start-switch:session-a" {
				t.Fatalf("delegation=%d %s calls=%v", res.Code, res.Body.String(), f.calls)
			}
			want := domain.AccountsManagerSwitch{ID: "switch-a", SessionID: "session-a", SourceRevision: 7, TargetMode: domain.AccountsManagerManaged, TargetAccountID: "amc_b", Policy: domain.SessionInterfaceTransitionPolicy(policy), NewConversation: true}
			if !reflect.DeepEqual(f.switchInput, want) {
				t.Fatalf("choice changed: got %+v want %+v", f.switchInput, want)
			}
			var operation AccountsManagerSwitchResponse
			if json.Unmarshal(res.Body.Bytes(), &operation) != nil || operation.Phase != "waiting" {
				t.Fatal("accepted operation must not become ready in the response")
			}
		})
	}
}

func TestAccountControlSwitchRejectsAmbiguousInput(t *testing.T) {
	valid := `{"operationId":"switch-a","expectedRevision":7,"mode":"managed","accountId":"amc_b","policy":"interrupt"}`
	for name, body := range map[string]string{
		"missing target":        strings.Replace(valid, `,"accountId":"amc_b"`, "", 1),
		"missing mode":          strings.Replace(valid, `,"mode":"managed"`, "", 1),
		"missing policy":        strings.Replace(valid, `,"policy":"interrupt"`, "", 1),
		"unselected default":    strings.Replace(valid, `"amc_b"`, `""`, 1),
		"native with account":   strings.Replace(valid, `"managed"`, `"native"`, 1),
		"missing revision":      strings.Replace(valid, `"expectedRevision":7,`, "", 1),
		"zero revision":         strings.Replace(valid, `:7`, `:0`, 1),
		"unsafe revision":       strings.Replace(valid, `:7`, `:9007199254740992`, 1),
		"unknown field":         strings.TrimSuffix(valid, "}") + `,"token":"secret-token"}`,
		"duplicate target":      strings.TrimSuffix(valid, "}") + `,"accountId":"amc_a"}`,
		"case duplicate target": strings.TrimSuffix(valid, "}") + `,"AccountId":"amc_a"}`,
		"second object":         valid + "{}",
		"null":                  "null",
		"invalid operation":     strings.Replace(valid, `"switch-a"`, `"../switch-a"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			f := accountControlsFixture()
			res := accountControlRequest(t, f, http.MethodPost, "/sessions/session-a/account-switches", body)
			if res.Code != http.StatusBadRequest || len(f.calls) != 0 {
				t.Fatalf("invalid choice reached service: status=%d calls=%v body=%s", res.Code, f.calls, res.Body.String())
			}
		})
	}
	f := accountControlsFixture()
	res := accountControlRequest(t, f, http.MethodPost, "/sessions/session-a/account-switches", strings.Repeat(" ", 4097))
	if res.Code != http.StatusRequestEntityTooLarge || len(f.calls) != 0 {
		t.Fatalf("unbounded input: %d %v", res.Code, f.calls)
	}
}

func TestAccountControlSwitchRecoveryStaysSessionScoped(t *testing.T) {
	for _, action := range []string{"retry", "cancel"} {
		for _, foreign := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/foreign=%t", action, foreign), func(t *testing.T) {
				f := accountControlsFixture()
				if foreign {
					f.switchOp.SessionID = "session-b"
				}
				res := accountControlRequest(t, f, http.MethodPost, "/sessions/session-a/account-switches/switch-a/"+action, "{}")
				wantStatus, wantCalls := http.StatusAccepted, []string{"switch:session-a:switch-a", action + "-switch:session-a:switch-a"}
				if foreign {
					wantStatus, wantCalls = http.StatusNotFound, wantCalls[:1]
				}
				if res.Code != wantStatus || !reflect.DeepEqual(f.calls, wantCalls) {
					t.Fatalf("recovery=%d calls=%v", res.Code, f.calls)
				}
			})
		}
	}
}

func TestAccountControlRemovalRequiresObservedRevision(t *testing.T) {
	for name, body := range map[string]string{
		"missing": `{"operationId":"removal-a","confirmed":true}`,
		"null":    `{"operationId":"removal-a","expectedRevision":null,"confirmed":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := accountControlsFixture()
			res := accountControlRequest(t, f, http.MethodPost, "/accounts-manager/accounts/amc_a/removals", body)
			if res.Code != http.StatusBadRequest || len(f.calls) != 0 {
				t.Fatalf("unobserved removal revision reached service: status=%d calls=%v", res.Code, f.calls)
			}
		})
	}
}

func TestAccountControlRemovalImpactAndConfirmation(t *testing.T) {
	f := accountControlsFixture()
	res := accountControlRequest(t, f, http.MethodGet, "/accounts-manager/accounts/amc_a/removal-impact", "")
	var impact AccountsManagerRemovalImpactResponse
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &impact) != nil || len(impact.Sessions) != 2 || impact.Revision != 9 {
		t.Fatalf("impact=%d %s", res.Code, res.Body.String())
	}
	res = accountControlRequest(t, f, http.MethodPost, "/accounts-manager/accounts/amc_a/removals", `{"operationId":"removal-a","expectedRevision":9,"confirmed":true}`)
	if res.Code != http.StatusAccepted || f.calls[1] != "start-removal:removal-a:amc_a:9:true" {
		t.Fatalf("removal=%d %s calls=%v", res.Code, res.Body.String(), f.calls)
	}
	f = accountControlsFixture()
	res = accountControlRequest(t, f, http.MethodPost, "/accounts-manager/accounts/amc_a/removals", `{"operationId":"removal-a","expectedRevision":9,"confirmed":false}`)
	if res.Code != http.StatusBadRequest || len(f.calls) != 0 {
		t.Fatal("unconfirmed removal reached service")
	}
}

func TestAccountControlRemovalAcceptsExplicitZero(t *testing.T) {
	f := accountControlsFixture()
	f.removal.Impact.Revision = 0
	f.removal.Impact.Sessions = nil
	res := accountControlRequest(t, f, http.MethodPost, "/accounts-manager/accounts/amc_a/removals", `{"operationId":"removal-a","expectedRevision":0,"confirmed":true}`)
	if res.Code != http.StatusAccepted || len(f.calls) != 1 || f.calls[0] != "start-removal:removal-a:amc_a:0:true" {
		t.Fatalf("explicit zero must reach authoritative admission: status=%d calls=%v", res.Code, f.calls)
	}
}

func TestAccountControlErrorsAreSafeAndCorrelated(t *testing.T) {
	for name, test := range map[string]struct {
		err    error
		status int
	}{
		"conflict":    {domain.ErrAccountsManagerBindingConflict, http.StatusConflict},
		"missing":     {apierr.NotFound("internal", "secret-token"), http.StatusNotFound},
		"unsupported": {apierr.NotImplemented("internal", "secret-token"), http.StatusNotImplemented},
		"unavailable": {apierr.Unavailable("internal", "secret-token"), http.StatusServiceUnavailable},
		"unknown":     {errors.New("secret-token"), http.StatusBadGateway},
	} {
		t.Run(name, func(t *testing.T) {
			f := accountControlsFixture()
			f.err = fmt.Errorf("private-error: %w", test.err)
			res := accountControlRequest(t, f, http.MethodGet, "/sessions/session-a/account", "")
			var body struct {
				RequestID string `json:"requestId"`
			}
			if res.Code != test.status || json.Unmarshal(res.Body.Bytes(), &body) != nil || body.RequestID != "control-test-request" {
				t.Fatalf("error=%d %s", res.Code, res.Body.String())
			}
		})
	}
}
