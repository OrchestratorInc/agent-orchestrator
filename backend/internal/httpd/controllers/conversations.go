package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

const (
	maxConversationImageBytes  = 10 << 20
	maxConversationImagesBytes = 25 << 20
	maxConversationBody        = maxConversationImagesBytes*4/3 + (2 << 20)
)

type ConversationService interface {
	Snapshot(ctx context.Context, session domain.SessionID) (chatsvc.Snapshot, error)
	Send(ctx context.Context, session domain.SessionID, msg ports.ChatUserMessage) (domain.ConversationTurn, error)
	EditMessage(ctx context.Context, session domain.SessionID, turnID string, msg ports.ChatUserMessage) (chatsvc.EditMessageResult, error)
	ActivateBranch(ctx context.Context, session domain.SessionID, branchID string) (string, error)
	Resolve(ctx context.Context, session domain.SessionID, requestID string, decision ports.ChatDecision) error
	ResolveInput(ctx context.Context, session domain.SessionID, requestID string, response ports.ChatInputResponse) error
	Interrupt(ctx context.Context, session domain.SessionID) error
	Steer(ctx context.Context, session domain.SessionID, msg ports.ChatUserMessage) (chatsvc.SteerResult, error)
	RecoverSteer(ctx context.Context, session domain.SessionID, clientMessageID string) (chatsvc.SteerResult, error)
	PromoteQueuedTurn(ctx context.Context, session domain.SessionID, turnID string) (chatsvc.PromoteQueuedTurnResult, error)
	CancelQueuedTurn(ctx context.Context, session domain.SessionID, turnID string) error
	EditQueuedTurn(ctx context.Context, session domain.SessionID, turnID string, edit chatsvc.QueuedMessageEdit) error
	ReorderQueuedTurns(ctx context.Context, session domain.SessionID, turnIDs []string) error
	Models(ctx context.Context, session domain.SessionID) ([]ports.ChatModel, domain.ConversationSettings, error)
	ConfigOptions(ctx context.Context, session domain.SessionID) ([]ports.ChatConfigOption, error)
	SetConfigOption(ctx context.Context, session domain.SessionID, configID string, value ports.ChatConfigOptionValue) ([]ports.ChatConfigOption, error)
	Skills(ctx context.Context, session domain.SessionID) ([]ports.ChatSkill, error)
	SetTurnSettings(ctx context.Context, session domain.SessionID, settings domain.ConversationSettings) (domain.ConversationSettings, error)
	Compact(ctx context.Context, session domain.SessionID) (ports.ChatCompactionResult, error)
	Rollback(ctx context.Context, session domain.SessionID, turnID, filePlanID string) (int, error)
	RollbackPreview(ctx context.Context, session domain.SessionID, turnID string) (chatsvc.RollbackPreview, error)
	RetryTurn(ctx context.Context, session domain.SessionID, turnID string) (domain.ConversationTurn, error)
	SetTitle(ctx context.Context, session domain.SessionID, title string) (string, error)
	ReloadMCPServers(ctx context.Context, session domain.SessionID) ([]domain.ConversationMCPServer, error)
	SwitchProvider(ctx context.Context, session domain.SessionID, request domain.ProviderSwitchRequest) error
	SwitchProviderAndSend(ctx context.Context, session domain.SessionID, request domain.ProviderSwitchRequest, msg ports.ChatUserMessage) (domain.ConversationTurn, bool, error)
	SessionHistory(ctx context.Context, session domain.SessionID, offset, limit int) ([]domain.HandoffTurn, int, error)
}

type pagedConversationService interface {
	SnapshotPage(ctx context.Context, session domain.SessionID, beforeSequence, afterSequence, limit int64) (chatsvc.Snapshot, error)
}

type reviewerConversationService interface {
	ModelsForOwner(context.Context, domain.ConversationOwner) ([]ports.ChatModel, domain.ConversationSettings, error)
	SetTurnSettingsForOwner(context.Context, domain.ConversationOwner, domain.ConversationSettings) (domain.ConversationSettings, error)
	SnapshotPageForReview(ctx context.Context, reviewID string, beforeSequence, limit int64) (chatsvc.Snapshot, error)
	SendForOwner(ctx context.Context, owner domain.ConversationOwner, msg ports.ChatUserMessage) (domain.ConversationTurn, error)
	ResolveForOwner(ctx context.Context, owner domain.ConversationOwner, requestID string, decision ports.ChatDecision) error
	ResolveInputForOwner(ctx context.Context, owner domain.ConversationOwner, requestID string, response ports.ChatInputResponse) error
	InterruptForOwner(ctx context.Context, owner domain.ConversationOwner) error
}

type chatViewService interface {
	SetChatView(context.Context, domain.SessionID, string, bool) error
}

type turnAutoResumeService interface {
	SetTurnAutoResume(context.Context, domain.SessionID, string, bool) error
}

type ConversationsController struct {
	Svc          ConversationService
	UserMessages UserMessageRecorder
	Renders      *attachmentstore.Store
	Search       ConversationSearch
}

func (c *ConversationsController) Register(r chi.Router) {
	r.Get("/conversations/{conversationId}/find", c.findConversation)
	r.Get("/conversations/{conversationId}/find-window", c.findConversationWindow)
	r.Get("/conversations/search", c.searchConversations)
	r.Patch("/sessions/{sessionId}/conversation/side-chats/{sideId}/label", c.renameSideChat)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}/cancel", c.cancelSideQuestion)
	r.Post("/sessions/{sessionId}/conversation/side-chats", c.createSideChat)
	r.Get("/sessions/{sessionId}/conversation/side-chats", c.listSideChats)
	r.Get("/sessions/{sessionId}/conversation/side-chats/{sideId}", c.sideSnapshot)
	r.Get("/sessions/{sessionId}/conversation/side-chats/{sideId}/events", c.streamSideChat)
	r.Delete("/sessions/{sessionId}/conversation/side-chats/{sideId}", c.closeSideChat)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/messages", c.sendSideQuestion)
	r.Patch("/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}", c.editSideQuestion)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}/retry", c.retrySideQuestion)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/interrupt", c.interruptSideQuestion)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/recover", c.recoverSideChat)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/compact", c.compactSideChat)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/approvals/{requestId}/resolve", c.resolveSideApproval)
	r.Post("/sessions/{sessionId}/conversation/side-chats/{sideId}/inputs/{requestId}/resolve", c.resolveSideInput)
	r.Patch("/sessions/{sessionId}/conversation/side-chats/{sideId}/settings", c.sideSettings)
	r.Get("/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", c.getSideDraft)
	r.Put("/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", c.putSideDraft)
	r.Post("/side-chats/launch/claim", c.claimSideLaunch)
	r.Get("/side-chats/launch/state", c.exportSideLaunch)
	r.Post("/side-chats/launch/state", c.recoverSideLaunch)
	r.Post("/side-chats/launch/retire", c.retireSideLaunch)
	r.Post("/sessions/{sessionId}/chat-view", c.setChatView)
	r.Get("/sessions/{sessionId}/conversation", c.snapshot)
	r.Post("/sessions/{sessionId}/conversation/messages", c.send)
	r.Post("/sessions/{sessionId}/conversation/prompt-suggestion", c.promptSuggestion)
	r.Post("/sessions/{sessionId}/conversation/approvals/{requestId}/resolve", c.resolve)
	r.Post("/sessions/{sessionId}/conversation/inputs/{requestId}/resolve", c.resolveInput)
	r.Post("/sessions/{sessionId}/conversation/interrupt", c.interrupt)
	r.Post("/sessions/{sessionId}/conversation/steer", c.steer)
	r.Post("/sessions/{sessionId}/conversation/steer-or-send", c.steerOrSend)
	r.Post("/sessions/{sessionId}/conversation/turns/{turnId}/steer", c.promoteQueuedTurn)
	r.Post("/sessions/{sessionId}/conversation/turns/{turnId}/cancel", c.cancelQueuedTurn)
	r.Post("/sessions/{sessionId}/conversation/turns/{turnId}/queue/edit", c.editQueuedTurn)
	r.Post("/sessions/{sessionId}/conversation/queue/reorder", c.reorderQueuedTurns)
	r.Post("/sessions/{sessionId}/conversation/compact", c.compact)
	r.Post("/agents/{agent}/chat/prewarm", c.prewarmAgent)
	r.Get("/agents/chat/unavailable", c.unavailableChatAgents)
	r.Get("/agents/chat/permission-modes", c.chatPermissionModes)
	r.Get("/sessions/{sessionId}/conversation/models", c.models)
	r.Get("/sessions/{sessionId}/conversation/config-options", c.configOptions)
	r.Patch("/sessions/{sessionId}/conversation/config-options/{configId}", c.setConfigOption)
	r.Get("/sessions/{sessionId}/conversation/skills", c.skills)
	r.Patch("/sessions/{sessionId}/conversation/settings", c.setSettings)
	r.Get("/sessions/{sessionId}/conversation/turns/{turnId}/rollback", c.rollbackPreview)
	r.Post("/sessions/{sessionId}/conversation/turns/{turnId}/rollback", c.rollback)
	r.Post("/sessions/{sessionId}/conversation/turns/{turnId}/edit", c.editMessage)
	r.Post("/sessions/{sessionId}/conversation/turns/{turnId}/retry", c.retryTurn)
	r.Patch("/sessions/{sessionId}/conversation/turns/{turnId}/auto-resume", c.setTurnAutoResume)
	r.Post("/sessions/{sessionId}/conversation/branches/{branchId}/activate", c.activateBranch)
	r.Put("/sessions/{sessionId}/conversation/title", c.setTitle)
	r.Post("/sessions/{sessionId}/conversation/mcp/reload", c.reloadMCPServers)
	r.Post("/sessions/{sessionId}/provider-switch", c.switchProvider)
	r.Get("/sessions/{sessionId}/history", c.sessionHistory)
	r.Post("/sessions/{sessionId}/renders", c.publishRender)
	r.Post("/sessions/{sessionId}/renders/check", c.checkRender)
	r.Get("/sessions/{sessionId}/renders/{renderId}", c.renderFile)
	r.Post("/sessions/{sessionId}/renders/{renderId}/artifact", c.saveRenderArtifact)
	r.Get("/reviews/{reviewId}/conversation/models", c.reviewModels)
	r.Patch("/reviews/{reviewId}/conversation/settings", c.reviewSetSettings)
	r.Get("/reviews/{reviewId}/conversation", c.reviewSnapshot)
	r.Post("/reviews/{reviewId}/conversation/messages", c.reviewSend)
	r.Post("/reviews/{reviewId}/conversation/approvals/{requestId}/resolve", c.reviewResolve)
	r.Post("/reviews/{reviewId}/conversation/inputs/{requestId}/resolve", c.reviewResolveInput)
	r.Post("/reviews/{reviewId}/conversation/interrupt", c.reviewInterrupt)
}

