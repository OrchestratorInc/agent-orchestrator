package controllers

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

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
	// A message sent with the switch is checked before anything stops, so a
	// malformed one refuses the switch instead of following it.
	var message ports.ChatUserMessage
	if req.Message != nil {
		var invalid *attachmentError
		if message, invalid = chatUserMessage(*req.Message, chi.URLParam(r, "sessionId")); invalid != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", invalid.code, invalid.message, nil)
			return
		}
	}
	sessionID := domain.SessionID(chi.URLParam(r, "sessionId"))
	// The switch and its message finish within the request's time limit even if
	// the client goes away (a restarted app); its retry of the message is a duplicate.
	ctx, cancel := withoutClientCancel(r.Context())
	defer cancel()
	request := domain.ProviderSwitchRequest{
		TargetHarness: domain.AgentHarness(req.TargetHarness), IdempotencyKey: req.IdempotencyKey,
		Model:    capActivityMeta(strings.TrimSpace(domain.SanitizeControlChars(req.Model))),
		Effort:   capActivityMeta(strings.TrimSpace(domain.SanitizeControlChars(req.Effort))),
		FastMode: req.FastMode, Ultracode: req.Ultracode, AutoCompactWindow: req.AutoCompactWindow,
		PermissionMode: domain.PermissionMode(req.PermissionMode), Force: req.Force,
	}
	warnings, err := c.checkSwitchPicks(ctx, request)
	if err != nil {
		writeProviderSwitchError(w, r, err)
		return
	}
	if req.Message == nil {
		result, err := c.Svc.SwitchProviderReport(ctx, sessionID, request)
		if err != nil {
			writeProviderSwitchError(w, r, err)
			return
		}
		envelope.WriteJSON(w, http.StatusOK, ProviderSwitchResponse{
			Status: "ok", StoppedTurn: result.StoppedTurn, CancelledQueuedMessages: result.CancelledQueued,
			FailedPendingApprovals: result.FailedApprovals, SettingsUpdated: result.SettingsUpdated,
			Warnings: append(warnings, result.Warnings...),
		})
		return
	}
	turn, switched, err := c.Svc.SwitchProviderAndSend(ctx, sessionID, request, message)
	if err != nil {
		if switched {
			// A send that fails now is not a switch failure: the header tells the
			// client the switch went through.
			w.Header().Set(providerSwitchedHeader, "1")
			// The renderer calls the daemon cross-origin, so it reads the header only if exposed.
			w.Header().Set("Access-Control-Expose-Headers", providerSwitchedHeader)
			writeConversationError(w, r, err)
			return
		}
		writeProviderSwitchError(w, r, err)
		return
	}
	response := sendConversationMessageResponse(turn)
	envelope.WriteJSON(w, http.StatusOK, ProviderSwitchResponse{Status: "ok", Message: &response, Warnings: warnings})
}

func (c *ConversationsController) checkSwitchPicks(ctx context.Context, request domain.ProviderSwitchRequest) ([]string, error) {
	if c.Models == nil || (request.Model == "" && request.Effort == "") {
		return nil, nil
	}
	lookup, cancel := context.WithTimeout(ctx, switchCatalogLookupLimit)
	defer cancel()
	catalog, err := c.Models.ModelCatalog(lookup, string(request.TargetHarness), "", false)
	if err != nil || len(catalog.Models) == 0 {
		return nil, nil //nolint:nilerr // An unreadable catalog must not block a switch.
	}
	target := request.TargetHarness
	var warnings []string
	var picked *ports.AgentModelInfo
	if request.Model != "" {
		base, _, _ := strings.Cut(strings.ToLower(request.Model), "[")
		alias := false
		for i := range catalog.Models {
			id := strings.ToLower(catalog.Models[i].ID)
			if id == strings.ToLower(request.Model) || id == base {
				picked = &catalog.Models[i]
				break
			}
			alias = alias || strings.Contains(id, base)
		}
		switch {
		case picked != nil || alias:
		case catalog.CustomModelEntry != ports.CustomModelEntryDirect:
			return nil, &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnsupported,
				Detail: fmt.Sprintf("%s is not one of %s's models: %s", request.Model, target, strings.Join(catalogModelIDs(catalog), ", "))}
		default:
			warnings = append(warnings, fmt.Sprintf("%s is not in %s's model list; the agent may refuse it", request.Model, target))
		}
	}
	if request.Effort == "" {
		return warnings, nil
	}
	allowed := catalogEfforts(catalog)
	if picked != nil {
		if len(picked.Efforts) == 0 {
			return nil, &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnsupported,
				Detail: fmt.Sprintf("%s takes no reasoning effort", picked.ID)}
		}
		allowed = picked.Efforts
	}
	for _, effort := range allowed {
		if strings.EqualFold(effort, request.Effort) {
			return warnings, nil
		}
	}
	return nil, &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnsupported,
		Detail: fmt.Sprintf("effort %q is not one of %s", request.Effort, strings.Join(allowed, ", "))}
}

func catalogModelIDs(catalog ports.AgentModelCatalog) []string {
	ids := make([]string, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		ids = append(ids, model.ID)
	}
	return ids
}

func catalogEfforts(catalog ports.AgentModelCatalog) []string {
	var efforts []string
	for _, model := range catalog.Models {
		for _, effort := range model.Efforts {
			if !slices.Contains(efforts, effort) {
				efforts = append(efforts, effort)
			}
		}
	}
	return efforts
}

var switchCatalogLookupLimit = 3 * time.Second

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

// providerSwitchRefusalDetails names, as a new task's refusal does, what an agent
// refused for the chat's permission mode lacks and the modes it runs in (Pi).
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

// providerSwitchedHeader marks an error response to a switch that carried a
// message: the switch completed, and only the message's send failed.
const providerSwitchedHeader = "X-AO-Provider-Switched"

// sessionHistory serves `ao session history`: a session's turns in full, newest
// first, for what a handoff note left out.
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

// withoutClientCancel keeps the request's deadline but not its cancellation
// when the client disconnects.
func withoutClientCancel(parent context.Context) (context.Context, context.CancelFunc) {
	detached := context.WithoutCancel(parent)
	if deadline, ok := parent.Deadline(); ok {
		return context.WithDeadline(detached, deadline)
	}
	return context.WithCancel(detached)
}
