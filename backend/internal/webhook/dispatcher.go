package webhook

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	runEndsPerMinute  = 10
	maxQueued         = 200
	maxSummaryNames   = 20
	lastReplyLimit    = 3000
	errorLimit        = 500
	giveUpAfter       = time.Hour
	defaultPause      = 15 * time.Minute
	defaultExitSettle = 10 * time.Second
	deskIdleLimit     = 15 * time.Minute
	deskCheckTTL      = 10 * time.Second
)

var defaultBackoff = []time.Duration{5 * time.Second, 20 * time.Second, time.Minute}

// NotificationSource is the live notification fan-out (notify.Hub).
type NotificationSource interface {
	Subscribe(projectID domain.ProjectID) (<-chan domain.NotificationEvent, func())
}

// ChangeSource is the live change-log fan-out (cdc.Broadcaster).
type ChangeSource interface {
	Subscribe(fn func(cdc.Event)) (unsubscribe func())
}

// Sessions reads session records.
type Sessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
}

// History reads a session's turns, newest first.
type History interface {
	SessionHistory(ctx context.Context, id domain.SessionID, offset, limit int) ([]domain.HandoffTurn, int, error)
}

// Turns reads a session's latest Chat turn.
type Turns interface {
	ConversationForSession(ctx context.Context, session domain.SessionID) (domain.ConversationRecord, error)
	LatestConversationTurn(ctx context.Context, conversationID string) (domain.ConversationTurn, error)
}

// Deps configures a Dispatcher. History and Turns are optional.
type Deps struct {
	Store         *Store
	Notifications NotificationSource
	Changes       ChangeSource
	Sessions      Sessions
	History       History
	Turns         Turns
	HostID        string
	Log           *slog.Logger
}

// Payload is the JSON body POSTed for every event.
type Payload struct {
	Event       string                         `json:"event"`
	EventID     string                         `json:"eventId"`
	At          time.Time                      `json:"at"`
	HostID      string                         `json:"hostId"`
	Machine     string                         `json:"machine"`
	ProjectID   string                         `json:"projectId,omitempty"`
	SessionID   string                         `json:"sessionId,omitempty"`
	SessionName string                         `json:"sessionName,omitempty"`
	SessionKind string                         `json:"sessionKind,omitempty"`
	PRURL       string                         `json:"prUrl,omitempty"`
	Title       string                         `json:"title,omitempty"`
	Text        string                         `json:"text"`
	LastReply   string                         `json:"lastReply,omitempty"`
	TurnID      string                         `json:"turnId,omitempty"`
	Error       string                         `json:"error,omitempty"`
	UsageLimit  *domain.ConversationUsageLimit `json:"usageLimit,omitempty"`
	Count       int                            `json:"count,omitempty"`
	Sessions    []string                       `json:"sessions,omitempty"`
	FirstAt     *time.Time                     `json:"firstAt,omitempty"`
	LastAt      *time.Time                     `json:"lastAt,omitempty"`

	tries    int
	firstTry time.Time
}

// Status is a hook's delivery state since the daemon started.
type Status struct {
	Sent        int        `json:"sent"`
	Queued      int        `json:"queued"`
	Folded      int        `json:"folded"`
	LastSentAt  *time.Time `json:"lastSentAt,omitempty"`
	LastError   string     `json:"lastError,omitempty"`
	LastErrorAt *time.Time `json:"lastErrorAt,omitempty"`
	PausedUntil *time.Time `json:"pausedUntil,omitempty"`
}

// Dispatcher turns live events into webhook POSTs, one queue per hook.
type Dispatcher struct {
	deps    Deps
	client  *http.Client
	clock   func() time.Time
	desk    func() bool
	machine string
	home    string
	changes chan cdc.Event
	settle  time.Duration
	backoff []time.Duration

	mu       sync.Mutex
	runCtx   context.Context
	queues   map[string]*queue
	deskAt   time.Time
	deskSeen bool

	activity map[domain.SessionID]domain.ActivityState
}

