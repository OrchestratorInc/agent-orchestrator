package chat

import (
	"cmp"
	"context"
	"sort"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// artifactStamp is what a turn compares to tell that an artifact file changed.
type artifactStamp struct {
	size  int64
	mtime time.Time
}

// snapshotArtifacts lists the session's artifact files by path. It reports
// false when the directory cannot be read, so a turn is never credited with
// every file because the snapshot before it came back empty.
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

// turnArtifactChanges lists the files in after that are new since before, or
// whose size or modification time moved, sorted by path. A file only in before
// was deleted and is left out.
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

// sessionArtifactDir is the session's artifact directory: the one its record
// names, or else the canonical one under the data directory, which is also
// where `ao artifact add` resolves it. Empty only without a data directory.
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

// captureArtifactBaseline records the artifact directory as a turn starts, so
// its end can tell which files the turn wrote, whatever wrote them: a provider
// edit tool, a shell command, or `ao artifact add`. replace false keeps a
// baseline already taken for the same turn, for a provider-started turn whose
// start AO only learns of from its event.
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

// recordTurnArtifacts stores, on the turn that just ended, the artifact files
// it created or edited. A turn with no baseline (one AO did not see start, or a
// daemon restart in between) records nothing rather than guessing.
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
