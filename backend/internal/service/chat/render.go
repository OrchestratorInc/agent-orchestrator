package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	maxRenderHTMLBytes  = 1 << 20
	maxRenderTitleRunes = 200
	minRenderHeight     = 80
	maxRenderHeight     = 2000

	defaultRenderCheckWidth = 720
	minRenderCheckWidth     = 240
	maxRenderCheckWidth     = 1600
)

// ErrRenderInvalid reports a page the agent must fix before publishing. The
// wrapped message says what to change.
var ErrRenderInvalid = errors.New("invalid render")

// RenderFiles stores render pages; *attachmentstore.Store satisfies it.
type RenderFiles interface {
	PutRender(ctx context.Context, id domain.SessionID, renderID string, data []byte) error
	RemoveRender(ctx context.Context, id domain.SessionID, renderID string) error
}

// RenderInput is a self-contained HTML page an agent shows in its thread.
type RenderInput struct {
	HTML   string
	Title  string
	Height int
}

// RenderResult names the stored page and the timeline row that shows it.
type RenderResult struct {
	RenderID   string
	ActivityID string
	Path       string
}

// PublishRender stores an agent's HTML page and shows it in the turn the agent
// is running, above its final reply.
func (s *Service) PublishRender(ctx context.Context, id domain.SessionID, in RenderInput) (RenderResult, error) {
	title := strings.TrimSpace(in.Title)
	if runes := []rune(title); len(runes) > maxRenderTitleRunes {
		title = string(runes[:maxRenderTitleRunes])
	}
	switch {
	case strings.TrimSpace(in.HTML) == "":
		return RenderResult{}, fmt.Errorf("%w: the page is empty", ErrRenderInvalid)
	case len(in.HTML) > maxRenderHTMLBytes:
		return RenderResult{}, fmt.Errorf("%w: the page is %d bytes; the limit is %d", ErrRenderInvalid, len(in.HTML), maxRenderHTMLBytes)
	case title == "":
		return RenderResult{}, fmt.Errorf("%w: a title is required", ErrRenderInvalid)
	case s.renders == nil:
		return RenderResult{}, errors.New("render storage is not configured")
	}
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return RenderResult{}, err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return RenderResult{}, err
	}
	renderID := s.newID()
	if err := s.renders.PutRender(ctx, id, renderID, []byte(injectRenderBootstrap(in.HTML))); err != nil {
		return RenderResult{}, fmt.Errorf("store render: %w", err)
	}
	path := "/api/v1/sessions/" + url.PathEscape(string(id)) + "/renders/" + url.PathEscape(renderID)
	height := min(max(in.Height, minRenderHeight), maxRenderHeight)
	activityID, err := controller.recordRender(ctx, renderID, title, height, path)
	if err != nil {
		// Only the timeline row lets anything find the page, so an unrecorded page goes.
		if removeErr := s.renders.RemoveRender(context.WithoutCancel(ctx), id, renderID); removeErr != nil {
			s.log.Warn("render cleanup failed", "session", id, "render", renderID, "error", removeErr)
		}
		return RenderResult{}, err
	}
	return RenderResult{RenderID: renderID, ActivityID: activityID, Path: path}, nil
}

// recordRender attaches a published page to the turn in flight, as a system
// activity identified by its "render" discriminator, the way a steer is.
func (c *Controller) recordRender(ctx context.Context, renderID, title string, height int, path string) (string, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	providerTurnID, ok := c.awaitAcknowledgedTurn(ctx)
	if !ok {
		return "", ErrNoActiveTurn
	}
	detail, err := json.Marshal(map[string]any{
		"event": "render",
		"render": map[string]any{
			"id": renderID, "title": title, "height": height, "path": path,
		},
	})
	if err != nil {
		return "", fmt.Errorf("encode render detail: %w", err)
	}
	activityID := c.newID()
	if err := c.store.UpsertActivity(ctx, c.conversation.ID, providerTurnID, domain.ConversationActivity{
		ID:             activityID,
		Kind:           domain.ActivityKindSystem,
		Status:         domain.ActivityStatusCompleted,
		Summary:        title,
		Detail:         detail,
		ProviderItemID: "render:" + renderID,
	}, c.now()); err != nil {
		return "", fmt.Errorf("record render on turn %s: %w", providerTurnID, err)
	}
	return activityID, nil
}

