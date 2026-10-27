package workerexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestDiscoverCodexModelsUsesNativeAppServerCatalog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	workspace := t.TempDir()
	binary := filepath.Join(workspace, "codex")
	script := `#!/bin/sh
read initialize
printf '%s\n' '{"id":1,"result":{}}'
read initialized
read list
printf '%s\n' '{"id":2,"result":{"data":[{"id":"account-model","displayName":"Account Model","isDefault":true,"supportedReasoningEfforts":[{"reasoningEffort":"high"}]}]}}'
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	models, err := DiscoverCodexModels(context.Background(), Command{Path: binary, Dir: workspace})
	if err != nil {
		t.Fatalf("discover models: %v", err)
	}
	if len(models) != 1 || models[0].ID != "account-model" || len(models[0].Efforts) != 1 || models[0].Efforts[0] != "high" {
		t.Fatalf("models = %+v", models)
	}
}

func TestDiscoverCodexModelsBeforeTheCheckoutExists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "codex")
	script := `#!/bin/sh
read initialize
printf '%s\n' '{"id":1,"result":{}}'
read initialized
read list
printf '%s\n' '{"id":2,"result":{"data":[{"id":"account-model","displayName":"Account Model","isDefault":true}]}}'
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// The session's checkout has not been cloned yet.
	models, err := DiscoverCodexModels(context.Background(), Command{Path: binary, Dir: filepath.Join(dir, "work", "repository")})
	if err != nil {
		t.Fatalf("discover models: %v", err)
	}
	if len(models) != 1 || models[0].ID != "account-model" {
		t.Fatalf("models = %+v", models)
	}
}

func TestDiscoverCodexModelsRunsWithTheAgentsCodexHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "codex-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "codex")
	// Like Codex, the catalog needs the credential in CODEX_HOME.
	script := `#!/bin/sh
[ -f "$CODEX_HOME/auth.json" ] || exit 1
read initialize
printf '%s\n' '{"id":1,"result":{}}'
read initialized
read list
printf '%s\n' '{"id":2,"result":{"data":[{"id":"account-model","displayName":"Account Model","isDefault":true}]}}'
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", filepath.Join(dir, "empty-home"))
	models, err := DiscoverCodexModels(context.Background(), Command{Path: binary, Dir: dir, Env: map[string]string{"CODEX_HOME": home}})
	if err != nil {
		t.Fatalf("discover models: %v", err)
	}
	if len(models) != 1 || models[0].ID != "account-model" {
		t.Fatalf("models = %+v", models)
	}
}

func TestParseCodexModelListFiltersHiddenModelsAndRetainsEfforts(t *testing.T) {
	models, cursor, err := parseCodexModelList(json.RawMessage(`{
		"data": [
			{"id":"gpt-test","displayName":"GPT Test","isDefault":true,"defaultReasoningEffort":"medium","supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"high"}]},
			{"id":"hidden","hidden":true}
		], "nextCursor":"page-2"
	}`))
	if err != nil {
		t.Fatalf("parse models: %v", err)
	}
	if cursor != "page-2" || len(models) != 1 || models[0].ID != "gpt-test" ||
		models[0].DisplayName != "GPT Test" || !models[0].Default ||
		models[0].DefaultEffort != "medium" || len(models[0].Efforts) != 2 ||
		models[0].Efforts[1] != "high" {
		t.Fatalf("models = %+v, cursor = %q", models, cursor)
	}
	// Codex applies every approval mode as its own policy, whatever the model.
	if got, want := models[0].ApprovalModes, []string{"default", "accept-edits", "auto", "bypass-permissions"}; !slices.Equal(got, want) {
		t.Fatalf("Codex approval modes = %v, want %v", got, want)
	}
}
