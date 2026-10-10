package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAccountHTTPRefusalsPreserveRequestIDWithoutExposingJoinedPrivateFailures(t *testing.T) {
	errorsToTest := []struct {
		cause  error
		status int
		code   string
	}{
		{ports.ErrProviderLoginUnknown, http.StatusNotFound, "PROVIDER_LOGIN_NOT_FOUND"},
		{ports.ErrProviderLoginCallbackBusy, http.StatusConflict, "PROVIDER_LOGIN_CALLBACK_BUSY"},
		{ports.ErrProviderAccountConflict, http.StatusConflict, "PROVIDER_ACCOUNT_CONFLICT"},
		{ports.ErrProviderAccountBusy, http.StatusConflict, "PROVIDER_ACCOUNT_IN_USE"},
		{ports.ErrProviderPrimaryRequired, http.StatusConflict, "PROVIDER_PRIMARY_REQUIRED"},
		{ports.ErrProviderAccountUnknown, http.StatusNotFound, "PROVIDER_ACCOUNT_NOT_FOUND"},
		{ports.ErrProviderLoginRequired, http.StatusConflict, "PROVIDER_LOGIN_REQUIRED"},
		{ports.ErrProviderAccountIncompatible, http.StatusBadRequest, "PROVIDER_ACCOUNT_INCOMPATIBLE"},
		{ports.ErrProviderAccountRecovery, http.StatusConflict, "PROVIDER_ACCOUNT_RECOVERY_REQUIRED"},
	}
	routes := []struct{ method, path, body string }{
		{http.MethodPut, "/provider-accounts/a/primary", ""},
		{http.MethodPost, "/provider-accounts/a/sign-out", `{}`},
		{http.MethodDelete, "/provider-accounts/a", `{}`},
		{http.MethodPut, "/sessions/s/provider-account", `{"accountId":"a"}`},
		{http.MethodPost, "/provider-accounts/login", `{"provider":"codex"}`},
		{http.MethodGet, "/provider-accounts/login/attempt", ""},
		{http.MethodDelete, "/provider-accounts/login/attempt", ""},
	}
	for _, failure := range errorsToTest {
		for _, route := range routes {
			t.Run(failure.code+"/"+route.method+route.path, func(t *testing.T) {
				private := errors.New("access_token=PRIVATE-TOKEN credential=/private/account-secret.json oauth_state=PRIVATE-STATE")
				joined := errors.Join(fmt.Errorf("operation metadata PRIVATE-METADATA: %w", failure.cause), private)
				accounts := &accountHTTPFake{err: joined}
				login := &loginHTTPFake{err: joined}
				controller := &controllers.ProviderAccountsController{Svc: accounts, Login: login}
				router := chi.NewRouter()
				router.Use(middleware.RequestID)
				controller.Register(router)
				request := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
				request.Header.Set("X-Request-ID", "account-operation-request")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if response.Code != failure.status {
					t.Fatalf("expected refusal %d, got %d: %s", failure.status, response.Code, response.Body.String())
				}
				var envelope map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope["code"] != failure.code || envelope["message"] != failure.cause.Error() {
					t.Fatalf("error contract changed: %v", envelope)
				}
				if envelope["requestId"] != "account-operation-request" {
					t.Fatalf("request identity was lost: %v", envelope)
				}
				for _, secret := range []string{"PRIVATE-TOKEN", "PRIVATE-METADATA", "PRIVATE-STATE", "account-secret.json", "access_token", "oauth_state"} {
					if strings.Contains(response.Body.String(), secret) {
						t.Fatalf("error envelope leaked %s", secret)
					}
				}
				if len(accounts.calls)+len(login.calls) != 1 {
					t.Fatal("refused operation executed a follow-up read or mutation")
				}
			})
		}
	}
}

