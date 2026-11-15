package mimocode

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var authProbeTimeout = 20 * time.Second
var authVerificationTimeout = 20 * time.Second

var runAuthProbe = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
	return aoprocess.CommandContext(ctx, binary, args...).CombinedOutput()
}

func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, authProbeTimeout)
	defer cancel()
	out, _ := runAuthProbe(probeCtx, binary, "auth", "list")
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if probeCtx.Err() != nil {
		return ports.AgentAuthStatusUnknown, nil
	}
	if status, ok := providerListStatus(string(out)); ok && status == ports.AgentAuthStatusConfigured {
		ports.RecordAuthEvidence(ctx, domain.CommandSource(binary, "auth", "list"))
		verificationCtx, cancelVerification := context.WithTimeout(ctx, authVerificationTimeout)
		defer cancelVerification()
		status = verifySelectedConnection(verificationCtx, binary)
		if err := verificationCtx.Err(); err != nil {
			code := "probe_timeout"
			if err == context.Canceled {
				code = "probe_cancelled"
			}
			ports.RecordAuthDiagnostic(ctx, domain.NewAgentAuthDiagnostic("provider_validation", code))
			return ports.AgentAuthStatusConfigured, nil
		}
		return status, nil
	}
	models, err := runAuthProbe(probeCtx, binary, "models", "mimo")
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if probeCtx.Err() != nil {
		return ports.AgentAuthStatusUnknown, nil
	}
	if err == nil && freeModelListed(string(models)) {
		ports.RecordAuthEvidence(ctx, domain.CommandSource(binary, "models", "mimo"))
		return ports.AgentAuthStatusConfigured, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

func freeModelListed(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "mimo/mimo-auto" {
			return true
		}
	}
	return false
}

var providerCountRE = regexp.MustCompile(`(?m)\b([1-9][0-9]*)\s+(credentials?|environment variables?)\b`)

func providerListStatus(output string) (ports.AgentAuthStatus, bool) {
	text := strings.ToLower(output)
	if providerCountRE.MatchString(text) {
		return ports.AgentAuthStatusConfigured, true
	}
	if strings.Contains(text, "0 credentials") && strings.Contains(text, "0 environment variable") {
		return ports.AgentAuthStatusUnknown, true
	}
	return ports.AgentAuthStatusUnknown, false
}
