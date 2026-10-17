package cline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveACPProviderUsesSelectedProfile(t *testing.T) {
	const providers = `{"lastUsedProvider":"openai-codex","providers":{"cline":{"settings":{"provider":"cline"}},"openai-codex":{"settings":{"provider":"openai-codex"}}}}`
	for _, profile := range []string{"home", "cline-dir", "data-dir", "settings-path", "relative-settings-path"} {
		t.Run(profile, func(t *testing.T) {
			workspace := t.TempDir()
			env := map[string]string{
				"HOME": workspace, "USERPROFILE": workspace,
				"CLINE_PROVIDER": "", "CLINE_DIR": "", "CLINE_DATA_DIR": "", "CLINE_PROVIDER_SETTINGS_PATH": "",
			}
			path := filepath.Join(workspace, ".cline", "data", "settings", "providers.json")
			switch profile {
			case "cline-dir":
				env["CLINE_DIR"] = filepath.Join(workspace, "profile")
				path = filepath.Join(env["CLINE_DIR"], "data", "settings", "providers.json")
			case "data-dir":
				env["CLINE_DIR"] = filepath.Join(workspace, "unused")
				env["CLINE_DATA_DIR"] = filepath.Join(workspace, "data")
				path = filepath.Join(env["CLINE_DATA_DIR"], "settings", "providers.json")
			case "settings-path", "relative-settings-path":
				env["CLINE_DIR"] = filepath.Join(workspace, "unused")
				env["CLINE_DATA_DIR"] = filepath.Join(workspace, "unused-data")
				path = filepath.Join(workspace, "selected.json")
				env["CLINE_PROVIDER_SETTINGS_PATH"] = path
				if profile == "relative-settings-path" {
					env["CLINE_PROVIDER_SETTINGS_PATH"] = "selected.json"
				}
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(providers), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := ResolveACPProvider(t.Context(), workspace, env)
			if err != nil || got != "openai-codex" {
				t.Fatalf("provider = %q, error=%v, want saved openai-codex", got, err)
			}
		})
	}
}

func TestResolveACPProviderDoesNotGuessAnotherAccount(t *testing.T) {
	for _, tt := range []struct {
		name, content    string
		write, wantError bool
	}{
		{name: "missing profile"},
		{name: "no saved choice", write: true, content: `{"providers":{"cline":{},"openai-codex":{}}}`},
		{name: "missing selected provider", write: true, content: `{"lastUsedProvider":"missing","providers":{"cline":{}}}`, wantError: true},
		{name: "malformed profile", write: true, content: `not JSON`, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "providers.json")
			if tt.write {
				if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ResolveACPProvider(t.Context(), "", map[string]string{"CLINE_PROVIDER": "", "CLINE_PROVIDER_SETTINGS_PATH": path})
			if (err != nil) != tt.wantError || got != "" {
				t.Fatalf("provider=%q, error=%v, wantError=%v", got, err, tt.wantError)
			}
		})
	}
}

func TestResolveACPProviderHonorsAmbientOverride(t *testing.T) {
	t.Setenv("CLINE_PROVIDER", "openrouter")
	provider, err := ResolveACPProvider(t.Context(), "", nil)
	if err != nil || provider != "openrouter" {
		t.Fatalf("provider=%q, error=%v, want explicit openrouter", provider, err)
	}
}

func TestResolveACPProviderHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ResolveACPProvider(ctx, "", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want canceled", err)
	}
}
