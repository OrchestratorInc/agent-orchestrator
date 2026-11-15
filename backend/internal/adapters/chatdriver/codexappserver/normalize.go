package codexappserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/commanddetail"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	itemUserMessage       = codexproto.ThreadItemTypeUserMessage
	itemAgentMessage      = codexproto.ThreadItemTypeAgentMessage
	itemReasoning         = codexproto.ThreadItemTypeReasoning
	itemPlan              = codexproto.ThreadItemTypePlan
	itemCommandExecution  = codexproto.ThreadItemTypeCommandExecution
	itemFileChange        = codexproto.ThreadItemTypeFileChange
	itemMcpToolCall       = codexproto.ThreadItemTypeMcpToolCall
	itemWebSearch         = codexproto.ThreadItemTypeWebSearch
	itemContextCompaction = codexproto.ThreadItemTypeContextCompaction
)

const itemError codexproto.ThreadItemType = "error"

type threadItem struct {
	codexproto.ThreadItem
	Message string `json:"message"`
}

func (it threadItem) itemID() string { return deref(it.ID) }

type contextEnvelope struct {
	TokenUsage struct {
		Last struct {
			TotalTokens int64 `json:"totalTokens"`
		} `json:"last"`
		ModelContextWindow *int64 `json:"modelContextWindow"`
	} `json:"tokenUsage"`
}

func contextPositionFrom(n notification) (used, window int64, ok bool) {
	if n.Method != codexproto.MethodThreadTokenUsageUpdated {
		return 0, 0, false
	}
	var p contextEnvelope
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return 0, 0, false
	}
	if p.TokenUsage.ModelContextWindow != nil {
		window = *p.TokenUsage.ModelContextWindow
	}
	return p.TokenUsage.Last.TotalTokens, window, true
}

type resolvedEnvelope struct {
	ThreadID  string          `json:"threadId"`
	RequestID json.RawMessage `json:"requestId"`
	ID        json.RawMessage `json:"id"`
}

