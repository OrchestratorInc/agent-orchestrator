package coderagents

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/commanddetail"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Decision IDs offered on an AO approval for a Coder tool call.
const (
	decisionAllow  = "allow"
	decisionReject = "reject"
)

var approvalDecisions = []ports.ChatDecisionOption{
	{ID: decisionAllow, Label: "Allow", Kind: ports.ChatDecisionAllowOnce},
	{ID: decisionReject, Label: "Reject", Kind: ports.ChatDecisionRejectOnce},
}

// needsApproval maps AO's permission mode onto AO's own tool gate.
func needsApproval(class toolClass, mode ports.PermissionMode) bool {
	switch class {
	case toolClassEdit:
		return mode == ports.PermissionModeDefault
	case toolClassCommand:
		return mode == ports.PermissionModeDefault || mode == ports.PermissionModeAcceptEdits
	default:
		return false
	}
}

// startCalls runs one batch of Coder tool calls in the background. A batch
// whose calls are all already running or answered (a replayed
// action_required event) is ignored.
func (c *conversation) startCalls(calls []coderapi.ToolCall) {
	c.mu.Lock()
	fresh := false
	for _, call := range calls {
		if !c.inflight[call.ToolCallID] && !c.submitted[call.ToolCallID] {
			fresh = true
		}
	}
	if !fresh || c.closed {
		c.mu.Unlock()
		return
	}
	for _, call := range calls {
		c.inflight[call.ToolCallID] = true
	}
	c.wg.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.wg.Done()
		c.runCalls(calls)
	}()
}

func (c *conversation) runCalls(calls []coderapi.ToolCall) {
	c.toolMu.Lock()
	defer c.toolMu.Unlock()
	defer func() {
		c.mu.Lock()
		for _, call := range calls {
			delete(c.inflight, call.ToolCallID)
		}
		c.mu.Unlock()
	}()
	c.mu.Lock()
	t, chatID := c.turn, c.chatID
	c.mu.Unlock()
	if t == nil || chatID == "" {
		return
	}
	results := make([]coderapi.ToolResult, 0, len(calls))
	for _, call := range calls {
		outcome, ok := c.executeCall(t, call)
		if !ok {
			// The turn was interrupted or AO is closing. Unanswered calls stay
			// pending in Coder; a resume answers them from the journal.
			return
		}
		results = append(results, coderapi.ToolResult{ToolCallID: call.ToolCallID, Output: outcome.Output, IsError: outcome.IsError})
	}
	if err := c.submit(chatID, results); err != nil {
		c.emit(ports.ChatEvent{Kind: ports.ChatEventError, ProviderTurnID: t.id,
			Err: ports.NewChatProviderFailure("Coder Agents", "AO could not return tool results to Coder", err)})
		return
	}
	c.mu.Lock()
	for _, result := range results {
		c.submitted[result.ToolCallID] = true
	}
	c.mu.Unlock()
	for _, result := range results {
		if err := c.journal.submitted(result.ToolCallID); err != nil {
			c.log.Warn("coder agents: record submitted tool call", "error", err)
		}
	}
}

// submit sends every result of the batch together, as Coder requires. A
// conflict means Coder is no longer waiting for these calls (already
// answered or the run was interrupted), so there is nothing to resend.
func (c *conversation) submit(chatID string, results []coderapi.ToolResult) error {
	var err error
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * time.Second):
			case <-c.ctx.Done():
				return c.ctx.Err()
			}
		}
		requestCtx, cancel := coderapi.WithRequestTimeout(c.ctx)
		err = c.client.SubmitToolResults(requestCtx, chatID, results)
		cancel()
		status := coderapi.StatusCode(err)
		if err == nil || status == http.StatusConflict {
			return nil
		}
		if status != 0 && status < 500 {
			return err
		}
	}
	return err
}

