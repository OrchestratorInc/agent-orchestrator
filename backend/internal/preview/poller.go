package preview

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DefaultPollInterval is the preview poller's scan interval when none is configured.
const DefaultPollInterval = 250 * time.Millisecond

type sessionPreviewSource interface {
	ListPreviewSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

type previewSetter interface {
	SetPreview(ctx context.Context, id domain.SessionID, previewURL string) (domain.Session, error)
}

// PollerConfig configures preview poller timing and logging.
type PollerConfig struct {
	Interval time.Duration
	// Changes signals a session row change. When set, the poller waits for it
	// instead of polling while it has no preview file to watch.
	Changes <-chan struct{}
	Logger  *slog.Logger
}

// Poller watches explicitly selected workspace previews and persists refreshes
// through the normal session service path. It never chooses a preview for a
// fresh worker: selection belongs to `ao preview`, a managed server start, or
// deliberate user navigation.
type Poller struct {
	source   sessionPreviewSource
	setter   previewSetter
	baseURL  string
	interval time.Duration
	changes  <-chan struct{}
	logger   *slog.Logger
	seen     map[domain.SessionID]entryState
}

type entryState struct {
	path    string
	modUnix int64
	size    int64
	// cleared is set when the poller itself cleared the preview URL because the
	// workspace entry was missing. When the file reappears, shouldRefresh uses
	// this to re-discover even though the revision was bumped by the clear.
	cleared bool
}

// NewPoller constructs a preview poller over the supplied session source and setter.
func NewPoller(source sessionPreviewSource, setter previewSetter, baseURL string, cfg PollerConfig) *Poller {
	p := &Poller{
		source:   source,
		setter:   setter,
		baseURL:  baseURL,
		interval: cfg.Interval,
		changes:  cfg.Changes,
		logger:   cfg.Logger,
		seen:     map[domain.SessionID]entryState{},
	}
	if p.interval <= 0 {
		p.interval = DefaultPollInterval
	}
	if p.logger == nil {
		p.logger = slog.Default()
	}
	return p
}

// Start runs an immediate poll followed by interval polling until ctx is
// cancelled. The returned channel closes after the goroutine exits.
func (p *Poller) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := p.pollAndLog(ctx)
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			tick := ticker.C
			var changes <-chan struct{}
			// Only a session change can give an idle poller something to watch.
			if p.changes != nil && err == nil && len(p.seen) == 0 {
				tick, changes = nil, p.changes
			}
			select {
			case <-ctx.Done():
				return
			case <-tick:
			case <-changes:
			}
			err = p.pollAndLog(ctx)
		}
	}()
	return done
}

func (p *Poller) pollAndLog(ctx context.Context) error {
	err := p.Poll(ctx)
	if err != nil {
		p.logger.Error("preview poller: poll failed", "err", err)
	}
	return err
}

// Poll performs one deterministic scan of active worker sessions.
func (p *Poller) Poll(ctx context.Context) error {
	if p.source == nil || p.setter == nil {
		return nil
	}
	sessions, err := p.source.ListPreviewSessions(ctx)
	if err != nil {
		return fmt.Errorf("preview poller list sessions: %w", err)
	}
	activeIDs := make(map[domain.SessionID]struct{}, len(sessions))
	for _, sess := range sessions {
		if sess.IsTerminated {
			continue
		}
		activeIDs[sess.ID] = struct{}{}
		if sess.Kind != domain.KindWorker {
			continue
		}
		storedEntry, workspaceOwned := StoredWorkspaceEntry(sess.Metadata.PreviewURL, sess.ID)
		previous, seenBefore := p.seen[sess.ID]
		restoringCleared := false
		if !workspaceOwned {
			// Only restore an entry after the poller itself cleared it because
			// the selected file temporarily disappeared. A blank fresh session
			// or an explicit user clear must remain blank.
			if !seenBefore || !previous.cleared || previous.path == "" {
				// Nothing to watch or restore, so the poller may go idle.
				delete(p.seen, sess.ID)
				continue
			}
			storedEntry = previous.path
			workspaceOwned = true
			restoringCleared = true
		}
		entry, ok := EntryAtPath(sess.Metadata.WorkspacePath, storedEntry)
		if !ok {
			if workspaceOwned {
				if !restoringCleared {
					if _, err := p.setter.SetPreview(ctx, sess.ID, ""); err != nil {
						p.logger.Error("preview poller: failed to clear stale preview",
							"session", sess.ID, "err", err)
					}
				}
				p.seen[sess.ID] = entryState{path: storedEntry, cleared: true}
			}
			continue
		}
		state := stateFor(entry)
		if seenBefore && previous == state {
			continue
		}
		target, err := FileURL(p.baseURL, sess.ID, entry.Path)
		if err != nil {
			p.logger.Error("preview poller: cannot build isolated preview URL", "session", sess.ID, "err", err)
			p.seen[sess.ID] = state
			continue
		}
		if !p.shouldRefresh(sess, target, seenBefore, workspaceOwned) {
			p.seen[sess.ID] = state
			continue
		}
		if _, err := p.setter.SetPreview(ctx, sess.ID, target); err != nil {
			return fmt.Errorf("preview poller set preview %s: %w", sess.ID, err)
		}
		p.seen[sess.ID] = state
	}
	for id := range p.seen {
		if _, ok := activeIDs[id]; !ok {
			delete(p.seen, id)
		}
	}
	return nil
}

func (p *Poller) shouldRefresh(sess domain.SessionRecord, target string, seenBefore, workspaceOwned bool) bool {
	current := strings.TrimSpace(sess.Metadata.PreviewURL)
	if current == "" {
		if !seenBefore {
			return sess.Metadata.PreviewRevision == 0
		}
		previous := p.seen[sess.ID]
		return previous.cleared
	}
	if current == target {
		return seenBefore
	}
	return workspaceOwned
}

func stateFor(entry Entry) entryState {
	return entryState{path: entry.Path, modUnix: entry.ModTime.UnixNano(), size: entry.Size}
}
