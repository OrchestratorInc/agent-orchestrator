package mimocode

import (
	"context"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// authProbeTimeout outlasts one recovery of a large SQLite write-ahead log, which
// took 28 seconds for 900 MB. A shorter limit kills MiMo mid-recovery, so every
// later probe starts it again and none finishes.
var authProbeTimeout = 40 * time.Second
var authVerificationTimeout = 20 * time.Second

// disableClaudeImportEnv stops a MiMo command from copying all Claude Code
// history into its own database at startup. Every command but --version does
// that, so a status probe could otherwise run for minutes.
const disableClaudeImportEnv = "MIMOCODE_DISABLE_CLAUDE_IMPORT"

var runAuthProbe = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, binary, args...)
	// `mimo` is a Node wrapper that waits on the real binary. Killing only the
	// wrapper would leave that binary running with our stdout pipe open.
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.Env = append(os.Environ(), disableClaudeImportEnv+"=1")
	return cmd.CombinedOutput()
}

// AuthStatus reports whether MiMo Code has credentials or its built-in free model.
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
		// Credential detection and optional verification have separate budgets.
		// Once credentials are known, an interrupted verification is inconclusive.
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
	// MiMo Auto is usable without a login, so it is absent from `auth list`.
	// Ask MiMo itself whether the free channel is available before reporting
	// an inconclusive auth state.
	models, err := runAuthProbe(probeCtx, binary, "models", "mimo")
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if probeCtx.Err() != nil {
		return ports.AgentAuthStatusUnknown, nil
	}
	if err == nil && freeModelListed(string(models)) {
		// The free MiMo Auto channel needs no login.
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
