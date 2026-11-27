package approval

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Request kinds. KindSavedLogin is the credential vault's first use of a
// saved login on a site.
const (
	KindBrowserAction = "browser-action"
	KindSavedLogin    = "credential-use"
)

// DefaultTimeout is how long a request waits for the user before denying.
const DefaultTimeout = 2 * time.Minute

// BrowserWaitTimeout is how long an agent's browser command waits for the
// user. It stays under the common two-minute tool-call limit of agent
// harnesses, so the agent sees a clean deny rather than its own timeout.
const BrowserWaitTimeout = 90 * time.Second

// BrowserCommandBudget is the daemon's whole budget for one browser command:
// the approval wait plus the command's own run. The CLI waits slightly
// longer than this so the daemon's answer (a deny) always arrives first.
// TestBrowserTimeoutsNest keeps the three in order.
const BrowserCommandBudget = BrowserWaitTimeout + 90*time.Second

// MaxTimeout bounds caller-chosen timeouts.
const MaxTimeout = 10 * time.Minute

// Decision is how a request ended.
type Decision string

const (
	// DecisionAllowOnce allows this action only.
	DecisionAllowOnce Decision = "allow-once"
	// DecisionAllowSite allows it and remembers the site for this category.
	DecisionAllowSite Decision = "allow-site"
	// DecisionDeny refuses the action.
	DecisionDeny Decision = "deny"
	// DecisionTimeout means nobody answered in time; the action is refused.
	DecisionTimeout Decision = "timeout"
	// DecisionCancelled means the agent's command went away first; refused.
	DecisionCancelled Decision = "cancelled"
	// DecisionSiteAllowed means an earlier "always allow on this site" let it
	// through without asking.
	DecisionSiteAllowed Decision = "site-allowed"
)

// Allowed reports whether the action may run.
func (d Decision) Allowed() bool {
	return d == DecisionAllowOnce || d == DecisionAllowSite || d == DecisionSiteAllowed
}

// ErrNotPending reports a decision for a request that is no longer waiting.
var ErrNotPending = errors.New("approval request is not pending")

// ErrSiteRuleNotAllowed is returned for an "Always allow" on the catch-all
// script category, which covers eval and anything that could not be read.
var ErrSiteRuleNotAllowed = errors.New("this kind of action cannot be always allowed for a site")

// ErrInvalidDecision reports a decision the user cannot make.
var ErrInvalidDecision = errors.New("decision must be allow-once, allow-site, or deny")

// Request is what a caller asks the user to approve.
type Request struct {
	Kind      string `json:"kind"`
	SessionID string `json:"sessionId"`
	// Site is the host the action happens on; "always allow" is keyed on it.
	Site      string `json:"site"`
	URL       string `json:"url,omitempty"`
	PageTitle string `json:"pageTitle,omitempty"`
	// Action says what will happen, e.g. `Click "Place order" on shop.example`.
	Action   string `json:"action"`
	Category string `json:"category"`
	// Reason says why it was flagged.
	Reason string `json:"reason"`
	// Element is the target's role and label, when known.
	Element string `json:"element,omitempty"`
	// Image is an optional base64 PNG of the element in its page. It is shown
	// in the prompt and never written to the audit log.
	Image   string        `json:"image,omitempty"`
	Timeout time.Duration `json:"-"`
}

// Pending is a request waiting for the user.
type Pending struct {
	Request
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Outcome is the result of Request.
type Outcome struct {
	ID       string   `json:"id"`
	Decision Decision `json:"decision"`
}

// Allowed reports whether the action may run.
func (o Outcome) Allowed() bool { return o.Decision.Allowed() }

// Event types published to subscribers.
const (
	EventRequested = "requested"
	EventResolved  = "resolved"
)

// Event is a change to the pending set.
type Event struct {
	Type     string   `json:"type"`
	Pending  *Pending `json:"pending,omitempty"`
	ID       string   `json:"id,omitempty"`
	Decision Decision `json:"decision,omitempty"`
}

// AuditEntry is one gated action in the audit log.
type AuditEntry struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	Kind      string    `json:"kind"`
	SessionID string    `json:"sessionId"`
	Site      string    `json:"site"`
	URL       string    `json:"url,omitempty"`
	Action    string    `json:"action"`
	Category  string    `json:"category"`
	Reason    string    `json:"reason,omitempty"`
	Decision  Decision  `json:"decision"`
	// DecidedAt is when the request ended; equal to Time for site-allowed.
	DecidedAt time.Time `json:"decidedAt"`
}

