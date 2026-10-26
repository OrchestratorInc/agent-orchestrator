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

// liveTestStore serves no events and a live state the test controls, and
// records presence calls.
type liveTestStore struct {
	Store
	mu        sync.Mutex
	viewers   []domain.SessionViewer
	joined    []string
	refreshed []string
	left      []string
	leftCh    chan string
}

func newLiveTestStore() *liveTestStore {
	return &liveTestStore{leftCh: make(chan string, 4)}
}

func (s *liveTestStore) ListClientEvents(context.Context, domain.Principal, string, string, int64, int, []string) ([]domain.ClientEvent, bool, error) {
	return nil, false, nil
}

func (s *liveTestStore) JoinSessionViewers(_ context.Context, _ domain.Principal, _, _ string, connectionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.joined = append(s.joined, connectionID)
	return nil
}

func (s *liveTestStore) RefreshSessionViewer(_ context.Context, _ string, connectionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshed = append(s.refreshed, connectionID)
	return nil
}

func (s *liveTestStore) LeaveSessionViewers(_ context.Context, _ string, connectionID string) error {
	s.mu.Lock()
	s.left = append(s.left, connectionID)
	s.mu.Unlock()
	s.leftCh <- connectionID
	return nil
}

func (s *liveTestStore) SessionLiveState(context.Context, domain.Principal, string, string) (domain.SessionLiveState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return domain.SessionLiveState{Viewers: append([]domain.SessionViewer(nil), s.viewers...)}, nil
}

func (s *liveTestStore) setViewers(viewers ...domain.SessionViewer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.viewers = viewers
}

func (s *liveTestStore) joinedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.joined...)
}

type sseFrame struct {
	event string
	id    string
	data  string
}

// sseFrames parses frames from a stream in the background; comments and the
// retry preamble are skipped.
func sseFrames(reader *bufio.Reader) <-chan sseFrame {
	frames := make(chan sseFrame, 16)
	go func() {
		defer close(frames)
		var frame sseFrame
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case line == "":
				if frame.data != "" {
					frames <- frame
				}
				frame = sseFrame{}
			case strings.HasPrefix(line, "event: "):
				frame.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "id: "):
				frame.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "data: "):
				frame.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return frames
}

func nextFrame(t *testing.T, frames <-chan sseFrame, within time.Duration) sseFrame {
	t.Helper()
	select {
	case frame, ok := <-frames:
		if !ok {
			t.Fatal("stream closed")
		}
		return frame
	case <-time.After(within):
		t.Fatalf("no frame within %s", within)
		return sseFrame{}
	}
}

func expectNoFrame(t *testing.T, frames <-chan sseFrame, within time.Duration) {
	t.Helper()
	select {
	case frame, ok := <-frames:
		if ok {
			t.Fatalf("unexpected frame %+v", frame)
		}
	case <-time.After(within):
	}
}

func liveTestServer(t *testing.T, store Store) (*Server, *httptest.Server) {
	t.Helper()
	server := &Server{
		store:               store,
		logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		drain:               make(chan struct{}),
		sessionEventWaiters: newWorkWaiters(),
		sessionLiveWaiters:  newWorkWaiters(),
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

func openLiveStream(t *testing.T, httpServer *httptest.Server, query string) (<-chan sseFrame, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		httpServer.URL+"/orgs/"+sessionEventsTestOrg+"/sessions/"+sessionEventsTestSession+"/events"+query, nil)
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
	return sseFrames(bufio.NewReader(response.Body)), cancel
}

func decodeLiveFrame(t *testing.T, frame sseFrame) sessionLiveResponse {
	t.Helper()
	if frame.event != "session.live" || frame.id != "" {
		t.Fatalf("frame = %+v, want an id-less session.live frame", frame)
	}
	var live sessionLiveResponse
	if err := json.Unmarshal([]byte(frame.data), &live); err != nil {
		t.Fatalf("decode %q: %v", frame.data, err)
	}
	if live.Type != "session.live" {
		t.Fatalf("type = %q", live.Type)
	}
	return live
}

// A stream that does not opt in never records presence and never receives a
// live frame, so clients that predate live frames see no change.
func TestEventStreamWithoutLiveSendsNoLiveFrames(t *testing.T) {
	t.Parallel()
	store := newLiveTestStore()
	store.setViewers(domain.SessionViewer{UserID: "user-2", Name: "Bob", Role: "editor"})
	server, httpServer := liveTestServer(t, store)
	frames, _ := openLiveStream(t, httpServer, "")
	server.HandleSessionLiveNotify(sessionEventsTestSession)
	expectNoFrame(t, frames, 300*time.Millisecond)
	if joined := store.joinedIDs(); len(joined) != 0 {
		t.Fatalf("joined without opting in: %v", joined)
	}
}

// A live stream counts its owner as present, sends the current viewers at
// once, sends a new frame only when they change, and leaves when it closes.
func TestLiveEventStreamTracksViewers(t *testing.T) {
	t.Parallel()
	store := newLiveTestStore()
	alice := domain.SessionViewer{UserID: "user-1", Name: "Alice", AvatarURL: "https://lh3.googleusercontent.com/alice", Role: "member"}
	bob := domain.SessionViewer{UserID: "user-2", Name: "Bob", Role: "editor"}
	store.setViewers(alice)
	server, httpServer := liveTestServer(t, store)
	frames, cancel := openLiveStream(t, httpServer, "?live=1")

	first := decodeLiveFrame(t, nextFrame(t, frames, 2*time.Second))
	if first.You != "user-1" {
		t.Fatalf("first frame names the stream's user %q, want user-1", first.You)
	}
	if len(first.Viewers) != 1 || first.Viewers[0] != (sessionViewerResponse{UserID: "user-1", Name: "Alice", AvatarURL: "https://lh3.googleusercontent.com/alice", Role: "member"}) {
		t.Fatalf("first frame viewers = %+v", first.Viewers)
	}
	joined := store.joinedIDs()
	if len(joined) != 1 {
		t.Fatalf("joins = %v", joined)
	}

	// A wake that changes nothing sends nothing.
	server.HandleSessionLiveNotify(sessionEventsTestSession)
	expectNoFrame(t, frames, 300*time.Millisecond)

	store.setViewers(alice, bob)
	server.HandleSessionLiveNotify(sessionEventsTestSession)
	second := decodeLiveFrame(t, nextFrame(t, frames, 2*time.Second))
	if len(second.Viewers) != 2 || second.Viewers[1].Name != "Bob" {
		t.Fatalf("second frame viewers = %+v", second.Viewers)
	}

	cancel()
	select {
	case left := <-store.leftCh:
		if left != joined[0] {
			t.Fatalf("left %q, joined %q", left, joined[0])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing the stream did not leave the viewer list")
	}
}

// A store without live support serves a live request as a plain stream.
func TestLiveEventStreamWithoutStoreSupport(t *testing.T) {
	t.Parallel()
	store := &growingEventStore{}
	_, httpServer := liveTestServer(t, store)
	frames, _ := openLiveStream(t, httpServer, "?live=1")
	store.append(1)
	frame := nextFrame(t, frames, 5*time.Second)
	if frame.event != "chat.assistant_delta" || frame.id != "1" {
		t.Fatalf("frame = %+v", frame)
	}
}
