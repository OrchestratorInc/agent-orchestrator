package zcode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// zcodeAuthEnvNames are the API-key environments ZCode's CLI and provider
// configuration accept for key-based access. Z.ai's GLM Coding Plan login
// stores its credential in the CLI's own credential store, which has no
// documented read-only probe.
var zcodeAuthEnvNames = []string{"ZAI_API_KEY", "ZCODE_API_KEY", "GLM_API_KEY", "Z_AI_API_KEY"}

// zcodeAuthProbeTimeout bounds the live verification turn. A GLM answer to a
// one-word prompt is normally a few seconds; the budget absorbs provider and
// network latency so the readiness layer's own deadline is not what kills the
// probe.
const zcodeAuthProbeTimeout = 75 * time.Second

// zcodeAuthProbePrompt is the smallest turn that still forces a provider
// round-trip. It runs in plan mode, so the verification cannot mutate the
// probe workspace.
const zcodeAuthProbePrompt = "Reply with exactly: ok"

// AuthStatus reports the selected local auth route, verifying with a real
// provider turn. ZCode has no auth-status subcommand and `zcode doctor --json`
// carries no provider verdict, so presence alone would stay inconclusive
// forever — the audit's finding. The verification costs one tiny plan-mode
// turn in a throwaway directory, and runs for every installed ZCode:
// `zcode login` stores credentials in a store AO cannot read, so absent file
// evidence proves nothing.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	status, _, _, err := p.AuthStatusWithDiagnostics(ctx)
	return status, err
}

// AuthStatusWithDiagnostics implements ports.AgentAuthDiagnosticsChecker: the
// verdict, the account identity (unknown — ZCode has no documented account
// probe), and authored evidence from the same check. A completed turn
// verifies; the provider's plan-exhaustion and rejection codes refute;
// everything else stays unknown rather than guessed.
func (p *Plugin) AuthStatusWithDiagnostics(ctx context.Context) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, nil, err
	}
	started := time.Now()
	presenceDiag := p.credentialPresenceDiagnostic(started)

	binary, err := p.ResolveBinary(ctx)
	if errors.Is(err, ports.ErrAgentBinaryNotFound) {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{},
			diagsWith(presenceDiag, newZCodeAuthDiagnostic("binary_missing", started)), nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, nil, err
	}

	workspace, err := newAuthProbeWorkspace()
	if err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, nil, err
	}
	defer func() { _ = os.RemoveAll(workspace) }()

	probeCtx, cancel := context.WithTimeout(ctx, zcodeAuthProbeTimeout)
	defer cancel()
	cmd := process.CommandContext(probeCtx, binary,
		"--prompt", zcodeAuthProbePrompt, "--output-format", "stream-json", "--cwd", workspace, "--mode", "plan")
	cmd.Dir = workspace
	output, runErr := cmd.CombinedOutput()

	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{},
			diagsWith(presenceDiag, newZCodeAuthDiagnostic("probe_timeout", started)), nil
	}
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, nil, err
	}

	status, code := classifyZCodeAuthProbe(string(output), runErr)
	diag := newZCodeAuthDiagnostic(code, started)
	diag.DurationMs = time.Since(started).Milliseconds()
	return status, domain.AgentAccountIdentity{}, diagsWith(presenceDiag, diag), nil
}

// newAuthProbeWorkspace keeps transient agent work under AO's configured data
// directory, including installations that use AO_DATA_DIR for isolation.
func newAuthProbeWorkspace() (string, error) {
	dataDir := os.Getenv("AO_DATA_DIR")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dataDir = filepath.Join(home, ".ao", "data")
	}
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	root := filepath.Join(dataDir, "auth-probes")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, "zcode-")
}

// credentialPresenceDiagnostic records, as authored evidence, that a
// documented credential location held a usable key. It never decides the
// verdict by itself: `zcode login` users have no file evidence at all.
func (p *Plugin) credentialPresenceDiagnostic(started time.Time) *domain.AgentAuthDiagnostic {
	if present, source := p.credentialEvidence(); present {
		diag := newZCodeAuthDiagnostic("credential_present", started)
		diag.Source = source
		return &diag
	}
	return nil
}

// diagsWith orders the optional presence evidence ahead of the verdict.
func diagsWith(presence *domain.AgentAuthDiagnostic, verdict domain.AgentAuthDiagnostic) []domain.AgentAuthDiagnostic {
	diags := make([]domain.AgentAuthDiagnostic, 0, 2)
	if presence != nil {
		diags = append(diags, *presence)
	}
	return append(diags, verdict)
}

