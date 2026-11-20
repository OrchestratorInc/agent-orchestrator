package session

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// CardSummaryInterval is the shortest gap between two card summary rewrites of
// one session. A done, needs-input or stuck report is never held back.
const CardSummaryInterval = time.Minute

const (
	// cardSummaryTimeout bounds one light-model rewrite.
	cardSummaryTimeout = 45 * time.Second
	// cardSummarySlots caps rewrites in flight across sessions. A report that
	// finds them all busy gets the plain cut of its note instead.
	cardSummarySlots = 4
)

// CardSummaryStore writes the card summary of a live session.
type CardSummaryStore interface {
	UpdateSessionCardSummary(ctx context.Context, id domain.SessionID, summary string) (bool, error)
}

// CardSummaryRewriter condenses one report note into a card line with a light
// model, in a call apart from the worker's conversation.
type CardSummaryRewriter func(ctx context.Context, id domain.SessionID, note string) (string, error)

// CardSummarizer turns worker reports into one-line kanban card summaries. Each
// report note goes to a light model, at most once per CardSummaryInterval per
// session; a note that arrives sooner waits for the interval, and a newer one
// replaces it. A failed or skipped rewrite shows the note cut at a word. Nothing
// is written while card summaries are off.
type CardSummarizer struct {
	ctx     context.Context
	store   CardSummaryStore
	enabled func(context.Context) bool
	rewrite CardSummaryRewriter
	now     func() time.Time
	after   func(time.Duration, func())
	logger  *slog.Logger
	slots   chan struct{}
	// writeMu orders the newest-wins check with the store write.
	writeMu sync.Mutex
	wg      sync.WaitGroup

	mu       sync.Mutex
	sessions map[domain.SessionID]*cardSummaryState
}

type cardSummaryState struct {
	// lastNote is the newest note taken, so a repeat report is not rewritten.
	lastNote string
	// startedAt is when the last rewrite started, for the interval.
	startedAt time.Time
	// seq counts taken reports; written is the seq of the text on the card, so
	// a slow rewrite never replaces a newer one.
	seq, written uint64
	// pending is a note held back by the interval; waiting is set while a
	// timer will pick it up.
	pending string
	waiting bool
}

// NewCardSummarizer returns a summarizer that rewrites through rewrite and
// writes through store while enabled reports true. A nil rewrite always uses
// the cut note. ctx bounds every rewrite and write.
func NewCardSummarizer(ctx context.Context, store CardSummaryStore, enabled func(context.Context) bool, rewrite CardSummaryRewriter, logger *slog.Logger) *CardSummarizer {
	if logger == nil {
		logger = slog.Default()
	}
	return &CardSummarizer{
		ctx: ctx, store: store, enabled: enabled, rewrite: rewrite, logger: logger,
		now:      time.Now,
		after:    func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		slots:    make(chan struct{}, cardSummarySlots),
		sessions: map[domain.SessionID]*cardSummaryState{},
	}
}

// ReportFiled takes a newly created report as the session's next card summary.
// It returns at once; the rewrite and the write run in the background.
func (c *CardSummarizer) ReportFiled(report domain.ReportRecord) {
	note := strings.TrimSpace(report.Note)
	if note == "" {
		note = strings.TrimSpace(report.Message)
	}
	if note == "" || !c.enabled(c.ctx) {
		return
	}
	urgent := report.State == domain.ReportDone || report.State == domain.ReportNeedsInput || report.State == domain.ReportStuck
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.sessions[report.SessionID]
	if st == nil {
		st = &cardSummaryState{}
		c.sessions[report.SessionID] = st
	}
	if note == st.lastNote {
		return
	}
	st.lastNote = note
	st.seq++
	if wait := CardSummaryInterval - c.now().Sub(st.startedAt); !urgent && !st.startedAt.IsZero() && wait > 0 {
		st.pending = note
		if !st.waiting {
			st.waiting = true
			id := report.SessionID
			c.after(wait, func() { c.flush(id) })
		}
		return
	}
	st.pending = ""
	c.startLocked(report.SessionID, st, note)
}

// flush starts the rewrite of a note the interval held back, unless a newer
// report already went through.
func (c *CardSummarizer) flush(id domain.SessionID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.sessions[id]
	st.waiting = false
	if st.pending == "" {
		return
	}
	note := st.pending
	st.pending = ""
	c.startLocked(id, st, note)
}

func (c *CardSummarizer) startLocked(id domain.SessionID, st *cardSummaryState, note string) {
	st.startedAt = c.now()
	seq := st.seq
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.write(id, seq, c.condense(id, note))
	}()
}

// condense returns the model's card line for note, or note cut at a word when
// no rewrite runs or it fails.
func (c *CardSummarizer) condense(id domain.SessionID, note string) string {
	fallback := domain.CardSummaryFromNote(note)
	if c.rewrite == nil {
		return fallback
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return fallback
	}
	ctx, cancel := context.WithTimeout(c.ctx, cardSummaryTimeout)
	defer cancel()
	out, err := c.rewrite(ctx, id, note)
	if err != nil {
		c.logger.Info("card summary rewrite failed; showing the report note", "sessionID", id, "error", err)
		return fallback
	}
	if line := domain.CardSummaryFromNote(strings.Trim(strings.TrimSpace(out), "\"'`")); line != "" {
		return line
	}
	return fallback
}

func (c *CardSummarizer) write(id domain.SessionID, seq uint64, text string) {
	if text == "" {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	st := c.sessions[id]
	stale := seq <= st.written
	c.mu.Unlock()
	if stale || !c.enabled(c.ctx) {
		return
	}
	if _, err := c.store.UpdateSessionCardSummary(c.ctx, id, text); err != nil {
		c.logger.Warn("update card summary failed", "sessionID", id, "error", err)
		return
	}
	c.mu.Lock()
	st.written = seq
	c.mu.Unlock()
}
