package triage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const catalogAccountID = "12345678-1234-4234-8234-123456789abc"

func catalogFixture(t *testing.T) *Catalog {
	t.Helper()
	c := NewCatalog(t.TempDir())
	if err := os.Chmod(c.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	c.native = func(context.Context) []Choice { return nil }
	c.resolve["codex"] = func(context.Context) (string, error) { return "/fixture/codex", nil }
	return c
}

func configuredCodexFixture() Choice {
	return Choice{Harness: "codex", Models: []ModelChoice{{ID: "gpt-6.1-sol", Label: "GPT-6.1 Sol", Efforts: []string{"high", "xhigh"}}},
		Accounts: []Account{{ID: "device-codex", Label: "This computer"}}}
}

func TestCatalogPersistsOnlyConfiguredChoices(t *testing.T) {
	c := catalogFixture(t)
	choice := configuredCodexFixture()
	choice.Label = "sk-secret-value-that-must-not-persist"
	choice.UnavailableReason = "Bearer credential-value-that-must-not-persist"
	choice.Models = append(choice.Models,
		ModelChoice{ID: "gpt-6.1-sol", Label: "duplicate"},
		ModelChoice{ID: "../../credential"},
		ModelChoice{ID: "sk-secret-value-that-must-not-persist"},
	)
	choice.Accounts = []Account{
		{ID: "device-codex", Label: "This computer"},
		{ID: catalogAccountID, Label: "/tmp/private-credential"},
		{ID: "../../credential", Label: "unsafe"},
	}
	input := []Choice{
		choice,
		{Harness: "unknown-agent", Models: []ModelChoice{{ID: "unknown-model"}}},
		{Harness: "claude-code"}, // Keep the explicit row without inventing a default.
		{Harness: "amp", Models: []ModelChoice{{ID: "not-an-amp-mode"}, {ID: "high"}}},
	}
	if err := c.Configure(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	got, err := c.Choices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Harness != "codex" || len(got[0].Models) != 1 || got[0].Models[0].ID != "gpt-6.1-sol" {
		t.Fatalf("configured choices = %#v", got)
	}
	if !got[0].Available || got[0].Label != "Codex" {
		t.Fatalf("choice metadata = %#v", got[0])
	}
	if got[1].Available || len(got[1].Models) != 0 || !strings.Contains(got[1].UnavailableReason, "No configured models") {
		t.Fatalf("empty configured model choice = %#v", got[1])
	}
	if got[2].Available || got[2].UnavailableReason == "" || len(got[2].Models) != 1 || got[2].Models[0].ID != "high" {
		t.Fatalf("configured incompatible choice = %#v", got[2])
	}
	if len(got[0].Accounts) != 2 || got[0].Accounts[1].Label != "Saved account" {
		t.Fatalf("filtered accounts = %#v", got[0].Accounts)
	}
	raw, err := os.ReadFile(c.path())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret-value", "credential-value", "/private/credential", "/tmp/private", "unavailableReason", "source", "unknown-agent", "not-an-amp-mode", "../../"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("configuration retained %q: %s", forbidden, raw)
		}
	}
	for _, path := range []string{c.dataDir, filepath.Join(c.dataDir, "reports"), c.root(), c.path()} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("non-private configuration path %s: %v", path, err)
		}
	}
}

