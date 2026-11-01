package agentauth

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
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

func TestPiCatalogKeyIsEscapedAndKeepsOtherEntries(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"zai":{"type":"oauth","access":"a","refresh":"r","expires":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewWithAgentResolver(foundExecutable("pi"), nil, nil, t.TempDir())
	if methods := loginMethods("pi"); methods[0].ID != catalogKeyMethod {
		t.Fatalf("Pi methods = %+v", methods)
	}
	if _, err := svc.StartLoginWithOptions(t.Context(), "pi", catalogKeyMethod, "!sk-$HOME", LoginOptions{Provider: "anthropic"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var auth map[string]map[string]any
	if err := json.Unmarshal(data, &auth); err != nil || auth["anthropic"]["type"] != "api_key" || auth["anthropic"]["key"] != "$!sk-$$HOME" || auth["zai"]["type"] != "oauth" {
		t.Fatalf("auth.json = %s, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "auth.json.lock")); !os.IsNotExist(err) {
		t.Fatal("left Pi's lock behind")
	}
	if _, err := svc.StartLoginWithOptions(t.Context(), "pi", catalogKeyMethod, "key", LoginOptions{Provider: "amazon-bedrock"}); err == nil {
		t.Fatal("accepted a provider that needs more than a key")
	}
}

func TestOMPCatalogKeyIsImportedThroughItsCLI(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	capture := filepath.Join(t.TempDir(), "import.json")
	var argv []string
	svc := NewWithAgentResolver(foundExecutable("omp"), nil, nil, t.TempDir())
	svc.commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		argv = append([]string{name}, args...)
		return exec.CommandContext(ctx, "/bin/sh", "-c", `cat > "$0"`, capture)
	}
	if _, err := svc.StartLoginWithOptions(t.Context(), "omp", catalogKeyMethod, "sk-omp", LoginOptions{Provider: "openrouter"}); err != nil {
		t.Fatal(err)
	}
	if len(argv) != 4 || argv[1] != "auth" || argv[2] != "import" || argv[3] != "-" {
		t.Fatalf("argv = %v", argv)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Version     int `json:"version"`
		Credentials []struct {
			Provider   string            `json:"provider"`
			Credential map[string]string `json:"credential"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || payload.Version != 1 || len(payload.Credentials) != 1 ||
		payload.Credentials[0].Provider != "openrouter" || payload.Credentials[0].Credential["key"] != "sk-omp" || payload.Credentials[0].Credential["source"] != "login" {
		t.Fatalf("import payload = %s, %v", data, err)
	}
	if _, err := svc.StartLoginWithOptions(t.Context(), "omp", catalogKeyMethod, "!cat ~/.ssh/id_rsa", LoginOptions{Provider: "openrouter"}); err == nil {
		t.Fatal("accepted a key OMP would run as a shell command")
	}
}
