package agentauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeOpenCodeCatalog(t *testing.T) (dataDir string) {
	t.Helper()
	cache := t.TempDir()
	dataDir = t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("OPENCODE_DATA_DIR", dataDir)
	catalog := `{
		"zeta": {"id": "zeta", "name": "Zeta AI", "env": ["ZETA_API_KEY"], "models": {"zeta-2": {}, "zeta-1": {}}},
		"google": {"id": "google", "name": "Google", "env": ["GOOGLE_API_KEY", "GEMINI_API_KEY"], "models": {"gemini-2.5-pro": {}}},
		"azure": {"id": "azure", "name": "Azure", "env": ["AZURE_RESOURCE_NAME", "AZURE_API_KEY"], "models": {}},
		"Bad ID": {"id": "bad", "name": "Bad", "env": ["BAD_API_KEY"], "models": {}}
	}`
	if err := os.MkdirAll(filepath.Join(cache, "opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "opencode", "models.json"), []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func TestOpenCodeCatalogListsKeyOnlyProviders(t *testing.T) {
	writeOpenCodeCatalog(t)
	svc := NewWithAgentResolver(foundExecutable("opencode"), nil, nil, t.TempDir())
	providers, err := svc.CatalogProviders(t.Context(), "opencode")
	if err != nil || len(providers) != 2 || providers[0].ID != "google" || providers[1].ID != "zeta" || providers[1].Models != 2 {
		t.Fatalf("CatalogProviders = %+v, %v", providers, err)
	}
	models, err := svc.CatalogModels(t.Context(), "opencode", "zeta")
	if err != nil || len(models) != 2 || models[0] != "zeta-1" {
		t.Fatalf("CatalogModels = %v, %v", models, err)
	}
	if _, err := svc.CatalogModels(t.Context(), "opencode", "azure"); err == nil {
		t.Fatal("offered a provider that needs more than a key")
	}
	if _, err := svc.CatalogProviders(t.Context(), "goose"); err == nil {
		t.Fatal("listed a catalog for a harness without one")
	}
	methods := loginMethods("opencode")
	if len(methods) != 2 || methods[0].ID != catalogKeyMethod || !methods[0].Secret || methods[1].ID != "native" {
		t.Fatalf("OpenCode methods = %+v", methods)
	}
}

func TestOpenCodeCatalogKeyIsSavedInNativeStore(t *testing.T) {
	dataDir := writeOpenCodeCatalog(t)
	svc := NewWithAgentResolver(foundExecutable("opencode"), nil, nil, t.TempDir())
	login, err := svc.StartLoginWithOptions(t.Context(), "opencode", catalogKeyMethod, "zeta-secret", LoginOptions{Provider: "zeta"})
	if err != nil || login.Status != "succeeded" {
		t.Fatalf("catalog login = %+v, %v", login, err)
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var auth map[string]struct{ Type, Key string }
	if err := json.Unmarshal(data, &auth); err != nil || auth["zeta"].Type != "api" || auth["zeta"].Key != "zeta-secret" {
		t.Fatalf("auth.json = %s, %v", data, err)
	}
	for _, tc := range []struct {
		name string
		opts LoginOptions
	}{
		{"unknown provider", LoginOptions{Provider: "azure"}},
		{"missing provider", LoginOptions{}},
		{"model", LoginOptions{Provider: "zeta", Model: "zeta-1"}},
		{"device", LoginOptions{Provider: "zeta", Device: true}},
	} {
		if _, err := svc.StartLoginWithOptions(t.Context(), "opencode", catalogKeyMethod, "secret", tc.opts); err == nil {
			t.Fatalf("%s: accepted", tc.name)
		}
	}
	if _, err := svc.StartLoginWithOptions(t.Context(), "opencode", "native", "", LoginOptions{Provider: "zeta"}); err == nil {
		t.Fatal("accepted a provider for a native login")
	}
}
