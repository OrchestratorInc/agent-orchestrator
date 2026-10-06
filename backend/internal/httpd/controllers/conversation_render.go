package controllers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

const (
	publishRenderPath = "/api/v1/sessions/{sessionId}/renders"
	checkRenderPath   = "/api/v1/sessions/{sessionId}/renders/check"
	renderFilePath    = "/api/v1/sessions/{sessionId}/renders/{renderId}"
	// Scripts run, but the opaque origin keeps the page out of the app's
	// session and storage, and corsMiddleware refuses Origin: null, so even a
	// render opened top-level cannot call the daemon. No popups, no modals,
	// no top navigation.
	renderContentSecurityPolicy = "sandbox allow-scripts allow-forms"
	// A terminal session has no thread to show a page in; point the agent at
	// the command that does work there.
	renderNeedsChatMessage = "ao render works only in chat sessions; in a terminal session, open the file with ao preview <file>"
)

var (
	_ renderPublisher = (*chatsvc.Service)(nil)
	_ renderChecker   = (*chatsvc.Service)(nil)
)

type renderPublisher interface {
	PublishRender(context.Context, domain.SessionID, chatsvc.RenderInput) (chatsvc.RenderResult, error)
}

func (c *ConversationsController) publishRender(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(renderPublisher)
	if !ok {
		apispec.NotImplemented(w, r, "POST", publishRenderPath)
		return
	}
	var req PublishRenderRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	result, err := svc.PublishRender(r.Context(), sessionID(r), chatsvc.RenderInput{
		HTML: req.HTML, Title: req.Title, Height: req.Height,
	})
	switch {
	case err == nil:
		envelope.WriteJSON(w, http.StatusCreated, PublishRenderResponse{
			RenderID: result.RenderID, ActivityID: result.ActivityID, Path: result.Path,
		})
	case errors.Is(err, chatsvc.ErrRenderInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "RENDER_INVALID", err.Error(), nil)
	case errors.Is(err, chatsvc.ErrNoActiveTurn):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "RENDER_NO_ACTIVE_TURN",
			"a render is shown in the turn the agent is running, and no turn is in flight", nil)
	case errors.Is(err, chatsvc.ErrNotChatMode):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SESSION_MODE_MISMATCH", renderNeedsChatMessage, nil)
	default:
		writeConversationError(w, r, err)
	}
}

type renderChecker interface {
	CheckRender(context.Context, domain.SessionID, chatsvc.RenderCheckInput) (chatsvc.RenderCheckResult, error)
}

func (c *ConversationsController) checkRender(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(renderChecker)
	if !ok {
		apispec.NotImplemented(w, r, "POST", checkRenderPath)
		return
	}
	var req RenderCheckRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	// The desktop app loads the page from the origin the CLI reached: the
	// loopback listener (preview hosts never reach this route).
	result, err := svc.CheckRender(r.Context(), sessionID(r), chatsvc.RenderCheckInput{
		HTML: req.HTML, Width: req.Width, BaseURL: "http://" + r.Host,
	})
	var desktopErr browserruntime.CommandError
	switch {
	case err == nil:
		messages := make([]RenderConsoleMessage, 0, len(result.ConsoleMessages))
		for _, m := range result.ConsoleMessages {
			messages = append(messages, RenderConsoleMessage{Level: m.Level, Text: m.Text})
		}
		envelope.WriteJSON(w, http.StatusOK, RenderCheckResponse{
			Screenshot:      RenderCheckScreenshot{MimeType: "image/png", Data: result.PNG, Width: result.Width, Height: result.Height},
			ContentHeight:   result.ContentHeight,
			ConsoleMessages: messages,
		})
	case errors.Is(err, chatsvc.ErrRenderInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "RENDER_INVALID", err.Error(), nil)
	case errors.Is(err, chatsvc.ErrRenderCheckUnavailable):
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "RENDER_CHECK_UNAVAILABLE",
			"render check needs the AO desktop app; open it, or publish without a check", nil)
	case errors.As(err, &desktopErr):
		// The desktop app reached the page but could not check it (it did not
		// load in time, say). The agent needs the reason, not an opaque 500.
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "RENDER_CHECK_FAILED",
			desktopErr.Message, map[string]any{"desktopCode": desktopErr.Code})
	case errors.Is(err, chatsvc.ErrNotChatMode):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SESSION_MODE_MISMATCH", renderNeedsChatMessage, nil)
	default:
		writeConversationError(w, r, err)
	}
}

func (c *ConversationsController) renderFile(w http.ResponseWriter, r *http.Request) {
	if c.Renders == nil {
		apispec.NotImplemented(w, r, "GET", renderFilePath)
		return
	}
	file, info, err := c.Renders.OpenRender(r.Context(), sessionID(r), chi.URLParam(r, "renderId"))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "RENDER_NOT_FOUND", "render not found", nil)
		return
	}
	defer func() { _ = file.Close() }()
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", renderContentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	// A render never changes after publish; its id is its version.
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "", info.ModTime(), file)
}
