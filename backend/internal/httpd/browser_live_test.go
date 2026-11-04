package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserstream"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/preview"
)

type browserLiveSessionStub struct{ calls int }

func (s *browserLiveSessionStub) Get(context.Context, domain.SessionID) (domain.Session, error) {
	s.calls++
	return domain.Session{}, nil
}

func TestBrowserLiveIsLANOnly(t *testing.T) {
	sessions := &browserLiveSessionStub{}
	hub := NewBrowserLiveHub(nil, sessions, func() bool { return true }, nil)
	r := chi.NewRouter()
	mountBrowserLive(r, hub)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s1/browser/live", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("loopback status = %d, want 404", res.Code)
	}
	if sessions.calls != 0 {
		t.Fatalf("session lookup calls = %d, want 0", sessions.calls)
	}
}

func TestBrowserLiveRequiresDesktopOptIn(t *testing.T) {
	sessions := &browserLiveSessionStub{}
	hub := NewBrowserLiveHub(nil, sessions, func() bool { return false }, nil)
	r := chi.NewRouter()
	mountBrowserLive(r, hub)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s1/browser/live", nil)
	req = req.WithContext(requestscope.WithLAN(req.Context()))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("disabled status = %d, want 403", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"code":"BROWSER_REMOTE_DISABLED"`) {
		t.Fatalf("disabled sharing error = %s", res.Body.String())
	}
	if sessions.calls != 0 {
		t.Fatalf("session lookup calls = %d, want 0", sessions.calls)
	}
}

func TestBrowserLiveHTTPProbeDoesNotReserveOrStartBrowser(t *testing.T) {
	sessions := &browserLiveSessionStub{}
	hub := NewBrowserLiveHub(nil, sessions, func() bool { return true }, nil)
	r := chi.NewRouter()
	mountBrowserLive(r, hub)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s1/browser/live", nil)
	req = req.WithContext(requestscope.WithLAN(req.Context()))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusUpgradeRequired {
		t.Fatalf("plain HTTP status = %d, want 426", res.Code)
	}
	if sessions.calls != 0 {
		t.Fatalf("session lookup calls = %d, want 0", sessions.calls)
	}
	_, release, ok := hub.reserve("s1")
	if !ok {
		t.Fatal("plain HTTP request reserved a lease")
	}
	release()
}

func TestBrowserLiveRechecksDesktopOptInAfterReserve(t *testing.T) {
	sessions := &browserLiveSessionStub{}
	enabledCalls := 0
	hub := NewBrowserLiveHub(nil, sessions, func() bool {
		enabledCalls++
		return enabledCalls == 1
	}, nil)
	r := chi.NewRouter()
	mountBrowserLive(r, hub)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/s1/browser/live", nil)
	req.Header.Set("Upgrade", "websocket")
	req = req.WithContext(requestscope.WithLAN(req.Context()))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("disabled-after-reserve status = %d, want 403", res.Code)
	}
	if enabledCalls != 2 {
		t.Fatalf("enabled calls = %d, want 2", enabledCalls)
	}
	if sessions.calls != 1 {
		t.Fatalf("session lookup calls = %d, want 1", sessions.calls)
	}
	_, release, ok := hub.reserve("s1")
	if !ok {
		t.Fatal("lease was not released after sharing was disabled")
	}
	release()
}

func TestBrowserLiveLeaseIsExclusiveAndRevocable(t *testing.T) {
	hub := NewBrowserLiveHub(nil, nil, nil, nil)
	ctx, release, ok := hub.reserve("s1")
	if !ok {
		t.Fatal("first lease was rejected")
	}
	if _, _, ok := hub.reserve("s1"); ok {
		t.Fatal("second lease was accepted")
	}
	hub.CloseAll()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("CloseAll did not revoke lease")
	}
	release()
	_, releaseAgain, ok := hub.reserve("s1")
	if !ok {
		t.Fatal("lease was not reusable after release")
	}
	releaseAgain()
}

func TestBrowserLivePreviewTargetUsesOnlyStoredSelection(t *testing.T) {
	workspace, artifacts := t.TempDir(), t.TempDir()
	for _, root := range []string{workspace, artifacts} {
		if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>hi</h1>"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	workspaceURL, err := preview.FileURL("http://127.0.0.1:4317", "s1", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	artifactURL, err := preview.ArtifactFileURL("http://127.0.0.1:4317", "s1", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	oldArtifactURL, err := preview.ArtifactFileURL("http://127.0.0.1:9999", "s1", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, selection, want string
	}{
		{"unselected repository index", "", ""},
		{"cleared selection", "  ", ""},
		{"running app", " http://localhost:5173/ ", "http://localhost:5173/"},
		{"external page", "https://example.com/app", "https://example.com/app"},
		{"workspace file", "index.html", workspaceURL},
		{"legacy workspace URL", "http://localhost:9999/api/v1/sessions/s1/preview/files/index.html", workspaceURL},
		{"artifact file", "__ao_artifacts__/index.html", artifactURL},
		{"artifact URL with stale port", oldArtifactURL, artifactURL},
		{"missing selected file", "missing.html", ""},
		{"unsupported scheme", "file:///tmp/index.html", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hub := NewBrowserLiveHub(nil, nil, nil, nil)
			hub.SetLoopbackBaseURL("http://127.0.0.1:4317")
			session := domain.Session{ID: "s1", Metadata: domain.SessionMetadata{
				PreviewURL: tt.selection, WorkspacePath: workspace, ArtifactDir: artifacts,
			}}
			if got := hub.previewTarget(session); got != tt.want {
				t.Fatalf("preview target = %q, want %q", got, tt.want)
			}
		})
	}

	// A same-named workspace file cannot substitute for a missing artifact.
	hub := NewBrowserLiveHub(nil, nil, nil, nil)
	hub.SetLoopbackBaseURL("http://127.0.0.1:4317")
	session := domain.Session{ID: "s1", Metadata: domain.SessionMetadata{
		PreviewURL: oldArtifactURL, WorkspacePath: workspace, ArtifactDir: t.TempDir(),
	}}
	if got := hub.previewTarget(session); got != "" {
		t.Fatalf("missing artifact target = %q, want empty", got)
	}
	session.Metadata.PreviewURL = "index.html"
	hub.SetLoopbackBaseURL("")
	if got := hub.previewTarget(session); got != "" {
		t.Fatalf("file target without loopback origin = %q, want empty", got)
	}
}

// A phone can start watching before the agent has produced a page. Keep the
// stream open and verify the newly selected entry is offered without any
// desktop panel being mounted or a mobile reconnect.
type livePreviewSession struct {
	mu        sync.Mutex
	workspace string
	selection string
	lookedUp  chan struct{}
	once      sync.Once
}

func (s *livePreviewSession) Get(context.Context, domain.SessionID) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.once.Do(func() { close(s.lookedUp) })
	return domain.Session{ID: "s1", Metadata: domain.SessionMetadata{WorkspacePath: s.workspace, PreviewURL: s.selection}}, nil
}

type livePreviewBroker struct{ offers chan string }

func (*livePreviewBroker) Subscribe(context.Context, string) (*browserstream.Subscription, error) {
	return nil, nil
}
func (b *livePreviewBroker) Send(ctx context.Context, _ string, kind string, payload any) error {
	if kind == "preview" {
		select {
		case b.offers <- payload.(map[string]string)["url"]:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func TestBrowserLiveOffersPreviewSelectedAfterConnection(t *testing.T) {
	workspace := t.TempDir()
	sessions := &livePreviewSession{workspace: workspace, lookedUp: make(chan struct{})}
	broker := &livePreviewBroker{offers: make(chan string, 4)}
	hub := NewBrowserLiveHub(broker, sessions, nil, nil)
	hub.SetLoopbackBaseURL("http://127.0.0.1:4317")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); hub.watchPreview(ctx, "s1") }()
	<-sessions.lookedUp
	if err := os.WriteFile(filepath.Join(workspace, "index.html"), []byte("<h1>new page</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions.mu.Lock()
	sessions.selection = "index.html"
	sessions.mu.Unlock()
	for range 2 {
		select {
		case target := <-broker.offers:
			if !strings.HasSuffix(target, ":4317/index.html") {
				t.Fatalf("offered target = %q", target)
			}
		case <-time.After(6 * time.Second):
			t.Fatal("selected preview was not offered or retried")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("preview polling survived stream cancellation")
	}
}

func TestBrowserWriterSendsControlsBeforePendingFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	controls := make(chan browserstream.Event, 2)
	frames := make(chan browserstream.Event, 1)
	controls <- browserstream.Event{Type: "state", Payload: []byte(`{"url":""}`)}
	controls <- browserstream.Event{Type: "state", Payload: []byte(`{"url":"https://example.com"}`)}
	frames <- browserstream.Event{Frame: &browserstream.Frame{Sequence: 100, Width: 10, Height: 10, Captured: time.Now(), JPEG: []byte{1}}}
	hub := NewBrowserLiveHub(nil, nil, nil, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_ = hub.writeBrowserEvents(ctx, conn, controls, frames)
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = conn.CloseNow() }()
	for _, wantURL := range []string{"", "https://example.com"} {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kind != websocket.MessageText {
			t.Fatalf("received frame before state: %v", kind)
		}
		var message struct {
			Type    string
			Payload struct{ URL string }
		}
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type != "state" || message.Payload.URL != wantURL {
			t.Fatalf("control = %s", data)
		}
	}
	kind, _, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageBinary {
		t.Fatalf("frame kind=%v err=%v", kind, err)
	}
}
