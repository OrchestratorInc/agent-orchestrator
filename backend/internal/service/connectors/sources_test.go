package connectors

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func writeSource(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sourceHome(t *testing.T) (home, project string) {
	t.Helper()
	home, project = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeSource(t, filepath.Join(home, ".claude.json"), `{
		"mcpServers": {
			"Linear": {"type": "http", "url": "https://mcp.linear.app/mcp?team=1"},
			"old": {"type": "sse", "url": "https://sse.example.com/sse"},
			"files": {"command": "npx", "args": ["-y", "files"], "env": {"ROOT": "/tmp", "TOKEN": "${FILES_TOKEN}"}}
		},
		"projects": {"`+filepath.ToSlash(project)+`": {"mcpServers": {"scoped": {"command": "scoped-server"}}}}
	}`)
	writeSource(t, filepath.Join(home, ".codex", "config.toml"), `
[mcp_servers.sentry]
url = "https://mcp.sentry.dev/mcp"
bearer_token_env_var = "AO_TEST_SENTRY_TOKEN"

[mcp_servers.ao]
command = "ao"
`)
	writeSource(t, filepath.Join(project, ".mcp.json"), `{"mcpServers": {"repo": {"command": "repo-server"}}}`)
	return home, project
}

func TestNativeNamesReadTheAgentsOwnConfiguration(t *testing.T) {
	home, project := sourceHome(t)
	claude := NativeNames(domain.HarnessClaudeCode, filepath.ToSlash(project), nil)
	for _, name := range []string{"Linear", "linear", "old", "files", "scoped", "repo"} {
		if !claude[name] {
			t.Errorf("claude names = %v, missing %q", claude, name)
		}
	}
	if codex := NativeNames(domain.HarnessCodex, project, nil); !codex["sentry"] || codex["Linear"] {
		t.Errorf("codex names = %v", codex)
	}
	other := filepath.Join(home, "account")
	writeSource(t, filepath.Join(other, ".claude.json"), `{"mcpServers": {"account-only": {"command": "x"}}}`)
	if got := NativeNames(domain.HarnessClaudeCode, "", map[string]string{"CLAUDE_CONFIG_DIR": other}); !got["account-only"] || got["Linear"] {
		t.Errorf("account folder names = %v", got)
	}
	writeSource(t, filepath.Join(home, ".cursor", "mcp.json"), `{"mcpServers": {"Figma": {"url": "https://mcp.figma.com/mcp"}}}`)
	writeSource(t, filepath.Join(project, ".cursor", "mcp.json"), `{"mcpServers": {"repo-cursor": {"command": "x"}}}`)
	if got := NativeNames(domain.HarnessCursor, project, nil); !got["figma"] || !got["repo-cursor"] || got["Linear"] {
		t.Errorf("cursor names = %v", got)
	}
}

func TestImportListsCandidatesWithoutValuesAndAddsTheChosenOnes(t *testing.T) {
	_, project := sourceHome(t)
	svc := New(t.TempDir(), "http://127.0.0.1:1")
	svc.SetImportProjects(func(context.Context) []ImportProject {
		return []ImportProject{{ID: "p1", Path: filepath.ToSlash(project)}}
	})
	ctx := context.Background()
	candidates, err := svc.ImportCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]ImportCandidate{}
	for _, candidate := range candidates {
		byKey[candidate.Key] = candidate
	}
	if len(byKey) != 5 || byKey["old"].ID != "" || byKey["ao"].ID != "" {
		t.Fatalf("candidates = %+v", candidates)
	}
	if got := byKey["linear"]; got.URL != "https://mcp.linear.app/mcp?team=***" || got.Transport != TransportHTTP || len(got.NeedsValue) != 0 {
		t.Errorf("linear = %+v", got)
	}
	if got := byKey["files"]; !slices.Equal(got.NeedsValue, []string{"TOKEN"}) || !slices.Equal(got.EnvKeys, []string{"ROOT", "TOKEN"}) {
		t.Errorf("files = %+v", got)
	}
	if got := byKey["sentry"]; !slices.Equal(got.NeedsValue, []string{"Authorization"}) {
		t.Errorf("sentry = %+v", got)
	}
	if byKey["scoped"].ProjectID != "p1" || byKey["repo"].ProjectID != "p1" {
		t.Errorf("project scope: scoped=%+v repo=%+v", byKey["scoped"], byKey["repo"])
	}

	if _, err := svc.Import(ctx, []string{byKey["files"].ID}, nil); err == nil {
		t.Fatal("import without the value it needs succeeded")
	}
	added, err := svc.Import(ctx, []string{byKey["files"].ID, byKey["linear"].ID, byKey["sentry"].ID}, map[string]map[string]string{
		byKey["files"].ID:  {"TOKEN": "typed"},
		byKey["sentry"].ID: {"Authorization": "typed-token"},
	})
	if err != nil || len(added) != 3 {
		t.Fatalf("import = %+v, %v", added, err)
	}
	if added[1].Auth != AuthOAuth || added[2].Auth != AuthHeader || added[0].Transport != TransportStdio {
		t.Errorf("added = %+v", added)
	}
	records, err := readFile(svc.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, saved := range records {
		if saved.Key == "sentry" && saved.Headers["Authorization"] != "Bearer typed-token" {
			t.Errorf("the typed token of a bearer variable = %q", saved.Headers["Authorization"])
		}
	}
	for _, rec := range records {
		if rec.Key == "files" && (rec.Env["TOKEN"] != "typed" || rec.Env["ROOT"] != "/tmp") {
			t.Errorf("files env = %v", rec.Env)
		}
	}
	again, err := svc.ImportCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range again {
		if candidate.Exists != (candidate.Key == "files" || candidate.Key == "linear" || candidate.Key == "sentry") {
			t.Errorf("exists flag wrong for %+v", candidate)
		}
	}
}

func TestImportUsesTheTokenOfABearerVariableWhenTheDaemonHasIt(t *testing.T) {
	sourceHome(t)
	t.Setenv("AO_TEST_SENTRY_TOKEN", "from-env")
	svc := New(t.TempDir(), "http://127.0.0.1:1")
	candidates, err := svc.ImportCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if candidate.Key != "sentry" {
			continue
		}
		if len(candidate.NeedsValue) != 0 {
			t.Fatalf("sentry needs = %v", candidate.NeedsValue)
		}
		if _, err := svc.Import(context.Background(), []string{candidate.ID}, nil); err != nil {
			t.Fatal(err)
		}
		records, _ := readFile(svc.path)
		if len(records) != 1 || records[0].Headers["Authorization"] != "Bearer from-env" {
			t.Fatalf("records = %+v", records)
		}
		return
	}
	t.Fatal("no sentry candidate")
}

