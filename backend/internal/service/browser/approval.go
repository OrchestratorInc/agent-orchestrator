package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/approval"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// describeAction asks a runtime what a gated action would act on. It is
// internal: agents cannot call it.
const describeAction = "describe"

// DefaultApprovalTimeout is how long an agent's command waits for the user.
// It stays under the common two-minute tool-call limit of agent harnesses.
const DefaultApprovalTimeout = 90 * time.Second

type approvalGate interface {
	SessionMode(sessionID string) approval.Mode
	Request(ctx context.Context, req approval.Request) (approval.Outcome, error)
}

// ApprovalError is returned when the user denied a gated action or did not
// answer in time. It unwraps to the API error the agent sees.
type ApprovalError struct {
	Decision approval.Decision
	Summary  string
	api      *apierr.Error
}

func (e *ApprovalError) Error() string { return e.api.Error() }

// Unwrap exposes the API error for transport mapping.
func (e *ApprovalError) Unwrap() error { return e.api }

// UseApprovals turns on the approval gate for consequential actions.
func (s *Service) UseApprovals(gate approvalGate, timeout time.Duration) {
	if timeout <= 0 {
		timeout = DefaultApprovalTimeout
	}
	s.approvals = gate
	s.approvalTimeout = timeout
}

// Preflight checks one action against the approval gate without running it,
// asking the user when it is consequential. Execute calls it for every
// command; a multi-step batch must call it (or Execute) for each step.
func (s *Service) Preflight(ctx context.Context, sessionID domain.SessionID, action string, args map[string]interface{}) error {
	action = strings.ToLower(strings.TrimSpace(action))
	return s.preflight(ctx, sessionID, s.runtimeFor(s.targetFor(sessionID)), action, args)
}

func (s *Service) preflight(ctx context.Context, sessionID domain.SessionID, rt runtime, action string, args map[string]interface{}) error {
	if s.approvals == nil || !mayActConsequentially(action, args) {
		return nil
	}
	mode := s.approvals.SessionMode(string(sessionID))
	if mode == approval.ModeOff {
		return nil
	}
	target, image := s.describe(ctx, sessionID, rt, action, args)
	verdict := approval.Classify(target, mode)
	if !verdict.NeedsApproval {
		return nil
	}
	element := strings.TrimSpace(strings.TrimSpace(target.Role) + " " + quoted(target.Name))
	outcome, err := s.approvals.Request(ctx, approval.Request{
		Kind:      approval.KindBrowserAction,
		SessionID: string(sessionID),
		Site:      approval.Site(target.PageURL),
		URL:       target.PageURL,
		PageTitle: target.PageTitle,
		Action:    verdict.Summary,
		Category:  verdict.Category,
		Reason:    verdict.Reason,
		Element:   element,
		Image:     image,
		Timeout:   s.approvalTimeout,
	})
	if err != nil {
		return err
	}
	if outcome.Allowed() {
		return nil
	}
	return approvalRefusal(outcome.Decision, verdict.Summary, s.approvalTimeout)
}

// mayActConsequentially filters out actions that can never need approval
// before paying for a describe round trip.
func mayActConsequentially(action string, args map[string]interface{}) bool {
	if _, ok := approval.GatedActions[action]; !ok {
		return false
	}
	switch action {
	case "press":
		key, _ := args["key"].(string)
		return strings.EqualFold(strings.TrimSpace(key), "enter")
	case "act":
		verb, _ := args["action"].(string)
		verb = strings.ToLower(strings.TrimSpace(verb))
		return verb == "" || verb == "click" || verb == "dblclick"
	case "check", "uncheck", "select":
		// Only every-submit mode could care, and only on submit; a value
		// change alone never submits.
		return false
	}
	return true
}

// describe asks the runtime about the target. A runtime that cannot answer
// (an older Chrome extension) still lets the action's own arguments decide.
func (s *Service) describe(ctx context.Context, sessionID domain.SessionID, rt runtime, action string, args map[string]interface{}) (approval.Target, string) {
	fallback := approval.Target{Action: action}
	describeArgs := map[string]interface{}{"action": action}
	for _, key := range []string{"ref", "instruction", "nth", "key"} {
		if value, ok := args[key]; ok {
			describeArgs[key] = value
		}
	}
	fallback.Instruction, _ = args["instruction"].(string)
	fallback.Key, _ = args["key"].(string)

	result, err := rt.Execute(ctx, sessionID, describeAction, describeArgs)
	if err != nil {
		if s.log != nil {
			s.log.Info("browser approval: describe unavailable; classifying from arguments", "session", sessionID, "action", action, "err", err)
		}
		return fallback, ""
	}
	var described struct {
		Target approval.Target `json:"target"`
		Image  string          `json:"image"`
	}
	raw, err := json.Marshal(result.Value)
	if err != nil || json.Unmarshal(raw, &described) != nil {
		return fallback, ""
	}
	target := described.Target
	target.Action = action
	if target.Instruction == "" {
		target.Instruction = fallback.Instruction
	}
	if target.Key == "" {
		target.Key = fallback.Key
	}
	return target, described.Image
}

func approvalRefusal(decision approval.Decision, summary string, timeout time.Duration) *ApprovalError {
	if decision == approval.DecisionTimeout {
		return &ApprovalError{Decision: decision, Summary: summary, api: apierr.Forbidden(
			"BROWSER_APPROVAL_TIMEOUT",
			fmt.Sprintf("%s needs the user's approval in Orchestrator, and nobody answered within %s, so it was not performed. Tell the user what you were about to do and wait for them before trying again.", summary, timeout.Round(time.Second)),
		)}
	}
	return &ApprovalError{Decision: decision, Summary: summary, api: apierr.Forbidden(
		"BROWSER_ACTION_DENIED",
		fmt.Sprintf("The user denied this action: %s. It was not performed. Do not retry it or work around it; ask the user how to proceed.", summary),
	)}
}

func quoted(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return fmt.Sprintf("%q", name)
}