func (c *ConversationsController) setChatView(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(chatViewService)
	if !ok {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/{sessionId}/chat-view")
		return
	}
	var req SetChatViewRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.ViewID == "" || len(req.ViewID) > 128 || strings.TrimSpace(req.ViewID) != req.ViewID {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_VIEW_ID_INVALID", "viewId must be a nonempty identifier of at most 128 bytes", nil)
		return
	}
	if !req.activePresent {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_VIEW_ACTIVE_INVALID", "active must be true or false", nil)
		return
	}
	if err := svc.SetChatView(r.Context(), sessionID(r), req.ViewID, req.Active); err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ConversationsController) reviewService(w http.ResponseWriter, r *http.Request) (reviewerConversationService, bool) {
	svc, ok := c.Svc.(reviewerConversationService)
	if !ok {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
	}
	return svc, ok
}

func (c *ConversationsController) reviewModels(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	models, selected, err := svc.ModelsForOwner(r.Context(), domain.ReviewConversationOwner(chi.URLParam(r, "reviewId")))
	if err != nil && !errors.Is(err, chatsvc.ErrModelsUnsupported) {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, conversationModelsResponse(models, selected))
}

func (c *ConversationsController) reviewSetSettings(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	var req ConversationTurnSettingsPayload
	if !decodeConversationBody(w, r, &req) {
		return
	}
	approval := domain.PermissionMode(req.ApprovalMode)
	if req.ApprovalMode != "" && !approval.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_APPROVAL_MODE_INVALID", "unknown approval mode", nil)
		return
	}
	settings, err := svc.SetTurnSettingsForOwner(r.Context(), domain.ReviewConversationOwner(chi.URLParam(r, "reviewId")), domain.ConversationSettings{Model: req.Model, ReasoningEffort: req.ReasoningEffort, ApprovalMode: approval})
	if errors.Is(err, chatsvc.ErrReviewerPermissionsFixed) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_REVIEW_PERMISSIONS_FIXED", err.Error(), nil)
		return
	}
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, turnSettingsPayload(settings))
}

func (c *ConversationsController) reviewSnapshot(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	before, err := optionalPositiveInt64(r.URL.Query().Get("beforeSequence"))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CONVERSATION_CURSOR_INVALID", err.Error(), nil)
		return
	}
	limit := int64(200)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || limit < 1 || limit > 500 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CONVERSATION_LIMIT_INVALID", "limit must be between 1 and 500", nil)
			return
		}
	}
	snapshot, err := svc.SnapshotPageForReview(r.Context(), chi.URLParam(r, "reviewId"), before, limit)
	if err != nil {
		writeConversationErrorWithCode(w, r, err, "CONVERSATION_READ_FAILED", "Conversation read failed")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, conversationSnapshotResponse(snapshot))
}

func (c *ConversationsController) reviewSend(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	var req SendConversationMessageRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.Text == "" && len(req.Attachments) == 0 && len(req.Resources) == 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_MESSAGE_EMPTY", "message text is required", nil)
		return
	}
	content, attachmentErr := conversationContent(req)
	if attachmentErr != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", attachmentErr.code, attachmentErr.message, nil)
		return
	}
	text := req.Text
	if text == "" {
		text = fmt.Sprintf("Attached %d item(s) for context", len(content))
	}
	turn, err := svc.SendForOwner(r.Context(), domain.ReviewConversationOwner(chi.URLParam(r, "reviewId")), ports.ChatUserMessage{Text: text, Content: content, ClientMessageID: req.ClientMessageID, Origin: domain.MessageOriginHuman})
	if err != nil {
		writeConversationErrorWithCode(w, r, err, "CHAT_DELIVERY_FAILED", "Chat delivery failed")
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, SendConversationMessageResponse{TurnID: turn.ID, ProviderTurnID: turn.ProviderTurnID, State: turn.State, Duplicate: turn.ID == ""})
}

func (c *ConversationsController) reviewResolve(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	var req ResolveConversationApprovalRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.DecisionID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_DECISION_REQUIRED", "decisionId is required", nil)
		return
	}
	requestID, ok := conversationRequestID(w, r)
	if !ok {
		return
	}
	if err := svc.ResolveForOwner(r.Context(), domain.ReviewConversationOwner(chi.URLParam(r, "reviewId")), requestID, ports.ChatDecision{ID: req.DecisionID}); err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ConversationsController) reviewResolveInput(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	var req ResolveConversationInputRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	action := ports.ChatInputAction(req.Action)
	if !action.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_INPUT_ACTION_INVALID", "action must be accept, decline, or cancel", nil)
		return
	}
	if action != ports.ChatInputActionAccept && len(req.Content) > 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_INPUT_CONTENT_INVALID", "content is only allowed with accept", nil)
		return
	}
	requestID, ok := conversationRequestID(w, r)
	if !ok {
		return
	}
	if err := svc.ResolveInputForOwner(r.Context(), domain.ReviewConversationOwner(chi.URLParam(r, "reviewId")), requestID, ports.ChatInputResponse{Action: action, Content: req.Content}); err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ConversationsController) reviewInterrupt(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.reviewService(w, r)
	if !ok {
		return
	}
	if err := svc.InterruptForOwner(r.Context(), domain.ReviewConversationOwner(chi.URLParam(r, "reviewId"))); err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ConversationsController) editMessage(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST",
			"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/edit")
		return
	}
	var req EditConversationMessageRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_EDIT_TURN_INVALID", "edited message text is required", nil)
		return
	}
	result, err := c.Svc.EditMessage(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")),
		chi.URLParam(r, "turnId"), ports.ChatUserMessage{
			Text: req.Text, ClientMessageID: req.ClientMessageID, Origin: domain.MessageOriginHuman,
		})
	if err != nil {
		writeConversationEditError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, EditConversationMessageResponse{
		SourceBranchID: result.SourceBranchID,
		ActiveBranchID: result.ActiveBranchID,
		TurnID:         result.Turn.ID,
		ProviderTurnID: result.Turn.ProviderTurnID,
		State:          result.Turn.State,
	})
}