func normalizeNotification(n notification, now time.Time) []ports.ChatEvent {
	switch n.Method {
	case codexproto.MethodTurnStarted:
		var p codexproto.TurnStartedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:                   ports.ChatEventTurnStarted,
			ProviderTurnID:         firstNonEmpty(p.Turn.ID, turnIDFallback(n.Params)),
			ProviderConversationID: p.ThreadID,
		}}

	case codexproto.MethodTurnCompleted:
		var p codexproto.TurnCompletedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil {
			return nil
		}
		ev := ports.ChatEvent{
			Kind:                   ports.ChatEventTurnCompleted,
			ProviderTurnID:         firstNonEmpty(p.Turn.ID, turnIDFallback(n.Params)),
			ProviderConversationID: p.ThreadID,
			TurnState:              turnStateFrom(string(p.Turn.Status)),
		}
		if p.Turn.Error != nil {
			ev.Err = codexProviderFailure(*p.Turn.Error)
		}
		return []ports.ChatEvent{ev}

	case codexproto.MethodItemAgentMessageDelta:
		var p codexproto.AgentMessageDeltaNotification
		if err := json.Unmarshal(n.Params, &p); err != nil || p.Delta == "" {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventMessageDelta,
			ProviderTurnID: p.TurnID,
			ProviderItemID: p.ItemID,
			Delta:          p.Delta,
		}}

	case codexproto.MethodItemReasoningSummaryTextDelta:
		var p codexproto.ReasoningSummaryTextDeltaNotification
		if err := json.Unmarshal(n.Params, &p); err != nil || p.Delta == "" {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventReasoningDelta,
			ProviderTurnID: p.TurnID,
			ProviderItemID: p.ItemID,
			Delta:          p.Delta,
		}}

	case codexproto.MethodItemReasoningSummaryPartAdded:
		var p codexproto.ReasoningSummaryPartAddedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil || p.SummaryIndex <= 0 {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventReasoningDelta,
			ProviderTurnID: p.TurnID,
			ProviderItemID: p.ItemID,
			Delta:          "\n\n",
		}}

	case codexproto.MethodItemReasoningTextDelta:
		var p codexproto.ReasoningTextDeltaNotification
		if err := json.Unmarshal(n.Params, &p); err != nil || p.Delta == "" {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventReasoningDelta,
			ProviderTurnID: p.TurnID,
			ProviderItemID: p.ItemID,
			Delta:          p.Delta,
		}}

	case codexproto.MethodItemCommandExecutionOutputDelta:
		var p codexproto.CommandExecutionOutputDeltaNotification
		if err := json.Unmarshal(n.Params, &p); err != nil || p.Delta == "" {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventCommandOutputDelta,
			ProviderTurnID: p.TurnID,
			ProviderItemID: p.ItemID,
			Delta:          p.Delta,
		}}

	case codexproto.MethodItemCommandExecutionTerminalInteraction:
		var p codexproto.TerminalInteractionNotification
		if err := json.Unmarshal(n.Params, &p); err != nil || p.Stdin == "" {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventCommandInput,
			ProviderTurnID: p.TurnID,
			ProviderItemID: p.ItemID,
			Delta:          p.Stdin,
			Detail:         encodeDetail(map[string]any{"processId": p.ProcessID}),
		}}

	case codexproto.MethodItemMcpToolCallProgress:
		var p codexproto.McpToolCallProgressNotification
		if err := json.Unmarshal(n.Params, &p); err != nil || p.Message == "" {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventActivityText,
			ProviderTurnID: p.TurnID,
			ProviderItemID: p.ItemID,
			Delta:          p.Message + "\n",
		}}

	case codexproto.MethodItemFileChangeOutputDelta:
		var p codexproto.FileChangeOutputDeltaNotification
		if err := json.Unmarshal(n.Params, &p); err != nil || p.Delta == "" {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventActivityText,
			ProviderTurnID: p.TurnID,
			ProviderItemID: p.ItemID,
			Delta:          p.Delta,
		}}

	case codexproto.MethodItemFileChangePatchUpdated:
		var p codexproto.FileChangePatchUpdatedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil || len(p.Changes) == 0 {
			return nil
		}
		files := fileChanges(p.Changes)
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventActivityStarted,
			ProviderTurnID: p.TurnID,
			ProviderItemID: p.ItemID,
			ActivityKind:   domain.ActivityKindFileChange,
			ActivityStatus: domain.ActivityStatusRunning,
			Summary:        fileChangeSummary(files),
			Detail:         encodeDetail(map[string]any{"files": files}),
		}}

	case codexproto.MethodItemPlanDelta:
		var p codexproto.PlanDeltaNotification
		if err := json.Unmarshal(n.Params, &p); err != nil || p.Delta == "" {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventActivityText,
			ProviderTurnID: p.TurnID,
			ProviderItemID: p.ItemID,
			Delta:          p.Delta,
		}}

	case codexproto.MethodTurnPlanUpdated:
		var p codexproto.TurnPlanUpdatedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil {
			return nil
		}
		plan := planFrom(p)
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventPlanUpdated,
			ProviderTurnID: p.TurnID,
			Plan:           &plan,
			Summary:        planSummary(plan),
		}}

	case codexproto.MethodItemAutoApprovalReviewStarted:
		var p codexproto.ItemGuardianApprovalReviewStartedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventActivityStarted,
			ProviderTurnID: p.TurnID,
			ProviderItemID: reviewItemID(p.ReviewID),
			ActivityKind:   domain.ActivityKindAutoReview,
			ActivityStatus: domain.ActivityStatusRunning,
			Summary:        autoReviewSummary(p.Action, p.Review),
			Detail: encodeDetail(autoReviewDetail(
				p.ReviewID, p.TargetItemID, p.Action, p.Review, "", p.StartedAtMs, 0)),
		}}

	case codexproto.MethodItemAutoApprovalReviewCompleted:
		var p codexproto.ItemGuardianApprovalReviewCompletedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventActivityCompleted,
			ProviderTurnID: p.TurnID,
			ProviderItemID: reviewItemID(p.ReviewID),
			ActivityKind:   domain.ActivityKindAutoReview,
			ActivityStatus: autoReviewStatus(p.Review.Status),
			Summary:        autoReviewSummary(p.Action, p.Review),
			Detail: encodeDetail(autoReviewDetail(
				p.ReviewID, p.TargetItemID, p.Action, p.Review,
				string(p.DecisionSource), p.StartedAtMs, p.CompletedAtMs)),
		}}

	case codexproto.MethodModelRerouted:
		var p codexproto.ModelReroutedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil || p.ToModel == "" {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventModelRerouted,
			ProviderTurnID: p.TurnID,
			Reroute: &ports.ChatModelReroute{
				FromModel: p.FromModel,
				ToModel:   p.ToModel,
				Reason:    string(p.Reason),
			},
		}}

	case codexproto.MethodAccountUpdated:
		var p codexproto.AccountUpdatedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil {
			return nil
		}
		account := ports.ChatAccount{}
		if p.AuthMode != nil {
			account.AuthMode = string(*p.AuthMode)
		}
		if p.PlanType != nil {
			account.PlanLabel = string(*p.PlanType)
		}
		if account.AuthMode == "" && account.PlanLabel == "" {
			return nil
		}
		return []ports.ChatEvent{{Kind: ports.ChatEventAccountChanged, Account: &account}}

	case codexproto.MethodThreadStatusChanged:
		var p codexproto.ThreadStatusChangedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil {
			return nil
		}
		state := ports.ChatThreadState{
			Status:    threadStatusFrom(p.Status.Type),
			WaitingOn: waitingOnFrom(p.Status.ActiveFlags),
		}
		if state.Status == "" {
			return nil
		}
		return []ports.ChatEvent{{Kind: ports.ChatEventThreadState, ThreadState: &state}}

	case codexproto.MethodThreadArchived, codexproto.MethodThreadUnarchived:
		archived := n.Method == codexproto.MethodThreadArchived
		return []ports.ChatEvent{{
			Kind:        ports.ChatEventThreadState,
			ThreadState: &ports.ChatThreadState{Archived: &archived},
		}}

	case codexproto.MethodThreadClosed:
		return []ports.ChatEvent{{
			Kind:        ports.ChatEventThreadState,
			ThreadState: &ports.ChatThreadState{Closed: true, Status: domain.ThreadStatusClosed},
		}}

	case codexproto.MethodMcpServerStartupStatusUpdated:
		var p codexproto.McpServerStatusUpdatedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil || p.Name == "" {
			return nil
		}
		server := ports.ChatMCPServer{Name: p.Name, Status: string(p.Status)}
		if p.Error != nil {
			server.Error = *p.Error
		}
		if p.FailureReason != nil {
			server.FailureReason = string(*p.FailureReason)
		}
		return []ports.ChatEvent{{
			Kind:       ports.ChatEventMCPServers,
			MCPServers: []ports.ChatMCPServer{server},
		}}

	case codexproto.MethodTurnDiffUpdated:
		var p codexproto.TurnDiffUpdatedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil {
			return nil
		}
		files, truncated := parseTurnDiff(p.Diff)
		if len(files) == 0 {
			return []ports.ChatEvent{{
				Kind:           ports.ChatEventTurnDiff,
				ProviderTurnID: p.TurnID,
				Diff:           &ports.ChatTurnDiff{},
			}}
		}
		ev := ports.ChatEvent{
			Kind:           ports.ChatEventTurnDiff,
			ProviderTurnID: p.TurnID,
			Diff:           &ports.ChatTurnDiff{Files: files},
		}
		if truncated {
			ev.Summary = domain.ChatDiffTruncatedSummary
		}
		return []ports.ChatEvent{ev}

	case codexproto.MethodItemStarted:
		return normalizeItem(n.Params, false)

	case codexproto.MethodItemCompleted:
		return normalizeItem(n.Params, true)

	case codexproto.MethodThreadTokenUsageUpdated:
		var p codexproto.ThreadTokenUsageUpdatedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil {
			return nil
		}
		usage := &ports.ChatUsage{
			InputTokens:  p.TokenUsage.Total.InputTokens,
			OutputTokens: p.TokenUsage.Total.OutputTokens,
			CachedTokens: p.TokenUsage.Total.CachedInputTokens,
			TotalTokens:  p.TokenUsage.Total.TotalTokens,
			ContextKnown: true,
			TotalsKnown:  true,
			ContextUsed:  p.TokenUsage.Last.TotalTokens,
		}
		if p.TokenUsage.ModelContextWindow != nil {
			usage.ContextWindow = *p.TokenUsage.ModelContextWindow
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventUsage,
			ProviderTurnID: p.TurnID,
			Usage:          usage,
		}}

	case codexproto.MethodAccountRateLimitsUpdated:
		var p capacityReadEnvelope
		if err := json.Unmarshal(n.Params, &p); err != nil {
			return nil
		}
		limits := chatRateLimitsFromCapacity(capacityObservationFromEnvelope(p, now.UTC(), true), now)
		return []ports.ChatEvent{{Kind: ports.ChatEventRateLimits, RateLimits: &limits}}

	case codexproto.MethodThreadCompacted: //nolint:staticcheck // deliberately reading the deprecated spelling
		var p codexproto.ContextCompactedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventCompacted,
			ProviderTurnID: p.TurnID,
		}}

	case codexproto.MethodThreadNameUpdated:
		var p codexproto.ThreadNameUpdatedNotification
		if err := json.Unmarshal(n.Params, &p); err != nil {
			return nil
		}
		title := ""
		if p.ThreadName != nil {
			title = strings.TrimSpace(*p.ThreadName)
		}
		return []ports.ChatEvent{{
			Kind:  ports.ChatEventThreadRenamed,
			Title: title,
		}}

	case codexproto.MethodServerRequestResolved:
		var p resolvedEnvelope
		if err := json.Unmarshal(n.Params, &p); err != nil {
			return nil
		}
		id := rawID(p.RequestID)
		if id == "" {
			id = rawID(p.ID)
		}
		if id == "" {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:      ports.ChatEventApprovalResolved,
			RequestID: id,
		}}

	case codexproto.MethodError:
		var p codexproto.ErrorNotification
		if err := json.Unmarshal(n.Params, &p); err == nil &&
			(strings.TrimSpace(p.Error.Message) != "" || p.Error.AdditionalDetails != nil || p.Error.CodexErrorInfo != nil) {
			failure := codexProviderFailure(p.Error)
			if failure == nil {
				return nil
			}
			if p.WillRetry && p.TurnID != "" {
				return []ports.ChatEvent{{
					Kind: ports.ChatEventActivityStarted, ProviderConversationID: p.ThreadID,
					ProviderTurnID: p.TurnID, ProviderItemID: "codex-retry:" + p.ThreadID + ":" + p.TurnID,
					ActivityKind: domain.ActivityKindSystem, ActivityStatus: domain.ActivityStatusRunning,
					Summary: failure.Error(), Detail: json.RawMessage(`{"event":"provider.failure"}`),
				}}
			}
			return []ports.ChatEvent{{
				Kind:                   ports.ChatEventError,
				ProviderTurnID:         p.TurnID,
				ProviderConversationID: p.ThreadID,
				Err:                    failure,
			}}
		}
		return []ports.ChatEvent{{
			Kind: ports.ChatEventError,
			Err:  fmt.Errorf("provider error: %s", truncateForLog(n.Params)),
		}}

	default:
		return nil
	}
}

