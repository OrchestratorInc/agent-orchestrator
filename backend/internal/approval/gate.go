package approval

import (
	"bufio"
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

// Request kinds.
const (
	KindBrowserAction = "browser-action"
	KindCredentialUse = "credential-use"
)

// DefaultTimeout is how long a request waits for the user before denying.
const DefaultTimeout = 2 * time.Minute

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
	auditMu sync.Mutex
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
// without the user; err is reserved for failures to ask at all.
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
		g.audit(req, id, now, now, DecisionSiteAllowed)
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
	g.audit(req, id, now, g.now(), decision)
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

func (g *Gate) publishLocked(ev Event) {
	for _, ch := range g.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// SessionMode returns a session's mode; risky unless the user changed it.
func (g *Gate) SessionMode(sessionID string) Mode {
	g.mu.Lock()
	defer g.mu.Unlock()
	if mode, ok := g.policy.Sessions[sessionID]; ok && ValidMode(mode) {
		return mode
	}
	return ModeRisky
}

// SetSessionMode changes and persists a session's mode.
func (g *Gate) SetSessionMode(sessionID string, mode Mode) error {
	if !ValidMode(mode) {
		return fmt.Errorf("mode must be off, risky, or every-submit")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.policy.Sessions == nil {
		g.policy.Sessions = map[string]Mode{}
	}
	g.policy.Sessions[sessionID] = mode
	return g.savePolicyLocked()
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
	kept := g.policy.Sites[:0]
	for _, rule := range g.policy.Sites {
		if rule.Site != site || rule.Category != category {
			kept = append(kept, rule)
		}
	}
	g.policy.Sites = kept
	return g.savePolicyLocked()
}

func (g *Gate) siteAllowedLocked(site, category string) bool {
	if site == "" {
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
	g.policy.Sites = append(g.policy.Sites, SiteRule{Site: site, Category: category, CreatedAt: g.now().UTC()})
	return g.savePolicyLocked()
}

func (g *Gate) savePolicyLocked() error {
	raw, err := json.MarshalIndent(g.policy, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(g.dir, "policy-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), g.policyPath())
}

func (g *Gate) audit(req Request, id string, at, decidedAt time.Time, decision Decision) {
	entry := AuditEntry{
		ID: id, Time: at.UTC(), Kind: req.Kind, SessionID: req.SessionID, Site: req.Site, URL: req.URL,
		Action: req.Action, Category: req.Category, Reason: req.Reason, Decision: decision, DecidedAt: decidedAt.UTC(),
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return
	}
	g.auditMu.Lock()
	defer g.auditMu.Unlock()
	f, err := os.OpenFile(g.auditPath(), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(raw, '\n'))
}

// Audit returns up to limit entries, newest first.
func (g *Gate) Audit(limit int) ([]AuditEntry, error) {
	g.auditMu.Lock()
	defer g.auditMu.Unlock()
	f, err := os.Open(g.auditPath())
	if errors.Is(err, os.ErrNotExist) {
		return []AuditEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var all []AuditEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var entry AuditEntry
		if json.Unmarshal(scanner.Bytes(), &entry) == nil {
			all = append(all, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	out := make([]AuditEntry, 0, min(len(all), max(limit, 0)))
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, all[i])
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
