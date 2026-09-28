package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	coresession "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/session"
)

func TestBindingsRevokeOnlyChangedSessionAndCachedSelectorScope(t *testing.T) {
	capability, err := newRouteCapability(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a := routeClaims{Provider: "codex", AuthIndex: "a", SessionID: "first", AccountID: "public-a", BindingRevision: 1}
	b := routeClaims{Provider: "codex", AuthIndex: "b", SessionID: "second", AccountID: "public-b", BindingRevision: 1}
	if _, err := capability.Mint(a); err == nil {
		t.Fatal("unreconciled registry minted token")
	}
	snapshot := routeBindingSnapshot{Revision: 1, Bindings: []routeBinding{
		{SessionID: a.SessionID, Provider: a.Provider, Mode: "managed", AccountID: a.AccountID, Revision: 1},
		{SessionID: b.SessionID, Provider: b.Provider, Mode: "managed", AccountID: b.AccountID, Revision: 1},
	}}
	if err := capability.Reconcile(snapshot); err != nil {
		t.Fatal(err)
	}
	tokenA, err := capability.Mint(a)
	if err != nil {
		t.Fatal(err)
	}
	tokenB, err := capability.Mint(b)
	if err != nil {
		t.Fatal(err)
	}
	request := func(token string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		return r
	}
	result, authErr := capability.Authenticate(t.Context(), request(tokenA))
	if authErr != nil {
		t.Fatal(authErr)
	}
	old := snapshot
	snapshot.Bindings = append([]routeBinding(nil), snapshot.Bindings...)
	snapshot.Revision, snapshot.Bindings[0].Revision, snapshot.Bindings[0].AccountID = 2, 2, b.AccountID
	if err := capability.Reconcile(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := capability.Reconcile(old); err == nil {
		t.Fatal("stale snapshot accepted")
	}
	if _, err := capability.Authenticate(t.Context(), request(tokenA)); err == nil {
		t.Fatal("revoked token authenticated")
	}
	if _, err := capability.Authenticate(t.Context(), request(tokenB)); err != nil {
		t.Fatal("unrelated token revoked")
	}
	selector := newExactRouteSelector(capability)
	selected, err := selector.Pick(t.Context(), "codex", "model", coreexecutor.Options{Metadata: map[string]any{coreexecutor.CallerScopeMetadataKey: coresession.CallerScope(result.Principal)}}, []*coreauth.Auth{{Index: "a", Provider: "codex", Status: coreauth.StatusActive}})
	if err == nil || selected != nil {
		t.Fatal("cached selector scope bypassed revocation")
	}
	a.AccountID, a.AuthIndex, a.BindingRevision = b.AccountID, b.AuthIndex, 2
	if _, err := capability.Mint(a); err != nil {
		t.Fatal(err)
	}
	snapshot.Revision, snapshot.Bindings[0].Revision, snapshot.Bindings[0].Mode, snapshot.Bindings[0].AccountID = 3, 3, "native", ""
	if err := capability.Reconcile(snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := capability.Mint(a); err == nil {
		t.Fatal("native binding authorized managed route")
	}
}

func TestBindingLeaseExpiresAndOnlyCurrentSnapshotRenews(t *testing.T) {
	capability, err := newRouteCapability(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	capability.now = func() time.Time { return now }
	token := mintTestRoute(t, capability, routeClaims{Provider: "codex", AuthIndex: "a", SessionID: "session"})
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	now = now.Add(5 * time.Second)
	if _, err := capability.Authenticate(t.Context(), request); err == nil {
		t.Fatal("expired binding lease authenticated")
	}
	claims, err := capability.open(token)
	if err != nil {
		t.Fatal(err)
	}
	installTestBinding(t, capability, claims)
	if _, err := capability.Authenticate(t.Context(), request); err != nil {
		t.Fatal("current snapshot did not renew lease")
	}
	if err := capability.Reconcile(routeBindingSnapshot{Revision: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := capability.Authenticate(t.Context(), request); err == nil {
		t.Fatal("deleted binding authenticated")
	}
	if err := capability.Reconcile(routeBindingSnapshot{Revision: 1}); err == nil {
		t.Fatal("stale empty registry accepted")
	}
}

func TestDurableSwitchFenceRevokesWithoutChangingSelectedAccount(t *testing.T) {
	capability, _ := newRouteCapability(make([]byte, 32))
	token := mintTestRoute(t, capability, routeClaims{Provider: "codex", AuthIndex: "a", SessionID: "session"})
	claims, err := capability.open(token)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := routeBindingSnapshot{Revision: 2, Bindings: []routeBinding{{SessionID: claims.SessionID, Provider: claims.Provider, Mode: "managed", AccountID: claims.AccountID, Revision: claims.BindingRevision, Blocked: true}}}
	if err := capability.Reconcile(snapshot); err != nil {
		t.Fatal(err)
	}
	if capability.admitsBinding(claims) {
		t.Fatal("stopping operation authorized the old controller")
	}
	if _, err := capability.Mint(claims); err == nil {
		t.Fatal("stopping operation minted another old-controller token")
	}
	snapshot.Bindings[0].Blocked = false
	if err := capability.Reconcile(snapshot); err == nil {
		t.Fatal("unchanged snapshot clock cleared a durable fence")
	}
	snapshot.Revision++
	if err := capability.Reconcile(snapshot); err != nil {
		t.Fatal(err)
	}
	if !capability.admitsBinding(claims) {
		t.Fatal("confirmed recovery could not reopen unchanged source")
	}
}

func TestBindingSnapshotsRejectInvalidOrConflictingIntent(t *testing.T) {
	for _, test := range []struct {
		name     string
		snapshot routeBindingSnapshot
	}{
		{"zero clock", routeBindingSnapshot{}},
		{"native account", routeBindingSnapshot{Revision: 1, Bindings: []routeBinding{{SessionID: "session", Provider: "codex", Mode: "native", AccountID: "a", Revision: 1}}}},
		{"missing account", routeBindingSnapshot{Revision: 1, Bindings: []routeBinding{{SessionID: "session", Provider: "codex", Mode: "managed", Revision: 1}}}},
		{"invalid provider", routeBindingSnapshot{Revision: 1, Bindings: []routeBinding{{SessionID: "session", Provider: "unknown", Mode: "native", Revision: 1}}}},
		{"control character", routeBindingSnapshot{Revision: 1, Bindings: []routeBinding{{SessionID: "session\x00other", Provider: "codex", Mode: "native", Revision: 1}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			capability, err := newRouteCapability(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			if err := capability.Reconcile(test.snapshot); err == nil {
				t.Fatal("invalid registry accepted")
			}
		})
	}
	capability, _ := newRouteCapability(make([]byte, 32))
	claims := routeClaims{SessionID: "session", Provider: "codex", AccountID: "a", BindingRevision: 1}
	installTestBinding(t, capability, claims)
	for _, clock := range []int64{1, 2} {
		snapshot := routeBindingSnapshot{Revision: clock, Bindings: []routeBinding{{SessionID: claims.SessionID, Provider: claims.Provider, Mode: "managed", AccountID: "changed-without-revision", Revision: 1}}}
		if err := capability.Reconcile(snapshot); err == nil {
			t.Fatal("same revision changed account")
		}
	}
}

func TestBindingAuthorizationConcurrentReconciliation(t *testing.T) {
	capability, _ := newRouteCapability(make([]byte, 32))
	claims := routeClaims{Provider: "codex", AuthIndex: "a", SessionID: "session", AccountID: "a", BindingRevision: 1}
	installTestBinding(t, capability, claims)
	token, err := capability.Mint(claims)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
				request.Header.Set("Authorization", "Bearer "+token)
				_, _ = capability.Authenticate(context.Background(), request)
			}
		})
	}
	for revision := int64(2); revision < 50; revision++ {
		if err := capability.Reconcile(routeBindingSnapshot{Revision: revision}); err != nil {
			t.Fatal(err)
		}
	}
	workers.Wait()
	if capability.admitsBinding(claims) {
		t.Fatal("concurrent reader resurrected revoked binding")
	}
}

func TestRouteHandlerRejectsIdentityMismatchAndTrailingJSON(t *testing.T) {
	capability, _ := newRouteCapability(make([]byte, 32))
	claims := routeClaims{Provider: "codex", AuthIndex: "private-a", SessionID: "session", AccountID: "wrong-public-id", BindingRevision: 1}
	installTestBinding(t, capability, claims)
	handler := newRouteTokenHandler("key", "http://127.0.0.1:43127", capability, func(string, string) bool { return true })
	for _, body := range []string{
		`{"provider":"codex","authIndex":"private-a","sessionId":"session","accountId":"wrong-public-id","bindingRevision":1}`,
		`{"revision":2,"bindings":[]} {"revision":1}`,
	} {
		method, path, want := "POST", "/ao/internal/routes/token", http.StatusConflict
		if strings.Contains(body, "bindings") {
			method, path, want = "PUT", "/ao/internal/routes/bindings", http.StatusBadRequest
		}
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer key")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("status=%d want=%d", response.Code, want)
		}
	}
}