func codexProviderFailure(turnErr codexproto.TurnError) error {
	detail := ""
	if turnErr.AdditionalDetails != nil {
		detail = *turnErr.AdditionalDetails
	}
	var cause error
	if turnErr.CodexErrorInfo != nil {
		var reason string
		_ = json.Unmarshal(*turnErr.CodexErrorInfo, &reason)
		switch reason {
		case "unauthorized":
			cause = ports.ErrChatAuthRequired
		case "usageLimitExceeded":
			cause = errCodexUsageLimitExceeded
		}
	}
	if strings.TrimSpace(turnErr.Message) == "" && strings.TrimSpace(detail) == "" && cause == nil {
		return nil
	}
	if strings.TrimSpace(turnErr.Message) == "" && strings.TrimSpace(detail) == "" && errors.Is(cause, errCodexUsageLimitExceeded) {
		return ports.NewChatProviderFailure("Usage limit exceeded", "", cause)
	}
	failure := ports.NewChatProviderFailure(turnErr.Message, detail, cause)
	if errors.Is(cause, errCodexUsageLimitExceeded) && domain.ParseConversationUsageLimit(failure.Error(), nil) == nil {
		return ports.NewChatProviderFailure("Usage limit exceeded", failure.Error(), cause)
	}
	return failure
}

