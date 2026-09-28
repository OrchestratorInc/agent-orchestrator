package runner

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestExactRouteSelectorRejectsMalformedCachedClaims(t *testing.T) {
	for name, value := range map[string]any{"text": "unexpected", "number": 1, "bare_claims": routeClaims{}, "typed_nil": (*routeClaimsEntry)(nil)} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Error("malformed cached claims panicked instead of rejecting admission")
				}
			}()
			capability, err := newRouteCapability(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			capability.claims.Store("scope", value)
			selector := newExactRouteSelector(capability)
			admissions := 0
			selector.admit = func(context.Context, *coreauth.Auth) bool { admissions++; return true }
			picked, err := selector.Pick(t.Context(), "codex", "model", coreexecutor.Options{Metadata: map[string]any{coreexecutor.CallerScopeMetadataKey: "scope"}},
				[]*coreauth.Auth{{Index: "unrelated", Provider: "codex", Status: coreauth.StatusActive}})
			if !errors.Is(err, errPinnedAccountUnavailable) || picked != nil || admissions != 0 {
				t.Fatal("malformed cached claims admitted an account or changed the failure category")
			}
		})
	}
}

type checkCloseFailure struct{ io.Reader }

func (checkCloseFailure) Close() error { return errors.New("private transport detail") }

func TestCredentialCheckCloseFailureDoesNotReturnVerifiedPayload(t *testing.T) {
	runtime := &credentialRuntime{checkTransport: runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: request,
			Body: checkCloseFailure{Reader: strings.NewReader(`{"data":[{"id":"model"}]}`)}}, nil
	})}
	data, err := runtime.credentialCheck(t.Context(), "https://provider.example.test/models", make(http.Header))
	if !errors.Is(err, errCredentialCheckUnavailable) || len(data) != 0 {
		t.Fatal("failed transport cleanup returned a verification payload")
	}
	if strings.Contains(err.Error(), "private transport detail") {
		t.Fatal("transport cleanup exposed private details")
	}
}

func TestOAuthResponseCloseFailureDoesNotReportSuccess(t *testing.T) {
	client := &http.Client{Transport: runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Request: request,
			Body: checkCloseFailure{Reader: strings.NewReader(`{"status":"ok"}`)}}, nil
	})}
	coordinator := newOAuthCoordinator("http://127.0.0.1:1", "synthetic-management", client, nil)
	defer coordinator.Close()
	if err := coordinator.upstreamJSON(t.Context(), http.MethodGet, "/fixture", nil, nil); err == nil || strings.Contains(err.Error(), "private transport detail") {
		t.Fatal("transport cleanup reported success or exposed private details")
	}
}

type browserCloseFailure struct{ net.Listener }

func (l browserCloseFailure) Close() error {
	_ = l.Listener.Close()
	return errors.New("private listener detail")
}

func TestCredentialBrowserClosesListenerBeforeCommit(t *testing.T) {
	vault := newTestVault(t)
	manager := coreauth.NewManager(vault, nil, nil)
	runtime := &credentialRuntime{vault: vault, manager: manager}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	authenticator := memoryAuthenticator{login: func(context.Context, *sdkconfig.Config, *sdkauth.LoginOptions) (*coreauth.Auth, error) {
		return vaultFixture(), nil
	}}
	auth, err := runtime.ConnectBrowser(t.Context(), "close-before-commit", authenticator, &sdkconfig.Config{}, browserCloseFailure{listener}, func(context.Context, string) error { return nil })
	if auth != nil || !errors.Is(err, errCredentialStorage) || strings.Contains(err.Error(), "private listener detail") {
		t.Fatal("listener failure returned a successful credential or unsafe error")
	}
	records, readErr := vault.List(t.Context())
	if readErr != nil || len(records) != 0 || len(manager.List()) != 0 {
		t.Fatal("listener close failure committed or published a credential")
	}
	if _, err := vault.Commit(t.Context(), "close-before-commit", vaultFixture()); !errors.Is(err, errCredentialFenced) {
		t.Fatal("failed listener cleanup left a committable login")
	}
}
