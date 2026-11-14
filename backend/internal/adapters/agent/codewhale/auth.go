package codewhale

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var activeProviderPattern = regexp.MustCompile(`(?mi)^active provider:\s*([^\s(]+)`)
var activeSourcePattern = regexp.MustCompile(`(?mi)^active source:\s*([^\s(]+)`)
var liveCheckPassedPattern = regexp.MustCompile(`(?mi)^Ready: .*API check passed`)

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
		ports.RecordAuthEvidence(ctx, domain.CommandSource("codewhale", "doctor", "--probe-api"))
		return ports.AgentAuthStatusAuthorized, nil
	}
	ports.RecordAuthEvidence(ctx, domain.CommandSource("codewhale", "auth", "status", "--provider", provider))
	return ports.AgentAuthStatusConfigured, nil
}

func (p *Plugin) liveCheckPassed(ctx context.Context) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	out, _ := p.execute(ctx, "", nil, "doctor", "--probe-api")
	return liveCheckPassedPattern.Match(out)
}