func turnIDFallback(params json.RawMessage) string {
	var p struct {
		TurnID string `json:"turnId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return ""
	}
	return p.TurnID
}

func planFrom(p codexproto.TurnPlanUpdatedNotification) domain.ConversationPlan {
	plan := domain.ConversationPlan{Steps: make([]domain.ConversationPlanStep, 0, len(p.Plan))}
	if p.Explanation != nil {
		plan.Explanation = strings.TrimSpace(*p.Explanation)
	}
	for _, step := range p.Plan {
		text := strings.TrimSpace(step.Step)
		if text == "" {
			continue
		}
		plan.Steps = append(plan.Steps, domain.ConversationPlanStep{
			Text:   text,
			Status: planStepStatus(step.Status),
		})
	}
	return plan
}

func planStepStatus(status codexproto.TurnPlanStepStatus) domain.PlanStepStatus {
	switch status {
	case codexproto.TurnPlanStepStatusInProgress:
		return domain.PlanStepInProgress
	case codexproto.TurnPlanStepStatusCompleted:
		return domain.PlanStepCompleted
	default:
		return domain.PlanStepPending
	}
}

func planSummary(plan domain.ConversationPlan) string {
	if len(plan.Steps) == 0 {
		return "Cleared the plan"
	}
	done := 0
	current := ""
	for _, step := range plan.Steps {
		if step.Status == domain.PlanStepCompleted {
			done++
		}
		if current == "" && step.Status == domain.PlanStepInProgress {
			current = step.Text
		}
	}
	if current != "" {
		return fmt.Sprintf("Plan %d/%d: %s", done, len(plan.Steps), truncateLabel(current))
	}
	return fmt.Sprintf("Plan %d/%d steps done", done, len(plan.Steps))
}

func reviewItemID(reviewID string) string {
	if reviewID == "" {
		return ""
	}
	return "ao-review-" + reviewID
}

func autoReviewStatus(status codexproto.GuardianApprovalReviewStatus) domain.ActivityStatus {
	switch status {
	case codexproto.GuardianApprovalReviewStatusApproved:
		return domain.ActivityStatusCompleted
	case codexproto.GuardianApprovalReviewStatusInProgress:
		return domain.ActivityStatusRunning
	default:
		return domain.ActivityStatusFailed
	}
}

func autoReviewSummary(
	action codexproto.GuardianApprovalReviewAction,
	review codexproto.GuardianApprovalReview,
) string {
	subject := autoReviewSubject(action)
	switch review.Status {
	case codexproto.GuardianApprovalReviewStatusApproved:
		risk := ""
		if review.RiskLevel != nil {
			risk = fmt.Sprintf(" (%s risk)", *review.RiskLevel)
		}
		return "Auto-approved " + subject + risk
	case codexproto.GuardianApprovalReviewStatusDenied:
		return "Auto-declined " + subject
	case codexproto.GuardianApprovalReviewStatusInProgress:
		return "Reviewing " + subject
	default:
		return fmt.Sprintf("Auto-review %s: %s", review.Status, subject)
	}
}

func autoReviewSubject(action codexproto.GuardianApprovalReviewAction) string {
	switch {
	case action.Command != nil && *action.Command != "":
		return commandSummary(*action.Command)
	case action.ToolName != nil && *action.ToolName != "":
		return "tool " + *action.ToolName
	case action.Host != nil && *action.Host != "":
		return "network access to " + *action.Host
	case len(action.Files) > 0:
		return fmt.Sprintf("changes to %d file(s)", len(action.Files))
	case action.Program != nil && *action.Program != "":
		return *action.Program
	default:
		return string(action.Type)
	}
}

func autoReviewDetail(
	reviewID string,
	targetItemID *string,
	action codexproto.GuardianApprovalReviewAction,
	review codexproto.GuardianApprovalReview,
	decisionSource string,
	startedAtMs, completedAtMs int64,
) map[string]any {
	detail := map[string]any{
		"reviewId":   reviewID,
		"actionType": string(action.Type),
		"status":     string(review.Status),
	}
	if targetItemID != nil && *targetItemID != "" {
		detail["targetItemId"] = *targetItemID
	}
	if action.Command != nil && *action.Command != "" {
		detail["command"] = commanddetail.UnwrapShell(*action.Command)
		detail["rawCommand"] = *action.Command
	}
	if action.Cwd != nil && *action.Cwd != "" {
		detail["cwd"] = string(*action.Cwd)
	}
	if action.Source != nil {
		detail["commandSource"] = string(*action.Source)
	}
	if action.ToolName != nil && *action.ToolName != "" {
		detail["toolName"] = *action.ToolName
	}
	if action.Server != nil && *action.Server != "" {
		detail["server"] = *action.Server
	}
	if action.Host != nil && *action.Host != "" {
		detail["host"] = *action.Host
	}
	if len(action.Files) > 0 {
		paths := make([]string, 0, len(action.Files))
		for _, file := range action.Files {
			paths = append(paths, string(file))
		}
		detail["files"] = paths
	}
	if review.RiskLevel != nil {
		detail["riskLevel"] = string(*review.RiskLevel)
	}
	if review.UserAuthorization != nil {
		detail["userAuthorization"] = string(*review.UserAuthorization)
	}
	if review.Rationale != nil && *review.Rationale != "" {
		detail["rationale"] = *review.Rationale
	}
	if decisionSource != "" {
		detail["decisionSource"] = decisionSource
	}
	if startedAtMs > 0 && completedAtMs > startedAtMs {
		detail["durationMs"] = completedAtMs - startedAtMs
	}
	return detail
}

func threadStatusFrom(status codexproto.ThreadStatusType) domain.ThreadStatus {
	switch status {
	case codexproto.ThreadStatusTypeActive:
		return domain.ThreadStatusActive
	case codexproto.ThreadStatusTypeIdle:
		return domain.ThreadStatusIdle
	case codexproto.ThreadStatusTypeNotLoaded:
		return domain.ThreadStatusNotLoaded
	case codexproto.ThreadStatusTypeSystemError:
		return domain.ThreadStatusSystemError
	default:
		return ""
	}
}

func waitingOnFrom(flags []codexproto.ThreadActiveFlag) []string {
	if len(flags) == 0 {
		return nil
	}
	out := make([]string, 0, len(flags))
	for _, flag := range flags {
		switch flag {
		case codexproto.ThreadActiveFlagWaitingOnApproval:
			out = append(out, "waiting_on_approval")
		case codexproto.ThreadActiveFlagWaitingOnUserInput:
			out = append(out, "waiting_on_user_input")
		default:
			out = append(out, string(flag))
		}
	}
	return out
}

func normalizeItem(params json.RawMessage, completed bool) []ports.ChatEvent {
	var p struct {
		ThreadID string     `json:"threadId"`
		TurnID   string     `json:"turnId"`
		Item     threadItem `json:"item"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil
	}
	it := p.Item

	if it.Type == itemUserMessage {
		return nil
	}

	if it.Type == itemContextCompaction {
		if !completed {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventCompacted,
			ProviderTurnID: p.TurnID,
			ProviderItemID: it.itemID(),
		}}
	}

	if it.Type == itemAgentMessage {
		if !completed {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventMessageCompleted,
			ProviderTurnID: p.TurnID,
			ProviderItemID: it.itemID(),
			Text:           deref(it.Text),
		}}
	}

	kind, summary, ok := activityFor(it)
	if !ok {
		return nil
	}

	ev := ports.ChatEvent{
		ProviderTurnID: p.TurnID,
		ProviderItemID: it.itemID(),
		ActivityKind:   kind,
		Summary:        summary,
		Detail:         activityDetail(it),
	}
	if kind == domain.ActivityKindReasoning {
		ev.Text = joinReasoning(it.Summary)
	}
	if completed {
		ev.Kind = ports.ChatEventActivityCompleted
		ev.ActivityStatus = domain.ActivityStatusCompleted
		if it.ExitCode != nil && *it.ExitCode != 0 {
			ev.ActivityStatus = domain.ActivityStatusFailed
		}
		if it.Type == itemError {
			ev.ActivityStatus = domain.ActivityStatusFailed
		}
		if it.Error != nil && it.Error.Message != "" {
			ev.ActivityStatus = domain.ActivityStatusFailed
		}
		if it.Success != nil && !*it.Success {
			ev.ActivityStatus = domain.ActivityStatusFailed
		}
	} else {
		ev.Kind = ports.ChatEventActivityStarted
		ev.ActivityStatus = domain.ActivityStatusRunning
	}
	return []ports.ChatEvent{ev}
}