// NewDispatcher constructs a Dispatcher.
func NewDispatcher(d Deps) *Dispatcher {
	if d.Log == nil {
		d.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	host, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	return &Dispatcher{
		deps: d,
		client: &http.Client{
			Timeout:       15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		clock:    time.Now,
		desk:     deskActive,
		machine:  strings.TrimSuffix(host, ".local"),
		home:     home,
		changes:  make(chan cdc.Event, 1024),
		settle:   defaultExitSettle,
		backoff:  defaultBackoff,
		queues:   map[string]*queue{},
		activity: map[domain.SessionID]domain.ActivityState{},
	}
}

// Run delivers events until ctx ends.
func (d *Dispatcher) Run(ctx context.Context) {
	d.mu.Lock()
	d.runCtx = ctx
	for name, q := range d.queues {
		go d.drain(ctx, name, q)
	}
	d.mu.Unlock()
	notes, unsubscribeNotes := d.deps.Notifications.Subscribe("")
	defer unsubscribeNotes()
	unsubscribeChanges := d.deps.Changes.Subscribe(func(e cdc.Event) {
		if e.Type != cdc.EventSessionUpdated && e.Type != cdc.EventSessionCreated {
			return
		}
		select {
		case d.changes <- e:
		default:
			d.deps.Log.Warn("webhook: change buffer full, event dropped", "seq", e.Seq)
		}
	})
	defer unsubscribeChanges()
	if recs, err := d.deps.Sessions.ListSessions(ctx, ""); err == nil {
		for _, r := range recs {
			if !r.IsTerminated {
				d.activity[r.ID] = r.Activity.State
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-notes:
			if !ok {
				return
			}
			if p, ok := d.fromNotification(ctx, ev); ok {
				d.route(ctx, p)
			}
		case e := <-d.changes:
			if p, ok := d.fromChange(ctx, e); ok {
				d.route(ctx, p)
			}
		}
	}
}

// Test sends one test event to the named hook and waits for the answer.
func (d *Dispatcher) Test(ctx context.Context, name string) error {
	h, ok := d.deps.Store.Get(name)
	if !ok {
		return ErrNotFound
	}
	p := d.base(ctx, EventTest, "", "", d.clock())
	p.Text = "Test event from Orchestrator on " + d.machine
	_, err := d.post(ctx, h, p)
	q := d.queue(name)
	q.mu.Lock()
	q.record(err, d.clock())
	q.mu.Unlock()
	return err
}

// Statuses returns delivery state per hook name.
func (d *Dispatcher) Statuses() map[string]Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]Status, len(d.queues))
	for name, q := range d.queues {
		q.mu.Lock()
		s := q.status
		s.Queued = len(q.urgent) + len(q.normal)
		if q.pausedUntil.After(d.clock()) {
			t := q.pausedUntil
			s.PausedUntil = &t
		}
		q.mu.Unlock()
		out[name] = s
	}
	return out
}

func (d *Dispatcher) fromNotification(ctx context.Context, ev domain.NotificationEvent) (Payload, bool) {
	rec := ev.Record
	kind := ""
	at := rec.CreatedAt
	switch {
	case ev.Kind == domain.NotificationCreated:
		kind = map[domain.NotificationType]string{
			domain.NotificationNeedsInput:             EventNeedsInput,
			domain.NotificationReadyToMerge:           EventReadyToMerge,
			domain.NotificationPRMerged:               EventPRMerged,
			domain.NotificationPRClosedUnmerged:       EventPRClosed,
			domain.NotificationReviewCompleted:        EventReviewCompleted,
			domain.NotificationReviewChangesRequested: EventReviewChangesRequested,
		}[rec.Type]
	case ev.Kind == domain.NotificationResolved && rec.Type == domain.NotificationNeedsInput:
		kind, at = EventInputResolved, rec.ResolvedAt
	}
	if kind == "" {
		return Payload{}, false
	}
	p := d.base(ctx, kind, rec.SessionID, rec.ProjectID, at)
	p.PRURL = rec.PRURL
	p.Title = rec.Title
	p.Text = rec.Title
	if rec.Body != "" {
		p.Text += ": " + rec.Body
	}
	return p, true
}