func TestImportAsksForAVariableInAnAddressOrACommandAndAddsAllOrNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeSource(t, filepath.Join(home, ".claude.json"), `{"mcpServers": {
		"tokened": {"command": "npx", "args": ["server", "--token", "${API_TOKEN}", "--region", "${REGION:-eu}"]},
		"hosted": {"type": "http", "url": "https://${TENANT}.example.com/mcp"},
		"plain": {"command": "plain-server"},
		"`+strings.Repeat("n", 70)+`": {"command": "long-name-server"}
	}}`)
	svc := New(t.TempDir(), "http://127.0.0.1:1")
	ctx := context.Background()
	candidates, err := svc.ImportCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]ImportCandidate{}
	for _, candidate := range candidates {
		byKey[candidate.Key] = candidate
	}
	if got := byKey["tokened"]; !slices.Equal(got.NeedsValue, []string{"API_TOKEN"}) {
		t.Fatalf("tokened = %+v", got)
	}
	if got := byKey["hosted"]; !slices.Equal(got.NeedsValue, []string{"TENANT"}) {
		t.Fatalf("hosted = %+v", got)
	}
	long := byKey[strings.Repeat("n", 32)]
	if long.ID == "" {
		t.Fatalf("no candidate for the long name: %+v", candidates)
	}
	if _, err := svc.Import(ctx, []string{byKey["plain"].ID, long.ID}, nil); err == nil {
		t.Fatal("an import with a server that cannot be added succeeded")
	}
	if all, _ := svc.List(ctx); len(all) != 0 {
		t.Fatalf("a failed import added %+v", all)
	}
	added, err := svc.Import(ctx, []string{byKey["tokened"].ID, byKey["hosted"].ID}, map[string]map[string]string{
		byKey["tokened"].ID: {"API_TOKEN": "typed-token"},
		byKey["hosted"].ID:  {"TENANT": "acme"},
	})
	if err != nil || len(added) != 2 {
		t.Fatalf("import = %+v, %v", added, err)
	}
	if !slices.Equal(added[0].Args, []string{"server", "--token", "typed-token", "--region", "eu"}) || added[1].URL != "https://acme.example.com/mcp" {
		t.Fatalf("added = %+v", added)
	}
}
