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
	publishRenderPath  = "/api/v1/sessions/{sessionId}/renders"
	checkRenderPath    = "/api/v1/sessions/{sessionId}/renders/check"
	renderFilePath     = "/api/v1/sessions/{sessionId}/renders/{renderId}"
	renderArtifactPath = "/api/v1/sessions/{sessionId}/renders/{renderId}/artifact"
	// Scripts run, but the opaque origin keeps the page out of the app's
	// session and storage, and corsMiddleware refuses Origin: null, so even a
	// render opened top-level cannot call the daemon. No popups, no modals,
	// no top navigation.
	// No workers: a worker's requests carry no frame, so the desktop app's
	// guard that keeps agent pages off the local network could not see them.
	renderContentSecurityPolicy = "sandbox allow-scripts allow-forms; worker-src 'none'"
	// An HTML artifact framed inline keeps a real origin of its own, the
	// ao-inline-artifact.<session>.localhost host, so its module scripts, fetch
	// of sibling files and fonts load same-origin. That origin is not the app's,
	// and corsMiddleware refuses it everywhere but its own host, so the page
	// still cannot call the daemon.
	inlineArtifactContentSecurityPolicy = "sandbox allow-scripts allow-forms allow-same-origin; worker-src 'none'"
	// A page made by an agent whose sandbox has no network loads nothing over
	// the network either: only what it carries inline.
	// ponytail: CSP does not cover WebRTC or a dns-prefetch hint; the reader's
	// frames would need a proxy, as the check window has, to close those too.
	offlineRenderContentSecurityPolicy = renderContentSecurityPolicy + "; default-src data: blob: 'unsafe-inline' 'unsafe-eval'; form-action 'none'"
	// A terminal session has no thread to show a page in; point the agent at
	// the command that does work there.
	renderNeedsChatMessage = "ao render works only in chat sessions; in a terminal session, open the file with ao preview <file>"
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
	// The desktop app measures the page from the origin the CLI reached, as
	// a render check loads it.
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
	renderID := chi.URLParam(r, "renderId")
	csp := renderContentSecurityPolicy
	if chatsvc.RenderOffline(renderID) {
		csp = offlineRenderContentSecurityPolicy
	}
	// A stored render never changes, so its id and the bootstrap version name
	// the served page, and a revalidation is answered without reading it. An
	// id unfit for a header is left to the store, which refuses it.
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

// renderTagID is a render id fit to put in an ETag.
var renderTagID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

// pageTag is the ETag of the response serveSandboxedPage sends for tag:
// ?source=1 is a different body, so it has a tag the page's can never match.
func pageTag(r *http.Request, tag string) string {
	if r.URL.Query().Get("source") == "1" {
		return "src-" + tag
	}
	return tag
}

// serveSandboxedPage serves an agent's HTML page with the theme bootstrap, or
// with ?source=1 the page as the agent wrote it, for reading: plain text,
// without the bootstrap. tag names the stored page and the bootstrap version,
// since the bootstrap is added as the page is served.
func serveSandboxedPage(w http.ResponseWriter, r *http.Request, csp string, page []byte, tag string) {
	if r.URL.Query().Get("source") == "1" {
		serveSandboxed(w, r, csp, page, "text/plain; charset=utf-8", pageTag(r, tag))
		return
	}
	serveSandboxed(w, r, csp, renderpage.Document(page), "text/html; charset=utf-8", tag)
}

// setSandboxHeaders sets what every sandboxed response carries, 304s included:
// the sandbox CSP, no sniffing, no referrer, revalidation on every load, and
// the ETag.
func setSandboxHeaders(h http.Header, csp, etag string) {
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", `"`+etag+`"`)
}

// notModified answers 304 when the request already holds etag, so a frame
// that revalidates on every load costs no file read.
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

// serveSandboxed serves body in the sandbox csp names, with setSandboxHeaders.
func serveSandboxed(w http.ResponseWriter, r *http.Request, csp string, body []byte, contentType, etag string) {
	setSandboxHeaders(w.Header(), csp, etag)
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}