func activityFor(it threadItem) (domain.ActivityKind, string, bool) {
	switch it.Type {
	case itemCommandExecution:
		return domain.ActivityKindCommand, commandSummary(deref(it.Command)), true
	case itemFileChange:
		return domain.ActivityKindFileChange, fileChangeSummary(fileChanges(it.Changes)), true
	case itemPlan:
		return domain.ActivityKindPlan, "Updated plan", true
	case itemReasoning:
		return domain.ActivityKindReasoning, "Reasoning", true
	case itemMcpToolCall:
		return domain.ActivityKindMCPTool, mcpToolSummary(it), true
	case itemWebSearch:
		if query := strings.TrimSpace(deref(it.Query)); query != "" {
			return domain.ActivityKindCommand, "Searched the web for " + truncateLabel(query), true
		}
		return domain.ActivityKindCommand, "Searched the web", true
	case itemError:
		return domain.ActivityKindError, firstNonEmpty(it.Message, "Provider error"), true
	default:
		return "", "", false
	}
}

func mcpToolSummary(it threadItem) string {
	tool := strings.TrimSpace(deref(it.Tool))
	server := strings.TrimSpace(deref(it.Server))
	switch {
	case tool != "" && server != "":
		return "Called " + server + "/" + tool
	case tool != "":
		return "Called " + tool
	case server != "":
		return "Called a tool on " + server
	default:
		return "Called a tool"
	}
}