// executeCall answers one call. A call the journal already knows is never
// run again: a finished call returns its recorded result, and a call that
// started without finishing reports that its effect is unknown.
func (c *conversation) executeCall(t *turnState, call coderapi.ToolCall) (toolOutcome, bool) {
	record, known, err := c.journal.load(call.ToolCallID)
	if err != nil {
		return errorOutcome("AO could not read its record of this tool call, so it was not run: " + err.Error()), true
	}
	if known {
		switch record.State {
		case callDone:
			if record.Outcome != nil {
				return *record.Outcome, true
			}
		case callSubmitted:
			return errorOutcome("AO already completed this tool call and returned its result."), true
		}
		return errorOutcome("AO stopped while this tool call was running, so its effect is unknown. It was not run again; inspect the worktree before retrying."), true
	}
	if t.ctx.Err() != nil {
		// Interrupted or closing: nothing more runs for this turn.
		return toolOutcome{}, false
	}
	c.mu.Lock()
	started := t.toolStarted(call.ToolCallID, call.ToolName, call.Args)
	c.mu.Unlock()
	for _, event := range started {
		c.emit(event)
	}
	spec, ours := specFor(call.ToolName)
	if !ours {
		outcome := errorOutcome("unknown AO tool " + call.ToolName)
		c.toolCompleted(t, call, outcome, domain.ActivityStatusFailed)
		return outcome, true
	}
	if needsApproval(spec.class, t.approval) {
		allowed, ok := c.requestApproval(t, call)
		if !ok {
			return toolOutcome{}, false
		}
		if !allowed {
			outcome := errorOutcome("The user declined this action in AO.")
			c.toolCompleted(t, call, outcome, domain.ActivityStatusCancelled)
			return outcome, true
		}
	}
	if t.ctx.Err() != nil {
		return toolOutcome{}, false
	}
	if err := c.journal.start(call.ToolCallID); err != nil {
		outcome := errorOutcome("AO could not record this tool call, so it was not run: " + err.Error())
		c.toolCompleted(t, call, outcome, domain.ActivityStatusFailed)
		return outcome, true
	}
	outcome := c.tools.run(t.ctx, call.ToolName, call.Args)
	if err := c.journal.finish(call.ToolCallID, outcome); err != nil {
		c.log.Warn("coder agents: record finished tool call", "error", err)
	}
	status := domain.ActivityStatusCompleted
	if outcome.IsError || (outcome.ExitCode != nil && *outcome.ExitCode != 0) {
		status = domain.ActivityStatusFailed
	}
	c.toolCompleted(t, call, outcome, status)
	return outcome, true
}

func (c *conversation) toolCompleted(t *turnState, call coderapi.ToolCall, outcome toolOutcome, status domain.ActivityStatus) {
	c.mu.Lock()
	if t.completed[call.ToolCallID] {
		c.mu.Unlock()
		return
	}
	t.completed[call.ToolCallID] = true
	c.mu.Unlock()
	kind, summary, base := toolPresentation(call.ToolName, call.Args)
	detail := map[string]any{}
	_ = json.Unmarshal(base, &detail)
	output, cut := commanddetail.TailChars(outcome.Text, commanddetail.MaxOutputChars)
	detail["output"] = output
	if cut {
		detail["outputTruncated"] = true
	}
	if outcome.ExitCode != nil {
		detail["exitCode"] = *outcome.ExitCode
	}
	encoded, _ := json.Marshal(detail)
	c.emit(ports.ChatEvent{
		Kind: ports.ChatEventActivityCompleted, ProviderTurnID: t.id, ProviderItemID: toolItemID(call.ToolCallID),
		ActivityKind: kind, ActivityStatus: status, Summary: summary, Detail: encoded,
	})
}

// requestApproval parks the call on an AO approval card. It returns ok=false
// when the turn is interrupted or the conversation closes first.
func (c *conversation) requestApproval(t *turnState, call coderapi.ToolCall) (allowed, ok bool) {
	requestID := "coder-agents:" + call.ToolCallID
	pending := &pendingApproval{turnID: t.id, decided: make(chan bool, 1)}
	c.mu.Lock()
	c.approvals[requestID] = pending
	c.mu.Unlock()
	kind, summary, detail := toolPresentation(call.ToolName, call.Args)
	c.emit(ports.ChatEvent{
		Kind: ports.ChatEventApprovalRequested, ProviderTurnID: t.id, ProviderItemID: toolItemID(call.ToolCallID),
		ActivityKind: kind, ActivityStatus: domain.ActivityStatusPending,
		Summary: summary, Detail: detail, RequestID: requestID, Decisions: approvalDecisions,
	})
	select {
	case allowed := <-pending.decided:
		return allowed, true
	case <-t.ctx.Done():
	}
	c.mu.Lock()
	_, stillPending := c.approvals[requestID]
	delete(c.approvals, requestID)
	closing := c.closed
	c.mu.Unlock()
	if stillPending && !closing {
		c.emit(ports.ChatEvent{Kind: ports.ChatEventApprovalResolved, ProviderTurnID: t.id, RequestID: requestID})
	}
	return false, false
}