func TestAccountHTTPOnlyAcceptsOneBoundedJSONObject(t *testing.T) {
	cases := []struct{ name, body string }{
		{"null", `null`},
		{"array", `[]`},
		{"string", `"codex"`},
		{"number", `123`},
		{"boolean", `true`},
		{"trailing-object", `{} {}`},
		{"trailing-array", `{} []`},
		{"trailing-null", `{} null`},
		{"trailing-token", `{} trailing-secret-token`},
		{"utf8-bom", "\ufeff{}"},
		{"wrong-account-type", `{"accountId":123}`},
		{"wrong-replacement-type", `{"replacementPrimaryId":[]}`},
		{"extra-nested-field", `{"private":{"access_token":"PRIVATE-TOKEN"}}`},
		{"oversized-account-value", `{"accountId":"` + strings.Repeat("a", 1<<20) + `"}`},
		{"oversized-whitespace-after-object", `{}` + strings.Repeat(" ", 1<<20)},
		{"hidden-token-after-limit", `{}` + strings.Repeat(" ", 1<<20) + `PRIVATE-TOKEN`},
	}
	for _, route := range []struct{ method, path string }{{http.MethodPost, "/provider-accounts/a/sign-out"}, {http.MethodDelete, "/provider-accounts/a"}, {http.MethodPut, "/sessions/s/provider-account"}, {http.MethodPost, "/provider-accounts/login"}} {
		for _, input := range cases {
			t.Run(route.method+route.path+"/"+input.name, func(t *testing.T) {
				accounts := &accountHTTPFake{}
				login := &loginHTTPFake{}
				response := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: accounts, Login: login}, route.method, route.path, input.body)
				if response.Code != http.StatusBadRequest {
					t.Fatalf("non-object or oversized request accepted: %d", response.Code)
				}
				if len(accounts.calls) != 0 || len(login.calls) != 0 {
					t.Fatal("invalid body reached the account service")
				}
				if strings.Contains(response.Body.String(), "PRIVATE-TOKEN") || strings.Contains(response.Body.String(), "trailing-secret-token") {
					t.Fatal("body validation echoed untrusted private data")
				}
				if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
					t.Fatal("validation did not preserve the API error envelope")
				}
			})
		}
	}
}

func TestAccountHTTPExactlyBoundedValidObjectDoesNotTruncateFields(t *testing.T) {
	accountID := "chosen-private-account-id"
	body := `{"accountId":"` + accountID + `"}`
	body += strings.Repeat(" ", (1<<20)-len(body))
	accounts := &accountHTTPFake{managed: true}
	response := accountHTTPRequest(t, &controllers.ProviderAccountsController{Svc: accounts}, http.MethodPut, "/sessions/s/provider-account", body)
	if response.Code != http.StatusOK {
		t.Fatalf("bounded valid body refused: %d %s", response.Code, response.Body.String())
	}
	if len(accounts.calls) != 2 || accounts.calls[0] != "switch:s:"+accountID || accounts.calls[1] != "session:s" {
		t.Fatalf("full account choice was not passed exactly once: %v", accounts.calls)
	}
}

type brokenAccountBody struct {
	remaining string
	failure   error
}

func (b *brokenAccountBody) Read(output []byte) (int, error) {
	if b.remaining == "" {
		return 0, b.failure
	}
	n := copy(output, b.remaining)
	b.remaining = b.remaining[n:]
	return n, nil
}
func (*brokenAccountBody) Close() error { return nil }

func TestAccountHTTPReadFailureNeverAdmitsPartiallyReceivedMutation(t *testing.T) {
	for _, failure := range []error{context.Canceled, errors.New("transport PRIVATE-FAILURE")} {
		t.Run(failure.Error(), func(t *testing.T) {
			accounts := &accountHTTPFake{}
			controller := &controllers.ProviderAccountsController{Svc: accounts}
			router := chi.NewRouter()
			controller.Register(router)
			request := httptest.NewRequest(http.MethodPut, "/sessions/s/provider-account", nil)
			request.Body = &brokenAccountBody{remaining: `{"accountId":"a"}`, failure: failure}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || len(accounts.calls) != 0 {
				t.Fatal("partial transport failure admitted a session switch")
			}
			if strings.Contains(response.Body.String(), "PRIVATE-FAILURE") {
				t.Fatal("request reader exposed transport details")
			}
		})
	}
}
