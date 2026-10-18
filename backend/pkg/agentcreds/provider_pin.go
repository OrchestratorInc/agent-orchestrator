package agentcreds

import (
	"strings"
)

// Provider pin support for per-role provider selection (issue #6156): a project
// role (worker / orchestrator / reviewer) may pin its Anthropic provider
// independently of the default gateway resolution. The pin vocabulary lives in
// domain (ProviderDirect and validated base URLs); this file is the one place
// that turns a pin — or the absence of one, the default gateway entry — into
// the launch env entries every consumer — session spawn, relaunch, reviewer
// launch, and model-catalog discovery — applies, so a pinned role and its
// stamp can never disagree.
//
// Gateway entries live in AO's own storage (the daemon owns
// ~/.claude/settings.json and never writes it); callers fetch the entry list
// from that storage and hand it in, so this package stays storage-free.

// ProviderPinDirect is the pin value that bypasses every configured gateway.
// It mirrors domain.ProviderDirect; duplicated as an unexported-looking
// literal to keep pkg/ free of an internal-domain import.
const ProviderPinDirect = "direct"

// Gateway scopes, mirroring the settings API's vocabulary.
const (
	GatewayScopeApp     = "app"
	GatewayScopeProject = "project"
)

// providerPinShadowKeys are the env keys the direct pin shadows.
var providerPinShadowKeys = []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL"}

// GatewayEntry is one stored gateway record: what a scope is configured with.
// The token travels only through launch env — never into files AO writes.
type GatewayEntry struct {
	Scope     string // GatewayScopeApp or GatewayScopeProject
	ProjectID string // empty for the app scope
	BaseURL   string
	Token     string
	Model     string
}

// empty reports whether the entry configures nothing at all.
func (e GatewayEntry) empty() bool {
	return e.BaseURL == "" && e.Token == "" && e.Model == ""
}

// pinCandidates orders the entries the way the resolution chain does: the
// project's own entry first, then the app-wide one.
func pinCandidates(projectID string, entries []GatewayEntry) []GatewayEntry {
	candidates := make([]GatewayEntry, 0, 2)
	for _, entry := range entries {
		if entry.Scope == GatewayScopeProject && entry.ProjectID == projectID {
			candidates = append(candidates, entry)
		}
	}
	for _, entry := range entries {
		if entry.Scope == GatewayScopeApp {
			candidates = append(candidates, entry)
		}
	}
	return candidates
}

// ProviderPinEnv maps a role's provider pin to the env entries a launch must
// carry. An empty pin returns nil (the caller applies the default gateway
// entry instead, if one is configured). The direct pin returns empty strings
// for the provider keys — explicit launch env wins over settings files in
// ResolveClaudeSettings, so an empty value shadows everything, including a
// user-managed ~/.claude/settings.json. A base-URL pin returns the URL and the
// credential keys of the matching gateway entry, project scope first; a pin
// that matches no configured entry returns nil, degrading to the default
// resolution — the settings screen only offers real entries, so this only
// fires for a hand-edited config pointing at a gateway that no longer exists.
func ProviderPinEnv(pin, projectID string, entries []GatewayEntry) map[string]string {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return nil
	}
	if pin == ProviderPinDirect {
		env := make(map[string]string, len(providerPinShadowKeys))
		for _, key := range providerPinShadowKeys {
			env[key] = ""
		}
		return env
	}
	// A pinned URL must name a configured entry; its credentials travel with
	// the entry. Only keys the entry actually sets are returned: the pin
	// overlay runs after the project env, and an empty value would clear an
	// explicitly configured model or credential the entry never had.
	for _, entry := range pinCandidates(projectID, entries) {
		if entry.BaseURL != pin {
			continue
		}
		env := make(map[string]string, len(providerPinShadowKeys))
		if entry.Token != "" {
			env["ANTHROPIC_AUTH_TOKEN"] = entry.Token
		}
		if entry.Model != "" {
			env["ANTHROPIC_MODEL"] = entry.Model
		}
		env["ANTHROPIC_BASE_URL"] = pin
		return env
	}
	return nil
}

// DefaultGatewayEnv maps the effective gateway entry — the project's own, else
// the app-wide one — to launch env, so a pinless role still resolves against
// AO's configured gateway deterministically instead of whatever the settings
// chain happens to yield. No configured entry returns nil.
func DefaultGatewayEnv(projectID string, entries []GatewayEntry) map[string]string {
	for _, entry := range pinCandidates(projectID, entries) {
		if entry.empty() {
			continue
		}
		env := make(map[string]string, len(providerPinShadowKeys))
		if entry.BaseURL != "" {
			env["ANTHROPIC_BASE_URL"] = entry.BaseURL
		}
		if entry.Token != "" {
			env["ANTHROPIC_AUTH_TOKEN"] = entry.Token
		}
		if entry.Model != "" {
			env["ANTHROPIC_MODEL"] = entry.Model
		}
		return env
	}
	return nil
}

// ProviderLaunchEnv is what a launch applies for a role: the pin's env when a
// pin is set, else the default gateway entry's env. Nil means nothing to
// overlay — the launch falls through to the settings chain.
func ProviderLaunchEnv(pin, projectID string, entries []GatewayEntry) map[string]string {
	if env := ProviderPinEnv(pin, projectID, entries); env != nil {
		return env
	}
	return DefaultGatewayEnv(projectID, entries)
}
