package agentlaunch

import (
	"reflect"
	"strings"
	"testing"
)

func proxyConfig(t *testing.T, args []string) map[string]string {
	t.Helper()
	values := make(map[string]string)
	if len(args)%2 != 0 {
		t.Fatalf("odd config argument count=%v", args)
	}
	for i := 0; i < len(args); i += 2 {
		if args[i] != "-c" {
			t.Fatalf("non-config argument=%s", args[i])
		}
		key, value, ok := strings.Cut(args[i+1], "=")
		if !ok {
			t.Fatalf("invalid override=%q", args[i+1])
		}
		values[key] = value
	}
	return values
}
func TestProviderProxyCodexUsesPrivateHTTPProvider(t *testing.T) {
	env := map[string]string{"AO_PROXY_ENDPOINT": "http://127.0.0.1:4567", "AO_PROXY_TICKET": "private-ticket"}
	args := CodexProxyArgs(env)
	cfg := proxyConfig(t, args)
	want := map[string]string{
		"model_provider":                                  `"ao-managed"`,
		"model_providers.ao-managed.name":                 `"AO Account Manager"`,
		"model_providers.ao-managed.base_url":             `"http://127.0.0.1:4567/v1"`,
		"model_providers.ao-managed.env_key":              `"AO_PROXY_TICKET"`,
		"model_providers.ao-managed.wire_api":             `"responses"`,
		"model_providers.ao-managed.requires_openai_auth": "false",
		"model_providers.ao-managed.supports_websockets":  "false",
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("managed provider=%v", cfg)
	}
	if strings.Contains(strings.Join(args, " "), "private-ticket") {
		t.Fatal("private ticket leaked into process arguments")
	}
	if env["AO_PROXY_ENDPOINT"] != "http://127.0.0.1:4567" || len(env) != 2 {
		t.Fatalf("configuration mutated env=%v", env)
	}
}
func TestProviderProxyNativeCodexArgvIsUnchanged(t *testing.T) {
	cases := [][]string{nil, {}, {"codex"}, {"codex", "app-server", "--listen", "stdio://"}, {"codex", "resume", "thread-123"}}
	for _, argv := range cases {
		for _, env := range []map[string]string{nil, {}, {"OPENAI_API_KEY": "ambient"}, {"CODEX_HOME": "/native"}} {
			got := CodexProxyArgv(argv, env)
			if !reflect.DeepEqual(got, argv) {
				t.Fatalf("native argv=%v became %v", argv, got)
			}
		}
	}
}
func TestProviderProxyMalformedManagedConfigFailsClosed(t *testing.T) {
	cases := []struct{ name, endpoint, ticket string }{
		{"missing-ticket", "http://127.0.0.1:4567", ""},
		{"missing-endpoint", "", "private"},
		{"external-ip", "http://10.0.0.1:4567", "private"},
		{"external-host", "http://provider.example:4567", "private"},
		{"localhost-dns", "http://localhost:4567", "private"},
		{"loopback-ipv6", "http://[::1]:4567", "private"},
		{"https", "https://127.0.0.1:4567", "private"},
		{"userinfo", "http://secret@127.0.0.1:4567", "private"},
		{"query", "http://127.0.0.1:4567?account=other", "private"},
		{"fragment", "http://127.0.0.1:4567#other", "private"},
		{"path", "http://127.0.0.1:4567/global", "private"},
		{"missing-port", "http://127.0.0.1", "private"},
		{"non-numeric-port", "http://127.0.0.1:bad", "private"},
		{"zero-port", "http://127.0.0.1:0", "private"},
		{"port-overflow", "http://127.0.0.1:99999", "private"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := proxyConfig(t, CodexProxyArgs(map[string]string{"AO_PROXY_ENDPOINT": tc.endpoint, "AO_PROXY_TICKET": tc.ticket}))
			if cfg["model_provider"] != `"ao-managed"` || cfg["model_providers.ao-managed.base_url"] != `"http://127.0.0.1:0/v1"` || cfg["model_providers.ao-managed.requires_openai_auth"] != "false" {
				t.Fatalf("unsafe config fell back to device=%v", cfg)
			}
		})
	}
}
func TestProviderProxyAppendsOverridesAfterExistingProviderChoices(t *testing.T) {
	argv := []string{"codex", "app-server", "--listen", "stdio://", "-c", `model_provider="openai"`, "-c", `model_providers.ao-managed.base_url="https://wrong.example"`}
	before := append([]string(nil), argv...)
	got := CodexProxyArgv(argv, map[string]string{"AO_PROXY_ENDPOINT": "http://127.0.0.1:3456", "AO_PROXY_TICKET": "private"})
	if !reflect.DeepEqual(argv, before) {
		t.Fatal("injected overrides modified input slice")
	}
	if !reflect.DeepEqual(got[:len(argv)], argv) {
		t.Fatalf("native launch command changed=%v", got)
	}
	cfg := proxyConfig(t, got[len(argv):])
	if cfg["model_provider"] != `"ao-managed"` || cfg["model_providers.ao-managed.base_url"] != `"http://127.0.0.1:3456/v1"` {
		t.Fatalf("late overrides=%v", cfg)
	}
}
func TestProviderProxyEnvSliceMatchesMapLaunch(t *testing.T) {
	argv := []string{"codex", "resume", "thread-123"}
	env := []string{"PATH=/binary", "AO_PROXY_ENDPOINT=http://127.0.0.1:3456", "AO_PROXY_TICKET=old", "AO_PROXY_TICKET=new=private", "MALFORMED"}
	fromSlice := CodexProxyArgvFromEnv(argv, env)
	fromMap := CodexProxyArgv(argv, map[string]string{"AO_PROXY_ENDPOINT": "http://127.0.0.1:3456", "AO_PROXY_TICKET": "new=private"})
	if !reflect.DeepEqual(fromSlice, fromMap) {
		t.Fatalf("slice=%v map=%v", fromSlice, fromMap)
	}
	if strings.Contains(strings.Join(fromSlice, " "), "private") {
		t.Fatal("ticket entered launch argv")
	}
}

func TestProviderProxyPlacesConfigBeforeDashPrefixedPrompt(t *testing.T) {
	argv := []string{"codex", "--model", "gpt-example", "--", "--this-is-my-prompt"}
	env := map[string]string{"AO_PROXY_ENDPOINT": "http://127.0.0.1:4567", "AO_PROXY_TICKET": "private"}
	got := CodexProxyArgv(argv, env)
	separator := -1
	for i, arg := range got {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || len(got) != len(argv)+len(CodexProxyArgs(env)) || got[len(got)-1] != "--this-is-my-prompt" {
		t.Fatalf("prompt corrupted=%v", got)
	}
	cfg := proxyConfig(t, got[3:separator])
	if cfg["model_provider"] != `"ao-managed"` {
		t.Fatalf("config became part of prompt=%v", got)
	}
	if got[separator+1] != "--this-is-my-prompt" || separator+2 != len(got) {
		t.Fatalf("prompt gained config text=%v", got)
	}
}