func fileChangeSummary(files []fileChangeDetail) string {
	switch len(files) {
	case 0:
		return "Edited files"
	case 1:
		return fileChangeVerb(files[0].Status) + " " + truncateLabel(files[0].Path)
	default:
		return fmt.Sprintf("Edited %d files", len(files))
	}
}

func fileChangeVerb(status string) string {
	switch status {
	case diffStatusAdded:
		return "Created"
	case diffStatusDeleted:
		return "Deleted"
	case diffStatusRenamed:
		return "Renamed"
	default:
		return "Edited"
	}
}

func commandSummary(command string) string {
	cmd := commanddetail.UnwrapShell(strings.TrimSpace(command))
	if cmd == "" {
		return "Ran a command"
	}
	return truncateLabel(cmd)
}

func truncateLabel(value string) string {
	const limit = 120
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

func activityDetail(it threadItem) []byte {
	detail := map[string]any{}
	if cmd := deref(it.Command); cmd != "" {
		detail["command"] = commanddetail.UnwrapShell(cmd)
		detail["rawCommand"] = cmd
	}
	if it.Cwd != nil && *it.Cwd != "" {
		detail["cwd"] = string(*it.Cwd)
	}
	if it.ProcessID != nil && *it.ProcessID != "" {
		detail["processId"] = *it.ProcessID
	}
	if out := deref(it.AggregatedOutput); out != "" {
		kept, cut := commanddetail.TailChars(out, commanddetail.MaxOutputChars)
		detail["output"] = kept
		if cut {
			detail["outputTruncated"] = true
		}
		detail["outputMayBePartial"] = true
		detail["outputSource"] = "aggregate"
	}
	if it.ExitCode != nil {
		detail["exitCode"] = *it.ExitCode
	}
	if it.DurationMs != nil {
		detail["durationMs"] = *it.DurationMs
	}
	if text := strings.TrimSpace(deref(it.Text)); text != "" {
		detail["text"] = text
	}
	if summary := joinReasoning(it.Summary); summary != "" {
		detail["text"] = summary
	}
	if len(it.Changes) > 0 {
		detail["files"] = fileChanges(it.Changes)
	}
	if tool := deref(it.Tool); tool != "" {
		detail["toolName"] = tool
	}
	if server := deref(it.Server); server != "" {
		detail["server"] = server
	}
	if ns := deref(it.Namespace); ns != "" {
		detail["namespace"] = ns
	}
	if len(it.Arguments) > 0 && !isJSONNull(it.Arguments) {
		detail["arguments"] = commanddetail.TruncatedJSON(it.Arguments)
	}
	if len(it.Result) > 0 && !isJSONNull(it.Result) {
		detail["result"] = commanddetail.TruncatedJSON(it.Result)
	}
	if it.Error != nil && it.Error.Message != "" {
		detail["error"] = it.Error.Message
	}
	if it.Success != nil {
		detail["success"] = *it.Success
	}
	if query := strings.TrimSpace(deref(it.Query)); query != "" {
		detail["query"] = query
	}
	if it.Message != "" {
		detail["message"] = it.Message
	}
	if len(detail) == 0 {
		return nil
	}
	return encodeDetail(detail)
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func joinReasoning(summary []string) string {
	parts := make([]string, 0, len(summary))
	for _, part := range summary {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, "\n\n")
}

type fileChangeDetail struct {
	Path           string `json:"path"`
	OldPath        string `json:"oldPath,omitempty"`
	Status         string `json:"status"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	Patch          string `json:"patch,omitempty"`
	PatchTruncated bool   `json:"patchTruncated,omitempty"`
}

func fileChanges(changes []codexproto.FileUpdateChange) []fileChangeDetail {
	if len(changes) == 0 {
		return nil
	}
	files := make([]fileChangeDetail, 0, len(changes))
	for _, change := range changes {
		file := fileChangeDetail{
			Path:   change.Path,
			Status: patchStatus(change.Kind.Type),
			Patch:  change.Diff,
		}
		if change.Kind.MovePath != nil && *change.Kind.MovePath != "" {
			file.Status = diffStatusRenamed
			file.OldPath = change.Path
			file.Path = *change.Kind.MovePath
		}
		file.Additions, file.Deletions = countPatchLines(change.Diff, file.Status)
		if len(file.Patch) > commanddetail.MaxPatchChars {
			file.Patch = commanddetail.Head(file.Patch, commanddetail.MaxPatchChars)
			file.PatchTruncated = true
		}
		files = append(files, file)
	}
	return files
}

func patchStatus(kind codexproto.PatchChangeKindType) string {
	switch kind {
	case codexproto.PatchChangeKindTypeAdd:
		return diffStatusAdded
	case codexproto.PatchChangeKindTypeDelete:
		return diffStatusDeleted
	default:
		return diffStatusModified
	}
}

func countPatchLines(patch, status string) (additions, deletions int) {
	if patch == "" {
		return 0, 0
	}
	if !strings.Contains(patch, "@@") {
		lines := strings.Count(strings.TrimSuffix(patch, "\n"), "\n") + 1
		switch status {
		case diffStatusDeleted:
			return 0, lines
		case diffStatusAdded:
			return lines, 0
		default:
			return 0, 0
		}
	}
	inHunk := false
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case !inHunk:
			continue
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}

func turnStateFrom(status string) domain.TurnState {
	switch status {
	case string(codexproto.TurnStatusCompleted):
		return domain.TurnStateCompleted
	case string(codexproto.TurnStatusInterrupted), "cancelled", "canceled":
		return domain.TurnStateInterrupted
	case string(codexproto.TurnStatusInProgress), "in_progress", "running", "active":
		return domain.TurnStateRunning
	case "queued", "pending":
		return domain.TurnStateQueued
	default:
		return domain.TurnStateFailed
	}
}

func rawID(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	return strings.Trim(s, `"`)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func encodeDetail(detail map[string]any) []byte {
	encoded, err := json.Marshal(detail)
	if err != nil {
		return nil
	}
	return encoded
}

func truncateForLog(raw []byte) string {
	const maxLogBytes = 400
	s := string(raw)
	if len(s) > maxLogBytes {
		return s[:maxLogBytes] + "…"
	}
	return s
}