// SiteRule is an "always allow on this site" choice.
type SiteRule struct {
	Site      string    `json:"site"`
	Category  string    `json:"category"`
	CreatedAt time.Time `json:"createdAt"`
}

type policyFile struct {
	// Default applies to sessions that have not chosen their own mode.
	Default  Mode            `json:"default,omitempty"`
	Sessions map[string]Mode `json:"sessions,omitempty"`
	Sites    []SiteRule      `json:"sites,omitempty"`
}

type waiting struct {
	pending Pending
	done    chan Decision
}

// Gate holds approval requests until the user answers, and owns the policy
// and audit log stored in its directory.
type Gate struct {
	dir string
	now func() time.Time

	mu      sync.Mutex
	waiting map[string]*waiting
	policy  policyFile
	subs    map[int]chan Event
	nextSub int
	// observers see every event, in order, without drops. They are called
	// with mu held and must not block or call back into the gate.
	observers []func(Event)
	auditMu   sync.Mutex
	// writeAudit appends to the audit file; tests replace it to fail a write.
	writeAudit func(f *os.File, b []byte) (int, error)
}

// Open loads (or starts) the gate state in dir.
func Open(dir string) (*Gate, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("approval dir: %w", err)
	}
	g := &Gate{dir: dir, now: time.Now, waiting: map[string]*waiting{}, subs: map[int]chan Event{}}
	raw, err := os.ReadFile(g.policyPath())
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &g.policy); err != nil {
			return nil, fmt.Errorf("approval policy: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("approval policy: %w", err)
	}
	return g, nil
}

func (g *Gate) policyPath() string { return filepath.Join(g.dir, "policy.json") }
func (g *Gate) auditPath() string  { return filepath.Join(g.dir, "audit.jsonl") }

// Request asks the user and blocks until they decide, the timeout passes, or
// ctx ends. Only a timeout or cancellation returns a non-allowed outcome
// without the user. err reports a failure to ask or to record the outcome in
// the audit log; the outcome is then never allowed.
func (g *Gate) Request(ctx context.Context, req Request) (Outcome, error) {
	if req.Category == "" || req.Action == "" {
		return Outcome{}, errors.New("approval request needs an action and category")
	}
	id, err := newID()
	if err != nil {
		return Outcome{}, err
	}
	now := g.now()
	g.mu.Lock()
	if g.siteAllowedLocked(req.Site, req.Category) {
		g.mu.Unlock()
		if err := g.audit(req, id, now, now, DecisionSiteAllowed); err != nil {
			// No record, no action: an allowed outcome needs its audit line.
			return Outcome{ID: id, Decision: DecisionDeny}, err
		}
		return Outcome{ID: id, Decision: DecisionSiteAllowed}, nil
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	timeout = min(timeout, MaxTimeout)
	w := &waiting{
		pending: Pending{Request: req, ID: id, CreatedAt: now, ExpiresAt: now.Add(timeout)},
		done:    make(chan Decision, 1),
	}
	g.waiting[id] = w
	snapshot := w.pending
	g.publishLocked(Event{Type: EventRequested, Pending: &snapshot})
	g.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var decision Decision
	select {
	case decision = <-w.done:
	case <-timer.C:
		decision = g.resolve(w, DecisionTimeout)
	case <-ctx.Done():
		decision = g.resolve(w, DecisionCancelled)
	}
	if err := g.audit(req, id, now, g.now(), decision); err != nil {
		return Outcome{ID: id, Decision: DecisionDeny}, err
	}
	return Outcome{ID: id, Decision: decision}, nil
}

// resolve ends a waiting request with decision unless the user got there
// first, and returns whichever decision won.
func (g *Gate) resolve(w *waiting, decision Decision) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.waiting[w.pending.ID]; !ok {
		// Decide removed it and already sent on the buffered channel.
		return <-w.done
	}
	delete(g.waiting, w.pending.ID)
	g.publishLocked(Event{Type: EventResolved, ID: w.pending.ID, Decision: decision})
	return decision
}

