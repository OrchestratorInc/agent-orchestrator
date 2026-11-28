package preview

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
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
	Logger   *slog.Logger
	// Wake, when set, signals that a session changed (a preview may have been
	// chosen). The poller then polls every Interval while it watches at least
	// one preview, and every IdleInterval otherwise; a wake while it watches
	// none polls at once, or one Interval after the previous poll if that is
	// later. Without it (or with IdleInterval not above Interval) it polls
	// every Interval, as before.
	Wake         <-chan struct{}
	IdleInterval time.Duration
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
	logger   *slog.Logger
	seen     map[domain.SessionID]entryState

	wake         <-chan struct{}
	idleInterval time.Duration
	lastPoll     time.Time
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
		logger:   cfg.Logger,
		seen:     map[domain.SessionID]entryState{},

		wake:         cfg.Wake,
		idleInterval: cfg.IdleInterval,
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
		watching := p.pollAndLog(ctx)
		if p.wake == nil || p.idleInterval <= p.interval {
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
		}
		// With nothing to watch, the fast interval only re-lists sessions that
		// have no chosen preview: poll at the idle interval instead. A preview
		// is chosen through a session write, so its session change polls at
		// once, no sooner than one interval after the last poll.
		timer := time.NewTimer(p.nextPoll(watching))
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
				if !watching {
					watching = true // poll now; the poll decides whether to stay fast
					timer.Reset(max(0, time.Until(p.lastPoll.Add(p.interval))))
				}
			case <-timer.C:
				watching = p.pollAndLog(ctx)
				timer.Reset(p.nextPoll(watching))
			}
		}
	}()
	return done
}

func (p *Poller) nextPoll(watching bool) time.Duration {
	if watching {
		return p.interval
	}
	return p.idleInterval
}

// pollAndLog runs one poll and reports whether any preview is being watched:
// a chosen workspace entry, or one the poller cleared and may restore.
func (p *Poller) pollAndLog(ctx context.Context) bool {
	p.lastPoll = time.Now()
	if err := p.Poll(ctx); err != nil {
		p.logger.Error("preview poller: poll failed", "err", err)
	}
	return len(p.seen) > 0
}

// Poll performs one deterministic scan of active worker sessions.
func (p *Poller) Poll(ctx context.Context) error {
	if p.source == nil || p.setter == nil {
		return nil
	}
	// Not cancellable: a cancellable context makes the SQLite driver and
	// database/sql each start a watcher goroutine for every query, and this runs
	// four times a second. The loop still stops on ctx; one in-flight query simply
	// finishes.
	sessions, err := p.source.ListPreviewSessions(context.WithoutCancel(ctx))
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
				// Nothing to restore: forget the session, so a poller left with
				// nothing to watch can rest.
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
