package chat

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
	reportsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/report"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

var _ reportsvc.ArtifactRecorder = (*Service)(nil)

func (s *Service) RecordReportedArtifact(ctx context.Context, id domain.SessionID, reference string) {
	if err := s.recordReportedArtifact(ctx, id, reference); err != nil {
		s.log.Debug("reported artifact not shown in the thread", "session", id, "reference", reference, "error", err)
	}
}

func (s *Service) recordReportedArtifact(ctx context.Context, id domain.SessionID, reference string) error {
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return err
	}
	dir := cmp.Or(record.Metadata.ArtifactDir, sessionartifacts.Dir(s.dataDir, id))
	rel, ok := sessionartifacts.RelPath(dir, reference)
	if !ok {
		return errors.New("not in the session's artifact directory")
	}
	if ext := strings.ToLower(path.Ext(rel)); ext != ".html" && ext != ".htm" {
		return errors.New("not an HTML page")
	}
	entry, ok := previewutil.EntryAtPath(dir, rel)
	if !ok {
		return errors.New("not a regular file the artifact file route serves")
	}
	if entry.Size > attachmentstore.MaxFileBytes {
		return fmt.Errorf("%d bytes; the artifact file route serves up to %d", entry.Size, attachmentstore.MaxFileBytes)
	}
	controller, err := s.Controller(id)
	if err != nil {
		return err
	}
	return controller.recordArtifact(ctx, entry.Path)
}

func (c *Controller) recordArtifact(ctx context.Context, rel string) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	providerTurnID, ok := c.awaitAcknowledgedTurn(ctx)
	if !ok {
		return ErrNoActiveTurn
	}
	if c.artifactShown(providerTurnID, rel) {
		return nil
	}
	name := path.Base(rel)
	fileURL := "/api/v1/sessions/" + url.PathEscape(string(c.sessionID)) + "/artifact-files/" + (&url.URL{Path: rel}).EscapedPath()
	detail, err := json.Marshal(map[string]any{
		"event":    "artifact",
		"artifact": map[string]string{"path": rel, "name": name, "url": fileURL},
	})
	if err != nil {
		return fmt.Errorf("encode artifact detail: %w", err)
	}
	if err := c.store.UpsertActivity(ctx, c.conversation.ID, providerTurnID, domain.ConversationActivity{
		ID:             c.newID(),
		Kind:           domain.ActivityKindSystem,
		Status:         domain.ActivityStatusCompleted,
		Summary:        name,
		Detail:         detail,
		ProviderItemID: "artifact:" + providerTurnID + ":" + rel,
	}, c.now()); err != nil {
		return fmt.Errorf("record artifact on turn %s: %w", providerTurnID, err)
	}
	c.markArtifactShown(providerTurnID, rel)
	return nil
}

func (c *Controller) markArtifactShown(turn, rel string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.artifactsShownTurn != turn {
		c.artifactsShownTurn, c.artifactsShown = turn, map[string]bool{}
	}
	c.artifactsShown[rel] = true
}

func (c *Controller) artifactShown(turn, rel string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.artifactsShownTurn == turn && c.artifactsShown[rel]
}
