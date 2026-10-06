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
