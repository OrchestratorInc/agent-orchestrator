package codebuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const authProbeTimeout = 30 * time.Second
const authProbeOutputLimit = 64 << 10

var _ ports.AgentAuthDiagnosticsChecker = (*Plugin)(nil)

// AuthStatus verifies the provider selected by the native CLI. AO does not
// inspect its credential stores or infer login from another harness's keys.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	status, _, _, err := p.AuthStatusWithDiagnostics(ctx)
	return status, err
}

// AuthStatusWithDiagnostics performs a tiny inference using the same native
// user profile and inherited environment as CodeBuddy startup. Harness settings
// has no project scope: project/local settings and project instructions are
// excluded. A successful check may consume a small amount of provider quota.
func (p *Plugin) AuthStatusWithDiagnostics(ctx context.Context) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, nil, err
	}
	fallback := ports.AgentAuthStatusUnknown
	for _, key := range []string{"CODEBUDDY_API_KEY", "CODEBUDDY_AUTH_TOKEN"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			fallback = ports.AgentAuthStatusConfigured
			break
		}
	}
	identity := domain.CommandSource("codebuddy", "--print")
	finish := func(status ports.AgentAuthStatus, code string, started time.Time) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
		step := domain.NewAgentAuthDiagnostic("native_inference", code)
		step.DurationMs = time.Since(started).Milliseconds()
		if code == "provider_quota" {
			step.Reason = "CodeBuddy's selected provider reported a rate or usage limit; this does not establish that you are signed out."
			step.NextStep = "Check that provider's quota, balance and plan endpoint, then try again."
		}
		if status.HasCredential() {
			ports.RecordAuthEvidence(ctx, identity)
		}
		return status, identity, []domain.AgentAuthDiagnostic{step}, nil
	}
	started := time.Now()
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return finish(fallback, "command_failed", started)
	}
	// AO-owned transient files stay under AO_DATA_DIR (or ~/.ao), never the
	// invoking checkout or a provider profile. Each check has an empty directory.
	base := ports.AgentAuthDataDir(ctx)
	if base == "" {
		base = strings.TrimSpace(os.Getenv("AO_DATA_DIR"))
	}
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return finish(fallback, "check_failed", started)
		}
		base = filepath.Join(home, ".ao")
	}
	base = filepath.Join(base, "auth-probes")
	if err := os.MkdirAll(base, 0700); err != nil {
		return finish(fallback, "check_failed", started)
	}
	dir, err := os.MkdirTemp(base, "codebuddy-")
	if err != nil {
		return finish(fallback, "check_failed", started)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	probeCtx, cancel := context.WithTimeout(ctx, authProbeTimeout)
	defer cancel()
	args := []string{
		"--print", "--output-format", "json", "--tools", "", "--max-turns", "1",
		"--no-session-persistence", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--setting-sources", "user", "--settings", `{"disableAllHooks":true}`,
		"--agent", "cli", "--system-prompt", "Reply briefly without using tools.",
		"--", "Reply with OK.",
	}
	cmd := aoprocess.CommandContext(probeCtx, binary, args...)
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = time.Second
	cmd.Dir = dir
	cmd.Env = authProbeEnv()
	output := &boundedAuthOutput{cancel: cancel}
	cmd.Stdout = output
	// Native errors can echo credentials and URLs. They never enter diagnostics,
	// returned errors, or AO logs, and stderr cannot inflate the in-memory buffer.
	cmd.Stderr = io.Discard
	runErr := cmd.Run()
	if err := ctx.Err(); err != nil {
		_, id, steps, _ := finish(fallback, "probe_cancelled", started)
		return fallback, id, steps, err
	}
	if output.exceeded {
		return finish(fallback, "output_unrecognized", started)
	}
	if probeCtx.Err() != nil {
		return finish(fallback, "probe_timeout", started)
	}
	status, code := classifyAuthResult(output.Bytes(), runErr == nil, fallback)
	return finish(status, code, started)
}

func authProbeEnv() []string {
	env := os.Environ()
	// Function-hook mods have a separate native startup gate from disableAllHooks.
	// Override only the probe child; normal sessions retain their plugin setup.
	for _, binding := range []string{"CODEBUDDY_CODE_DISABLE_BACKGROUND_TASKS=1", "CODEBUDDY_DISABLE_CRON=1", "DISABLE_AUTOUPDATER=1", "CBC_ENABLE_FUNCTION_HOOKS=0", "CODEBUDDY_PLUGIN_DIRS="} {
		key, _, _ := strings.Cut(binding, "=")
		filtered := env[:0]
		for _, existing := range env {
			name, _, _ := strings.Cut(existing, "=")
			if !strings.EqualFold(name, key) {
				filtered = append(filtered, existing)
			}
		}
		env = append(filtered, binding)
	}
	return env
}

type boundedAuthOutput struct {
	bytes.Buffer
	exceeded bool
	cancel   context.CancelFunc
}

func (b *boundedAuthOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := authProbeOutputLimit - b.Len()
	if len(p) > remaining {
		_, _ = b.Buffer.Write(p[:remaining])
		b.exceeded = true
		b.cancel()
		return n, nil
	}
	return b.Buffer.Write(p)
}

// Native 2.164 ResultMessage is declared in stream-json-protocol.d.ts and built
// by result-message-utils. JSON mode emits a history array ending with result.
// Cancellation also uses subtype success, so the terminal marker and actual
// inference output must be checked. Never feed these bytes to a prose classifier.
func classifyAuthResult(raw []byte, exitedOK bool, fallback ports.AgentAuthStatus) (ports.AgentAuthStatus, string) {
	type result struct {
		Type           string `json:"type"`
		Subtype        string `json:"subtype"`
		IsError        *bool  `json:"is_error"`
		Result         string `json:"result"`
		TerminalReason string `json:"terminal_reason"`
		Usage          struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Errors     []json.RawMessage `json:"errors"`
		ErrorsInfo []struct {
			Status   int    `json:"status"`
			Category string `json:"category"`
		} `json:"errors_info"`
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return fallback, "output_unrecognized"
	}
	if raw[0] == '[' {
		var history []json.RawMessage
		if json.Unmarshal(raw, &history) != nil || len(history) == 0 {
			return fallback, "output_unrecognized"
		}
		raw = history[len(history)-1]
	}
	var r result
	if json.Unmarshal(raw, &r) != nil || r.Type != "result" || r.IsError == nil {
		return fallback, "output_unrecognized"
	}
	if !*r.IsError && exitedOK && r.Subtype == "success" && r.TerminalReason == "" && len(r.Errors) == 0 && len(r.ErrorsInfo) == 0 && strings.TrimSpace(r.Result) != "" && r.Usage.OutputTokens > 0 {
		return ports.AgentAuthStatusAuthorized, "provider_accepted"
	}
	if *r.IsError && r.Subtype == "error_during_execution" {
		// Quota/transport do not prove sign-out. Only structured credential rejection
		// establishes unauthorized; raw messages and unknown categories stay private.
		for _, info := range r.ErrorsInfo {
			if info.Status == 429 || info.Category == "quota" {
				return fallback, "provider_quota"
			}
			if info.Category == "network" || info.Status == 408 || info.Status >= 500 {
				return fallback, "provider_unreachable"
			}
		}
		for _, info := range r.ErrorsInfo {
			if info.Status == 401 || info.Status == 403 {
				return ports.AgentAuthStatusUnauthorized, "provider_rejected"
			}
		}
	}
	return fallback, "check_inconclusive"
}
