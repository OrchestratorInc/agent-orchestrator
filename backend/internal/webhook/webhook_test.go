package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestStoreValidatesPersistsAndPatches(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	bad := []struct {
		hook Hook
		want error
	}{
		{Hook{Name: "has space", URL: "https://x.test"}, ErrInvalidName},
		{Hook{Name: "a", URL: "http://example.com/hook"}, ErrInvalidURL},
		{Hook{Name: "a", URL: "ftp://x.test"}, ErrInvalidURL},
		{Hook{Name: "a", URL: "https://x.test", Events: []string{"nope"}}, ErrInvalidEvent},
		{Hook{Name: "a", URL: "https://x.test", Headers: map[string]string{"Authorization": "a\r\nX: y"}}, ErrInvalidHeader},
	}
	for _, tc := range bad {
		if _, err := s.Put(tc.hook, now); !errors.Is(err, tc.want) {
			t.Fatalf("Put(%+v) = %v, want %v", tc.hook, err, tc.want)
		}
	}
	if _, err := s.Put(Hook{Name: "local", URL: "http://127.0.0.1:9/x"}, now); err != nil {
		t.Fatalf("loopback http rejected: %v", err)
	}
	if _, err := s.Put(Hook{Name: "main", URL: "https://x.test/h", Headers: map[string]string{"Authorization": "Bearer k"}}, now); err != nil {
		t.Fatal(err)
	}
	events := []string{EventFinished}
	if _, err := s.Patch("main", Filters{Events: &events}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "webhooks.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("webhooks.json mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	again, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, ok := again.Get("main")
	if !ok || h.Headers["Authorization"] != "Bearer k" || len(h.Events) != 1 || h.URL != "https://x.test/h" {
		t.Fatalf("reloaded hook = %+v", h)
	}
	if _, err := s.Patch("missing", Filters{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Patch(missing) = %v", err)
	}
	if ok, err := s.Delete("main"); !ok || err != nil {
		t.Fatalf("Delete = %v, %v", ok, err)
	}
}

func TestHookWants(t *testing.T) {
	all := Hook{}
	if !all.Wants(EventFinished, "p") || all.Wants(EventWorkerFinished, "p") || !all.Wants(EventSummary, "p") {
		t.Fatal("default hook should take everything but worker turns")
	}
	site := Hook{Projects: []string{"site"}, Events: []string{EventNeedsInput}}
	if !site.Wants(EventInputResolved, "site") || site.Wants(EventNeedsInput, "api") || site.Wants(EventSummary, "site") || !site.Wants(EventTest, "api") {
		t.Fatal("project and kind filters not applied")
	}
}

func TestQueueFoldsBurstsOfFinishedTurns(t *testing.T) {
	q := &queue{signal: make(chan struct{}, 1)}
	now := time.Now()
	for i := range 15 {
		q.push(Payload{Event: EventFinished, SessionID: "s", At: now.Add(time.Duration(i) * time.Second)}, now)
	}
	q.push(Payload{Event: EventNeedsInput, SessionID: "w"}, now)
	if len(q.normal) != runEndsPerMinute || q.summary == nil || q.summary.Count != 5 {
		t.Fatalf("normal=%d summary=%+v", len(q.normal), q.summary)
	}
	if p, _ := q.pop(); p.Event != EventNeedsInput {
		t.Fatalf("first pop = %s, want the question first", p.Event)
	}
	for range runEndsPerMinute {
		q.pop()
	}
	p, ok := q.pop()
	if !ok || p.Event != EventSummary || p.Count != 5 || p.LastAt == nil {
		t.Fatalf("summary pop = %+v", p)
	}
}

type fakeHub struct{ ch chan domain.NotificationEvent }

func (f *fakeHub) Subscribe(domain.ProjectID) (<-chan domain.NotificationEvent, func()) {
	return f.ch, func() {}
}

type fakeChanges struct {
	mu sync.Mutex
	fn func(cdc.Event)
}

func (f *fakeChanges) Subscribe(fn func(cdc.Event)) func() {
	f.mu.Lock()
	f.fn = fn
	f.mu.Unlock()
	return func() {}
}

func (f *fakeChanges) emit(e cdc.Event) {
	for {
		f.mu.Lock()
		fn := f.fn
		f.mu.Unlock()
		if fn != nil {
			fn(e)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type fakeSessions struct {
	mu   sync.Mutex
	recs map[domain.SessionID]domain.SessionRecord
}

func (f *fakeSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.recs[id]
	return r, ok, nil
}

func (f *fakeSessions) ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.SessionRecord{}
	for _, r := range f.recs {
		out = append(out, r)
	}
	return out, nil
}

type fakeHistory struct{}

func (fakeHistory) SessionHistory(context.Context, domain.SessionID, int, int) ([]domain.HandoffTurn, int, error) {
	return []domain.HandoffTurn{{AgentResponse: "Done. Token sk-abcdefghijklmnopqrstuvwxyz0123 kept."}}, 1, nil
}

type fakeTurns map[domain.SessionID]domain.ConversationTurn

func (f fakeTurns) ConversationForSession(_ context.Context, id domain.SessionID) (domain.ConversationRecord, error) {
	return domain.ConversationRecord{ID: "conv-" + string(id)}, nil
}

func (f fakeTurns) LatestConversationTurn(_ context.Context, conv string) (domain.ConversationTurn, error) {
	t, ok := f[domain.SessionID(strings.TrimPrefix(conv, "conv-"))]
	if !ok {
		return t, domain.ErrNoConversationTurn
	}
	return t, nil
}

type receiver struct {
	mu     sync.Mutex
	got    []Payload
	status []int
	auth   []string
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var p Payload
	_ = json.NewDecoder(req.Body).Decode(&p)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auth = append(r.auth, req.Header.Get("Authorization"))
	if len(r.status) > 0 {
		code := r.status[0]
		r.status = r.status[1:]
		if code == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "1")
		}
		w.WriteHeader(code)
		return
	}
	r.got = append(r.got, p)
}

func (r *receiver) wait(t *testing.T, n int) []Payload {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		if len(r.got) >= n {
			out := append([]Payload(nil), r.got...)
			r.mu.Unlock()
			return out
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("got %d events, want %d: %+v", len(r.got), n, r.got)
	return nil
}

func waitSent(d *Dispatcher, n int) Status {
	deadline := time.Now().Add(5 * time.Second)
	st := d.Statuses()["main"]
	for st.Sent < n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		st = d.Statuses()["main"]
	}
	return st
}

func TestDispatcherDeliversLiveEvents(t *testing.T) {
	rcv := &receiver{}
	srv := httptest.NewServer(rcv)
	defer srv.Close()
	store, _ := OpenStore(t.TempDir())
	if _, err := store.Put(Hook{Name: "main", URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer secret"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(Hook{Name: "other", URL: srv.URL + "/other", Projects: []string{"api"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sessions := &fakeSessions{recs: map[domain.SessionID]domain.SessionRecord{
		"web-orch": {ID: "web-orch", ProjectID: "web", Kind: domain.KindOrchestrator, DisplayName: "Orchestrator", Activity: domain.Activity{State: domain.ActivityActive}},
		"web-1":    {ID: "web-1", ProjectID: "web", Kind: domain.KindWorker, DisplayName: "Fix login", Activity: domain.Activity{State: domain.ActivityActive}},
		"web-2":    {ID: "web-2", ProjectID: "web", Kind: domain.KindWorker, DisplayName: "Fix tests", Activity: domain.Activity{State: domain.ActivityActive}},
	}}
	resets := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	turns := fakeTurns{"web-2": {ID: "turn-9", State: domain.TurnStateFailed, ErrorMessage: "You've hit your limit in /Users/me/app", UsageLimit: &domain.ConversationUsageLimit{ResetsAt: &resets}}}
	hub := &fakeHub{ch: make(chan domain.NotificationEvent, 8)}
	changes := &fakeChanges{}
	d := NewDispatcher(Deps{Store: store, Notifications: hub, Changes: changes, Sessions: sessions, History: fakeHistory{}, Turns: turns, HostID: "host-1"})
	d.home = "/Users/me"
	d.settle = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	changes.emit(cdc.Event{Type: cdc.EventSessionUpdated, ProjectID: "web", SessionID: "web-2", Payload: json.RawMessage(`{"id":"web-2","activity":"idle","isTerminated":false}`)})
	hub.ch <- domain.NotificationEvent{Kind: domain.NotificationCreated, Record: domain.NotificationRecord{
		SessionID: "web-1", ProjectID: "web", Type: domain.NotificationNeedsInput, Title: "Fix login needs input", Body: "Which branch? Key sk-abcdefghijklmnopqrstuvwx in /Users/me/app", CreatedAt: time.Now(),
	}}
	changes.emit(cdc.Event{Type: cdc.EventSessionUpdated, ProjectID: "web", SessionID: "web-1", Payload: json.RawMessage(`{"id":"web-1","activity":"idle","isTerminated":false}`)})
	changes.emit(cdc.Event{Type: cdc.EventSessionUpdated, ProjectID: "web", SessionID: "web-orch", Payload: json.RawMessage(`{"id":"web-orch","terminalTitle":"x"}`)})
	changes.emit(cdc.Event{Type: cdc.EventSessionUpdated, ProjectID: "web", SessionID: "web-orch", Payload: json.RawMessage(`{"id":"web-orch","activity":"idle","isTerminated":false}`)})
	sessions.mu.Lock()
	r := sessions.recs["web-1"]
	r.Activity.State = domain.ActivityExited
	sessions.recs["web-1"] = r
	sessions.mu.Unlock()
	changes.emit(cdc.Event{Type: cdc.EventSessionUpdated, ProjectID: "web", SessionID: "web-1", Payload: json.RawMessage(`{"id":"web-1","activity":"exited","isTerminated":false}`)})

	got := rcv.wait(t, 4)
	kinds := map[string]Payload{}
	for _, p := range got {
		kinds[p.Event] = p
	}
	q, ok := kinds[EventNeedsInput]
	if !ok || q.SessionName != "Fix login" || q.SessionKind != "worker" || q.Text != "Fix login needs input: Which branch? Key [key] in ~/app" || q.HostID != "host-1" {
		t.Fatalf("needs_input payload = %+v", q)
	}
	f, ok := kinds[EventFinished]
	if !ok || f.SessionID != "web-orch" || f.LastReply != "Done. Token [key] kept." {
		t.Fatalf("finished payload = %+v", f)
	}
	if _, ok := kinds[EventExited]; !ok {
		t.Fatalf("exited not delivered: %+v", got)
	}
	fl, ok := kinds[EventFailed]
	if !ok || fl.SessionID != "web-2" || fl.TurnID != "turn-9" || fl.Error != "You've hit your limit in ~/app" ||
		fl.Text != "Fix tests hit a usage limit, resets 2026-11-14T15:00:00Z" || fl.UsageLimit == nil || !fl.UsageLimit.ResetsAt.Equal(resets) {
		t.Fatalf("failed payload = %+v", fl)
	}
	if _, ok := kinds[EventWorkerFinished]; ok {
		t.Fatal("worker turn reached a default hook")
	}
	if st := waitSent(d, 4); st.Sent != 4 || st.LastError != "" {
		t.Fatalf("status = %+v, want 4 sent", st)
	}
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	for _, a := range rcv.auth {
		if a != "Bearer secret" {
			t.Fatalf("authorization header = %q", a)
		}
	}
}

func TestDispatcherPausesOnRateLimit(t *testing.T) {
	rcv := &receiver{status: []int{http.StatusTooManyRequests}}
	srv := httptest.NewServer(rcv)
	defer srv.Close()
	store, _ := OpenStore(t.TempDir())
	_, _ = store.Put(Hook{Name: "main", URL: srv.URL}, time.Now())
	hub := &fakeHub{ch: make(chan domain.NotificationEvent, 1)}
	d := NewDispatcher(Deps{Store: store, Notifications: hub, Changes: &fakeChanges{}, Sessions: &fakeSessions{}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	start := time.Now()
	hub.ch <- domain.NotificationEvent{Kind: domain.NotificationCreated, Record: domain.NotificationRecord{
		SessionID: "s", Type: domain.NotificationPRMerged, Title: "PR merged", CreatedAt: time.Now(),
	}}
	got := rcv.wait(t, 1)
	if got[0].Event != EventPRMerged || time.Since(start) < time.Second {
		t.Fatalf("got %+v after %v; want pr_merged after the 1s Retry-After", got, time.Since(start))
	}
	if st := waitSent(d, 1); st.Sent != 1 || st.LastError == "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestDispatcherSendsUrgentEventsBeforeRetries(t *testing.T) {
	old := retryBackoff
	retryBackoff = []time.Duration{300 * time.Millisecond}
	defer func() { retryBackoff = old }()
	rcv := &receiver{status: []int{http.StatusBadGateway}}
	srv := httptest.NewServer(rcv)
	defer srv.Close()
	store, _ := OpenStore(t.TempDir())
	_, _ = store.Put(Hook{Name: "main", URL: srv.URL}, time.Now())
	hub := &fakeHub{ch: make(chan domain.NotificationEvent, 2)}
	d := NewDispatcher(Deps{Store: store, Notifications: hub, Changes: &fakeChanges{}, Sessions: &fakeSessions{}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	hub.ch <- domain.NotificationEvent{Kind: domain.NotificationCreated, Record: domain.NotificationRecord{
		SessionID: "s", Type: domain.NotificationPRMerged, Title: "PR merged", CreatedAt: time.Now(),
	}}
	deadline := time.Now().Add(5 * time.Second)
	for d.Statuses()["main"].LastError == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	hub.ch <- domain.NotificationEvent{Kind: domain.NotificationCreated, Record: domain.NotificationRecord{
		SessionID: "s", Type: domain.NotificationNeedsInput, Title: "Fix login needs input", CreatedAt: time.Now(),
	}}
	got := rcv.wait(t, 2)
	if got[0].Event != EventNeedsInput || got[1].Event != EventPRMerged {
		t.Fatalf("order = %s, %s; want needs_input before the retried pr_merged", got[0].Event, got[1].Event)
	}
}

func startQueueTest(t *testing.T, codes []int, sessions *fakeSessions) (*Dispatcher, *receiver) {
	t.Helper()
	old := retryBackoff
	retryBackoff = []time.Duration{300 * time.Millisecond}
	t.Cleanup(func() { retryBackoff = old })
	rcv := &receiver{status: codes}
	srv := httptest.NewServer(rcv)
	t.Cleanup(srv.Close)
	store, _ := OpenStore(t.TempDir())
	_, _ = store.Put(Hook{Name: "main", URL: srv.URL}, time.Now())
	d := NewDispatcher(Deps{Store: store, Notifications: &fakeHub{ch: make(chan domain.NotificationEvent)}, Changes: &fakeChanges{}, Sessions: sessions})
	d.settle = 0
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go d.Run(ctx)
	return d, rcv
}

func waitError(t *testing.T, d *Dispatcher, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(d.Statuses()["main"].LastError, want) {
		if time.Now().After(deadline) {
			t.Fatalf("last error %q never contained %q", d.Statuses()["main"].LastError, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDispatcherRechecksExitBeforeRetry(t *testing.T) {
	sessions := &fakeSessions{recs: map[domain.SessionID]domain.SessionRecord{
		"s": {ID: "s", Activity: domain.Activity{State: domain.ActivityExited}},
	}}
	d, rcv := startQueueTest(t, []int{http.StatusBadGateway}, sessions)
	d.queue("main").push(Payload{Event: EventExited, SessionID: "s", At: time.Now()}, time.Now())
	waitError(t, d, "HTTP 502")
	sessions.mu.Lock()
	sessions.recs["s"] = domain.SessionRecord{ID: "s", Activity: domain.Activity{State: domain.ActivityActive}}
	sessions.mu.Unlock()
	d.queue("main").push(Payload{Event: EventPRMerged, SessionID: "s", At: time.Now()}, time.Now())
	if got := rcv.wait(t, 1); got[0].Event != EventPRMerged {
		t.Fatalf("sent %s first; the agent is running again, so the exit retry must be dropped", got[0].Event)
	}
}

func TestDispatcherDeliversAgainAfterRetryIsRejected(t *testing.T) {
	d, rcv := startQueueTest(t, []int{http.StatusBadGateway, http.StatusBadRequest}, &fakeSessions{})
	d.queue("main").push(Payload{Event: EventPRMerged, SessionID: "s", At: time.Now()}, time.Now())
	waitError(t, d, "HTTP 400")
	d.queue("main").push(Payload{Event: EventFinished, SessionID: "s", At: time.Now()}, time.Now())
	if got := rcv.wait(t, 1); got[0].Event != EventFinished {
		t.Fatalf("sent %s; want the finished turn", got[0].Event)
	}
}

func TestDispatcherTestReportsRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer srv.Close()
	store, _ := OpenStore(t.TempDir())
	_, _ = store.Put(Hook{Name: "main", URL: srv.URL}, time.Now())
	d := NewDispatcher(Deps{Store: store, Sessions: &fakeSessions{}})
	err := d.Test(context.Background(), "main")
	if !errors.Is(err, errPermanent) || d.Statuses()["main"].LastError == "" {
		t.Fatalf("Test = %v, status %+v", err, d.Statuses()["main"])
	}
	if err := d.Test(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Test(nope) = %v", err)
	}
}
