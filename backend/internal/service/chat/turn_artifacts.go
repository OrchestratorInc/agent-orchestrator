package chat

import (
	"cmp"
	"context"
	"sort"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

type artifactStamp struct {
	size  int64
	mtime time.Time
}

func snapshotArtifacts(ctx context.Context, dir string) (map[string]artifactStamp, bool) {
	if dir == "" {
		return nil, false
	}
	files, err := sessionartifacts.List(ctx, dir)
	if err != nil {
		return nil, false
	}
	stamps := make(map[string]artifactStamp, len(files))
	for _, file := range files {
		stamps[file.Path] = artifactStamp{size: file.Size, mtime: file.UpdatedAt}
	}
	return stamps, true
}

func turnArtifactChanges(before, after map[string]artifactStamp) []domain.ConversationTurnArtifact {
	var changes []domain.ConversationTurnArtifact
	for path, stamp := range after {
		previous, existed := before[path]
		switch {
		case !existed:
			changes = append(changes, domain.ConversationTurnArtifact{Path: path, Status: domain.TurnArtifactAdded})
		case previous.size != stamp.size || !previous.mtime.Equal(stamp.mtime):
			changes = append(changes, domain.ConversationTurnArtifact{Path: path, Status: domain.TurnArtifactModified})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

func (s *Service) sessionArtifactDir(ctx context.Context, id domain.SessionID) string {
	if s.sessions == nil {
		return sessionartifacts.Dir(s.dataDir, id)
	}
	record, found, err := s.sessions.GetSession(ctx, id)
	if err != nil || !found {
		return sessionartifacts.Dir(s.dataDir, id)
	}
	return cmp.Or(record.Metadata.ArtifactDir, sessionartifacts.Dir(s.dataDir, id))
}

func (c *Controller) captureArtifactBaseline(ctx context.Context, replace bool) {
	if c.artifactDir == "" {
		return
	}
	c.mu.Lock()
	taken := c.artifactBaseline != nil
	c.mu.Unlock()
	if taken && !replace {
		return
	}
	stamps, ok := snapshotArtifacts(ctx, c.artifactDir)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !ok {
		c.artifactBaseline = nil
		return
	}
	c.artifactBaseline = stamps
}

func (c *Controller) recordTurnArtifacts(ctx context.Context, providerTurnID string) error {
	c.mu.Lock()
	before := c.artifactBaseline
	c.artifactBaseline = nil
	c.mu.Unlock()
	if before == nil || providerTurnID == "" {
		return nil
	}
	after, ok := snapshotArtifacts(ctx, c.artifactDir)
	if !ok {
		return nil
	}
	changes := turnArtifactChanges(before, after)
	if len(changes) == 0 {
		return nil
	}
	_, err := c.store.SetTurnArtifacts(ctx, c.conversation.ID, providerTurnID, changes)
	return err
}
