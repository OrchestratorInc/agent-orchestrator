package mastracode

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

//go:embed auth-probe.mjs
var nativeAuthProbe string

var _ ports.AgentAuthDiagnosticsChecker = (*Plugin)(nil)

// ProfileDir is shared by native login, auth checks and session launch. An
// explicit native profile override takes precedence over AO's isolated default.
func ProfileDir(dataDir string) string {
	if profile := os.Getenv("MASTRA_APP_DATA_DIR"); profile != "" {
		return profile
	}
	if dataDir == "" {
		dataDir = os.Getenv("AO_DATA_DIR")
	}
	if dataDir == "" {
		dataDir = filepath.Join(userHome(), ".ao", "data")
	}
	return filepath.Join(dataDir, adapterID, "profile")
}

// AuthStatus reports whether the selected provider accepted a native SDK probe.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	status, _, _, err := p.AuthStatusWithDiagnostics(ctx)
	return status, err
}

// AuthStatusWithDiagnostics delegates credential precedence and model routing to
// the installed native SDK. Only a successful selected-provider round trip can
// authorize; the probe creates no controller, conversation or history database.
func (p *Plugin) AuthStatusWithDiagnostics(ctx context.Context) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	started := time.Now()
	finish := func(status ports.AgentAuthStatus, code, provider string, httpStatus int, err error) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
		step := domain.NewAgentAuthDiagnostic("Mastra native SDK provider check", code)
		step.DurationMs = time.Since(started).Milliseconds()
		step.Provider, step.HTTPStatus = provider, httpStatus
		switch code {
		case "mastra_model_unselected":
			step.Reason = "Mastra credentials are configured, but no explicit native build model is selected for verification."
			step.NextStep = "Open Mastra Code in build mode and use /model or /setup in the same AO profile to select a build model, then check again."
		case "mastra_sdk_unsupported":
			step.Reason = "The installed Mastra version does not expose the reviewed authentication interface."
			step.NextStep = "Use Mastra's native provider setup and verify access in a terminal."
		case "provider_rate_limited":
			step.Reason = "The selected provider rate-limited the verification request; this is not a rejected login."
			step.NextStep = "Wait for the provider limit to reset, then check again."
		case "provider_quota_exhausted":
			step.Reason = "The selected provider reported a quota or billing limit; credentials remain configured."
			step.NextStep = "Review the provider's quota or billing, then check again."
		}
		identity := domain.AgentAccountIdentity{Provider: provider}
		if status.HasCredential() {
			ports.RecordAuthEvidence(ctx, identity)
		}
		return status, identity, []domain.AgentAuthDiagnostic{step}, err
	}
	if ctx.Err() != nil {
		return finish(ports.AgentAuthStatusUnknown, "probe_cancelled", "", 0, ctx.Err())
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return finish(ports.AgentAuthStatusUnknown, "command_failed", "", 0, nil)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return finish(ports.AgentAuthStatusUnknown, "command_failed", "", 0, nil)
	}
	profile := ProfileDir(ports.AgentAuthDataDir(ctx))
	if !filepath.IsAbs(profile) {
		return finish(ports.AgentAuthStatusUnknown, "check_inconclusive", "", 0, nil)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := aoprocess.CommandContext(probeCtx, node, "--input-type=module", "-e", nativeAuthProbe, "--", binary, profile)
	cmd.Env = append(os.Environ(), "MASTRA_APP_DATA_DIR="+profile)
	output := &authProbeOutput{}
	cmd.Stdout, cmd.Stderr = output, io.Discard
	err = cmd.Run()
	if ctx.Err() != nil {
		return finish(ports.AgentAuthStatusUnknown, "probe_cancelled", "", 0, ctx.Err())
	}
	if probeCtx.Err() != nil {
		return finish(ports.AgentAuthStatusUnknown, "probe_timeout", "", 0, nil)
	}
	var result struct {
		Status     ports.AgentAuthStatus `json:"status"`
		Code       string                `json:"code"`
		Provider   string                `json:"provider"`
		HTTPStatus int                   `json:"httpStatus"`
	}
	if err != nil || output.overflow || json.Unmarshal(output.Bytes(), &result) != nil {
		return finish(ports.AgentAuthStatusUnknown, "check_inconclusive", "", 0, nil)
	}
	allowed := map[string]ports.AgentAuthStatus{
		"provider_accepted": ports.AgentAuthStatusAuthorized, "provider_rejected": ports.AgentAuthStatusUnauthorized,
		"credential_missing": ports.AgentAuthStatusUnknown, "mastra_sdk_unsupported": ports.AgentAuthStatusUnknown,
		"credential_present": ports.AgentAuthStatusConfigured, "mastra_model_unselected": ports.AgentAuthStatusConfigured,
		"provider_unreachable": ports.AgentAuthStatusConfigured, "provider_rate_limited": ports.AgentAuthStatusConfigured,
		"provider_quota_exhausted": ports.AgentAuthStatusConfigured, "provider_unsupported": ports.AgentAuthStatusConfigured,
	}
	if status, ok := allowed[result.Code]; !ok || status != result.Status || !regexp.MustCompile(`^[a-zA-Z0-9._-]{0,64}$`).MatchString(result.Provider) || result.HTTPStatus < 0 || result.HTTPStatus > 599 {
		return finish(ports.AgentAuthStatusUnknown, "check_inconclusive", "", 0, nil)
	}
	return finish(result.Status, result.Code, result.Provider, result.HTTPStatus, nil)
}

// Keep subprocess output bounded even if an incompatible dependency writes to
// stdout. Raw stderr and provider text never become diagnostics or errors.
type authProbeOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *authProbeOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 4096 - b.Len()
	if n > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
