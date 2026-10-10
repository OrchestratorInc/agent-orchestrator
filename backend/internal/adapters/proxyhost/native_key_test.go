package proxyhost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// onlyThisComputer hides the real machine's agent settings from a test.
func onlyThisComputer(t *testing.T) (codexHome string) {
	t.Helper()
	for _, name := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN",
		"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "OPENAI_BASE_URL", "OPENAI_API_KEY",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	codexHome = t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	return codexHome
}

func idleClient(t *testing.T) *Client {
	t.Helper()
	return privateClient(t, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("reading this computer's key asked the helper: %s %s", r.Method, r.URL)
		return nil, nil
	})
}

func TestReadNativeAPIKeyFindsTheKeyClaudeCodeWouldUse(t *testing.T) {
	for name, tc := range map[string]struct {
		env       map[string]string
		key, base string
	}{
		"an API key":                 {map[string]string{"ANTHROPIC_API_KEY": "sk-ant-one"}, "sk-ant-one", "https://api.anthropic.com"},
		"a gateway token":            {map[string]string{"ANTHROPIC_AUTH_TOKEN": "gateway-token", "ANTHROPIC_BASE_URL": "https://gateway.example/anthropic/"}, "gateway-token", "https://gateway.example/anthropic"},
		"a key ahead of a token":     {map[string]string{"ANTHROPIC_API_KEY": "sk-ant-one", "ANTHROPIC_AUTH_TOKEN": "gateway-token"}, "sk-ant-one", "https://api.anthropic.com"},
		"nothing set":                {nil, "", ""},
		"a subscription token":       {map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "oauth-token", "ANTHROPIC_API_KEY": "sk-ant-one"}, "", ""},
		"a cloud provider":           {map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "ANTHROPIC_API_KEY": "sk-ant-one"}, "", ""},
		"AO's own helper and ticket": {map[string]string{"ANTHROPIC_AUTH_TOKEN": "session-ticket", "ANTHROPIC_BASE_URL": "http://127.0.0.1:12345"}, "", ""},
		"an address with a password": {map[string]string{"ANTHROPIC_API_KEY": "sk-ant-one", "ANTHROPIC_BASE_URL": "https://user:pass@gateway.example"}, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			onlyThisComputer(t)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			found, err := idleClient(t).ReadNativeAPIKey(context.Background(), "claude")
			if err != nil || found.APIKey != tc.key || found.BaseURL != tc.base || (found.Fingerprint == "") != (tc.key == "") {
				t.Fatalf("found key=%v base=%q fingerprint=%v err=%v", found.APIKey != "", found.BaseURL, found.Fingerprint != "", err)
			}
		})
	}
}

func TestReadNativeAPIKeyReadsClaudeCodesOwnSettings(t *testing.T) {
	onlyThisComputer(t)
	settings := `{"env": {"ANTHROPIC_AUTH_TOKEN": "settings-token", "ANTHROPIC_BASE_URL": "https://gateway.example"}}`
	if err := os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := idleClient(t).ReadNativeAPIKey(context.Background(), "claude")
	if err != nil || found.APIKey != "settings-token" || found.BaseURL != "https://gateway.example" {
		t.Fatalf("found key=%v base=%q err=%v", found.APIKey != "", found.BaseURL, err)
	}
}

func TestReadNativeAPIKeyFindsTheKeyCodexIsSignedInWith(t *testing.T) {
	for name, tc := range map[string]struct {
		auth, baseEnv string
		key, base     string
	}{
		"signed in with a key":             {`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-one","tokens":null}`, "", "sk-one", "https://api.openai.com/v1"},
		"a key against another address":    {`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-one"}`, "https://gateway.example/v1/", "sk-one", "https://gateway.example/v1"},
		"an older file with only a key":    {`{"OPENAI_API_KEY":"sk-one","tokens":null}`, "", "sk-one", "https://api.openai.com/v1"},
		"signed in with ChatGPT":           {`{"auth_mode":"chatgpt","OPENAI_API_KEY":"sk-one","tokens":{"access_token":"a"}}`, "", "", ""},
		"an older ChatGPT file with a key": {`{"OPENAI_API_KEY":"sk-one","tokens":{"access_token":"a"}}`, "", "", ""},
		"no key":                           {`{"auth_mode":"chatgpt","OPENAI_API_KEY":null,"tokens":{"access_token":"a"}}`, "", "", ""},
		"not signed in at all":             {"", "", "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			home := onlyThisComputer(t)
			// A key kept in the environment for other tools is not Codex's.
			t.Setenv("OPENAI_API_KEY", "sk-for-other-tools")
			t.Setenv("OPENAI_BASE_URL", tc.baseEnv)
			if tc.auth != "" {
				if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(tc.auth), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			found, err := idleClient(t).ReadNativeAPIKey(context.Background(), "codex")
			if err != nil || found.APIKey != tc.key || found.BaseURL != tc.base {
				t.Fatalf("found key=%v base=%q err=%v", found.APIKey != "", found.BaseURL, err)
			}
		})
	}
}

