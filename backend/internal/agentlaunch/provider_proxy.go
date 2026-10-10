package agentlaunch

import (
	"slices"
	"strconv"
	"strings"
)

// CodexProxyProvider is the name Codex knows AO's account helper by.
const CodexProxyProvider = "ao-managed"

// CodexProxyProviderFor names the provider a managed Codex launch uses, or nothing.
// Codex resumes a thread on the provider it was created with, so a resume names it.
func CodexProxyProviderFor(env map[string]string) string {
	if env["AO_PROXY_ENDPOINT"] == "" && env["AO_PROXY_TICKET"] == "" {
		return ""
	}
	return CodexProxyProvider
}

// CodexProxyArgv points a managed Codex launch at the account helper; the ticket stays in the environment.
func CodexProxyArgv(argv []string, env map[string]string) []string {
	if len(argv) == 0 || CodexProxyProviderFor(env) == "" {
		return argv
	}
	at := len(argv)
	if i := slices.Index(argv[1:], "--"); i >= 0 {
		at = i + 1
	}
	return slices.Concat(argv[:at], []string{
		"-c", `model_provider="` + CodexProxyProvider + `"`,
		"-c", `model_providers.ao-managed.name="AO Account Manager"`,
		"-c", "model_providers.ao-managed.base_url=" + strconv.Quote(strings.TrimRight(env["AO_PROXY_ENDPOINT"], "/")+"/v1"),
		"-c", `model_providers.ao-managed.env_key="AO_PROXY_TICKET"`,
		"-c", `model_providers.ao-managed.wire_api="responses"`,
		"-c", `model_providers.ao-managed.requires_openai_auth=false`,
		"-c", `model_providers.ao-managed.supports_websockets=false`,
	}, argv[at:])
}
