package chat

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
	reportsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/report"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

var _ reportsvc.ArtifactRecorder = (*Service)(nil)

// RecordReportedArtifact shows an HTML page a chat agent reported with
// ao report --artifact in the turn it is running. It is best effort and never
// fails the report: a terminal session, no turn in flight, a reference that is
// not an HTML file in the session's artifact directory, or any error is only
// logged.
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
	rel, ok := artifactRelPath(dir, reference)
	if !ok {
		return errors.New("not in the session's artifact directory")
	}
	if ext := strings.ToLower(path.Ext(rel)); ext != ".html" && ext != ".htm" {
		return errors.New("not an HTML page")
	}
	// Only what the artifact file route serves: the same lookup, and its cap.
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
	// entry.Path is the cleaned path the route serves, so the row and the
	// served file always name the same page.
	return controller.recordArtifact(ctx, entry.Path)
}

// artifactRelPath is reference as a slash-separated path inside dir: an
// absolute path under dir, or a path relative to it, once cleaned.
func artifactRelPath(dir, reference string) (string, bool) {
	if dir == "" {
		return "", false
	}
	rel := filepath.Clean(reference)
	if filepath.IsAbs(rel) {
		var err error
		if rel, err = filepath.Rel(filepath.Clean(dir), rel); err != nil {
			return "", false
		}
	}
	if !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// recordArtifact shows a reported HTML artifact in the turn in flight, as a
// system activity identified by its "artifact" discriminator. A page that turn
// already shows, reported before or kept from a render, is not shown again.
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

// markArtifactShown notes that provider turn turn shows the artifact at rel.
// ponytail: in memory, so a daemon restart mid-turn can show a page twice.
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
