package usage

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type cursorUsageStore interface {
	CursorUsageEnabled(context.Context) bool
	SetCursorUsage(context.Context, bool, time.Time) error
	ListAllSessions(context.Context) ([]domain.SessionRecord, error)
	GetUsageCursorRead(context.Context) (string, time.Time, bool, error)
	ReplaceCursorUsage(context.Context, domain.CursorUsageRead) error
}

const (
	// cursorRefetch is how far before the last read's end the next one
	// starts: Cursor can finalize an event after it was first listed.
	cursorRefetch      = time.Hour
	cursorReadInterval = 5 * time.Minute
)

// CursorUsage reads the usage of the Cursor sessions AO started from the
// user's Cursor account, while the user has it turned on. Only events for AO
// sessions' conversations are kept.
type CursorUsage struct {
	store    cursorUsageStore
	client   *http.Client
	endpoint string
	token    func(context.Context) (string, error)
	now      func() time.Time
	logger   *slog.Logger
	wake     chan struct{}

	mu      sync.Mutex
	readAt  time.Time
	failure string
}

// NewCursorUsage reads cursor.com with the Keychain login.
func NewCursorUsage(store cursorUsageStore, logger *slog.Logger) *CursorUsage {
	if logger == nil {
		logger = slog.Default()
	}
	return &CursorUsage{
		store: store, client: &http.Client{}, endpoint: CursorDashboardURL, token: CursorKeychainToken,
		now: time.Now, logger: logger, wake: make(chan struct{}, 1),
	}
}

// Run reads every few minutes until ctx ends, and at once when turned on.
func (c *CursorUsage) Run(ctx context.Context) {
	ticker := time.NewTicker(cursorReadInterval)
	defer ticker.Stop()
	for {
		if c.store.CursorUsageEnabled(ctx) {
			err := c.read(ctx)
			if ctx.Err() != nil {
				return
			}
			c.record(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-c.wake:
		}
	}
}

// Status reports the setting and how the last read went.
func (c *CursorUsage) Status(ctx context.Context) domain.CursorUsageStatus {
	status := domain.CursorUsageStatus{Supported: runtime.GOOS == "darwin", Enabled: c.store.CursorUsageEnabled(ctx)}
	if !status.Enabled {
		return status
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readAt.IsZero() {
		readAt := c.readAt
		status.ReadAt = &readAt
	}
	status.Error = c.failure
	return status
}

// SetEnabled turns reading on, which reads at once, or off.
func (c *CursorUsage) SetEnabled(ctx context.Context, enabled bool) (domain.CursorUsageStatus, error) {
	if enabled && runtime.GOOS != "darwin" {
		return domain.CursorUsageStatus{}, errors.New("cursor usage can only be read on macOS")
	}
	if err := c.store.SetCursorUsage(ctx, enabled, c.now()); err != nil {
		return domain.CursorUsageStatus{}, err
	}
	c.mu.Lock()
	c.readAt, c.failure = time.Time{}, ""
	c.mu.Unlock()
	if enabled {
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
	return c.Status(ctx), nil
}

func (c *CursorUsage) record(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case err == nil:
		c.readAt, c.failure = c.now(), ""
		return
	case errors.Is(err, ErrCursorSignedOut):
		c.failure = "signed_out"
	case errors.Is(err, ErrCursorKeychain):
		c.failure = "keychain"
	default:
		c.failure = "failed"
	}
	c.logger.Warn("read cursor usage", "err", err)
}

// read stores the Cursor events of AO's sessions since the last read, less
// the refetch margin; the first read for an account starts at the oldest AO
// Cursor session. It reads nothing, not even the Keychain, while no AO Cursor
// session could have new usage.
func (c *CursorUsage) read(ctx context.Context) error {
	sessions, err := c.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	account, readUntil, haveRead, err := c.store.GetUsageCursorRead(ctx)
	if err != nil {
		return err
	}
	conversations := map[string]domain.SessionID{}
	var earliest time.Time
	live := false
	for _, session := range sessions {
		conversation := cursorConversationID(session)
		if session.Harness != domain.HarnessCursor || conversation == "" {
			continue
		}
		conversations[conversation] = session.ID
		if earliest.IsZero() || session.CreatedAt.Before(earliest) {
			earliest = session.CreatedAt
		}
		live = live || !session.IsTerminated || session.UpdatedAt.After(readUntil.Add(-cursorRefetch))
	}
	if len(conversations) == 0 || haveRead && !live {
		return nil
	}

	token, err := c.token(ctx)
	if err != nil {
		return err
	}
	accountKey, err := CursorAccountKey(token)
	if err != nil {
		return err
	}
	// Whole seconds: the store replaces stored events by their second.
	until := c.now().UTC().Truncate(time.Second)
	since := earliest.UTC().Truncate(time.Second)
	if resume := readUntil.Add(-cursorRefetch).UTC().Truncate(time.Second); haveRead && account == accountKey && resume.After(since) {
		since = resume
	}
	if since.After(until) {
		since = until
	}
	events, err := ReadCursorDashboard(ctx, c.client, c.endpoint, token, since, until.Add(time.Second-time.Millisecond))
	if err != nil {
		return err
	}
	read := domain.CursorUsageRead{AccountKey: accountKey, Since: since, Until: until, Now: c.now()}
	for _, event := range events {
		if sessionID, ok := conversations[event.ConversationID]; ok {
			read.Events = append(read.Events, domain.CursorSessionEvent{
				SessionID: sessionID, ConversationID: event.ConversationID, Event: event.Event,
			})
		}
	}
	return c.store.ReplaceCursorUsage(ctx, read)
}

// cursorConversationID is the Cursor conversation a session drives: the chat
// session for a chat, the hook-reported conversation for a terminal session.
func cursorConversationID(session domain.SessionRecord) string {
	if domain.NormalizeSessionMode(session.Mode) == domain.SessionModeChat {
		if id := strings.TrimSpace(session.Metadata.ProviderConversationID); id != "" {
			return id
		}
	}
	return strings.TrimSpace(session.Metadata.AgentSessionID)
}