func TestReadNativeAPIKeyTellsDifferentKeysAndAddressesApart(t *testing.T) {
	prints := map[string]bool{}
	for _, env := range []map[string]string{
		{"ANTHROPIC_API_KEY": "one"},
		{"ANTHROPIC_API_KEY": "two"},
		{"ANTHROPIC_API_KEY": "one", "ANTHROPIC_BASE_URL": "https://gateway.example"},
	} {
		onlyThisComputer(t)
		for key, value := range env {
			t.Setenv(key, value)
		}
		found, err := idleClient(t).ReadNativeAPIKey(context.Background(), "claude")
		if err != nil || found.Fingerprint == "" || strings.Contains(found.Fingerprint, env["ANTHROPIC_API_KEY"]) {
			t.Fatalf("fingerprint=%q err=%v", found.Fingerprint, err)
		}
		prints[found.Fingerprint] = true
	}
	if len(prints) != 3 {
		t.Fatalf("%d distinct fingerprints for 3 distinct keys", len(prints))
	}
}

// keyHelper is an account helper that holds API keys and reports the one a
// sign-in id was tagged with.
type keyHelper struct {
	t      *testing.T
	held   []map[string]any
	tagged map[string]bool
	puts   int
}

func (h *keyHelper) serve(r *http.Request) (*http.Response, error) {
	switch {
	case r.URL.Path == "/ao/status":
		return fakeResponse(200, `{"protocol_version":2}`), nil
	case r.URL.Path == "/v0/management/claude-api-key" && r.Method == http.MethodGet:
		body, _ := json.Marshal(map[string]any{"claude-api-key": h.held})
		return fakeResponse(200, string(body)), nil
	case r.URL.Path == "/v0/management/claude-api-key" && r.Method == http.MethodPut:
		h.puts++
		if err := json.NewDecoder(r.Body).Decode(&h.held); err != nil {
			h.t.Fatal(err)
		}
		return fakeResponse(200, `{}`), nil
	case r.URL.Path == "/ao/tag-api-key":
		var tag struct {
			ID, Label string
		}
		if err := json.NewDecoder(r.Body).Decode(&tag); err != nil || tag.Label != "Global API key" {
			h.t.Fatalf("tag=%+v err=%v", tag, err)
		}
		h.tagged[tag.ID] = true
		return fakeResponse(200, `{"auth_id":"key-auth"}`), nil
	case strings.HasPrefix(r.URL.Path, "/ao/login-result/"):
		if !h.tagged[strings.TrimPrefix(r.URL.Path, "/ao/login-result/")] {
			return fakeResponse(404, `{}`), nil
		}
		// An older helper does not say this is an API key.
		return fakeResponse(200, `{"provider":"claude","email":"Global API key","credential_ref":"config-index:claude:7","auth_id":"key-auth"}`), nil
	}
	h.t.Fatalf("unexpected request %s %s", r.Method, r.URL)
	return nil, nil
}

func TestImportNativeAPIKeyAddsTheKeyOnceAndNamesItAnAPIKey(t *testing.T) {
	helper := &keyHelper{t: t, tagged: map[string]bool{}, held: []map[string]any{{"api-key": "another", "base-url": "https://api.anthropic.com"}}}
	c := privateClient(t, helper.serve)
	key := ports.NativeProviderAPIKey{Fingerprint: strings.Repeat("ab", 32), APIKey: "sk-ant-one", BaseURL: "https://api.anthropic.com"}
	for attempt := 0; attempt < 2; attempt++ {
		verified, err := c.ImportNativeAPIKey(context.Background(), "claude", key)
		if err != nil || verified.Kind != "api_key" || verified.CredentialRef != "config-index:claude:7" || verified.AuthID != "key-auth" || verified.Email != "Global API key" {
			t.Fatalf("attempt %d: verified=%+v err=%v", attempt, verified, err)
		}
	}
	// The second call found the key already imported and changed nothing.
	if helper.puts != 1 || len(helper.held) != 2 || helper.held[1]["api-key"] != "sk-ant-one" {
		t.Fatalf("puts=%d keys held=%d", helper.puts, len(helper.held))
	}
}

func TestImportNativeAPIKeyLeavesAKeyAddedByHandAlone(t *testing.T) {
	helper := &keyHelper{t: t, tagged: map[string]bool{}, held: []map[string]any{{"api-key": "sk-ant-one", "base-url": "https://api.anthropic.com/"}}}
	c := privateClient(t, helper.serve)
	key := ports.NativeProviderAPIKey{Fingerprint: strings.Repeat("ab", 32), APIKey: "sk-ant-one", BaseURL: "https://api.anthropic.com"}
	if _, err := c.ImportNativeAPIKey(context.Background(), "claude", key); !errors.Is(err, ports.ErrProviderAccountConflict) {
		t.Fatalf("err=%v, want a conflict", err)
	}
	if helper.puts != 0 || len(helper.tagged) != 0 {
		t.Fatalf("a key already held was written again: puts=%d tagged=%v", helper.puts, helper.tagged)
	}
	// Anything that is not a readable key is refused before the helper is asked.
	for _, bad := range []ports.NativeProviderAPIKey{{Fingerprint: "short", APIKey: "k"}, {Fingerprint: strings.Repeat("ab", 32)}} {
		if _, err := c.ImportNativeAPIKey(context.Background(), "claude", bad); !errors.Is(err, ports.ErrProviderAccountIncompatible) {
			t.Fatalf("err=%v", err)
		}
	}
}
