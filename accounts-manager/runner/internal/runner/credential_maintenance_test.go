package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCredentialMaintenanceRename(t *testing.T) {
	runtime, previous := trustedCredentialFixture(t, "codex")
	if err := runtime.vault.Begin(t.Context(), "other", "codex", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	other, err := runtime.Complete(t.Context(), "other", vaultFixture())
	if err != nil {
		t.Fatal(err)
	}
	otherSealed := append([]byte(nil), runtime.vault.state.Records[other.ID].Sealed...)
	handler := &credentialHTTP{key: "management", runtime: runtime}
	call := func(label string, generation uint64, want int) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"label": label, "generation": generation})
		req := httptest.NewRequest(http.MethodPatch, credentialPath+"/label?ref="+previous.Index, strings.NewReader(string(raw)))
		req.Header.Set("Authorization", "Bearer management")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("rename status=%d want=%d", res.Code, want)
		}
	}
	call(" Production ", 1, 204)
	call("overwrite", 1, 409)
	call(strings.Repeat("a", 81), 2, 409)
	call("control\nvalue", 2, 409)
	if _, err := runtime.vault.Save(t.Context(), previous); !errors.Is(err, errCredentialFenced) {
		t.Fatal("stale refresh reverted label")
	}
	updated, exists := runtime.manager.GetByID(previous.ID)
	if !exists || updated.Label != "Production" || updated.Index != previous.Index || !runtime.vault.Admit(t.Context(), updated) {
		t.Fatal("rename changed route identity or registration")
	}
	updated.Label = "SDK-owned label"
	if _, err := runtime.vault.Save(t.Context(), updated); err != nil {
		t.Fatal(err)
	}
	auths, err := runtime.vault.List(t.Context())
	if err != nil || len(auths) != 2 {
		t.Fatal("provider refresh overwrote user label")
	}
	for _, auth := range auths {
		if auth.ID == previous.ID && auth.Label != "Production" {
			t.Fatal("provider refresh overwrote user label")
		}
	}
	if !bytes.Equal(otherSealed, runtime.vault.state.Records[other.ID].Sealed) {
		t.Fatal("rename rewrote another account")
	}
	root := runtime.vault.root.Name()
	if err := runtime.vault.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openCredentialVault(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	auths, err = reopened.List(t.Context())
	if err != nil || len(auths) != 2 {
		t.Fatal("label was not durable")
	}
	for _, auth := range auths {
		if auth.ID == previous.ID && auth.Label != "Production" {
			t.Fatal("label was not durable")
		}
	}
}
