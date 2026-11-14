package fake

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const SpeedupEnv = "AO_FAKE_SPEEDUP"

const HarnessEnv = "AO_FAKE_HARNESS"

const basePhaseSeconds = 2.0

type Plugin struct {
	agentbase.Base
}

func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          "fake",
		Name:        "Fake",
		Description: "Deterministic LLM-free harness for e2e tests.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, _ ports.LaunchConfig) (cmd []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sh, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	return []string{sh, "-lc", timelineScript(phaseSleep())}, nil
}

func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if !isTruthy(os.Getenv(HarnessEnv)) {
		return ports.AgentAuthStatusUnauthorized, nil
	}
	return ports.AgentAuthStatusAuthorized, nil
}

func isTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path, err := exec.LookPath("sh")
	if err != nil {
		return "", fmt.Errorf("fake harness requires a POSIX shell (sh) on PATH: %w", err)
	}
	return path, nil
}

func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "session-start", "user-prompt-submit", "pr-push":
		return domain.ActivityActive, true
	case "agent-needs-input":
		return domain.ActivityWaitingInput, true
	case "permission-request":
		return domain.ActivityBlocked, true
	case "stop":
		return domain.ActivityIdle, true
	case "session-end":
		return domain.ActivityExited, true
	default:
		return "", false
	}
}

func phaseSleep() float64 {
	speedup := 1.0
	if raw := strings.TrimSpace(os.Getenv(SpeedupEnv)); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && parsed > 0 {
			speedup = parsed
		}
	}
	return basePhaseSeconds / speedup
}

func timelineScript(sleepSeconds float64) string {
	d := formatSeconds(sleepSeconds)
	var b strings.Builder
	phase := func(marker, event string, sleep bool) {
		fmt.Fprintf(&b, "printf '%%s\\n' 'fake-agent: %s'\n", marker)
		if event != "" {
			fmt.Fprintf(&b, "ao hooks fake %s </dev/null >/dev/null 2>&1 || true\n", event)
		}
		if sleep {
			fmt.Fprintf(&b, "sleep %s\n", d)
		}
	}
	phase("spawning", "", true)
	phase("active", "session-start", true)
	phase("waiting for input", "agent-needs-input", true)
	phase("active", "user-prompt-submit", true)
	phase("pushed PR https://github.com/aoagents/agent-orchestrator/pull/2483", "pr-push", true)
	phase("blocked", "permission-request", true)
	phase("done", "session-end", false)
	return b.String()
}

func formatSeconds(s float64) string {
	return strconv.FormatFloat(s, 'f', -1, 64)
}
