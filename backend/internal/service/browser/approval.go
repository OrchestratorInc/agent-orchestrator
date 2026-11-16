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
const DefaultApprovalTimeout = approval.BrowserWaitTimeout

// DefaultDescribeTimeout bounds the runtime's read of a target. Past it the
// target counts as unread, and the user is asked without the details.
const DefaultDescribeTimeout = 8 * time.Second

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
// command; a multi-step batch must call it (or Execute) for each step and
// dispatch the arguments it returns, which carry the approval binding the
// runtime checks before acting.
func (s *Service) Preflight(ctx context.Context, sessionID domain.SessionID, action string, args map[string]interface{}) (map[string]interface{}, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	return s.preflight(ctx, sessionID, s.runtimeFor(s.targetFor(sessionID)), action, args)
}

// preflight asks the user when the action is consequential. It returns the
// arguments to dispatch: an approved action carries the binding the runtime
// reported (tab, document, and act's element), so the runtime refuses it if
// the page changed while the user was deciding.
func (s *Service) preflight(ctx context.Context, sessionID domain.SessionID, rt runtime, action string, args map[string]interface{}) (map[string]interface{}, error) {
	if !mayActConsequentially(action, args) {
		return args, nil
	}
	if s.approvals == nil {
		if s.approvalsErr != nil {
			return nil, apierr.Unavailable("BROWSER_APPROVAL_UNAVAILABLE",
				"This action may need the user's approval, and approvals could not load on this machine, so it was not run")
		}
		return args, nil
	}
	mode := s.approvals.SessionMode(string(sessionID))
	if mode == approval.ModeOff {
		return args, nil
	}
	described := s.describe(ctx, sessionID, rt, action, args)
	target, image := described.target, described.image
	verdict := approval.Classify(target, mode)
	if !verdict.NeedsApproval && (!described.identified || described.partial) {
		// The element (or its form) could not be read: ask rather than guess.
		verdict = approval.Verdict{NeedsApproval: true, Category: approval.CategoryScript,
			Summary: unidentifiedSummary(action, args, target.PageURL),
			Reason:  "Orchestrator could not read what this acts on, so it asks first."}
	}
	if !verdict.NeedsApproval {
		return args, nil
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
		return nil, err
	}
	if !outcome.Allowed() {
		return nil, approvalRefusal(outcome.Decision, verdict.Summary, s.approvalTimeout)
	}
	if described.binding == nil {
		// Without a binding the runtime cannot confirm, at dispatch, that the
		// action still lands on what the user approved.
		return nil, apierr.Conflict("BROWSER_APPROVAL_UNBOUND",
			"The user approved this action, but this browser could not identify the page it targets, so it was not run. Update the Orchestrator browser extension, or take a new snapshot and try again.", nil)
	}
	bound := make(map[string]interface{}, len(args)+1)
	for key, value := range args {
		bound[key] = value
	}
	bound["approvalBinding"] = described.binding
	return bound, nil
}

// unidentifiedSummary says what will happen when the target is unknown.
func unidentifiedSummary(action string, args map[string]interface{}, pageURL string) string {
	var text string
	switch action {
	case "press":
		key, _ := args["key"].(string)
		text = fmt.Sprintf("Press %s", strings.TrimSpace(key))
	case "act":
		instruction, _ := args["instruction"].(string)
		text = fmt.Sprintf("Act: %q", strings.TrimSpace(instruction))
	default:
		ref, _ := args["ref"].(string)
		verb := "Click"
		if action == "dblclick" {
			verb = "Double-click"
		}
		text = fmt.Sprintf("%s element %s", verb, strings.TrimSpace(ref))
	}
	if host := approval.Site(pageURL); host != "" {
		text += " on " + strings.TrimPrefix(host, "www.")
	}
	return text
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
		return approval.IsActivationKey(key)
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

// describedTarget is what a runtime reported about an action's target.
type describedTarget struct {
	target approval.Target
	image  string
	// binding identifies what the user approved: the tab, the document, and
	// for act the element. The runtime checks it again before dispatch.
	binding map[string]interface{}
	// identified is false when nothing about the element could be read.
	identified bool
	// partial means the runtime named the element but could not read its form
	// (duplicate labels, a page that blocks scripts), so a benign verdict is
	// not trustworthy.
	partial bool
}

// describe asks the runtime about the target. When it cannot answer (an older
// Chrome extension, a closed tab), the action's own arguments are all there
// is, and the result is marked unidentified so preflight asks.
func (s *Service) describe(ctx context.Context, sessionID domain.SessionID, rt runtime, action string, args map[string]interface{}) describedTarget {
	fallback := approval.Target{Action: action}
	describeArgs := map[string]interface{}{"action": action}
	for _, key := range []string{"ref", "instruction", "nth", "key"} {
		if value, ok := args[key]; ok {
			describeArgs[key] = value
		}
	}
	fallback.Instruction, _ = args["instruction"].(string)
	fallback.Key, _ = args["key"].(string)

	limit := s.describeTimeout
	if limit <= 0 {
		limit = DefaultDescribeTimeout
	}
	describeCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	result, err := rt.Execute(describeCtx, sessionID, describeAction, describeArgs)
	if err != nil {
		if s.log != nil {
			s.log.Info("browser approval: describe unavailable; asking", "session", sessionID, "action", action, "err", err)
		}
		return describedTarget{target: fallback}
	}
	var described struct {
		Target  approval.Target        `json:"target"`
		Image   string                 `json:"image"`
		Binding map[string]interface{} `json:"binding"`
		Partial bool                   `json:"partial"`
	}
	raw, err := json.Marshal(result.Value)
	if err != nil || json.Unmarshal(raw, &described) != nil {
		return describedTarget{target: fallback}
	}
	target := described.Target
	target.Action = action
	if target.Instruction == "" {
		target.Instruction = fallback.Instruction
	}
	if target.Key == "" {
		target.Key = fallback.Key
	}
	identified := target.Role != "" || target.Name != "" || target.IsSubmit
	return describedTarget{target: target, image: described.Image, binding: described.Binding, identified: identified, partial: described.Partial}
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
