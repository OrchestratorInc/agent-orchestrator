package preview

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const DefaultPollInterval = 250 * time.Millisecond

type sessionPreviewSource interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

type previewSetter interface {
	SetPreview(ctx context.Context, id domain.SessionID, previewURL string) (domain.Session, error)
}

type PollerConfig struct {
	Interval time.Duration
	Logger   *slog.Logger
}

type Poller struct {
	source   sessionPreviewSource
	setter   previewSetter
	baseURL  string
	interval time.Duration
	logger   *slog.Logger
	seen     map[domain.SessionID]entryState
}

type entryState struct {
	path    string
	modUnix int64
	size    int64
	cleared bool
}

func NewPoller(source sessionPreviewSource, setter previewSetter, baseURL string, cfg PollerConfig) *Poller {
	p := &Poller{
		source:   source,
		setter:   setter,
		baseURL:  baseURL,
		interval: cfg.Interval,
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

func (p *Poller) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.pollAndLog(ctx)
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.pollAndLog(ctx)
			}
		}
	}()
	return done
}

func (p *Poller) pollAndLog(ctx context.Context) {
	if err := p.Poll(ctx); err != nil {
		p.logger.Error("preview poller: poll failed", "err", err)
	}
}

func (p *Poller) Poll(ctx context.Context) error {
	if p.source == nil || p.setter == nil {
		return nil
	}
	sessions, err := p.source.ListAllSessions(ctx)
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
			if !seenBefore || !previous.cleared || previous.path == "" {
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