func (d *Dispatcher) fromChange(ctx context.Context, e cdc.Event) (Payload, bool) {
	var body struct {
		ID           string `json:"id"`
		Activity     string `json:"activity"`
		IsTerminated bool   `json:"isTerminated"`
	}
	if json.Unmarshal(e.Payload, &body) != nil || body.Activity == "" {
		return Payload{}, false
	}
	id := domain.SessionID(cmp.Or(body.ID, e.SessionID))
	if body.IsTerminated {
		delete(d.activity, id)
		return Payload{}, false
	}
	next := domain.ActivityState(body.Activity)
	prev, known := d.activity[id]
	d.activity[id] = next
	if !known || prev == next {
		return Payload{}, false
	}
	var kind string
	switch {
	case prev == domain.ActivityActive && next == domain.ActivityIdle:
		kind = EventFinished
	case next == domain.ActivityExited:
		kind = EventExited
	default:
		return Payload{}, false
	}
	p := d.base(ctx, kind, id, domain.ProjectID(e.ProjectID), e.CreatedAt)
	name := cmp.Or(p.SessionName, p.SessionID)
	switch kind {
	case EventFinished:
		if p.SessionKind == string(domain.KindWorker) {
			p.Event = EventWorkerFinished
		}
		p.Text = name + " finished its turn"
		if turn, ok := d.failedTurn(ctx, id); ok {
			p.Event, p.TurnID, p.UsageLimit = EventFailed, turn.ID, turn.UsageLimit
			p.Error = d.redact(tail(turn.ErrorMessage, errorLimit))
			p.Text = name + "'s turn failed"
			if turn.UsageLimit != nil {
				p.Text = name + " hit a usage limit"
				if turn.UsageLimit.ResetsAt != nil {
					p.Text += ", resets " + turn.UsageLimit.ResetsAt.UTC().Format(time.RFC3339)
				}
			}
		}
	case EventExited:
		p.Text = name + "'s agent exited"
	}
	p.Title = p.Text
	return p, true
}

func (d *Dispatcher) failedTurn(ctx context.Context, id domain.SessionID) (domain.ConversationTurn, bool) {
	if d.deps.Turns == nil {
		return domain.ConversationTurn{}, false
	}
	conv, err := d.deps.Turns.ConversationForSession(ctx, id)
	if err != nil {
		return domain.ConversationTurn{}, false
	}
	turn, err := d.deps.Turns.LatestConversationTurn(ctx, conv.ID)
	return turn, err == nil && turn.State == domain.TurnStateFailed
}

func (d *Dispatcher) base(ctx context.Context, kind string, id domain.SessionID, project domain.ProjectID, at time.Time) Payload {
	p := Payload{
		Event:     kind,
		EventID:   "whe_" + uuid.NewString(),
		At:        cmp.Or(at, d.clock()).UTC(),
		HostID:    d.deps.HostID,
		Machine:   d.machine,
		ProjectID: string(project),
		SessionID: string(id),
	}
	if id == "" {
		return p
	}
	if rec, ok, err := d.deps.Sessions.GetSession(ctx, id); err == nil && ok {
		p.SessionName = rec.DisplayName
		p.SessionKind = string(rec.Kind)
		p.ProjectID = cmp.Or(p.ProjectID, string(rec.ProjectID))
	}
	return p
}

func (d *Dispatcher) route(ctx context.Context, p Payload) {
	var targets []Hook
	for _, h := range d.deps.Store.List() {
		if h.Wants(p.Event, p.ProjectID) {
			targets = append(targets, h)
		}
	}
	if len(targets) == 0 {
		return
	}
	p.Title, p.Text, p.SessionName = d.redact(p.Title), d.redact(p.Text), d.redact(p.SessionName)
	if (p.Event == EventFinished || p.Event == EventWorkerFinished) && d.deps.History != nil {
		if turns, _, err := d.deps.History.SessionHistory(ctx, domain.SessionID(p.SessionID), 0, 1); err == nil && len(turns) > 0 {
			p.LastReply = d.redact(tail(turns[0].AgentResponse, lastReplyLimit))
		}
	}
	quiet := false
	for _, h := range targets {
		if h.QuietAtDesk && p.Event != EventInputResolved {
			if !quiet {
				quiet = d.atDesk()
			}
			if quiet {
				continue
			}
		}
		d.queue(h.Name).push(p, d.clock())
	}
}

