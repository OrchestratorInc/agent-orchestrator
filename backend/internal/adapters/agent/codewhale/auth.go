package codewhale

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var activeProviderPattern = regexp.MustCompile(`(?mi)^active provider:\s*([^\s(]+)`)
var activeSourcePattern = regexp.MustCompile(`(?mi)^active source:\s*([^\s(]+)`)
var liveCheckPassedPattern = regexp.MustCompile(`(?mi)^Ready: .*API check passed`)

// AuthStatus asks Codewhale which provider is active, then inspects that
// provider's local credential source. A present credential is reported as
// configured; Codewhale's own `doctor --probe-api` connectivity check then
// exercises the credential against the live provider API, and only a passed
// live check reports authorized. A failed, offline, or errored probe keeps
// the presence result instead of claiming the user is signed out.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if _, err := p.ResolveBinary(ctx); err != nil {
		if errors.Is(err, ports.ErrAgentBinaryNotFound) {
			return ports.AgentAuthStatusUnknown, nil
		}
		return ports.AgentAuthStatusUnknown, err
	}
	out, err := p.execute(ctx, "", nil, "auth", "status")
	if err != nil {
		return ports.AgentAuthStatusUnknown, fmt.Errorf("codewhale auth status: %w", err)
	}
	match := activeProviderPattern.FindSubmatch(out)
	if len(match) < 2 || strings.TrimSpace(string(match[1])) == "" {
		return ports.AgentAuthStatusUnknown, nil
	}
	provider := strings.TrimSpace(string(match[1]))
	out, err = p.execute(ctx, "", nil, "auth", "status", "--provider", provider)
	if err != nil {
		return ports.AgentAuthStatusUnknown, fmt.Errorf("codewhale auth status for provider %q: %w", provider, err)
	}
	match = activeSourcePattern.FindSubmatch(out)
	if len(match) < 2 {
		return ports.AgentAuthStatusUnknown, nil
	}
	source := strings.ToLower(strings.TrimSpace(string(match[1])))
	if source == "" || source == "missing" || source == "unset" || source == "none" {
		return ports.AgentAuthStatusUnknown, nil
	}
	if p.liveCheckPassed(ctx) {
		return ports.AgentAuthStatusAuthorized, nil
	}
	return ports.AgentAuthStatusConfigured, nil
}

// liveCheckPassed runs Codewhale's diagnostics probe, which performs a live
// authenticated call against the active provider route. doctor reports the
// outcome as report text ("Ready: ... API check passed" versus "Not ready:
// ...") and its exit status does not discriminate, so the decision reads the
// report; any probe error, including timeout, counts as not passed.
func (p *Plugin) liveCheckPassed(ctx context.Context) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	out, _ := p.execute(ctx, "", nil, "doctor", "--probe-api")
	return liveCheckPassedPattern.Match(out)
}