func (c *ConversationsController) retryTurn(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST",
			"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/retry")
		return
	}
	turn, err := c.Svc.RetryTurn(r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")), chi.URLParam(r, "turnId"))
	if err != nil {
		writeConversationRetryError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, RetryTurnResponse{
		TurnID:         turn.ID,
		ProviderTurnID: turn.ProviderTurnID,
		State:          turn.State,
	})
}

func (c *ConversationsController) setTurnAutoResume(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(turnAutoResumeService)
	if !ok {
		apispec.NotImplemented(w, r, http.MethodPatch, "/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/auto-resume")
		return
	}
	var req SetTurnAutoResumeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil || req.Enabled == nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_AUTO_RESUME_INVALID", "enabled must be a boolean", nil)
		return
	}
	if err := svc.SetTurnAutoResume(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")), chi.URLParam(r, "turnId"), *req.Enabled); err != nil {
		if errors.Is(err, chatsvc.ErrUsageLimitAutoResumeInvalid) || errors.Is(err, chatsvc.ErrUsageLimitResetUnknown) || errors.Is(err, ports.ErrAgentAccountChanged) {
			envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "CHAT_AUTO_RESUME_UNAVAILABLE", err.Error(), nil)
		} else {
			writeConversationRetryError(w, r, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeConversationRetryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, chatsvc.ErrTurnNotRetryable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RETRY_NOT_RETRYABLE",
			"this turn cannot be retried: it is not a failed human turn in this conversation", nil)
	case errors.Is(err, chatsvc.ErrRetryDeliveryUncertain):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RETRY_DELIVERY_UNCERTAIN",
			"delivery of this turn was never confirmed by the agent, so retrying it could run the work twice", nil)
	case errors.Is(err, chatsvc.ErrRetryStaleBranch):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RETRY_STALE_BRANCH",
			"this turn is no longer on the active conversation branch", nil)
	case errors.Is(err, chatsvc.ErrRetryContentInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_RETRY_CONTENT_INVALID", err.Error(), nil)
	case errors.Is(err, chatsvc.ErrRetryUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RETRY_UNSUPPORTED", err.Error(), nil)
	case errors.Is(err, chatsvc.ErrTurnRunning), errors.Is(err, chatsvc.ErrControllerHandoff):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RETRY_BUSY", "stop the current turn before retrying this one", nil)
	case errors.Is(err, domain.ErrNoConversationTurn):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
			"CHAT_RETRY_TURN_NOT_FOUND", "that turn is not in this conversation", nil)
	default:
		writeConversationError(w, r, err)
	}
}

func (c *ConversationsController) activateBranch(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST",
			"/api/v1/sessions/{sessionId}/conversation/branches/{branchId}/activate")
		return
	}
	branchID, err := url.PathUnescape(chi.URLParam(r, "branchId"))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_BRANCH_INVALID", "conversation branch identifier is invalid", nil)
		return
	}
	active, err := c.Svc.ActivateBranch(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")),
		branchID)
	if errors.Is(err, domain.ErrNoConversationBranch) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
			"CHAT_BRANCH_NOT_FOUND", "that conversation branch does not exist", nil)
		return
	}
	if errors.Is(err, chatsvc.ErrTurnRunning) || errors.Is(err, chatsvc.ErrControllerHandoff) {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EDIT_BUSY", "stop the current turn before switching conversation branches", nil)
		return
	}
	if errors.Is(err, chatsvc.ErrBranchProviderMismatch) {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_BRANCH_PROVIDER_MISMATCH",
			"that branch belongs to the agent that handled this session before the last switch", nil)
		return
	}
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, ActivateConversationBranchResponse{ActiveBranchID: active})
}

func writeConversationEditError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, chatsvc.ErrEditDeliveryUncertain):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EDIT_UNCERTAIN",
			"the provider may have received this edit; retry only with the same recovery action", nil)
	case errors.Is(err, chatsvc.ErrEditIdempotencyConflict):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EDIT_IDEMPOTENCY_CONFLICT",
			"this delivery handle belongs to a different edit", nil)
	case errors.Is(err, chatsvc.ErrEditDeliveryRejected):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EDIT_REJECTED",
			"the edit was not accepted; retry deliberately with a new delivery handle", nil)
	case errors.Is(err, chatsvc.ErrForkUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EDIT_UNSUPPORTED", "this agent cannot branch an earlier prompt", nil)
	case errors.Is(err, chatsvc.ErrTurnRunning), errors.Is(err, chatsvc.ErrControllerHandoff):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EDIT_BUSY", "stop the current turn before branching", nil)
	case errors.Is(err, chatsvc.ErrEditTurnInvalid) && errors.Is(err, domain.ErrNoConversationTurn):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
			"CHAT_EDIT_TURN_INVALID", "that editable prompt is not in this conversation", nil)
	case errors.Is(err, chatsvc.ErrEditTurnInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_EDIT_TURN_INVALID", err.Error(), nil)
	default:
		writeConversationError(w, r, err)
	}
}

func (c *ConversationsController) configOptions(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/conversation/config-options")
		return
	}
	options, err := c.Svc.ConfigOptions(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if err != nil {
		if errors.Is(err, chatsvc.ErrConfigOptionsUnsupported) {
			envelope.WriteJSON(w, http.StatusOK, ConversationConfigOptionsResponse{
				Options: []ConversationConfigOptionResponse{},
			})
			return
		}
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, configOptionsPayload(options))
}

func (c *ConversationsController) setConfigOption(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/sessions/{sessionId}/conversation/config-options/{configId}")
		return
	}
	var req SetConversationConfigOptionRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if (req.Value == "") == (req.Enabled == nil) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_CONFIG_OPTION_VALUE_REQUIRED",
			"provide exactly one of value or enabled", nil)
		return
	}
	options, err := c.Svc.SetConfigOption(
		r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")),
		chi.URLParam(r, "configId"),
		ports.ChatConfigOptionValue{Select: req.Value, Boolean: req.Enabled},
	)
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, configOptionsPayload(options))
}

func (c *ConversationsController) reloadMCPServers(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/conversation/mcp/reload")
		return
	}
	session := domain.SessionID(chi.URLParam(r, "sessionId"))
	servers, err := c.Svc.ReloadMCPServers(r.Context(), session)
	if errors.Is(err, chatsvc.ErrTurnRunning) {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_TURN_RUNNING",
			"stop the agent before reloading its tool servers: it is in the middle of a turn", nil)
		return
	}
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReloadConversationMCPServersResponse{
		Servers: mcpServersPayload(servers),
	})
}

func (c *ConversationsController) rollbackPreview(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET",
			"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/rollback")
		return
	}
	preview, err := c.Svc.RollbackPreview(r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")), chi.URLParam(r, "turnId"))
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	files := make([]RollbackFileChange, 0, len(preview.Changes))
	for _, change := range preview.Changes {
		files = append(files, RollbackFileChange{Path: change.Path, Change: string(change.Kind)})
	}
	envelope.WriteJSON(w, http.StatusOK, RollbackPreviewResponse{
		FilesRestorable: preview.Unavailable == "",
		Reason:          preview.Unavailable,
		Files:           files,
		TotalFiles:      len(preview.Changes),
		CommitsSince:    preview.CommitsSince,
		PlanID:          preview.PlanID,
	})
}