func (d *Dispatcher) atDesk() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.clock().Sub(d.deskAt) > deskCheckTTL {
		d.deskSeen, d.deskAt = d.desk(), d.clock()
	}
	return d.deskSeen
}

func (d *Dispatcher) queue(name string) *queue {
	d.mu.Lock()
	defer d.mu.Unlock()
	q, ok := d.queues[name]
	if !ok {
		q = &queue{signal: make(chan struct{}, 1)}
		d.queues[name] = q
		if d.runCtx != nil {
			go d.drain(d.runCtx, name, q)
		}
	}
	return q
}

func (d *Dispatcher) drain(ctx context.Context, name string, q *queue) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-q.signal:
		}
		for {
			q.mu.Lock()
			until := q.pausedUntil
			if q.retryAt.After(until) {
				until = q.retryAt
			}
			wait := until.Sub(d.clock())
			q.mu.Unlock()
			if wait > 0 && !sleep(ctx, wait) {
				return
			}
			p, ok := q.pop()
			if !ok {
				break
			}
			if !d.deliver(ctx, name, q, p) {
				return
			}
		}
	}
}

func (d *Dispatcher) deliver(ctx context.Context, name string, q *queue, p Payload) bool {
	if p.Event == EventExited {
		if wait := p.At.Add(d.settle).Sub(d.clock()); p.tries == 0 && wait > 0 && !sleep(ctx, wait) {
			return false
		}
		rec, ok, err := d.deps.Sessions.GetSession(ctx, domain.SessionID(p.SessionID))
		if err == nil && (!ok || rec.IsTerminated || rec.Activity.State != domain.ActivityExited) {
			if p.tries > 0 {
				q.mu.Lock()
				q.failing = false
				q.mu.Unlock()
			}
			return true
		}
	}
	h, ok := d.deps.Store.Get(name)
	if !ok {
		q.clear()
		return true
	}
	retryAfter, err := d.post(ctx, h, p)
	now := d.clock()
	q.mu.Lock()
	defer q.mu.Unlock()
	q.record(err, now)
	switch {
	case err == nil:
		return true
	case errors.Is(err, errPermanent):
		q.failing = false
		d.deps.Log.Warn("webhook: event rejected", "hook", name, "event", p.Event, "err", err)
		return true
	}
	if p.tries == 0 {
		p.firstTry = now
	}
	if now.Sub(p.firstTry) > giveUpAfter {
		q.failing = false
		d.deps.Log.Warn("webhook: giving up on event", "hook", name, "event", p.Event, "err", err)
		return true
	}
	var limited *rateLimited
	if errors.As(err, &limited) {
		q.pausedUntil = now.Add(retryAfter)
	} else {
		q.retryAt = now.Add(d.backoff[min(p.tries, len(d.backoff)-1)])
	}
	p.tries++
	q.failing = true
	if urgent(p.Event) {
		q.urgent = append([]Payload{p}, q.urgent...)
	} else {
		q.normal = append([]Payload{p}, q.normal...)
	}
	return true
}

var errPermanent = errors.New("webhook refused the event")

type rateLimited struct{ msg string }

func (r *rateLimited) Error() string { return "rate limited: " + r.msg }