func TestCatalogEmptyAndStaleSnapshotsWorkWithoutDaemon(t *testing.T) {
	c := catalogFixture(t)
	// Installed executables alone are not configured choices.
	got, err := c.Choices(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("unconfigured catalog = %#v, %v", got, err)
	}
	for _, name := range []string{"config.yaml", "ao.db", "running.json"} {
		if err := os.WriteFile(filepath.Join(c.dataDir, name), []byte("corrupt daemon state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Configure(context.Background(), []Choice{configuredCodexFixture()}); err != nil {
		t.Fatal(err)
	}
	got, err = c.Choices(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("stale offline catalog = %#v, %v", got, err)
	}
	c.native = func(context.Context) []Choice { return []Choice{configuredCodexFixture()} }
	if err := c.Configure(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	got, err = c.Choices(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("explicit empty snapshot repopulated native choices: %#v, %v", got, err)
	}
}

func TestCatalogNativeFallbackDoesNotEnumerateModels(t *testing.T) {
	c := catalogFixture(t)
	c.native = func(context.Context) []Choice {
		return []Choice{{Harness: "codex", Models: []ModelChoice{{ID: ""}}}}
	}
	got, err := c.Choices(context.Background())
	if err != nil || len(got) != 1 || len(got[0].Models) != 1 || got[0].Models[0].ID != "" || got[0].Models[0].Label != "Configured default" {
		t.Fatalf("native default = %#v, %v", got, err)
	}
}

func TestCatalogExplicitZeroModelsCannotLaunchNativeDefault(t *testing.T) {
	c := catalogFixture(t)
	c.native = func(context.Context) []Choice {
		t.Fatal("explicit zero-model snapshot must not discover a native fallback")
		return nil
	}
	if err := c.Configure(context.Background(), []Choice{{Harness: "codex", Models: []ModelChoice{}}}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Choices(context.Background())
	if err != nil || len(got) != 1 || got[0].Available || len(got[0].Models) != 0 || !strings.Contains(got[0].UnavailableReason, "No configured models") {
		t.Fatalf("zero-model snapshot = %#v, %v", got, err)
	}
	if _, err := catalogLaunchEnv(context.Background(), c, Selection{Harness: "codex"}); err == nil {
		t.Fatal("zero-model snapshot launched an implicit native default")
	}
}

func TestCatalogMissingAccountMetadataCannotUseNativeLogin(t *testing.T) {
	c := catalogFixture(t)
	choice := configuredCodexFixture()
	choice.Accounts = nil
	if err := c.Configure(context.Background(), []Choice{choice}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Choices(context.Background())
	if err != nil || len(got) != 1 || got[0].Available || !strings.Contains(got[0].UnavailableReason, "account information is unavailable") {
		t.Fatalf("accountless snapshot = %#v, %v", got, err)
	}
	if env, err := catalogLaunchEnv(context.Background(), c, Selection{Harness: "codex", Model: choice.Models[0].ID}); err == nil || env != nil {
		t.Fatalf("missing account metadata used native login: %#v, %v", env, err)
	}
}

func TestCatalogPreservesExplicitDefaultsAndClearsConflicts(t *testing.T) {
	c := catalogFixture(t)
	choice := Choice{Harness: "codex", Models: []ModelChoice{{ID: "gpt-6.1-sol", Default: true}, {ID: "gpt-6-astra"}},
		Accounts: []Account{{ID: "device-codex"}, {ID: catalogAccountID, Default: true}}}
	if err := c.Configure(context.Background(), []Choice{choice}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Choices(context.Background())
	if err != nil || len(got) != 1 || !got[0].Models[0].Default || got[0].Models[1].Default || got[0].Accounts[0].Default || !got[0].Accounts[1].Default {
		t.Fatalf("explicit defaults = %#v, %v", got, err)
	}
	choice.Models[1].Default, choice.Accounts[0].Default = true, true
	if err := c.Configure(context.Background(), []Choice{choice}); err != nil {
		t.Fatal(err)
	}
	got, err = c.Choices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range got[0].Models {
		if model.Default {
			t.Fatal("conflicting defaults chose an arbitrary model")
		}
	}
	for _, account := range got[0].Accounts {
		if account.Default {
			t.Fatal("conflicting defaults chose an arbitrary account")
		}
	}
}

func TestCatalogInvalidSnapshotDoesNotReadDaemonOrFallback(t *testing.T) {
	for _, raw := range []string{"{", `{"version":99,"choices":[]}`, `{"version":1,"choices":[]} {}`} {
		t.Run(raw, func(t *testing.T) {
			c := catalogFixture(t)
			if err := c.Configure(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(c.path(), []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Choices(context.Background()); err == nil || !strings.Contains(err.Error(), "refresh configured agents") {
				t.Fatalf("invalid snapshot error = %v", err)
			}
		})
	}
}

func TestCatalogLaunchRequiresExactConfiguredSelection(t *testing.T) {
	c := catalogFixture(t)
	choice := configuredCodexFixture()
	choice.Accounts = []Account{{ID: "device-codex", Label: "This computer"}}
	if err := c.Configure(context.Background(), []Choice{choice, {Harness: "amp", Models: []ModelChoice{{ID: "high"}}}}); err != nil {
		t.Fatal(err)
	}
	valid := Selection{Harness: "codex", Model: "gpt-6.1-sol", Effort: "xhigh", AccountID: "device-codex"}
	env, err := catalogLaunchEnv(context.Background(), c, valid)
	if err != nil || len(env) != 0 {
		t.Fatalf("native launch env = %#v, %v", env, err)
	}
	for _, selection := range []Selection{
		{Harness: "unknown", Model: valid.Model},
		{Harness: "codex", Model: "unconfigured-model", AccountID: valid.AccountID},
		{Harness: "codex", Model: valid.Model, Effort: "ultra", AccountID: valid.AccountID},
		{Harness: "codex", Model: valid.Model},
		{Harness: "codex", Model: valid.Model, AccountID: catalogAccountID},
		{Harness: "codex", Model: valid.Model, AccountID: "device-claude"},
		{Harness: "amp", Model: "high"},
	} {
		if _, err := catalogLaunchEnv(context.Background(), c, selection); err == nil {
			t.Errorf("selection unexpectedly accepted: %#v", selection)
		}
	}
	c.resolve["codex"] = func(context.Context) (string, error) { return "", errors.New("secret provider diagnostic") }
	if _, err := catalogLaunchEnv(context.Background(), c, valid); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("missing executable error = %v", err)
	}
	got, err := c.Choices(context.Background())
	if err != nil || got[0].Available {
		t.Fatalf("missing configured executable should be unavailable: %#v, %v", got, err)
	}
}

func TestLocalConfiguredChoicesRequireCredentialEvidence(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{}
	getenv := func(key string) string { return env[key] }
	if got := localConfiguredChoices(context.Background(), getenv, home, false); len(got) != 0 {
		t.Fatalf("fabricated native configuration: %#v", got)
	}
	catalogWriteFixture(t, filepath.Join(home, ".codex", "auth.json"), `{"tokens":{"access_token":"fixture-private-token"}}`)
	catalogWriteFixture(t, filepath.Join(home, ".claude", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"fixture-private-token"}}`)
	got := localConfiguredChoices(context.Background(), getenv, home, false)
	if len(got) != 2 || got[0].Harness != "codex" || got[1].Harness != "claude-code" {
		t.Fatalf("configured native choices = %#v", got)
	}
	for _, choice := range got {
		if len(choice.Models) != 1 || choice.Models[0].ID != "" || !choice.Models[0].Default || len(choice.Accounts) != 1 || !choice.Accounts[0].Default {
			t.Fatalf("native fallback advertised public model catalog: %#v", choice)
		}
		serialized, err := json.Marshal(choice)
		if err != nil || strings.Contains(string(serialized), "fixture-private-token") || strings.Contains(string(serialized), home) {
			t.Fatalf("native metadata leaked private data: %s, %v", serialized, err)
		}
	}
	// A malformed credential file is not configuration evidence.
	emptyHome := t.TempDir()
	catalogWriteFixture(t, filepath.Join(emptyHome, ".codex", "auth.json"), `{}`)
	catalogWriteFixture(t, filepath.Join(emptyHome, ".claude", ".credentials.json"), `{}`)
	if got := localConfiguredChoices(context.Background(), getenv, emptyHome, false); len(got) != 0 {
		t.Fatalf("empty native credential files counted as configured: %#v", got)
	}
	env["OPENAI_API_KEY"] = "fixture-environment-key"
	if got := localConfiguredChoices(context.Background(), getenv, emptyHome, false); len(got) != 1 || got[0].Harness != "codex" {
		t.Fatalf("explicit credential environment = %#v", got)
	}
}

func TestFirstOutageDiscoversConfiguredTerminalAndChatHarnesses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AMP_API_KEY", "fixture-private-key")
	catalogWriteFixture(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"defaultProvider":"openai","defaultModel":"private-selected-model"}`)
	catalogWriteFixture(t, filepath.Join(home, ".continue", "config.yaml"), "models:\n  - name: Local user choice\n    provider: openai\n    model: user-model\ndefaults:\n  chat: Local user choice\n")
	catalogWriteFixture(t, filepath.Join(home, ".aider.conf.yml"), "model: custom-user-model\n")
	env := map[string]string{"AMP_API_KEY": "fixture-private-key"}
	c := catalogFixture(t)
	for _, name := range []string{"config.yaml", "ao.db", "running.json"} {
		if err := os.WriteFile(filepath.Join(c.dataDir, name), []byte("broken daemon state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c.native = func(ctx context.Context) []Choice {
		return additionalConfiguredChoices(ctx, func(key string) string { return env[key] }, home)
	}
	for _, harness := range []string{"pi", "continue", "aider", "amp"} {
		c.resolve[harness] = func(context.Context) (string, error) { return "/fixture/selected", nil }
	}
	choices, err := c.Choices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Choice{}
	for _, choice := range choices {
		got[choice.Harness] = choice
	}
	for harness, model := range map[string]string{"pi": "openai/private-selected-model", "continue": "user-model", "aider": "custom-user-model", "amp": ""} {
		choice, ok := got[harness]
		if !ok || !choice.Available || len(choice.Models) != 1 || choice.Models[0].ID != model || !choice.Models[0].Default {
			t.Fatalf("first-outage %s = %#v", harness, choice)
		}
	}
	if _, err := os.Stat(c.path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("first-outage choices required or created a saved snapshot")
	}
}

func TestCatalogInheritsOnlyExactConfiguredPermissionAndMode(t *testing.T) {
	c := catalogFixture(t)
	c.resolve["amp"] = func(context.Context) (string, error) { return "/fixture/amp", nil }
	choice := Choice{Harness: "amp", Mode: "high", Permissions: "auto", Models: []ModelChoice{{ID: "", Default: true}}}
	if err := c.Configure(context.Background(), []Choice{choice}); err != nil {
		t.Fatal(err)
	}
	selection, _, err := c.ResolveSelection(context.Background(), Selection{Harness: "amp"})
	if err != nil || selection.Mode != "high" || selection.Permissions != "auto" {
		t.Fatalf("native settings = %#v %v", selection, err)
	}
	for _, selection := range []Selection{{Harness: "amp", Mode: "ultra"}, {Harness: "amp", Permissions: "invalid"}} {
		if _, _, err := c.ResolveSelection(context.Background(), selection); err == nil {
			t.Fatalf("unconfigured settings admitted: %#v", selection)
		}
	}
}

func TestCatalogMissingModelsUseOnlyConfiguredAccountDefault(t *testing.T) {
	c := catalogFixture(t)
	c.native = func(context.Context) []Choice { t.Fatal("saved choices must not rediscover another login"); return nil }
	for _, test := range []struct {
		choice    Choice
		available bool
		models    int
	}{
		{Choice{Harness: "codex", ModelsMissing: true, Accounts: []Account{{ID: catalogAccountID}}}, true, 1},
		{Choice{Harness: "codex", ModelsMissing: true}, false, 1},
		{Choice{Harness: "codex", Accounts: []Account{{ID: "device-codex"}}}, false, 0},
	} {
		if err := c.Configure(context.Background(), []Choice{test.choice}); err != nil {
			t.Fatal(err)
		}
		choices, err := c.Choices(context.Background())
		if err != nil || len(choices) != 1 || choices[0].Available != test.available || len(choices[0].Models) != test.models || len(choices[0].Accounts) != len(test.choice.Accounts) {
			t.Fatalf("configured default = %#v, %v", choices, err)
		}
		if len(test.choice.Accounts) > 0 && choices[0].Accounts[0].ID != test.choice.Accounts[0].ID {
			t.Fatal("changed the selected account")
		}
		if test.models > 0 && choices[0].Models[0].ID != "" {
			t.Fatal("invented a model instead of using the selected account's native default")
		}
	}
}

func TestCatalogSeparatesSessionInterfaceFromAgentMode(t *testing.T) {
	c := catalogFixture(t)
	c.resolve["amp"] = c.resolve["codex"]
	for _, choice := range []Choice{
		{Harness: "codex", SessionMode: "tui", Models: []ModelChoice{{ID: ""}}, Accounts: []Account{{ID: "device-codex"}}},
		{Harness: "amp", Mode: "high", SessionMode: "chat", Models: []ModelChoice{{ID: ""}}},
	} {
		if err := c.Configure(context.Background(), []Choice{choice}); err != nil {
			t.Fatal(err)
		}
		selection, _, err := c.ResolveSelection(context.Background(), Selection{Harness: choice.Harness, AccountID: func() string {
			if len(choice.Accounts) > 0 {
				return choice.Accounts[0].ID
			}
			return ""
		}()})
		if err != nil || selection.SessionMode != "tui" || selection.Mode != choice.Mode {
			t.Fatalf("selection = %#v, %v", selection, err)
		}
		if _, _, err := c.ResolveSelection(context.Background(), Selection{Harness: choice.Harness, SessionMode: "chat"}); err == nil {
			t.Fatal("changed a frozen native interface")
		}
	}
}

func TestSavedAccountLaunchDoesNotSwitchNativeAccount(t *testing.T) {
	for _, harness := range []string{"codex", "claude-code"} {
		t.Run(harness, func(t *testing.T) {
			c := catalogFixture(t)
			c.resolve[harness] = func(context.Context) (string, error) { return "/fixture/provider", nil }
			provider, envKey, credentialFile := "codex", "CODEX_HOME", "auth.json"
			credential := `{"tokens":{"access_token":"saved-profile-token"}}`
			if harness == "claude-code" {
				provider, envKey, credentialFile = "claude", "CLAUDE_CONFIG_DIR", ".credentials.json"
				credential = `{"claudeAiOauth":{"accessToken":"saved-profile-token"}}`
			}
			home := filepath.Join(c.dataDir, "agent-accounts", provider, catalogAccountID)
			catalogWriteFixture(t, filepath.Join(home, credentialFile), credential)
			native := filepath.Join(c.dataDir, "native-credential.json")
			catalogWriteFixture(t, native, `{"token":"native-must-stay-unchanged"}`)
			before, _ := os.ReadFile(native)
			choice := Choice{Harness: harness, Models: []ModelChoice{{ID: ""}}, Accounts: []Account{{ID: catalogAccountID, Label: "Saved login"}}}
			if err := c.Configure(context.Background(), []Choice{choice}); err != nil {
				t.Fatal(err)
			}
			env, err := catalogLaunchEnv(context.Background(), c, Selection{Harness: harness, AccountID: catalogAccountID})
			if err != nil || env[envKey] != home {
				t.Fatalf("saved launch env = %#v, %v", env, err)
			}
			for _, key := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
				if value, exists := env[key]; exists && value != "" {
					t.Fatalf("account launch did not clear ambient override %s", key)
				}
			}
			after, _ := os.ReadFile(native)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("saved account launch changed native credentials")
			}
			if err := os.RemoveAll(home); err != nil {
				t.Fatal(err)
			}
			if env, err := catalogLaunchEnv(context.Background(), c, Selection{Harness: harness, AccountID: catalogAccountID}); err == nil || env != nil {
				t.Fatalf("missing saved credential silently fell back: %#v, %v", env, err)
			}
		})
	}
}

func TestManagedCodexAccountUsesExactPrivateProfile(t *testing.T) {
	c := catalogFixture(t)
	home := filepath.Join(c.dataDir, "harnesses", "codex", "accounts", catalogAccountID, "credential-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// The native CLI owns descriptor and credential validation, including login.
	env, err := configuredAccountEnv(context.Background(), c.dataDir, "codex", catalogAccountID)
	if err != nil || env["CODEX_HOME"] != home {
		t.Fatalf("managed Codex account = %#v, %v", env, err)
	}
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := configuredAccountEnv(context.Background(), c.dataDir, "codex", catalogAccountID); err == nil {
		t.Fatal("accepted nonprivate account directory")
	}
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../" + catalogAccountID, "/tmp/account", "device-claude", "not-an-account"} {
		if _, err := configuredAccountEnv(context.Background(), c.dataDir, "codex", id); err == nil {
			t.Errorf("accepted unsafe account ref %q", id)
		}
	}
	legacy := filepath.Join(c.dataDir, "agent-accounts", "codex", catalogAccountID)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := configuredAccountEnv(context.Background(), c.dataDir, "codex", catalogAccountID); err == nil {
		t.Fatal("accepted ambiguous managed and legacy profiles")
	}
}

func TestSavedAccountRejectsSymlinkDirectory(t *testing.T) {
	c := catalogFixture(t)
	outside := t.TempDir()
	catalogWriteFixture(t, filepath.Join(outside, "auth.json"), `{"OPENAI_API_KEY":"outside-key"}`)
	parent := filepath.Join(c.dataDir, "agent-accounts", "codex")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(parent, catalogAccountID)); err != nil {
		t.Fatal(err)
	}
	if _, err := configuredAccountEnv(context.Background(), c.dataDir, "codex", catalogAccountID); err == nil {
		t.Fatal("accepted saved account directory symlink")
	}
}

func TestCatalogRejectsSymlinkedSnapshotStorage(t *testing.T) {
	c := catalogFixture(t)
	if err := c.Configure(context.Background(), []Choice{configuredCodexFixture()}); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Rename(c.path(), filepath.Join(outside, "configured.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(c.root()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, c.root()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Choices(context.Background()); err == nil {
		t.Fatal("read configuration through a symlinked triage directory")
	}
	if err := c.Configure(context.Background(), nil); err == nil {
		t.Fatal("wrote configuration through a symlinked triage directory")
	}
}

func catalogWriteFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func catalogLaunchEnv(ctx context.Context, c *Catalog, selection Selection) (map[string]string, error) {
	_, env, err := c.ResolveSelection(ctx, selection)
	return env, err
}
