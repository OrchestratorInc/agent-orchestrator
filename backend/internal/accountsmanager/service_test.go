package accountsmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestServiceRoutesMirroredNativeCodexAccount(t *testing.T) {
	dataDir := t.TempDir()
	nativeRoot := filepath.Join(dataDir, "native")
	nativeID := "native-account-1"
	nativeDir := filepath.Join(nativeRoot, nativeID, "credential-home")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatalf("create native account: %v", err)
	}
	credential := map[string]any{
		"tokens": map[string]any{
			"account_id":    "account-provider-id",
			"access_token":  "test-token",
			"refresh_token": "refresh-token",
		},
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nativeDir, "auth.json"), raw, 0o600); err != nil {
		t.Fatalf("write native credential: %v", err)
	}

	service, err := New(Options{DataDir: dataDir, NativeAccountRoot: nativeRoot})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = service.Close(context.Background()) }()

	route, err := service.RouteForSession(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("RouteForSession: %v", err)
	}
	if route.BaseURL == "" || route.Token == "" || route.TokenEnv == "" {
		t.Fatalf("incomplete route: %+v", route)
	}
	if got := service.externalAccountID("ao-native-" + nativeID + ".json"); got != nativeID {
		t.Fatalf("external account id = %q, want %q", got, nativeID)
	}
	if got, ok := service.routes.accountForSession("session-1"); !ok || got != "ao-native-"+nativeID+".json" {
		t.Fatalf("persisted proxy pin = (%q, %t)", got, ok)
	}
	mirrored, err := os.ReadFile(filepath.Join(service.authDir, "ao-native-"+nativeID+".json"))
	if err != nil {
		t.Fatalf("read mirrored credential: %v", err)
	}
	var proxyCredential map[string]string
	if err := json.Unmarshal(mirrored, &proxyCredential); err != nil {
		t.Fatalf("decode mirrored credential: %v", err)
	}
	if proxyCredential["type"] != "codex" || proxyCredential["access_token"] != "test-token" || proxyCredential["account_id"] != "account-provider-id" {
		t.Fatalf("mirrored native credential was not converted for CLIProxyAPI: %#v", proxyCredential)
	}

	selected, err := service.SwitchSessionAccount(context.Background(), "session-1", nativeID)
	if err != nil {
		t.Fatalf("SwitchSessionAccount: %v", err)
	}
	if selected != nativeID {
		t.Fatalf("selected account = %q, want %q", selected, nativeID)
	}
}

func TestServicePreservesRouteAcrossReconstructionAndRevokesIt(t *testing.T) {
	dataDir := t.TempDir()
	nativeRoot := filepath.Join(dataDir, "native")
	nativeDir := filepath.Join(nativeRoot, "native-account-1", "credential-home")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatalf("create native account: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nativeDir, "auth.json"), []byte(`{"type":"codex","access_token":"test-token"}`), 0o600); err != nil {
		t.Fatalf("write native credential: %v", err)
	}

	first, err := New(Options{DataDir: dataDir, NativeAccountRoot: nativeRoot})
	if err != nil {
		t.Fatalf("New(first): %v", err)
	}
	firstRoute, err := first.RouteForSession(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("RouteForSession(first): %v", err)
	}
	if _, authErr := first.capability.Authenticate(context.Background(), httptestRequestWithToken(firstRoute.Token)); authErr != nil {
		t.Fatalf("first route authentication: %v", authErr)
	}
	firstPort := first.baseURL
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("Close(first): %v", err)
	}

	second, err := New(Options{DataDir: dataDir, NativeAccountRoot: nativeRoot})
	if err != nil {
		t.Fatalf("New(second): %v", err)
	}
	defer func() { _ = second.Close(context.Background()) }()
	if second.baseURL != firstPort {
		t.Fatalf("proxy endpoint changed across reconstruction: first=%q second=%q", firstPort, second.baseURL)
	}
	if _, authErr := second.capability.Authenticate(context.Background(), httptestRequestWithToken(firstRoute.Token)); authErr != nil {
		t.Fatalf("surviving route authentication after reconstruction: %v", authErr)
	}

	if err := second.RevokeSession(context.Background(), "session-1"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, authErr := second.capability.Authenticate(context.Background(), httptestRequestWithToken(firstRoute.Token)); authErr == nil {
		t.Fatal("revoked route bearer was accepted")
	}
	if _, ok := second.routes.accountForSession("session-1"); ok {
		t.Fatal("revoked session pin remained durable")
	}
}