// ErrRenderCheckUnavailable reports that no desktop app is connected to load the page.
var ErrRenderCheckUnavailable = errors.New("render check needs the AO desktop app")

// RenderCheck asks the desktop app to load a render URL in a hidden view. The
// daemon wires it to the browser-runtime broker.
type RenderCheck func(ctx context.Context, id domain.SessionID, args map[string]any) (any, error)

// RenderCheckInput is a page an agent wants to see before it publishes it.
// BaseURL is the daemon origin the desktop app can load, e.g. http://127.0.0.1:3001.
type RenderCheckInput struct {
	HTML    string
	Width   int
	BaseURL string
}

// RenderConsoleMessage is one console line the page wrote while it loaded.
type RenderConsoleMessage struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// RenderCheckResult is what the page looked like at the requested width.
type RenderCheckResult struct {
	PNG             string                 `json:"data"`
	Width           int                    `json:"width"`
	Height          int                    `json:"height"`
	ContentHeight   int                    `json:"contentHeight"`
	ConsoleMessages []RenderConsoleMessage `json:"consoleMessages"`
}

// SetRenderCheck installs the desktop-app page loader after daemon wiring.
func (s *Service) SetRenderCheck(check RenderCheck) {
	s.renderCheck = check
}

// CheckRender shows the agent its page as readers will see it: the same
// stored, bootstrapped document, loaded by the desktop app in a hidden view.
func (s *Service) CheckRender(ctx context.Context, id domain.SessionID, in RenderCheckInput) (RenderCheckResult, error) {
	width := in.Width
	if width == 0 {
		width = defaultRenderCheckWidth
	}
	switch {
	case strings.TrimSpace(in.HTML) == "":
		return RenderCheckResult{}, fmt.Errorf("%w: the page is empty", ErrRenderInvalid)
	case len(in.HTML) > maxRenderHTMLBytes:
		return RenderCheckResult{}, fmt.Errorf("%w: the page is %d bytes; the limit is %d", ErrRenderInvalid, len(in.HTML), maxRenderHTMLBytes)
	case width < minRenderCheckWidth || width > maxRenderCheckWidth:
		return RenderCheckResult{}, fmt.Errorf("%w: width must be %d-%d", ErrRenderInvalid, minRenderCheckWidth, maxRenderCheckWidth)
	case s.renders == nil || s.renderCheck == nil:
		return RenderCheckResult{}, ErrRenderCheckUnavailable
	}
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return RenderCheckResult{}, err
	}
	renderID := "check-" + s.newID()
	if err := s.renders.PutRender(ctx, id, renderID, []byte(injectRenderBootstrap(in.HTML))); err != nil {
		return RenderCheckResult{}, fmt.Errorf("store render check: %w", err)
	}
	defer func() {
		if err := s.renders.RemoveRender(context.WithoutCancel(ctx), id, renderID); err != nil {
			s.log.Warn("render check cleanup failed", "session", id, "render", renderID, "error", err)
		}
	}()
	pageURL := strings.TrimRight(in.BaseURL, "/") +
		"/api/v1/sessions/" + url.PathEscape(string(id)) + "/renders/" + url.PathEscape(renderID)
	value, err := s.renderCheck(ctx, id, map[string]any{"url": pageURL, "width": width})
	if err != nil {
		return RenderCheckResult{}, err
	}
	// The broker hands back decoded JSON, so numbers arrive as float64; a round
	// trip through the typed result turns them into ints.
	encoded, err := json.Marshal(value)
	if err != nil {
		return RenderCheckResult{}, fmt.Errorf("encode render check result: %w", err)
	}
	var result RenderCheckResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		return RenderCheckResult{}, fmt.Errorf("desktop app returned an unreadable render check: %w", err)
	}
	if result.PNG == "" {
		return RenderCheckResult{}, errors.New("desktop app returned a render check with no screenshot")
	}
	return result, nil
}