func (d *Dispatcher) post(ctx context.Context, h Hook, p Payload) (time.Duration, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", errPermanent, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("%w: bad request", errPermanent)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Orchestrator-Webhook/1")
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) && ue.Timeout() {
			return 0, errors.New("the webhook did not answer in time")
		}
		return 0, errors.New("could not reach the webhook")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	text := d.redact(strings.TrimSpace(string(raw)))
	if len(text) > 200 {
		text = text[:200]
	}
	lower := strings.ToLower(text)
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return 0, nil
	case resp.StatusCode == http.StatusTooManyRequests || strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many requests"):
		return retryAfter(resp.Header.Get("Retry-After"), d.clock()), &rateLimited{msg: fmt.Sprintf("HTTP %d %s", resp.StatusCode, text)}
	case resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500:
		return 0, fmt.Errorf("HTTP %d %s", resp.StatusCode, text)
	}
	return 0, fmt.Errorf("%w: HTTP %d %s", errPermanent, resp.StatusCode, text)
}

func retryAfter(v string, now time.Time) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return defaultPause
}

var keyPattern = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}|xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|(?i:bearer)\s+[A-Za-z0-9._~+/=-]{16,})`)

func (d *Dispatcher) redact(s string) string {
	if len(d.home) > 1 {
		s = strings.ReplaceAll(s, d.home, "~")
	}
	return keyPattern.ReplaceAllString(s, "[key]")
}

func tail(s string, limit int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= limit {
		return string(r)
	}
	return "…" + string(r[len(r)-limit:])
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type queue struct {
	mu          sync.Mutex
	signal      chan struct{}
	urgent      []Payload
	normal      []Payload
	summary     *Payload
	runEnds     []time.Time
	pausedUntil time.Time
	retryAt     time.Time
	failing     bool
	status      Status
}

func (q *queue) push(p Payload, now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case p.Event == EventFinished || p.Event == EventWorkerFinished:
		cut := 0
		for cut < len(q.runEnds) && now.Sub(q.runEnds[cut]) > time.Minute {
			cut++
		}
		q.runEnds = q.runEnds[cut:]
		if q.failing || now.Before(q.pausedUntil) || len(q.runEnds) >= runEndsPerMinute || len(q.normal) >= maxQueued {
			q.fold(p)
			break
		}
		q.runEnds = append(q.runEnds, now)
		q.normal = append(q.normal, p)
	case urgent(p.Event):
		q.urgent = append(q.urgent, p)
	default:
		if len(q.normal) >= maxQueued {
			q.normal = q.normal[1:]
		}
		q.normal = append(q.normal, p)
	}
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func urgent(kind string) bool {
	switch kind {
	case EventNeedsInput, EventExited, EventInputResolved, EventFailed:
		return true
	}
	return false
}

func (q *queue) fold(p Payload) {
	q.status.Folded++
	if q.summary == nil {
		first := p.At
		q.summary = &Payload{Event: EventSummary, HostID: p.HostID, Machine: p.Machine, FirstAt: &first}
	}
	s := q.summary
	s.Count++
	last := p.At
	s.LastAt = &last
	if len(s.Sessions) < maxSummaryNames {
		s.Sessions = append(s.Sessions, strings.TrimSpace(p.SessionID+" "+p.SessionName))
	}
}

func (q *queue) pop() (Payload, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case len(q.urgent) > 0:
		p := q.urgent[0]
		q.urgent = q.urgent[1:]
		return p, true
	case len(q.normal) > 0:
		p := q.normal[0]
		q.normal = q.normal[1:]
		return p, true
	case q.summary != nil && !q.failing:
		p := *q.summary
		q.summary = nil
		p.EventID = "whe_" + uuid.NewString()
		p.At = *p.LastAt
		p.Text = fmt.Sprintf("%d turns ended without their own message on %s", p.Count, p.Machine)
		return p, true
	}
	return Payload{}, false
}

func (q *queue) clear() {
	q.mu.Lock()
	q.urgent, q.normal, q.summary = nil, nil, nil
	q.mu.Unlock()
}

func (q *queue) record(err error, now time.Time) {
	if err == nil {
		q.status.Sent++
		q.status.LastSentAt = &now
		q.failing = false
		return
	}
	q.status.LastError = err.Error()
	q.status.LastErrorAt = &now
}