func TestServiceGlobalSwitchPreservesExistingAndPinsFutureSessions(t *testing.T) {
	dataDir := t.TempDir()
	nativeRoot := filepath.Join(dataDir, "native")
	for _, accountID := range []string{"native-account-1", "native-account-2"} {
		nativeDir := filepath.Join(nativeRoot, accountID, "credential-home")
		if err := os.MkdirAll(nativeDir, 0o700); err != nil {
			t.Fatalf("create native account %s: %v", accountID, err)
		}
		credential := []byte(`{"type":"codex","access_token":"` + accountID + `"}`)
		if err := os.WriteFile(filepath.Join(nativeDir, "auth.json"), credential, 0o600); err != nil {
			t.Fatalf("write native credential %s: %v", accountID, err)
		}
	}

	service, err := New(Options{DataDir: dataDir, NativeAccountRoot: nativeRoot})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = service.Close(context.Background()) }()
	for _, sessionID := range []string{"session-1", "session-2"} {
		if _, err := service.RouteForSession(context.Background(), sessionID); err != nil {
			t.Fatalf("RouteForSession(%s): %v", sessionID, err)
		}
	}
	selected, err := service.SwitchAllSessionsAccount(context.Background(), "native-account-2")
	if err != nil {
		t.Fatalf("SwitchAllSessionsAccount: %v", err)
	}
	if selected != "native-account-2" {
		t.Fatalf("selected account = %q, want native-account-2", selected)
	}
	for _, sessionID := range []string{"session-1", "session-2"} {
		if got, ok := service.routes.accountForSession(sessionID); !ok || got != "ao-native-native-account-1.json" {
			t.Fatalf("session %s pin = (%q, %t), want account 1", sessionID, got, ok)
		}
	}
	if _, err := service.RouteForSession(context.Background(), "session-3"); err != nil {
		t.Fatalf("RouteForSession(session-3): %v", err)
	}
	if got, ok := service.routes.accountForSession("session-3"); !ok || got != "ao-native-native-account-2.json" {
		t.Fatalf("future session pin = (%q, %t), want account 2", got, ok)
	}
	if _, err := service.SetRoutingPolicy(context.Background(), true, []string{"native-account-1"}); err != nil {
		t.Fatalf("SetRoutingPolicy: %v", err)
	}
	if _, err := service.RouteForSession(context.Background(), "session-4"); err != nil {
		t.Fatalf("RouteForSession(session-4): %v", err)
	}
	if got, ok := service.routes.accountForSession("session-4"); !ok || got != "ao-native-native-account-1.json" {
		t.Fatalf("preferred route pin = (%q, %t), want account 1", got, ok)
	}
}

