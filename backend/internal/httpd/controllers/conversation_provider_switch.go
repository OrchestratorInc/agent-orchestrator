package controllers

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (c *ConversationsController) switchProvider(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	var req ProviderSwitchRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	var message ports.ChatUserMessage
	if req.Message != nil {
		var invalid *attachmentError
		if message, invalid = chatUserMessage(*req.Message, chi.URLParam(r, "sessionId")); invalid != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", invalid.code, invalid.message, nil)
			return
		}
	}
	sessionID := domain.SessionID(chi.URLParam(r, "sessionId"))
	ctx, cancel := withoutClientCancel(r.Context())
	defer cancel()
	request := domain.ProviderSwitchRequest{
		TargetHarness: domain.AgentHarness(req.TargetHarness), IdempotencyKey: req.IdempotencyKey,
		Model:    capActivityMeta(strings.TrimSpace(domain.SanitizeControlChars(req.Model))),
		Effort:   capActivityMeta(strings.TrimSpace(domain.SanitizeControlChars(req.Effort))),
		FastMode: req.FastMode, Ultracode: req.Ultracode, AutoCompactWindow: req.AutoCompactWindow,
		PermissionMode: domain.PermissionMode(req.PermissionMode),
	}
	if req.Message == nil {
		if err := c.Svc.SwitchProvider(ctx, sessionID, request); err != nil {
			writeProviderSwitchError(w, r, err)
			return
		}
		envelope.WriteJSON(w, http.StatusOK, ProviderSwitchResponse{Status: "ok"})
		return
	}
	turn, switched, err := c.Svc.SwitchProviderAndSend(ctx, sessionID, request, message)
	if err != nil {
		if switched {
			w.Header().Set(providerSwitchedHeader, "1")
			w.Header().Set("Access-Control-Expose-Headers", providerSwitchedHeader)
			writeConversationError(w, r, err)
			return
		}
		writeProviderSwitchError(w, r, err)
		return
	}
	response := sendConversationMessageResponse(turn)
	envelope.WriteJSON(w, http.StatusOK, ProviderSwitchResponse{Status: "ok", Message: &response})
}

func writeProviderSwitchError(w http.ResponseWriter, r *http.Request, err error) {
	var refusal *domain.ProviderSwitchError
	if !errors.As(err, &refusal) {
		writeConversationError(w, r, err)
		return
	}
	status, category := http.StatusConflict, "conflict"
	switch refusal.Kind {
	case domain.ProviderSwitchRefusalUnsupported:
		status, category = http.StatusBadRequest, "validation"
	case domain.ProviderSwitchRefusalUnavailable:
		status, category = http.StatusServiceUnavailable, "unavailable"
	}
	envelope.WriteAPIError(w, r, status, category, "PROVIDER_SWITCH_"+string(refusal.Kind), refusal.Error(), providerSwitchRefusalDetails(refusal))
}

func providerSwitchRefusalDetails(refusal *domain.ProviderSwitchError) map[string]any {
	var capabilityErr *ports.ChatCapabilityError
	if !errors.As(refusal, &capabilityErr) {
		return nil
	}
	missing := make([]string, 0, len(capabilityErr.Missing))
	for _, capability := range capabilityErr.Missing {
		missing = append(missing, string(capability))
	}
	allowed := make([]string, 0, len(capabilityErr.AllowedPermissionModes))
	for _, mode := range capabilityErr.AllowedPermissionModes {
		allowed = append(allowed, string(mode))
	}
	return map[string]any{"missingCapabilities": missing, "allowedApprovalModes": allowed}
}

const providerSwitchedHeader = "X-AO-Provider-Switched"

func (c *ConversationsController) sessionHistory(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	query := r.URL.Query()
	offset, offsetErr := strconv.Atoi(cmp.Or(query.Get("offset"), "0"))
	limit, limitErr := strconv.Atoi(cmp.Or(query.Get("limit"), "10"))
	if offsetErr != nil || limitErr != nil || offset < 0 || limit < 1 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "INVALID_HISTORY_PAGE", "offset must be 0 or more and limit 1 or more", nil)
		return
	}
	turns, total, err := c.Svc.SessionHistory(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")), offset, min(limit, 50))
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionHistoryResponse{Turns: turns, Total: total, Offset: offset})
}

func withoutClientCancel(parent context.Context) (context.Context, context.CancelFunc) {
	detached := context.WithoutCancel(parent)
	if deadline, ok := parent.Deadline(); ok {
		return context.WithDeadline(detached, deadline)
	}
	return context.WithCancel(detached)
}
