package runner

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	sdkapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/api"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

// These pinned-SDK diagnostics establish which paths a store override cannot protect.
func TestPinnedSDKCredentialBoundaries(t *testing.T) {
	t.Run("callback transport requires plaintext storage", func(t *testing.T) {
		root := privateVaultDir(t)
		state := "vault-boundary-callback"
		sdkapi.RegisterOAuthSession(state, "codex")
		t.Cleanup(func() { sdkapi.CompleteOAuthSession(state) })
		path, err := sdkapi.WriteOAuthCallbackFileForPendingSession(root, "codex", state, "callback-vault-secret", "")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil || !bytes.Contains(raw, []byte("callback-vault-secret")) {
			t.Fatalf("pinned callback transport changed; re-review integration: %v", err)
		}
	})
	t.Run("import bypasses store", func(t *testing.T) {
		root := privateVaultDir(t)
		vault := newTestVault(t)
		manager := coreauth.NewManager(vault, nil, nil)
		var handler sdkapi.Handler
		handler.SetConfig(&sdkconfig.Config{AuthDir: root})
		handler.SetAuthManager(manager)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/auth-files?name=boundary.json", bytes.NewBufferString(`{"type":"codex","access_token":"import-vault-secret"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		handler.UploadAuthFile(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("import diagnostic returned %d", recorder.Code)
		}
		raw, err := os.ReadFile(filepath.Join(root, "boundary.json"))
		if err != nil || !bytes.Contains(raw, []byte("import-vault-secret")) {
			t.Fatalf("pinned import path changed; re-review integration: %v", err)
		}
		items, err := vault.List(context.Background())
		if err != nil || len(items) != 0 {
			t.Fatalf("unregistered import entered vault: count=%d err=%v", len(items), err)
		}
	})
	t.Run("runtime update ignores rejected persistence", func(t *testing.T) {
		ctx := context.Background()
		vault := newTestVault(t)
		if err := vault.Begin(ctx, "sdk-late-update", "codex", time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		created, err := vault.Commit(ctx, "sdk-late-update", vaultFixture())
		if err != nil {
			t.Fatal(err)
		}
		manager := coreauth.NewManager(vault, nil, nil)
		if err := manager.Load(ctx); err != nil {
			t.Fatal(err)
		}
		if err := vault.Delete(ctx, created.ID); err != nil {
			t.Fatal(err)
		}
		created.Metadata["access_token"] = "late-vault-secret"
		updated, err := manager.Update(ctx, created)
		if err != nil || updated == nil || updated.Metadata["access_token"] != "late-vault-secret" {
			t.Fatalf("pinned runtime persistence semantics changed; re-review admission: %v", err)
		}
		items, err := vault.List(ctx)
		if err != nil || len(items) != 0 {
			t.Fatalf("late SDK update resurrected durable credential: count=%d err=%v", len(items), err)
		}
	})
}
