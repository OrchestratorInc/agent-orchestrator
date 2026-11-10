// Package deepseekacp binds the user's own DeepSeek Harness installation to
// AO's reusable ACP Chat transport.
package deepseekharnessacp

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// acpProfile is the shipped DeepSeek Harness profile that serves the
// automation-only ACP v1 stdio surface.
const acpProfile = "acp"

// New launches `dsh --profile acp` from the exact binary resolved by the
// DeepSeek Harness agent plugin. The profile supplies the model route and
// credentials; AO adds no environment overlay of its own.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:                  domain.HarnessDeepSeek,
		Configure:                configure,
		SessionOptions:           sessionOptions,
		PromptFailureCode:        promptFailureCode,
		SessionPreamble:          SessionPreamble,
		PermissionPolicy:         permissionPolicy,
		RequireAdvertisedOptions: true,
	}, log)
}

// The harness reports a missing key as a generic ACP internal error. Only its
// explicit route-specific diagnostic is safe to turn into setup guidance.
func promptFailureCode(err error) domain.ConversationFailureCode {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != -32603 {
		return ""
	}
	detail, _ := requestErr.Data.(string)
	if detail == "" && strings.HasPrefix(requestErr.Message, "Internal error: ") {
		detail = strings.TrimPrefix(requestErr.Message, "Internal error: ")
	}
	if !strings.HasPrefix(strings.TrimSpace(detail),
		`turn failed: llm-deepseek: no API key for provider route "deepseek-official";`) {
		return ""
	}
	return domain.ConversationFailureProviderKeyMissing
}

func configure(_ context.Context, _ acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	return []string{"--profile", acpProfile}, nil, nil
}

// sessionOptions maps AO's per-turn choices onto DeepSeek Harness's ACP config
// options. Model values are opaque: Harness encodes each choice as a JSON array
// string of the form ["deepseek-official","deepseek-v4-flash"], and the value AO
// received from the session catalog is the value it sends back.
func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	options := make([]acpdriver.SessionOption, 0, 2)
	if model := strings.TrimSpace(settings.Model); model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: model})
	}
	if effort := strings.TrimSpace(settings.Effort); effort != "" {
		options = append(options, acpdriver.SessionOption{ID: "reasoning_effort", Value: deepseekEffort(effort)})
	}
	if len(options) == 0 {
		return nil
	}
	return options
}

// SessionPreamble delivers AO's standing instructions as a text block ahead of
// the first prompt. DeepSeek Harness accepts no system-prompt launch flag and
// its session/new handler reads only the working directory, so plain text on
// the first turn is the channel it follows — the same shape as agyacp. An
// embedded resource is avoided: Antigravity-class agents treated the resource
// URI as something to look up rather than as instructions in testing.
func SessionPreamble(cfg acpdriver.LaunchConfig) []acpsdk.ContentBlock {
	standing := strings.TrimSpace(cfg.SystemPrompt)
	if standing == "" {
		return nil
	}
	return []acpsdk.ContentBlock{acpsdk.TextBlock(
		"Standing instructions from Agent Orchestrator. Follow them for this whole session:\n" +
			"<ao_standing_instructions>\n" + standing + "\n</ao_standing_instructions>",
	)}
}

// deepseekEffort 只兼容关闭思考的旧别名，原生档位由会话实时目录校验。
func deepseekEffort(effort string) string {
	if strings.EqualFold(effort, "none") {
		return "off"
	}
	return effort
}

// permissionPolicy is AO's side of accept-edits and auto. DeepSeek Harness asks
// through session/request_permission with one-shot allow/reject choices and
// expects the client to answer, so AO answers per request rather than granting
// anything up front. Bypass needs no entry: Harness only asks about actions its
// own configuration has not already decided.
func permissionPolicy(
	mode ports.PermissionMode,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.PermissionOptionId, bool) {
	mode = ports.NormalizePermissionMode(mode)
	kind := params.ToolCall.Kind
	if mode != ports.PermissionModeAuto && (mode != ports.PermissionModeAcceptEdits || kind == nil ||
		(*kind != acpsdk.ToolKindEdit && *kind != acpsdk.ToolKindDelete && *kind != acpsdk.ToolKindMove)) {
		return "", false
	}
	for _, option := range params.Options {
		if option.Kind == acpsdk.PermissionOptionKindAllowOnce {
			return option.OptionId, true
		}
	}
	return "", false
}