// Decide answers a pending request with the user's choice.
func (g *Gate) Decide(id string, decision Decision) error {
	if decision != DecisionAllowOnce && decision != DecisionAllowSite && decision != DecisionDeny {
		return ErrInvalidDecision
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	w, ok := g.waiting[id]
	if !ok {
		return ErrNotPending
	}
	if decision == DecisionAllowSite {
		if w.pending.Category == CategoryScript {
			return ErrSiteRuleNotAllowed
		}
		if err := g.addSiteRuleLocked(w.pending.Site, w.pending.Category); err != nil {
			return err
		}
	}
	delete(g.waiting, id)
	w.done <- decision
	g.publishLocked(Event{Type: EventResolved, ID: id, Decision: decision})
	return nil
}

// Pending lists waiting requests, oldest first.
func (g *Gate) Pending() []Pending {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Pending, 0, len(g.waiting))
	for _, w := range g.waiting {
		out = append(out, w.pending)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Subscribe streams pending-set changes. Slow subscribers drop events and
// should re-list on reconnect.
func (g *Gate) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	g.mu.Lock()
	id := g.nextSub
	g.nextSub++
	g.subs[id] = ch
	g.mu.Unlock()
	return ch, func() {
		g.mu.Lock()
		if sub, ok := g.subs[id]; ok {
			delete(g.subs, id)
			close(sub)
		}
		g.mu.Unlock()
	}
}

// Observe registers fn for every pending-set change, delivered in order and
// never dropped, unlike Subscribe. fn runs with the gate locked: it must only
// hand the event off (queue it) and return.
func (g *Gate) Observe(fn func(Event)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.observers = append(g.observers, fn)
}

func (g *Gate) publishLocked(ev Event) {
	for _, fn := range g.observers {
		fn(ev)
	}
	for _, ch := range g.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// SessionMode returns a session's mode: its own choice, else the default.
func (g *Gate) SessionMode(sessionID string) Mode {
	g.mu.Lock()
	defer g.mu.Unlock()
	if mode, ok := g.policy.Sessions[sessionID]; ok && ValidMode(mode) {
		return mode
	}
	return g.defaultModeLocked()
}

// DefaultMode returns the mode for sessions without their own; risky unless
// the user changed it.
func (g *Gate) DefaultMode() Mode {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.defaultModeLocked()
}

func (g *Gate) defaultModeLocked() Mode {
	if ValidMode(g.policy.Default) {
		return g.policy.Default
	}
	return ModeRisky
}

// SetDefaultMode changes and persists the default mode.
func (g *Gate) SetDefaultMode(mode Mode) error {
	if !ValidMode(mode) {
		return fmt.Errorf("mode must be off, risky, or every-submit")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.updatePolicyLocked(func(p *policyFile) { p.Default = mode })
}

// SetSessionMode changes and persists a session's mode.
func (g *Gate) SetSessionMode(sessionID string, mode Mode) error {
	if !ValidMode(mode) {
		return fmt.Errorf("mode must be off, risky, or every-submit")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.updatePolicyLocked(func(p *policyFile) { p.Sessions[sessionID] = mode })
}

// SiteRules lists "always allow on this site" choices.
func (g *Gate) SiteRules() []SiteRule {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]SiteRule(nil), g.policy.Sites...)
}

// RemoveSiteRule forgets an "always allow" choice.
func (g *Gate) RemoveSiteRule(site, category string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.updatePolicyLocked(func(p *policyFile) {
		kept := p.Sites[:0]
		for _, rule := range p.Sites {
			if rule.Site != site || rule.Category != category {
				kept = append(kept, rule)
			}
		}
		p.Sites = kept
	})
}

func (g *Gate) siteAllowedLocked(site, category string) bool {
	// A script rule saved by an earlier build is never honoured.
	if site == "" || category == CategoryScript {
		return false
	}
	for _, rule := range g.policy.Sites {
		if rule.Site == site && rule.Category == category {
			return true
		}
	}
	return false
}

func (g *Gate) addSiteRuleLocked(site, category string) error {
	if site == "" || g.siteAllowedLocked(site, category) {
		return nil
	}
	rule := SiteRule{Site: site, Category: category, CreatedAt: g.now().UTC()}
	return g.updatePolicyLocked(func(p *policyFile) { p.Sites = append(p.Sites, rule) })
}

// updatePolicyLocked applies change to a copy of the policy, saves it, and
// only then makes it current: a failed save leaves the policy unchanged, so
// an unsaved "always allow" can never let a later action through.
func (g *Gate) updatePolicyLocked(change func(*policyFile)) error {
	next := policyFile{
		Default:  g.policy.Default,
		Sessions: make(map[string]Mode, len(g.policy.Sessions)+1),
		Sites:    append([]SiteRule(nil), g.policy.Sites...),
	}
	for id, mode := range g.policy.Sessions {
		next.Sessions[id] = mode
	}
	change(&next)
	if err := g.savePolicy(next); err != nil {
		return err
	}
	g.policy = next
	return nil
}

func (g *Gate) savePolicy(policy policyFile) error {
	raw, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(g.dir, "policy-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), g.policyPath())
}

// audit appends one record. Its error reaches Request, which then refuses
// the action rather than run it unrecorded.
func (g *Gate) audit(req Request, id string, at, decidedAt time.Time, decision Decision) error {
	entry := AuditEntry{
		ID: id, Time: at.UTC(), Kind: req.Kind, SessionID: req.SessionID, Site: req.Site, URL: req.URL,
		Action: req.Action, Category: req.Category, Reason: req.Reason, Decision: decision, DecidedAt: decidedAt.UTC(),
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("approval audit: %w", err)
	}
	g.auditMu.Lock()
	defer g.auditMu.Unlock()
	f, err := os.OpenFile(g.auditPath(), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("approval audit: %w", err)
	}
	// Start from a whole-record boundary: a write that failed part way, here
	// or before a restart, must not glue its prefix onto this record. If the
	// file cannot be repaired, nothing is appended and the outcome is refused.
	boundary, err := auditBoundary(f)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("approval audit: %w", err)
	}
	write := g.writeAudit
	if write == nil {
		write = (*os.File).Write
	}
	if _, err := write(f, append(raw, '\n')); err != nil {
		// Drop whatever part of the record landed; the next append repairs
		// it if this cannot.
		_ = f.Truncate(boundary)
		_ = f.Close()
		return fmt.Errorf("approval audit: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("approval audit: %w", err)
	}
	return nil
}

// auditBoundary returns the size of f's complete records, truncating an
// incomplete final record left by a failed or interrupted append.
func auditBoundary(f *os.File) (int64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if size == 0 {
		return 0, nil
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, size-1); err != nil {
		return 0, err
	}
	if last[0] == '\n' {
		return size, nil
	}
	// Find the newline that ends the last whole record.
	const chunk = 64 * 1024
	buf := make([]byte, chunk)
	end := size
	for end > 0 {
		start := max(end-chunk, 0)
		n, err := f.ReadAt(buf[:end-start], start)
		if err != nil && n < int(end-start) {
			return 0, err
		}
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			end = start + int64(i) + 1
			break
		}
		end = start
	}
	if err := f.Truncate(end); err != nil {
		return 0, err
	}
	return end, nil
}

// Audit returns up to limit entries, newest first.
func (g *Gate) Audit(limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		return []AuditEntry{}, nil
	}
	g.auditMu.Lock()
	defer g.auditMu.Unlock()
	f, err := os.Open(g.auditPath())
	if errors.Is(err, os.ErrNotExist) {
		return []AuditEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	// Read back from the end only as far as the newest limit entries, so a
	// long log costs no more than a short one (and the lock that audit
	// writes wait on is held briefly). Lines that do not parse are skipped.
	const chunk = 64 * 1024
	out := make([]AuditEntry, 0, limit)
	var carry []byte
	for end := info.Size(); end > 0 && len(out) < limit; {
		start := max(end-chunk, 0)
		buf := make([]byte, end-start, int(end-start)+len(carry))
		if _, err := f.ReadAt(buf, start); err != nil {
			return nil, err
		}
		buf = append(buf, carry...)
		lines := bytes.Split(buf, []byte("\n"))
		// The first piece may be the end of a line that begins earlier.
		carry = lines[0]
		if start == 0 {
			carry = nil
			lines = append([][]byte{nil}, lines...)
		}
		for i := len(lines) - 1; i >= 1 && len(out) < limit; i-- {
			var entry AuditEntry
			if len(lines[i]) > 0 && json.Unmarshal(lines[i], &entry) == nil {
				out = append(out, entry)
			}
		}
		end = start
	}
	return out, nil
}

func newID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "apr_" + hex.EncodeToString(b[:]), nil
}
