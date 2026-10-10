package agentlaunch

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// CodexProxyProvider is the name Codex knows AO's account helper by.
const CodexProxyProvider = "ao-managed"

// CodexProxyProviderFor names the provider a Codex process launched with env
// uses, or nothing when the launch is not routed through the account helper.
//
// Launching Codex with this provider as its default is not enough for a
// conversation that already exists: Codex resumes a thread on the provider the
// thread was created with, whatever the process default is. A caller resuming
// a thread must name this provider in the resume itself.
func CodexProxyProviderFor(env map[string]string) string {
	if len(CodexProxyArgs(env)) == 0 {
		return ""
	}
	return CodexProxyProvider
}

// CodexProxyArgs is shared by TUI and app-server so neither can select ambient
// OAuth credentials or retain a WebSocket account pin.
func CodexProxyArgs(env map[string]string) []string {
	endpoint := env["AO_PROXY_ENDPOINT"]
	if endpoint == "" && env["AO_PROXY_TICKET"] == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	valid := err == nil && parsed.Scheme == "http" && parsed.Hostname() == "127.0.0.1" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.Path == ""
	if valid {
		_, port, splitErr := net.SplitHostPort(parsed.Host)
		number, parseErr := strconv.Atoi(port)
		valid = splitErr == nil && parseErr == nil && number > 0 && number <= 65535
	}
	if !valid || env["AO_PROXY_TICKET"] == "" {
		endpoint = "http://127.0.0.1:0"
	}
	return []string{
		"-c", `model_provider="` + CodexProxyProvider + `"`,
		"-c", `model_providers.ao-managed.name="AO Account Manager"`,
		"-c", "model_providers.ao-managed.base_url=" + strconv.Quote(strings.TrimRight(endpoint, "/")+"/v1"),
		"-c", `model_providers.ao-managed.env_key="AO_PROXY_TICKET"`,
		"-c", `model_providers.ao-managed.wire_api="responses"`,
		"-c", `model_providers.ao-managed.requires_openai_auth=false`,
		"-c", `model_providers.ao-managed.supports_websockets=false`,
	}
}

// CodexProxyArgv adds the managed Responses provider configuration without exposing tickets in arguments.
func CodexProxyArgv(argv []string, env map[string]string) []string {
	flags := CodexProxyArgs(env)
	if len(argv) == 0 || len(flags) == 0 {
		return argv
	}
	result := make([]string, 0, len(argv)+len(flags))
	result = append(result, argv[0])
	insertion := len(argv)
	for i, arg := range argv[1:] {
		if arg == "--" {
			insertion = i + 1
			break
		}
	}
	result = append(result, argv[1:insertion]...)
	result = append(result, flags...)
	return append(result, argv[insertion:]...)
}

// CodexProxyArgvFromEnv reads managed routing values from the child environment.
func CodexProxyArgvFromEnv(argv, env []string) []string {
	values := make(map[string]string)
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	return CodexProxyArgv(argv, values)
}
