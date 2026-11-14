// Package webhook posts session, question and pull request events to
// user-configured HTTPS endpoints, such as a chat bot's webhook routine.
package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Event kinds a hook can subscribe to.
const (
	EventNeedsInput             = "needs_input"
	EventInputResolved          = "input_resolved"
	EventFinished               = "finished"
	EventWorkerFinished         = "worker_finished"
	EventFailed                 = "failed"
	EventExited                 = "exited"
	EventReadyToMerge           = "ready_to_merge"
	EventPRMerged               = "pr_merged"
	EventPRClosed               = "pr_closed"
	EventReviewCompleted        = "review_completed"
	EventReviewChangesRequested = "review_changes_requested"
	EventSummary                = "summary"
	EventTest                   = "test"
)

// Kinds lists the event kinds a hook can name in Events.
var Kinds = []string{
	EventNeedsInput, EventFinished, EventWorkerFinished, EventFailed, EventExited, EventReadyToMerge,
	EventPRMerged, EventPRClosed, EventReviewCompleted, EventReviewChangesRequested,
}

// Errors returned by Store.Put and Store.Patch.
var (
	ErrInvalidName   = errors.New("webhook name must be 1-32 letters, digits, - or _")
	ErrInvalidURL    = errors.New("webhook url must be an https:// URL, or http:// to this computer")
	ErrInvalidEvent  = errors.New("unknown webhook event kind")
	ErrNotFound      = errors.New("webhook not found")
	ErrInvalidHeader = errors.New("webhook header must be one Name: value line")
)

var (
	nameRE       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	headerNameRE = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

// Hook is one configured endpoint. Empty Projects means every project; empty
// Events means every kind except worker_finished.
type Hook struct {
	Name        string            `json:"name"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers,omitempty"`
	Projects    []string          `json:"projects,omitempty"`
	Events      []string          `json:"events,omitempty"`
	QuietAtDesk bool              `json:"quietAtDesk,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
}

// Filters is the part of a Hook that can change without re-entering its secret.
type Filters struct {
	Projects    *[]string
	Events      *[]string
	QuietAtDesk *bool
}

// Wants reports whether the hook receives an event of kind from project.
func (h Hook) Wants(kind, project string) bool {
	if kind == EventTest {
		return true
	}
	if len(h.Projects) > 0 && project != "" && !slices.Contains(h.Projects, project) {
		return false
	}
	switch kind {
	case EventInputResolved:
		kind = EventNeedsInput
	case EventSummary:
		return h.Wants(EventFinished, project) || h.Wants(EventWorkerFinished, project)
	}
	if len(h.Events) == 0 {
		return kind != EventWorkerFinished
	}
	return slices.Contains(h.Events, kind)
}

// Store keeps hooks in <dataDir>/webhooks.json, readable only by the user.
type Store struct {
	path  string
	mu    sync.Mutex
	hooks map[string]Hook
}

// OpenStore loads the hooks file, treating a missing file as no hooks.
func OpenStore(dataDir string) (*Store, error) {
	s := &Store{path: filepath.Join(dataDir, "webhooks.json"), hooks: map[string]Hook{}}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read webhooks: %w", err)
	}
	var file struct {
		Hooks []Hook `json:"hooks"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, fmt.Errorf("parse webhooks: %w", err)
	}
	for _, h := range file.Hooks {
		s.hooks[h.Name] = h
	}
	return s, nil
}

// List returns every hook, sorted by name.
func (s *Store) List() []Hook {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Hook, 0, len(s.hooks))
	for _, h := range s.hooks {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns the hook named name.
func (s *Store) Get(name string) (Hook, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.hooks[name]
	return h, ok
}

// Put creates or replaces a hook after validating it.
func (s *Store) Put(h Hook, now time.Time) (Hook, error) {
	if err := validate(h); err != nil {
		return Hook{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.hooks[h.Name]; ok {
		h.CreatedAt = old.CreatedAt
	} else {
		h.CreatedAt = now.UTC()
	}
	return h, s.commitLocked(func() { s.hooks[h.Name] = h })
}

// Patch changes a hook's filters and keeps its URL and headers.
func (s *Store) Patch(name string, f Filters) (Hook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.hooks[name]
	if !ok {
		return Hook{}, ErrNotFound
	}
	if f.Projects != nil {
		h.Projects = *f.Projects
	}
	if f.Events != nil {
		h.Events = *f.Events
	}
	if f.QuietAtDesk != nil {
		h.QuietAtDesk = *f.QuietAtDesk
	}
	if err := validate(h); err != nil {
		return Hook{}, err
	}
	return h, s.commitLocked(func() { s.hooks[name] = h })
}

// Delete removes a hook and reports whether it existed.
func (s *Store) Delete(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hooks[name]; !ok {
		return false, nil
	}
	return true, s.commitLocked(func() { delete(s.hooks, name) })
}

func validate(h Hook) error {
	if !nameRE.MatchString(h.Name) {
		return ErrInvalidName
	}
	u, err := url.Parse(h.URL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && (u.Scheme != "http" || !isLoopbackHost(u.Hostname()))) {
		return ErrInvalidURL
	}
	for k, v := range h.Headers {
		if !headerNameRE.MatchString(k) || strings.ContainsAny(v, "\r\n") {
			return ErrInvalidHeader
		}
	}
	for _, k := range h.Events {
		if !slices.Contains(Kinds, k) {
			return fmt.Errorf("%w: %s (want %s)", ErrInvalidEvent, k, strings.Join(Kinds, ", "))
		}
	}
	return nil
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (s *Store) commitLocked(mutate func()) error {
	prev := make(map[string]Hook, len(s.hooks))
	for k, v := range s.hooks {
		prev[k] = v
	}
	mutate()
	hooks := make([]Hook, 0, len(s.hooks))
	for _, h := range s.hooks {
		hooks = append(hooks, h)
	}
	sort.Slice(hooks, func(i, j int) bool { return hooks[i].Name < hooks[j].Name })
	if err := writePrivate(s.path, hooks); err != nil {
		s.hooks = prev
		return err
	}
	return nil
}

func writePrivate(path string, hooks []Hook) error {
	b, err := json.MarshalIndent(struct {
		Hooks []Hook `json:"hooks"`
	}{hooks}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("write webhooks: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".webhooks-*.json")
	if err != nil {
		return fmt.Errorf("write webhooks: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write webhooks: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write webhooks: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write webhooks: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write webhooks: %w", err)
	}
	return nil
}