func TestServiceRemovesMirrorsWhenNativeAccountRootDisappears(t *testing.T) {
	dataDir := t.TempDir()
	nativeRoot := filepath.Join(dataDir, "native")
	nativeID := "native-account-1"
	nativeDir := filepath.Join(nativeRoot, nativeID, "credential-home")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatalf("create native account: %v", err)
	}
	credential := []byte(`{"type":"codex","access_token":"test-token"}`)
	if err := os.WriteFile(filepath.Join(nativeDir, "auth.json"), credential, 0o600); err != nil {
		t.Fatalf("write native credential: %v", err)
	}

	service, err := New(Options{DataDir: dataDir, NativeAccountRoot: nativeRoot})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = service.Close(context.Background()) }()
	if _, err := service.RouteForSession(context.Background(), "session-1"); err != nil {
		t.Fatalf("RouteForSession: %v", err)
	}
	mirror := filepath.Join(service.authDir, "ao-native-"+nativeID+".json")
	if _, err := os.Stat(mirror); err != nil {
		t.Fatalf("stat mirrored credential: %v", err)
	}
	if err := os.RemoveAll(nativeRoot); err != nil {
		t.Fatalf("remove native account root: %v", err)
	}
	if err := service.refreshAccounts(context.Background()); err != nil {
		t.Fatalf("refreshAccounts after root removal: %v", err)
	}
	if _, err := os.Stat(mirror); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mirrored credential stat error = %v, want not exists", err)
	}
	service.nativeMu.Lock()
	defer service.nativeMu.Unlock()
	if len(service.nativeRefs) != 0 {
		t.Fatalf("native refs after root removal = %#v, want empty", service.nativeRefs)
	}
}

func TestServiceDoesNotOverwriteRefreshedMirrorWhenNativeCredentialIsUnchanged(t *testing.T) {
	dataDir := t.TempDir()
	nativeRoot := filepath.Join(dataDir, "native")
	nativeID := "native-account-1"
	nativeDir := filepath.Join(nativeRoot, nativeID, "credential-home")
	if err := os.MkdirAll(nativeDir, 0o700); err != nil {
		t.Fatalf("create native account: %v", err)
	}
	source := []byte(`{"type":"codex","access_token":"source-token"}`)
	if err := os.WriteFile(filepath.Join(nativeDir, "auth.json"), source, 0o600); err != nil {
		t.Fatalf("write native credential: %v", err)
	}
	service, err := New(Options{DataDir: dataDir, NativeAccountRoot: nativeRoot})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = service.Close(context.Background()) }()

	mirror := filepath.Join(service.authDir, "ao-native-"+nativeID+".json")
	refreshed := []byte(`{"type":"codex","access_token":"refreshed-token"}`)
	if err := os.WriteFile(mirror, refreshed, 0o600); err != nil {
		t.Fatalf("write refreshed mirror: %v", err)
	}
	if err := service.syncNativeAccounts(); err != nil {
		t.Fatalf("syncNativeAccounts: %v", err)
	}
	got, err := os.ReadFile(mirror)
	if err != nil {
		t.Fatalf("read mirrored credential: %v", err)
	}
	if string(got) != string(refreshed) {
		t.Fatalf("mirror = %s, want refreshed credential %s", got, refreshed)
	}
}

func TestServiceManagesCodexAPIKeyCredential(t *testing.T) {
	service, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = service.Close(context.Background()) }()

	snapshot, err := service.AddAPIKey(context.Background(), APIKeyInput{Key: "sk-test", Label: "work"})
	if err != nil {
		t.Fatalf("AddAPIKey: %v", err)
	}
	if len(snapshot.Accounts) != 1 || snapshot.Accounts[0].Kind != "api_key" {
		t.Fatalf("accounts after add = %#v, want one api-key account", snapshot.Accounts)
	}
	id := snapshot.Accounts[0].ID
	snapshot, err = service.SetAccountDisabled(context.Background(), id, true)
	if err != nil {
		t.Fatalf("SetAccountDisabled: %v", err)
	}
	if len(snapshot.Accounts) != 1 || !snapshot.Accounts[0].Disabled {
		t.Fatalf("accounts after disable = %#v, want disabled account", snapshot.Accounts)
	}
	snapshot, err = service.RemoveAccount(context.Background(), id)
	if err != nil {
		t.Fatalf("RemoveAccount: %v", err)
	}
	if len(snapshot.Accounts) != 0 {
		t.Fatalf("accounts after remove = %#v, want empty", snapshot.Accounts)
	}
}

func TestProxyCodexCredentialRejectsEmptyCodexDocuments(t *testing.T) {
	if _, ok := proxyCodexCredential([]byte(`{"type":"codex"}`)); ok {
		t.Fatal("proxyCodexCredential accepted a credential without tokens")
	}
}