func (c *ConversationsController) rollback(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST",
			"/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/rollback")
		return
	}
	var req RollbackConversationRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"INVALID_BODY", "request body is not valid JSON", nil)
		return
	}
	var filePlanID string
	if req.RestoreFiles {
		if req.PlanID == "" {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
				"CHAT_ROLLBACK_PLAN_REQUIRED", "restoreFiles needs the planId of the preview the user confirmed", nil)
			return
		}
		filePlanID = req.PlanID
	}
	discarded, err := c.Svc.Rollback(r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")), chi.URLParam(r, "turnId"), filePlanID)
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RollbackConversationResponse{TurnsDiscarded: discarded})
}

func (c *ConversationsController) setTitle(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/conversation/title")
		return
	}
	var req SetConversationTitleRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	title, err := c.Svc.SetTitle(r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")), req.Title)
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, SetConversationTitleResponse{Title: title})
}

func (c *ConversationsController) compact(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/conversation/compact")
		return
	}
	result, err := c.Svc.Compact(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, CompactConversationResponse{
		TokensBefore: result.TokensBefore,
		TokensAfter:  result.TokensAfter,
	})
}

type chatPrewarmer interface {
	PrewarmChat(harness domain.AgentHarness)
}

func (c *ConversationsController) prewarmAgent(w http.ResponseWriter, r *http.Request) {
	prewarmer, ok := c.Svc.(chatPrewarmer)
	if c.Svc == nil || !ok {
		apispec.NotImplemented(w, r, "POST", "/api/v1/agents/{agent}/chat/prewarm")
		return
	}
	agent := strings.TrimSpace(chi.URLParam(r, "agent"))
	prewarmer.PrewarmChat(domain.AgentHarness(agent))
	envelope.WriteJSON(w, http.StatusAccepted, PrewarmChatAgentResponse{Agent: agent})
}

type chatAvailability interface {
	ChatUnavailable(ctx context.Context, harnesses []domain.AgentHarness) map[domain.AgentHarness]string
}

func (c *ConversationsController) unavailableChatAgents(w http.ResponseWriter, r *http.Request) {
	checker, ok := c.Svc.(chatAvailability)
	if c.Svc == nil || !ok {
		apispec.NotImplemented(w, r, "GET", "/api/v1/agents/chat/unavailable")
		return
	}
	harnesses := queryHarnesses(r)
	unavailable := checker.ChatUnavailable(r.Context(), harnesses)
	response := UnavailableChatAgentsResponse{Agents: []UnavailableChatAgent{}}
	for _, harness := range harnesses {
		if reason, missing := unavailable[harness]; missing {
			response.Agents = append(response.Agents, UnavailableChatAgent{Agent: string(harness), Reason: reason})
		}
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

func queryHarnesses(r *http.Request) []domain.AgentHarness {
	var harnesses []domain.AgentHarness
	for _, agent := range strings.Split(r.URL.Query().Get("agents"), ",") {
		if agent = strings.TrimSpace(agent); agent != "" && len(harnesses) < 64 {
			harnesses = append(harnesses, domain.AgentHarness(agent))
		}
	}
	return harnesses
}

type chatPermissionModesLister interface {
	PermissionModes(ctx context.Context, harness domain.AgentHarness) ([]ports.PermissionMode, error)
}

func (c *ConversationsController) chatPermissionModes(w http.ResponseWriter, r *http.Request) {
	lister, ok := c.Svc.(chatPermissionModesLister)
	if c.Svc == nil || !ok {
		apispec.NotImplemented(w, r, "GET", "/api/v1/agents/chat/permission-modes")
		return
	}
	response := ChatPermissionModesResponse{Agents: []ChatAgentPermissionModes{}}
	for _, harness := range queryHarnesses(r) {
		modes, err := lister.PermissionModes(r.Context(), harness)
		if err != nil || len(modes) == 0 {
			continue
		}
		entry := ChatAgentPermissionModes{Agent: string(harness), Modes: make([]string, 0, len(modes))}
		for _, mode := range modes {
			entry.Modes = append(entry.Modes, string(mode))
		}
		response.Agents = append(response.Agents, entry)
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

func (c *ConversationsController) models(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/conversation/models")
		return
	}
	session := domain.SessionID(chi.URLParam(r, "sessionId"))
	models, selected, err := c.Svc.Models(r.Context(), session)
	if err != nil {
		if errors.Is(err, chatsvc.ErrModelsUnsupported) {
			envelope.WriteJSON(w, http.StatusOK, ConversationModelsResponse{
				Models:   []ConversationModelResponse{},
				Selected: turnSettingsPayload(selected),
			})
			return
		}
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, conversationModelsResponse(models, selected))
}

func (c *ConversationsController) setSettings(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/sessions/{sessionId}/conversation/settings")
		return
	}
	var req ConversationTurnSettingsPayload
	if !decodeConversationBody(w, r, &req) {
		return
	}
	approval := domain.PermissionMode(req.ApprovalMode)
	if req.ApprovalMode != "" && !approval.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_APPROVAL_MODE_INVALID",
			fmt.Sprintf("unknown approval mode %q", req.ApprovalMode), nil)
		return
	}

	settings, err := c.Svc.SetTurnSettings(r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")), domain.ConversationSettings{
			Model:           req.Model,
			ReasoningEffort: req.ReasoningEffort,
			ApprovalMode:    approval,

			FastMode:          req.FastMode,
			PlanMode:          req.PlanMode,
			AutoCompactWindow: req.AutoCompactWindow,
		})
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, turnSettingsPayload(settings))
}

func conversationModelsResponse(
	models []ports.ChatModel,
	selected domain.ConversationSettings,
) ConversationModelsResponse {
	out := ConversationModelsResponse{
		Models:   make([]ConversationModelResponse, 0, len(models)),
		Selected: turnSettingsPayload(selected),
	}
	for _, model := range models {
		out.Models = append(out.Models, ConversationModelResponse{
			ID:            model.ID,
			DisplayName:   model.DisplayName,
			Description:   model.Description,
			Default:       model.Default,
			Efforts:       model.Efforts,
			DefaultEffort: model.DefaultEffort,
			SupportsFast:  model.FastTier != "",
		})
	}
	return out
}

func configOptionsPayload(options []ports.ChatConfigOption) ConversationConfigOptionsResponse {
	out := ConversationConfigOptionsResponse{
		Options: make([]ConversationConfigOptionResponse, 0, len(options)),
	}
	for _, option := range options {
		item := ConversationConfigOptionResponse{
			ID:             option.ID,
			Name:           option.Name,
			Description:    option.Description,
			Category:       option.Category,
			Type:           string(option.Type),
			CurrentValue:   option.Current.Select,
			CurrentBoolean: option.Current.Boolean,
			Choices:        make([]ConversationConfigChoiceResponse, 0, len(option.Choices)),
		}
		for _, choice := range option.Choices {
			item.Choices = append(item.Choices, ConversationConfigChoiceResponse{
				PermissionMode: choice.PermissionMode,
				Value:          choice.Value,
				Name:           choice.Name,
				Description:    choice.Description,
				Group:          choice.Group,
				GroupName:      choice.GroupName,
			})
		}
		out.Options = append(out.Options, item)
	}
	return out
}

func turnSettingsPayload(settings domain.ConversationSettings) ConversationTurnSettingsPayload {
	return ConversationTurnSettingsPayload{
		Model:           settings.Model,
		ReasoningEffort: settings.ReasoningEffort,
		ApprovalMode:    string(settings.ApprovalMode),

		FastMode:          settings.FastMode,
		PlanMode:          settings.PlanMode,
		AutoCompactWindow: settings.AutoCompactWindow,
	}
}