// classifyZCodeAuthProbe requires the pinned CLI's successful turn.completed
// event and matching final result, with no error and a successful process exit.
// Session creation, prompt echoes, malformed/incomplete output and failed turns
// cannot establish authorization. Raw output is never returned in diagnostics.
func classifyZCodeAuthProbe(output string, runErr error) (ports.AgentAuthStatus, string) {
	text := strings.ToLower(output)
	switch {
	case strings.Contains(text, "1113"), strings.Contains(text, "insufficient balance"):
		return ports.AgentAuthStatusUnauthorized, "provider_plan_exhausted"
	case strings.Contains(text, "authenticate_error"),
		strings.Contains(text, "invalid_api_key"),
		strings.Contains(text, "invalid api key"),
		strings.Contains(text, "unauthorized"),
		strings.Contains(text, "not logged in"),
		strings.Contains(text, "no provider configured"),
		strings.Contains(text, "login required"):
		return ports.AgentAuthStatusUnauthorized, "provider_rejected_credential"
	}
	if runErr != nil {
		return ports.AgentAuthStatusUnknown, "check_inconclusive"
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	completedSession, completedTurn := "", ""
	success := false
	for {
		var event struct {
			Type      string          `json:"type"`
			SessionID string          `json:"sessionId"`
			TurnID    string          `json:"turnId"`
			Response  string          `json:"response"`
			Error     json.RawMessage `json:"error"`
			Errors    json.RawMessage `json:"errors"`
			Payload   struct {
				ResultType string          `json:"resultType"`
				Error      json.RawMessage `json:"error"`
			} `json:"payload"`
			Projection struct {
				Status    string `json:"status"`
				TurnCount int    `json:"turnCount"`
			} `json:"projection"`
		}
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return ports.AgentAuthStatusUnknown, "check_inconclusive"
		}
		for _, raw := range []json.RawMessage{event.Error, event.Errors, event.Payload.Error} {
			switch strings.TrimSpace(string(raw)) {
			case "", "null", "[]", "false":
			default:
				return ports.AgentAuthStatusUnknown, "check_inconclusive"
			}
		}
		switch event.Type {
		case "turn.failed":
			return ports.AgentAuthStatusUnknown, "check_inconclusive"
		case "turn.completed":
			if event.Payload.ResultType != "success" || event.SessionID == "" || event.TurnID == "" {
				return ports.AgentAuthStatusUnknown, "check_inconclusive"
			}
			completedSession, completedTurn = event.SessionID, event.TurnID
		case "result":
			success = completedSession != "" && completedTurn != "" && event.SessionID == completedSession && event.TurnID == completedTurn && event.Projection.Status == "idle" && event.Projection.TurnCount > 0 && strings.EqualFold(strings.TrimSpace(event.Response), "ok")
		}
	}
	if success {
		return ports.AgentAuthStatusAuthorized, "provider_turn_completed"
	}
	return ports.AgentAuthStatusUnknown, "check_inconclusive"
}

// newZCodeAuthDiagnostic builds the authored evidence entry for one auth
// check, using the shared diagnostic vocabulary (authored explanations, never
// raw provider output).
func newZCodeAuthDiagnostic(code string, started time.Time) domain.AgentAuthDiagnostic {
	diag := domain.NewAgentAuthDiagnostic("authentication", code)
	diag.DurationMs = time.Since(started).Milliseconds()
	return diag
}

// credentialEvidence reports whether any documented credential location holds
// a usable key: the environment, or a non-empty apiKey in the personal
// provider configuration.
func (p *Plugin) credentialEvidence() (bool, string) {
	for _, name := range zcodeAuthEnvNames {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true, "environment:" + name
		}
	}
	path := zcodeProviderConfigPath()
	if path == "" {
		return false, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, ""
	}
	if providerConfigHasAPIKey(data) {
		return true, "provider_config"
	}
	return false, ""
}

// zcodeProviderConfigPath resolves the personal model configuration file. See
// docs/usage/03-config.md in zai-org/ZCode: ZCODE_PERSONAL_PROVIDER_CONFIG_FILE
// overrides the file, ZCODE_DATA_BASE_DIR moves the data root, and the default
// is ~/.zcode/v2/provider_config.json.
func zcodeProviderConfigPath() string {
	if file := strings.TrimSpace(os.Getenv("ZCODE_PERSONAL_PROVIDER_CONFIG_FILE")); file != "" {
		return file
	}
	base := strings.TrimSpace(os.Getenv("ZCODE_DATA_BASE_DIR"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".zcode")
	}
	return filepath.Join(base, "v2", "provider_config.json")
}

// providerConfigHasAPIKey walks the decoded configuration for any non-empty
// apiKey value. The file's schema is versioned by its author (`schemaVersion`),
// so AO matches on the field name rather than a fixed path.
func providerConfigHasAPIKey(data []byte) bool {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return false
	}
	return walkAPIKey(doc)
}

func walkAPIKey(node any) bool {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if strings.EqualFold(strings.TrimSpace(key), "apiKey") {
				if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
					return true
				}
			}
			if walkAPIKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if walkAPIKey(child) {
				return true
			}
		}
	}
	return false
}
