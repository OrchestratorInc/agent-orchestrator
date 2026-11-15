package controllers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/renderpage"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

const (
	publishRenderPath                   = "/api/v1/sessions/{sessionId}/renders"
	checkRenderPath                     = "/api/v1/sessions/{sessionId}/renders/check"
	renderFilePath                      = "/api/v1/sessions/{sessionId}/renders/{renderId}"
	renderArtifactPath                  = "/api/v1/sessions/{sessionId}/renders/{renderId}/artifact"
	renderContentSecurityPolicy         = "sandbox allow-scripts allow-forms; worker-src 'none'"
	inlineArtifactContentSecurityPolicy = "sandbox allow-scripts allow-forms allow-same-origin; worker-src 'none'"
	offlineRenderContentSecurityPolicy  = renderContentSecurityPolicy + "; default-src data: blob: 'unsafe-inline' 'unsafe-eval'; form-action 'none'"
	renderNeedsChatMessage              = "ao render works only in chat sessions; in a terminal session, open the file with ao preview <file>"
)

var (
	_ renderPublisher     = (*chatsvc.Service)(nil)
	_ renderChecker       = (*chatsvc.Service)(nil)
	_ renderArtifactSaver = (*chatsvc.Service)(nil)
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
		HTML: req.HTML, Title: req.Title, Height: req.Height, BaseURL: "http://" + r.Host, Artifact: req.Artifact,
	})
	switch {
	case err == nil:
		envelope.WriteJSON(w, http.StatusCreated, PublishRenderResponse{
			RenderID: result.RenderID, ActivityID: result.ActivityID, Path: result.Path,
			ArtifactPath: result.ArtifactPath, ArtifactError: result.ArtifactError,
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

type renderArtifactSaver interface {
	SaveRenderAsArtifact(ctx context.Context, id domain.SessionID, renderID, title string) (chatsvc.RenderArtifact, error)
}

func (c *ConversationsController) saveRenderArtifact(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(renderArtifactSaver)
	if !ok {
		apispec.NotImplemented(w, r, "POST", renderArtifactPath)
		return
	}
	var req SaveRenderArtifactRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	result, err := svc.SaveRenderAsArtifact(r.Context(), sessionID(r), chi.URLParam(r, "renderId"), req.Title)
	switch {
	case err == nil:
		envelope.WriteJSON(w, http.StatusCreated, SaveRenderArtifactResponse{Path: result.Path, Name: result.Name})
	case errors.Is(err, chatsvc.ErrRenderNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "RENDER_NOT_FOUND", "render not found", nil)
	case errors.Is(err, chatsvc.ErrRenderInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "RENDER_INVALID", err.Error(), nil)
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
			Screenshot: RenderCheckScreenshot{
				MimeType: "image/png", Data: result.PNG, Width: result.Width, Height: result.Height,
				ImageWidth: result.ImageWidth, ImageHeight: result.ImageHeight,
			},
			ContentHeight:   result.ContentHeight,
			ConsoleMessages: messages,
			Network:         result.Network,
		})
	case errors.Is(err, chatsvc.ErrRenderInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "RENDER_INVALID", err.Error(), nil)
	case errors.Is(err, chatsvc.ErrRenderCheckUnavailable):
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "RENDER_CHECK_UNAVAILABLE",
			"render check needs the Orchestrator desktop app; open it, or publish without a check", nil)
	case errors.As(err, &desktopErr):
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
	renderID := chi.URLParam(r, "renderId")
	csp := renderContentSecurityPolicy
	if chatsvc.RenderOffline(renderID) {
		csp = offlineRenderContentSecurityPolicy
	}
	if renderTagID.MatchString(renderID) && notModified(w, r, csp, pageTag(r, renderpage.Version+"-"+renderID)) {
		return
	}
	file, _, err := c.Renders.OpenRender(r.Context(), sessionID(r), renderID)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "RENDER_NOT_FOUND", "render not found", nil)
		return
	}
	stored, err := io.ReadAll(io.LimitReader(file, attachmentstore.MaxFileBytes))
	_ = file.Close()
	if err != nil {
		envelope.WriteError(w, r, fmt.Errorf("read render: %w", err))
		return
	}
	serveSandboxedPage(w, r, csp, stored, renderpage.Version+"-"+renderID)
}

var renderTagID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

func pageTag(r *http.Request, tag string) string {
	if r.URL.Query().Get("source") == "1" {
		return "src-" + tag
	}
	return tag
}

func serveSandboxedPage(w http.ResponseWriter, r *http.Request, csp string, page []byte, tag string) {
	if r.URL.Query().Get("source") == "1" {
		serveSandboxed(w, r, csp, page, "text/plain; charset=utf-8", pageTag(r, tag))
		return
	}
	serveSandboxed(w, r, csp, renderpage.Document(page), "text/html; charset=utf-8", tag)
}

func setSandboxHeaders(h http.Header, csp, etag string) {
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", `"`+etag+`"`)
}

func notModified(w http.ResponseWriter, r *http.Request, csp, etag string) bool {
	quoted := `"` + etag + `"`
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/"); candidate == quoted || candidate == "*" {
			setSandboxHeaders(w.Header(), csp, etag)
			w.WriteHeader(http.StatusNotModified)
			return true
		}
	}
	return false
}

func serveSandboxed(w http.ResponseWriter, r *http.Request, csp string, body []byte, contentType, etag string) {
	setSandboxHeaders(w.Header(), csp, etag)
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}