func (c *ConversationsController) snapshot(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/conversation")
		return
	}
	session := domain.SessionID(chi.URLParam(r, "sessionId"))
	query := r.URL.Query()
	before, err := optionalPositiveInt64(query.Get("beforeSequence"))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CONVERSATION_CURSOR_INVALID", err.Error(), nil)
		return
	}
	after, err := optionalPositiveInt64(query.Get("afterSequence"))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CONVERSATION_CURSOR_INVALID", "afterSequence must be a positive integer", nil)
		return
	}
	limit := int64(200)
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || limit < 1 || limit > 500 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CONVERSATION_LIMIT_INVALID", "limit must be between 1 and 500", nil)
			return
		}
	}
	var outputLimit int64
	if raw := query.Get("outputLimit"); raw != "" {
		outputLimit, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || outputLimit < 64 || outputLimit > 65536 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CONVERSATION_OUTPUT_LIMIT_INVALID", "outputLimit must be between 64 and 65536", nil)
			return
		}
	}
	var snapshot chatsvc.Snapshot
	if paged, ok := c.Svc.(pagedConversationService); ok {
		snapshot, err = paged.SnapshotPage(r.Context(), session, before, after, limit)
	} else {
		snapshot, err = c.Svc.Snapshot(r.Context(), session)
	}
	if err != nil {
		writeConversationErrorWithCode(w, r, err, "CONVERSATION_READ_FAILED", "Conversation read failed")
		return
	}
	response := conversationSnapshotResponse(snapshot)
	if outputLimit > 0 {
		for _, activity := range response.Activities {
			capActivityOutput(activity.Detail, int(outputLimit))
		}
	}
	response.SupportsExcerpts = true
	response.SupportsFileReferences = true
	envelope.WriteJSON(w, http.StatusOK, response)
}

const outputElision = "\n…\n"

func capActivityOutput(detail map[string]any, limit int) {
	output, ok := detail["output"].(string)
	if !ok || len(output) <= limit {
		return
	}
	head := limit / 2
	for head > 0 && !utf8.RuneStart(output[head]) {
		head--
	}
	tail := len(output) - (limit - head)
	for tail < len(output) && !utf8.RuneStart(output[tail]) {
		tail++
	}
	detail["output"] = output[:head] + outputElision + output[tail:]
	detail["outputBytes"] = len(output)
}

func optionalPositiveInt64(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("beforeSequence must be a positive integer")
	}
	return value, nil
}

func (c *ConversationsController) send(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/conversation/messages")
		return
	}
	var req SendConversationMessageRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	message, invalid := chatUserMessage(req, chi.URLParam(r, "sessionId"))
	if invalid != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", invalid.code, invalid.message, nil)
		return
	}
	turn, err := c.Svc.Send(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")), message)
	if err != nil {
		writeConversationErrorWithCode(w, r, err, "CHAT_DELIVERY_FAILED", "Chat delivery failed")
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, sendConversationMessageResponse(turn))
}

func chatUserMessage(req SendConversationMessageRequest, sessionID string) (ports.ChatUserMessage, *attachmentError) {
	for _, ref := range req.FileReferences {
		if ref.SessionID != sessionID {
			return ports.ChatUserMessage{}, &attachmentError{"CHAT_FILE_REFERENCE_INVALID", "file reference belongs to another session"}
		}
	}
	if req.Text == "" && len(req.Attachments) == 0 && len(req.Resources) == 0 && len(req.Excerpts) == 0 && len(req.FileReferences) == 0 {
		return ports.ChatUserMessage{}, &attachmentError{code: "CHAT_MESSAGE_EMPTY", message: "message text is required"}
	}
	content, attachmentErr := conversationContent(req)
	if attachmentErr != nil {
		return ports.ChatUserMessage{}, attachmentErr
	}
	text := req.Text
	if text == "" {
		if len(req.FileReferences) > 0 {
			text = fmt.Sprintf("Use the attached %d annotation(s) as context", len(req.FileReferences)+len(req.Excerpts))
		} else if len(req.Excerpts) > 0 {
			text = fmt.Sprintf("Use the attached %d chat excerpt(s) as context", len(req.Excerpts))
		} else {
			text = fmt.Sprintf("Attached %d item(s) for context", len(content))
		}
	}
	excerpts := make([]ports.ChatExcerptReference, 0, len(req.Excerpts))
	for _, excerpt := range req.Excerpts {
		excerpts = append(excerpts, ports.ChatExcerptReference{
			ConversationID: excerpt.ConversationID, MessageID: excerpt.MessageID,
			Revision: excerpt.Revision, Text: excerpt.Text, ID: excerpt.ID, Order: excerpt.Order, Snapshot: excerpt.Snapshot, Comment: excerpt.Comment,
		})
	}
	return ports.ChatUserMessage{
		Text:            text,
		Content:         content,
		Excerpts:        excerpts,
		ClientMessageID: req.ClientMessageID,
		Origin:          domain.MessageOriginHuman,
		Continuation:    req.Continuation,
	}, nil
}

func sendConversationMessageResponse(turn domain.ConversationTurn) SendConversationMessageResponse {
	return SendConversationMessageResponse{
		TurnID:         turn.ID,
		ProviderTurnID: turn.ProviderTurnID,
		State:          turn.State,
		Duplicate:      turn.ID == "",
	}
}

func conversationContent(req SendConversationMessageRequest) ([]ports.ChatContent, *attachmentError) {
	if len(req.Excerpts)+len(req.FileReferences) > 8 {
		return nil, &attachmentError{"CHAT_ANNOTATION_LIMIT", "At most eight annotations may be attached"}
	}

	spawnInputs := make([]AttachmentInput, 0, len(req.Attachments))
	for _, attachment := range req.Attachments {
		spawnInputs = append(spawnInputs, AttachmentInput{
			MimeType: attachment.MIMEType,
			Data:     attachment.Data,
		})
	}
	if _, err := decodeAttachments(spawnInputs, maxConversationImageBytes, maxConversationImagesBytes); err != nil {
		return nil, err
	}
	content := make([]ports.ChatContent, 0, len(req.Attachments)+len(req.Resources))
	for _, attachment := range req.Attachments {
		content = append(content, ports.ChatContent{
			Type: "image", Data: attachment.Data,
			MIMEType: strings.ToLower(strings.TrimSpace(attachment.MIMEType)),
		})
	}
	for _, resource := range req.Resources {
		if strings.TrimSpace(resource.URI) == "" || strings.TrimSpace(resource.Name) == "" {
			return nil, &attachmentError{"INVALID_RESOURCE", "resource uri and name are required"}
		}
		if resource.URI == ports.ChatInternalReplayResourceURI {
			return nil, &attachmentError{"INVALID_RESOURCE", "resource uri is reserved for Orchestrator internal context"}
		}
		kind, text := "resource_link", ""
		if resource.Text != nil {
			kind = "resource"
			text = *resource.Text
		}
		content = append(content, ports.ChatContent{
			Type: kind, URI: resource.URI, Name: resource.Name,
			MIMEType: resource.MIMEType, Text: text,
		})
	}
	for _, input := range req.FileReferences {
		ref := ports.ChatFileReference(input)
		if err := chatsvc.ValidateFileReference(ref); err != nil {
			return nil, &attachmentError{"CHAT_FILE_REFERENCE_INVALID", err.Error()}
		}
		content = append(content, ports.ChatContent{Type: "file_reference", Text: chatsvc.FileReferenceQuotedPath, FileReference: &ref})
	}
	return content, nil
}

func (c *ConversationsController) resolve(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST",
			"/api/v1/sessions/{sessionId}/conversation/approvals/{requestId}/resolve")
		return
	}
	var req ResolveConversationApprovalRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.DecisionID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_DECISION_REQUIRED", "decisionId is required", nil)
		return
	}

	requestID, ok := conversationRequestID(w, r)
	if !ok {
		return
	}
	err := c.Svc.Resolve(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")),
		requestID, ports.ChatDecision{ID: req.DecisionID})
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ConversationsController) resolveInput(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST",
			"/api/v1/sessions/{sessionId}/conversation/inputs/{requestId}/resolve")
		return
	}
	var req ResolveConversationInputRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	action := ports.ChatInputAction(req.Action)
	if !action.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_INPUT_ACTION_INVALID", "action must be accept, decline, or cancel", nil)
		return
	}
	if action != ports.ChatInputActionAccept && len(req.Content) > 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_INPUT_CONTENT_INVALID", "content is only allowed with accept", nil)
		return
	}
	requestID, ok := conversationRequestID(w, r)
	if !ok {
		return
	}
	if err := c.Svc.ResolveInput(
		r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")),
		requestID,
		ports.ChatInputResponse{Action: action, Content: req.Content},
	); err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ConversationsController) interrupt(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/conversation/interrupt")
		return
	}
	err := c.Svc.Interrupt(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func conversationRequestID(w http.ResponseWriter, r *http.Request) (string, bool) {
	requestID, err := url.PathUnescape(chi.URLParam(r, "requestId"))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_REQUEST_INVALID", "conversation request identifier is invalid", nil)
		return "", false
	}
	return requestID, true
}

