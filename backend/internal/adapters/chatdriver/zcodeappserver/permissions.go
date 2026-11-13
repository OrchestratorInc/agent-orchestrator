package zcodeappserver

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// zcode session modes (`zcode --help`: build, edit, plan, yolo, guarded).
const (
	modeEdit  = "edit"
	modeBuild = "build"
	modeYolo  = "yolo"
)

// sessionMode maps AO's permission mode onto session/create.mode (DESIGN 4.4),
// the same table the TUI adapter uses for --mode so chat and TUI agree.
//
// ReadOnly is refused: zcode's `plan` is a planning mode, not a provider-enforced
// read-only sandbox, so claiming it would run a reviewer with write access.
func sessionMode(mode ports.PermissionMode, readOnly bool) (string, error) {
	if readOnly {
		return "", fmt.Errorf("%w: zcode has no provider-enforced read-only sandbox (plan is a planning mode)",
			ports.ErrChatPermissionModeUnsupported)
	}
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAuto:
		return modeBuild, nil
	case ports.PermissionModeBypassPermissions:
		return modeYolo, nil
	default:
		return modeEdit, nil
	}
}

// validateStart refuses what the provider would refuse later and worse: a
// relative --cwd and a read-only request (no provider-enforced sandbox). Every
// permission mode is admitted: interaction/requestPermission is PROVEN under
// mode edit (p5-approval-allow.ndjson), so the Phase 0 bypass-only guard is
// gone.
func validateStart(workspace string, mode ports.PermissionMode, readOnly bool) error {
	if !filepath.IsAbs(workspace) {
		// app-server validates --cwd (`zdi`) and a relative path would resolve
		// against its own process directory, silently putting the agent in the
		// wrong tree.
		return fmt.Errorf("workspace path must be absolute, got %q", workspace)
	}
	_, err := sessionMode(mode, readOnly)
	return err
}

// decisionKind classifies a provider permission option by its response object
// (PROVEN option set, p5-approval-allow.ndjson: allow_once {decision:allow},
// allow_always {decision:allow, permissionUpdates:[addRules]}, deny
// {decision:deny}).
func decisionKind(resp zcodeproto.PermissionResponse) ports.ChatDecisionKind {
	always := len(resp.PermissionUpdates) > 0 && strings.TrimSpace(string(resp.PermissionUpdates)) != "null"
	switch resp.Decision {
	case "allow":
		if always {
			return ports.ChatDecisionAllowAlways
		}
		return ports.ChatDecisionAllowOnce
	case "deny":
		if always {
			return ports.ChatDecisionRejectAlways
		}
		return ports.ChatDecisionRejectOnce
	case "modify", "escalate":
		return ports.ChatDecisionAllowOnce
	default:
		return ""
	}
}

// decisionOptions renders the provider's options for an approval card. Raw is
// the option's response object verbatim: the server zod-parses the reply
// strictly, so AO echoes rather than rebuilds it.
func decisionOptions(options []zcodeproto.PermissionOption) []ports.ChatDecisionOption {
	out := make([]ports.ChatDecisionOption, 0, len(options))
	for _, option := range options {
		if option.OptionID == "" {
			continue
		}
		var resp zcodeproto.PermissionResponse
		_ = json.Unmarshal(option.Response, &resp)
		label := option.Name
		if label == "" {
			label = option.OptionID
		}
		out = append(out, ports.ChatDecisionOption{
			ID: option.OptionID, Label: label, Kind: decisionKind(resp), Raw: option.Response,
		})
	}
	return out
}

// inputRequestFrom turns interaction/requestUserInput into AO's form request
// (DESIGN 4.2; question shape PROVEN, p6-user-input.ndjson). The schema is a
// flat object keyed by each question's header (or question text), with the
// offered values as an enum.
func inputRequestFrom(params zcodeproto.RequestUserInputParams) *ports.ChatInputRequest {
	message := params.Prompt
	if message == "" && len(params.Questions) > 0 {
		message = params.Questions[0].Question
	}
	properties := make(map[string]any, len(params.Questions))
	for i, question := range params.Questions {
		key := question.Header
		if key == "" {
			key = question.Question
		}
		if key == "" {
			key = fmt.Sprintf("answer_%d", i)
		}
		property := map[string]any{"type": "string"}
		if question.Question != "" {
			property["description"] = question.Question
		}
		if len(question.Options) > 0 {
			values := make([]any, 0, len(question.Options))
			for _, option := range question.Options {
				values = append(values, option.Value)
			}
			property["enum"] = values
		}
		properties[key] = property
	}
	return &ports.ChatInputRequest{
		Mode:          ports.ChatInputModeForm,
		Message:       message,
		Schema:        map[string]any{"type": "object", "properties": properties},
		ElicitationID: params.RequestID,
	}
}
