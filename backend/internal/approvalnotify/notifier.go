// Package approvalnotify turns browser approvals waiting for the user into
// notifications, so a paired phone is pushed and the row closes when the
// approval ends.
package approvalnotify

import (
	"context"
	"io"
	"log/slog"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/approval"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sourceKeyPrefix marks a notification as one approval's. The push
// dispatcher reads the approval id back from it.
const sourceKeyPrefix = "browser_approval:"

// SourceKey is the notification source key for an approval id.
func SourceKey(approvalID string) string { return sourceKeyPrefix + approvalID }

// ApprovalID returns the approval id a source key names, or "".
func ApprovalID(sourceKey string) string {
	if len(sourceKey) <= len(sourceKeyPrefix) || sourceKey[:len(sourceKeyPrefix)] != sourceKeyPrefix {
		return ""
	}
	return sourceKey[len(sourceKeyPrefix):]
}

// Writer is the notification write side, satisfied by *notify.Manager.
type Writer interface {
	Notify(ctx context.Context, intent ports.NotificationIntent) error
	Resolve(ctx context.Context, res ports.NotificationResolution) error
}

// SessionLookup names a session and its project for the notification. It
// may return zero values.
type SessionLookup func(sessionID string) (displayName string, projectID domain.ProjectID)

// Notifier queues gate events and writes them on its own goroutine, so a
// slow database never holds the gate's lock.
type Notifier struct {
	writer  Writer
	lookup  SessionLookup
	log     *slog.Logger
	mu      sync.Mutex
	queue   []approval.Event
	wake    chan struct{}
	session map[string]string // approval id -> session id, while open
}

// New constructs a Notifier. lookup and log may be nil.
func New(writer Writer, lookup SessionLookup, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Notifier{writer: writer, lookup: lookup, log: log, wake: make(chan struct{}, 1), session: map[string]string{}}
}

// Observe queues one gate event. It never blocks; pass it to Gate.Observe.
func (n *Notifier) Observe(ev approval.Event) {
	n.mu.Lock()
	n.queue = append(n.queue, ev)
	n.mu.Unlock()
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// Run writes queued events until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	for {
		n.mu.Lock()
		batch := n.queue
		n.queue = nil
		n.mu.Unlock()
		for _, ev := range batch {
			n.write(ctx, ev)
		}
		if len(batch) > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-n.wake:
		}
	}
}

func (n *Notifier) write(ctx context.Context, ev approval.Event) {
	switch ev.Type {
	case approval.EventRequested:
		if ev.Pending == nil {
			return
		}
		p := ev.Pending
		n.session[p.ID] = p.SessionID
		intent := ports.NotificationIntent{
			Type:      domain.NotificationBrowserApproval,
			SessionID: domain.SessionID(p.SessionID),
			SourceKey: SourceKey(p.ID),
			CreatedAt: p.CreatedAt.UTC(),
			// Never p.Action, p.Reason or p.Element: they can carry typed text.
			ApprovalCategory: p.Category,
			ApprovalSite:     p.Site,
		}
		if n.lookup != nil {
			intent.SessionDisplayName, intent.ProjectID = n.lookup(p.SessionID)
		}
		if err := n.writer.Notify(ctx, intent); err != nil {
			n.log.Warn("browser approval notification failed", "approval", p.ID, "err", err)
		}
	case approval.EventResolved:
		sessionID, ok := n.session[ev.ID]
		if !ok {
			return
		}
		delete(n.session, ev.ID)
		res := ports.NotificationResolution{
			Type:      domain.NotificationBrowserApproval,
			SessionID: domain.SessionID(sessionID),
			SourceKey: SourceKey(ev.ID),
		}
		if err := n.writer.Resolve(ctx, res); err != nil {
			n.log.Warn("browser approval notification resolve failed", "approval", ev.ID, "err", err)
		}
	}
}