func decodeConversationBody(w http.ResponseWriter, r *http.Request, into any) bool {
	return decodeConversationBodyLimit(w, r, into, maxConversationBody)
}

func decodeConversationBodyLimit(w http.ResponseWriter, r *http.Request, into any, limit int64) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"INVALID_BODY", "could not read request body", nil)
		return false
	}
	if err := json.Unmarshal(body, into); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"INVALID_BODY", "request body is not valid JSON", nil)
		return false
	}
	return true
}

func writeConversationError(w http.ResponseWriter, r *http.Request, err error) {
	writeConversationErrorWithCode(w, r, err, "", "")
}

func writeConversationErrorWithCode(w http.ResponseWriter, r *http.Request, err error, fallbackCode, fallbackMessage string) {
	switch {
	case errors.Is(err, chatsvc.ErrFileReferenceInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_FILE_REFERENCE_INVALID", err.Error(), nil)
	case errors.Is(err, chatsvc.ErrAnnotationLimit):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_ANNOTATION_LIMIT", err.Error(), nil)
	case errors.Is(err, ports.ErrSessionNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
			"SESSION_NOT_FOUND", "session not found", nil)

	case errors.Is(err, chatsvc.ErrNotChatMode):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"SESSION_MODE_MISMATCH",
			"this session was created in Terminal UI mode and has no chat conversation", nil)

	case errors.Is(err, chatsvc.ErrNoController), errors.Is(err, chatsvc.ErrNotProvisioning):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_CONTROLLER_NOT_READY",
			"the agent controller for this session is not running", nil)

	case errors.Is(err, chatsvc.ErrContinuationUnavailable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_CONTINUATION_UNAVAILABLE", err.Error(), nil)

	case errors.Is(err, chatsvc.ErrExcerptInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_EXCERPT_INVALID", err.Error(), nil)

	case errors.Is(err, chatsvc.ErrExcerptStale):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_EXCERPT_STALE", err.Error(), nil)

	case errors.Is(err, chatsvc.ErrPromptSuggestionStale):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_PROMPT_SUGGESTION_STALE", err.Error(), nil)

	case errors.Is(err, chatsvc.ErrControllerHandoff):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_INTERFACE_TRANSITION",
			"the session is switching interfaces; send again after the handoff completes", nil)

	case errors.Is(err, chatsvc.ErrCompactionUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_COMPACTION_UNSUPPORTED",
			"this agent cannot compact its conversation history", nil)

	case errors.Is(err, chatsvc.ErrCompactionWhileBusy):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_COMPACTION_BUSY",
			"finish or stop the current turn before compacting", nil)

	case errors.Is(err, chatsvc.ErrNoActiveTurn):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_NO_ACTIVE_TURN", "there is no turn in flight to interrupt", nil)

	case errors.Is(err, domain.ErrNoConversationTurn):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
			"CHAT_TURN_NOT_FOUND", "that turn is not in this session's conversation", nil)

	case errors.Is(err, domain.ErrClientMessageConflict):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_MESSAGE_IDEMPOTENCY_CONFLICT", "clientMessageId belongs to a different message", nil)

	case errors.Is(err, chatsvc.ErrTurnRunning):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_TURN_RUNNING",
			"stop the agent before rolling back: it is in the middle of a turn", nil)

	case errors.Is(err, chatsvc.ErrTurnNotRollbackable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_TURN_NOT_ROLLBACKABLE",
			"that turn never reached the agent, so there is nothing to undo", nil)

	case errors.Is(err, chatsvc.ErrTurnProviderMismatch):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_TURN_PROVIDER_MISMATCH",
			"that turn belongs to the agent that handled this session before the last switch", nil)

	case errors.Is(err, chatsvc.ErrRollbackUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_ROLLBACK_UNSUPPORTED", "this agent cannot discard conversation history", nil)

	case errors.Is(err, chatsvc.ErrFilesNotRestorable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_ROLLBACK_FILES_UNAVAILABLE", err.Error(), nil)

	case errors.Is(err, chatsvc.ErrFilesChanged):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_ROLLBACK_FILES_CHANGED", err.Error(), nil)

	case errors.Is(err, chatsvc.ErrForkUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_FORK_UNSUPPORTED", "this agent cannot branch a conversation", nil)

	case errors.Is(err, chatsvc.ErrRenameUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RENAME_UNSUPPORTED", "this agent's conversation carries no title", nil)

	case errors.Is(err, chatsvc.ErrOrchestratorRename):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"ORCHESTRATOR_RENAME_UNSUPPORTED", "the project orchestrator cannot be renamed", nil)

	case errors.Is(err, chatsvc.ErrMCPReloadUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_MCP_RELOAD_UNSUPPORTED", "this agent cannot reload its tool servers", nil)

	case errors.Is(err, chatsvc.ErrTitleRequired):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_TITLE_REQUIRED", "title is required", nil)

	case errors.Is(err, ports.ErrChatProviderSessionLost):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_PROVIDER_SESSION_LOST",
			"the agent's process lost this conversation and was stopped; resume the agent to continue", nil)

	case errors.Is(err, chatsvc.ErrProviderRefused):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_PROVIDER_REFUSED", err.Error(), nil)

	case errors.Is(err, ports.ErrChatRequestNotPending):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_REQUEST_NOT_PENDING",
			"this request has already been answered or is no longer waiting", nil)

	case errors.Is(err, ports.ErrChatDecisionNotOffered):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_DECISION_NOT_OFFERED", err.Error(), nil)

	case errors.Is(err, ports.ErrChatPermissionModeUnsupported):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_PERMISSION_MODE_UNSUPPORTED", err.Error(), nil)

	case errors.Is(err, ports.ErrChatConfigOptionInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_CONFIG_OPTION_INVALID", err.Error(), nil)

	case errors.Is(err, ports.ErrChatUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"SESSION_MODE_UNSUPPORTED", err.Error(), nil)

	case errors.Is(err, ports.ErrChatDriverUnavailable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_DRIVER_UNAVAILABLE", err.Error(), nil)

	case errors.Is(err, ports.ErrChatDriverIncompatible):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_DRIVER_INCOMPATIBLE", err.Error(), nil)

	case errors.Is(err, ports.ErrChatAuthRequired):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_AUTH_REQUIRED", "the agent is installed but not authenticated", nil)

	case errors.Is(err, ports.ErrChatResumeFailed):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_RESUME_FAILED",
			"the stored provider conversation could not be resumed", nil)

	default:
		if fallbackCode != "" {
			envelope.WriteErrorWithCode(w, r, err, fallbackCode, fallbackMessage)
		} else {
			envelope.WriteError(w, r, err)
		}
	}
}

func conversationSnapshotResponse(s chatsvc.Snapshot) ConversationSnapshotResponse {
	out := ConversationSnapshotResponse{
		ContinueTurnID:                   chatsvc.ContinuableTurnID(s.Turns),
		ConversationID:                   s.Conversation.ID,
		ActiveBranchID:                   s.Conversation.ActiveBranchID,
		BranchedFromEarlierMessage:       s.BranchedFromEarlierMessage,
		BatchedDeliveries:                s.BatchedDeliveries,
		SessionID:                        string(s.SessionID),
		Harness:                          string(s.Harness),
		Mode:                             string(s.Mode),
		Controller:                       string(s.Controller),
		LatestSequence:                   s.Conversation.LatestSequence,
		OldestSequence:                   s.OldestSequence,
		HasMoreBefore:                    s.HasMoreBefore,
		AfterSequence:                    s.AfterSequence,
		NativeForkAvailableAfterSequence: s.NativeForkAvailableAfterSequence,
		Turns:                            make([]ConversationTurnResponse, 0, len(s.Turns)),
		Messages:                         make([]ConversationMessageResponse, 0, len(s.Messages)),
		Activities:                       make([]ConversationActivityResponse, 0, len(s.Activities)),
		BranchPoints:                     make([]ConversationBranchPointResponse, 0, len(s.BranchPoints)),
		BranchMaterialization:            branchMaterializationPayload(s.ActiveBranch),
		Settings:                         turnSettingsPayload(s.Conversation.Settings),
		Usage:                            usagePayload(s.Usage),
		RateLimits:                       rateLimitsPayload(s.RateLimits),
		CompactedAt:                      optionalTimestamp(s.Conversation.CompactedAt),
		Title:                            s.Conversation.ProviderTitle,
		ModelReroute:                     modelReroutePayload(s.Conversation.ModelReroute),
		Account:                          accountPayload(s.Conversation.Account),
		ThreadState:                      threadStatePayload(s.Conversation.ThreadState),
		MCPServers:                       mcpServersPayload(s.Conversation.MCPServers),
		Capabilities:                     capabilityNames(s.Capabilities),
	}

	for _, turn := range s.Turns {
		out.Turns = append(out.Turns, ConversationTurnResponse{
			ID:              turn.ID,
			State:           string(turn.State),
			ProviderTurnID:  turn.ProviderTurnID,
			RetryOfTurnID:   turn.RetryOfTurnID,
			HasRetryAttempt: turn.HasRetryAttempt,
			HasHumanPrompt:  turn.HasHumanPrompt,
			ErrorMessage:    turn.ErrorMessage,
			FailureCode:     string(turn.FailureCode),
			UsageLimit:      usageLimitPayload(turn.UsageLimit),
			RequestedAt:     turn.RequestedAt.UTC().Format(time.RFC3339),
			StartedAt:       optionalTimestamp(turn.StartedAt),
			CompletedAt:     optionalTimestamp(turn.CompletedAt),
			Diff:            turnDiffPayload(turn.Diff),
			Artifacts:       turnArtifactsPayload(turn.Artifacts),
			Plan:            turnPlanPayload(turn.Plan),
			RolledBack:      turn.RolledBackAt != nil,
		})
	}

	for _, msg := range s.Messages {
		message := ConversationMessageResponse{
			Kind:              "message",
			ID:                msg.ID,
			TurnID:            msg.TurnID,
			Sequence:          msg.Sequence,
			Revision:          msg.Revision,
			Role:              string(msg.Role),
			Origin:            string(msg.Origin),
			Text:              msg.Text,
			SenderSessionID:   msg.SenderSessionID,
			SenderProjectID:   msg.SenderProjectID,
			SenderDisplayName: msg.SenderDisplayName,
			ClientMessageID:   msg.ClientMessageID,
			Streaming:         msg.Streaming,
			CreatedAt:         msg.CreatedAt.UTC().Format(time.RFC3339),
			Continuation:      msg.Continuation,
			AuthoredByUser:    msg.AuthoredByUser,
		}
		message.Content, message.EditAvailable = conversationContentSummary(msg)
		message.EditAvailable = message.EditAvailable && msg.Sequence > s.EditFloorSequence
		out.Messages = append(out.Messages, message)
	}
	for _, point := range s.BranchPoints {
		out.BranchPoints = append(out.BranchPoints, ConversationBranchPointResponse{
			TurnID: point.TurnID, Position: point.Position, Total: point.Total,
			PreviousBranchID: point.PreviousBranchID, NextBranchID: point.NextBranchID,
		})
	}

	for _, activity := range s.Activities {
		out.Activities = append(out.Activities, ConversationActivityResponse{
			Kind:           "activity",
			ID:             activity.ID,
			TurnID:         activity.TurnID,
			Sequence:       activity.Sequence,
			Revision:       activity.Revision,
			ActivityKind:   string(activity.Kind),
			Status:         string(activity.Status),
			Summary:        activity.Summary,
			Detail:         activityDetailPayload(activity),
			RequestID:      activity.RequestID,
			ProviderItemID: activity.ProviderItemID,
			CreatedAt:      activity.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func usageLimitPayload(limit *domain.ConversationUsageLimit) *ConversationUsageLimitResponse {
	if limit == nil {
		return nil
	}
	return &ConversationUsageLimitResponse{ResetsAt: optionalTimestamp(limit.ResetsAt), AutoResume: limit.AutoResume, ResumeError: limit.ResumeError}
}

func branchMaterializationPayload(branch domain.ConversationBranch) *ConversationBranchMaterializationResponse {
	if branch.ID == "" || branch.Strategy == "" {
		return nil
	}
	return &ConversationBranchMaterializationResponse{
		Strategy:        string(branch.Strategy),
		ReplayTruncated: branch.ReplayTruncated,
	}
}

func conversationContentSummary(msg domain.ConversationMessage) ([]ConversationContentSummaryResponse, bool) {
	if msg.Role != domain.MessageRoleUser || msg.Origin != domain.MessageOriginHuman {
		return nil, false
	}
	if msg.DeliveryContentJSON == "" {
		return nil, true
	}
	var content []ports.ChatContent
	if err := json.Unmarshal([]byte(msg.DeliveryContentJSON), &content); err != nil {
		return nil, false
	}
	summaries := make([]ConversationContentSummaryResponse, 0, len(content))
	for _, block := range content {
		if block.Type == "text" || ports.IsInternalReplayContent(block) {
			continue
		}
		name := block.Name
		if name == "" && block.Type != "image" && block.Type != "resource" && block.Type != "resource_link" {
			name = block.Type
		}
		summary := ConversationContentSummaryResponse{Type: block.Type, MIMEType: block.MIMEType, Name: name}
		if block.Type == "file_reference" && block.FileReference != nil {
			ref := ConversationFileReferenceRequest(*block.FileReference)
			summary.FileReference = &ref
			summary.Name = ref.Path
			summary.AnnotationID = ref.ID
			summary.AnnotationOrder = ref.Order
		}
		if block.Type == "excerpt" {
			summary.Name = "Chat excerpt"
			if block.Excerpt != nil {
				summary.Snapshot = excerptSnapshotSummary(block.Excerpt.Reference.Snapshot)
				summary.AnnotationID = block.Excerpt.Reference.ID
				summary.AnnotationOrder = block.Excerpt.Reference.Order
				summary.Text = block.Excerpt.SelectedText
				summary.Comment = block.Excerpt.Reference.Comment
				summary.SourceConversationID = block.Excerpt.Reference.ConversationID
				summary.SourceMessageID = block.Excerpt.Reference.MessageID
				summary.SourceRevision = &block.Excerpt.Reference.Revision
			} else {
				summary.Text = block.Text
				if strings.HasPrefix(block.URI, ports.ChatExcerptResourceURIPrefix) {
					summary.SourceMessageID = strings.TrimPrefix(block.URI, ports.ChatExcerptResourceURIPrefix)
				}
			}
		} else {
			summary.URI = block.URI
		}
		summaries = append(summaries, summary)
	}
	return summaries, true
}

func usagePayload(usage *domain.ConversationUsage) *ConversationUsagePayload {
	if usage == nil {
		return nil
	}
	return &ConversationUsagePayload{
		ContextUsed:   usage.ContextUsed,
		ContextWindow: usage.ContextWindow,
		InputTokens:   usage.InputTokens,
		OutputTokens:  usage.OutputTokens,
		CachedTokens:  usage.CachedTokens,
		TotalTokens:   usage.TotalTokens,
		Cost:          usage.Cost,
		Currency:      usage.Currency,
	}
}

func rateLimitsPayload(limits *domain.ConversationRateLimits) *ConversationRateLimitsPayload {
	if limits == nil {
		return nil
	}
	return &ConversationRateLimitsPayload{
		PrimaryUsedPercent:       limits.PrimaryUsedPercent,
		SecondaryUsedPercent:     limits.SecondaryUsedPercent,
		PrimaryResetsInSeconds:   limits.PrimaryResetsInSeconds,
		SecondaryResetsInSeconds: limits.SecondaryResetsInSeconds,
		PlanLabel:                limits.PlanLabel,
	}
}

func turnDiffPayload(diff *domain.ConversationTurnDiff) *ConversationTurnDiffResponse {
	if diff == nil {
		return nil
	}
	out := &ConversationTurnDiffResponse{
		Truncated: diff.Truncated,
		Files:     make([]ConversationDiffFileResponse, 0, len(diff.Files)),
	}
	for _, file := range diff.Files {
		out.Files = append(out.Files, ConversationDiffFileResponse{
			Path:      file.Path,
			Additions: file.Additions,
			Deletions: file.Deletions,
			Status:    file.Status,
			OldPath:   file.OldPath,
		})
	}
	return out
}

func turnArtifactsPayload(artifacts *domain.ConversationTurnArtifacts) *ConversationTurnArtifactsResponse {
	if artifacts == nil || len(artifacts.Files) == 0 {
		return nil
	}
	out := &ConversationTurnArtifactsResponse{Files: make([]ConversationTurnArtifactResponse, 0, len(artifacts.Files))}
	for _, file := range artifacts.Files {
		out.Files = append(out.Files, ConversationTurnArtifactResponse{Path: file.Path, Status: file.Status})
	}
	return out
}

func turnPlanPayload(plan *domain.ConversationPlan) *ConversationPlanResponse {
	if plan == nil {
		return nil
	}
	out := &ConversationPlanResponse{
		Explanation: plan.Explanation,
		Steps:       make([]ConversationPlanStepResponse, 0, len(plan.Steps)),
		SpansTurns:  plan.SpansTurns,
		Superseded:  plan.Superseded,
	}
	for _, step := range plan.Steps {
		out.Steps = append(out.Steps, ConversationPlanStepResponse{
			Text:   step.Text,
			Status: string(step.Status),
		})
	}
	return out
}

func modelReroutePayload(reroute *domain.ConversationModelReroute) *ConversationModelReroutePayload {
	if reroute == nil {
		return nil
	}
	return &ConversationModelReroutePayload{
		FromModel:      reroute.FromModel,
		ToModel:        reroute.ToModel,
		Reason:         reroute.Reason,
		ProviderTurnID: reroute.ProviderTurnID,
		At:             reroute.At.UTC().Format(time.RFC3339),
	}
}

func accountPayload(account *domain.ConversationAccount) *ConversationAccountPayload {
	if account == nil {
		return nil
	}
	state := account.AuthenticationState
	if state == "" {
		state = "unknown"
		if account.ReauthRequiredAt != nil {
			state = "required"
		}
	}
	return &ConversationAccountPayload{
		AuthenticationState:   state,
		AuthVerifiedAt:        optionalTimestamp(account.AuthVerifiedAt),
		LastAuthFailureAt:     optionalTimestamp(account.LastAuthFailureAt),
		LastAuthFailureReason: account.LastAuthFailureReason,
		AuthFailureID:         account.AuthFailureID,
		AuthMode:              account.AuthMode,
		PlanLabel:             account.PlanLabel,
		ReauthRequiredAt:      optionalTimestamp(account.ReauthRequiredAt),
		ReauthReason:          account.ReauthReason,
	}
}

func threadStatePayload(state *domain.ConversationThreadState) *ConversationThreadStatePayload {
	if state == nil {
		return nil
	}
	return &ConversationThreadStatePayload{
		Status:     string(state.Status),
		WaitingOn:  state.WaitingOn,
		ArchivedAt: optionalTimestamp(state.ArchivedAt),
		ClosedAt:   optionalTimestamp(state.ClosedAt),
	}
}

func mcpServersPayload(servers []domain.ConversationMCPServer) []ConversationMCPServerPayload {
	if len(servers) == 0 {
		return nil
	}
	out := make([]ConversationMCPServerPayload, 0, len(servers))
	for _, server := range servers {
		out = append(out, ConversationMCPServerPayload{
			Name:          server.Name,
			Status:        server.Status,
			Error:         server.Error,
			FailureReason: server.FailureReason,
		})
	}
	return out
}

func activityDetailPayload(activity domain.ConversationActivity) map[string]any {
	detail := decodeDetail(activity.Detail)
	if activity.CommandOutput != "" {
		if detail == nil {
			detail = map[string]any{}
		}
		detail["output"] = activity.CommandOutput
		detail["outputSource"] = "stream"
		detail["outputMayBePartial"] = true
		if activity.CommandOutputTruncated {
			detail["outputTruncated"] = true
		} else {
			delete(detail, "outputTruncated")
		}
	}
	if activity.StreamedText != "" {
		if detail == nil {
			detail = map[string]any{}
		}
		key, truncatedKey := streamedTextKeys(activity.Kind)
		detail[key] = activity.StreamedText
		if activity.StreamedTextTruncated {
			detail[truncatedKey] = true
		}
	}
	return detail
}

func streamedTextKeys(kind domain.ActivityKind) (key, truncatedKey string) {
	switch kind {
	case domain.ActivityKindReasoning:
		return "text", "textTruncated"
	case domain.ActivityKindCommand:
		return "terminalInput", "terminalInputTruncated"
	case domain.ActivityKindMCPTool:
		return "progress", "progressTruncated"
	case domain.ActivityKindFileChange:
		return "patchOutput", "patchOutputTruncated"
	default:
		return "streamedText", "streamedTextTruncated"
	}
}

func decodeDetail(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var detail map[string]any
	if err := json.Unmarshal(raw, &detail); err != nil {
		return nil
	}
	return detail
}

func optionalTimestamp(t *time.Time) *string {
	if t == nil {
		return nil
	}
	formatted := t.UTC().Format(time.RFC3339)
	return &formatted
}

type sideChatConversationService interface {
	SideChatNumbers(string, map[domain.SessionID]int) map[domain.SessionID]int
	RenameSideChat(context.Context, domain.SessionID, string, string) error
	CancelQueuedSideQuestion(context.Context, domain.SessionID, string, string) error
	ListIndependentSideChats(context.Context, domain.SessionID) ([]domain.SideConversation, error)
	CreateIndependentSideChat(context.Context, domain.SessionID, chatsvc.SideCreateRequest) (domain.SideConversation, error)
	SideSnapshot(context.Context, domain.SessionID, string, time.Time, int) (domain.SideSnapshot, error)
	SendSideQuestion(context.Context, domain.SessionID, string, ports.ChatUserMessage) (domain.SideTurn, error)
	EditQueuedSideQuestion(context.Context, domain.SessionID, string, string, string) error
	RetrySideQuestion(context.Context, domain.SessionID, string, string) error
	RetrySideConnection(context.Context, domain.SessionID, string) error
	UpdateSideSettings(context.Context, domain.SessionID, string, string, string) error
	SaveSideDraft(context.Context, domain.SessionID, string, string) error
	SideDraft(context.Context, domain.SessionID, string) (string, error)
	InterruptSideQuestion(context.Context, domain.SessionID, string) error
	CompactSideChat(context.Context, domain.SessionID, string) (ports.ChatCompactionResult, error)
	ResolveSideApproval(context.Context, domain.SessionID, string, string, string) error
	ResolveSideInput(context.Context, domain.SessionID, string, string, ports.ChatInputResponse) error
	CloseIndependentSideChat(context.Context, domain.SessionID, string) error
	ClaimSideChatLaunch(context.Context, string) error
	ExportSideChatLaunch(context.Context, string) ([]chatsvc.SideRecoveryRecord, error)
	RecoverSideChatLaunch(context.Context, string, []chatsvc.SideRecoveryRecord) error
	RetireSideChatLaunch(context.Context, string) error
	WatchSideChat(context.Context, domain.SessionID, string) (string, <-chan struct{}, func(), error)
}

func (c *ConversationsController) createSideChat(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/conversation/side-chats")
		return
	}
	var req CreateConversationSideChatRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	svc, ok := c.sideService(w, r)
	if !ok {
		return
	}

	var ref *ports.ChatExcerptReference
	if req.Reference != nil {
		ref = &ports.ChatExcerptReference{ConversationID: req.Reference.ConversationID,
			MessageID: req.Reference.MessageID, Revision: req.Reference.Revision, Text: req.Reference.Text}
	}
	side, err := svc.CreateIndependentSideChat(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")),
		chatsvc.SideCreateRequest{IdempotencyKey: req.IdempotencyKey, ForceNew: req.ForceNew, Label: req.Label, Reference: ref})
	if err != nil {
		writeSideChatError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, CreateConversationSideChatResponse{Side: side})
}
