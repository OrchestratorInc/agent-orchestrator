package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

const (
	sessionEventsTestOrg     = "00000000-0000-4000-8000-0000000000b1"
	sessionEventsTestSession = "00000000-0000-4000-8000-0000000000b2"
)

// growingEventStore serves the events appended so far after the requested
// sequence and counts its reads.
type growingEventStore struct {
	Store
	mu     sync.Mutex
	events []domain.ClientEvent
	reads  int
}

func (s *growingEventStore) ListClientEvents(_ context.Context, _ domain.Principal, _, _ string, after int64, _ int, _ []string) ([]domain.ClientEvent, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	var out []domain.ClientEvent
	for _, event := range s.events {
		if event.Sequence > after {
			out = append(out, event)
		}
	}
	return out, false, nil
}

func (s *growingEventStore) append(sequence int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, domain.ClientEvent{
		SessionID: sessionEventsTestSession, Sequence: sequence, Type: "chat.assistant_delta",
		Payload: json.RawMessage(`{"text":"hi"}`), CreatedAt: time.Now(),
	})
}

func (s *growingEventStore) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

func sessionEventsTestServer(t *testing.T, store Store) (*Server, *httptest.Server) {
	t.Helper()
	server := &Server{
		store:               store,
		logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		drain:               make(chan struct{}),
		sessionEventWaiters: newWorkWaiters(),
	}
	server.EnableSessionEventNotify()
	router := chi.NewRouter()
	router.Get("/orgs/{orgId}/sessions/{sessionId}/events", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), principalKey, domain.Principal{UserID: "user-1"}))
		server.streamClientEvents(w, r)
	})
	httpServer := httptest.NewServer(router)
	t.Cleanup(httpServer.Close)
	return server, httpServer
}

func openSessionEventStream(t *testing.T, httpServer *httptest.Server) *bufio.Reader {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		httpServer.URL+"/orgs/"+sessionEventsTestOrg+"/sessions/"+sessionEventsTestSession+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := httpServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	return bufio.NewReader(response.Body)
}

// nextEventID reads SSE lines until an id: field and returns it.
func nextEventID(t *testing.T, reader *bufio.Reader, within time.Duration) string {
	t.Helper()
	found := make(chan string, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				close(found)
				return
			}
			if id, ok := strings.CutPrefix(strings.TrimSpace(line), "id: "); ok {
				found <- id
				return
			}
		}
	}()
	select {
	case id, ok := <-found:
		if !ok {
			t.Fatal("stream closed before an event")
		}
		return id
	case <-time.After(within):
		t.Fatalf("no event within %s", within)
		return ""
	}
}

// With the listener running, a notification delivers a new event well before
// the fallback poll would have read it.
func TestSessionEventStreamWakesOnNotify(t *testing.T) {
	t.Parallel()
	store := &growingEventStore{}
	server, httpServer := sessionEventsTestServer(t, store)
	reader := openSessionEventStream(t, httpServer)

	// Wait for the stream's subscription before appending.
	deadline := time.Now().Add(2 * time.Second)
	for store.readCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	store.append(1)
	server.HandleSessionEventNotify(sessionEventsTestSession)
	if id := nextEventID(t, reader, sessionEventFallbackPoll/2); id != "1" {
		t.Fatalf("event id = %q, want 1", id)
	}
}

// A burst of notifications, like streamed assistant output, is folded into a
// bounded number of reads instead of one read per notification.
func TestSessionEventStreamBatchesABurstOfWakes(t *testing.T) {
	t.Parallel()
	store := &growingEventStore{}
	server, httpServer := sessionEventsTestServer(t, store)
	reader := openSessionEventStream(t, httpServer)
	deadline := time.Now().Add(2 * time.Second)
	for store.readCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	before := store.readCount()

	for sequence := int64(1); sequence <= 50; sequence++ {
		store.append(sequence)
		server.HandleSessionEventNotify(sessionEventsTestSession)
	}
	if id := nextEventID(t, reader, time.Second); id != "1" {
		t.Fatalf("first event id = %q", id)
	}
	time.Sleep(3 * sessionEventMinInterval)
	if reads := store.readCount() - before; reads > 4 {
		t.Fatalf("50 notifications caused %d reads, want them batched", reads)
	}
}

// Another session's notification does not wake this stream.
func TestSessionEventNotifyIsScopedToItsSession(t *testing.T) {
	t.Parallel()
	waiters := newWorkWaiters()
	server := &Server{sessionEventWaiters: waiters}
	wake, _, cancel := server.subscribeSessionEvents(sessionEventsTestSession)
	defer cancel()
	server.HandleSessionEventNotify("00000000-0000-4000-8000-0000000000ff")
	select {
	case <-wake:
		t.Fatal("woken by another session's event")
	default:
	}
	server.HandleSessionEventNotify(sessionEventsTestSession)
	select {
	case <-wake:
	default:
		t.Fatal("not woken by its own session's event")
	}
}

// Without a listener the stream keeps the one-second poll.
func TestSessionEventPollFallsBackWithoutAListener(t *testing.T) {
	t.Parallel()
	server := &Server{sessionEventWaiters: newWorkWaiters()}
	_, poll, cancel := server.subscribeSessionEvents(sessionEventsTestSession)
	cancel()
	if poll != sessionEventPoll {
		t.Fatalf("poll without listener = %s", poll)
	}
	server.EnableSessionEventNotify()
	_, poll, cancel = server.subscribeSessionEvents(sessionEventsTestSession)
	cancel()
	if poll != sessionEventFallbackPoll {
		t.Fatalf("poll with listener = %s", poll)
	}
}
