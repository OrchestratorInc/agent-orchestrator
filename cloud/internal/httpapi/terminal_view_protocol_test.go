package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/terminalview"
	"github.com/coder/websocket"
)

func TestParseTerminalViewerFrame(t *testing.T) {
	for _, tt := range []struct {
		name  string
		frame string
		want  terminalview.Viewer
		valid bool
	}{
		{"primary", `{"type":"viewer","role":"primary","visible":true,"columns":120,"rows":40}`, terminalview.Viewer{Role: "primary", Visible: true, Columns: 120, Rows: 40}, true},
		{"unmeasured secondary", `{"type":"viewer","role":"secondary","visible":false,"columns":0,"rows":0}`, terminalview.Viewer{Role: "secondary"}, true},
		{"missing visible", `{"type":"viewer","role":"secondary","columns":55,"rows":39}`, terminalview.Viewer{}, false},
		{"wrong type", `{"type":"resize","role":"primary","visible":true,"columns":120,"rows":40}`, terminalview.Viewer{}, false},
		{"bad role", `{"type":"viewer","role":"orchestrator","visible":true,"columns":120,"rows":40}`, terminalview.Viewer{}, false},
		{"fractional fit", `{"type":"viewer","role":"primary","visible":true,"columns":120.5,"rows":40}`, terminalview.Viewer{}, false},
		{"large fit", `{"type":"viewer","role":"primary","visible":true,"columns":65536,"rows":40}`, terminalview.Viewer{}, false},
		{"missing rows", `{"type":"viewer","role":"primary","visible":true,"columns":120}`, terminalview.Viewer{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTerminalViewerFrame([]byte(tt.frame))
			if (err == nil) != tt.valid || (tt.valid && got != tt.want) {
				t.Fatalf("got %+v, err %v; want %+v, valid %v", got, err, tt.want, tt.valid)
			}
		})
	}
}

type viewerOutputStore struct {
	Store
	mu   sync.Mutex
	grid terminalview.Grid
}

type viewerAttachStore struct {
	Store
	mu       sync.Mutex
	viewers  map[string]terminalview.Viewer
	released chan struct{}
}

func (*viewerAttachStore) OpenTerminal(_ context.Context, _, _ string, _ time.Duration) (domain.TerminalSession, error) {
	return domain.TerminalSession{ID: "term", OrgID: "org", SessionID: "session", WorkerEpoch: 1, Kind: "agent", Scopes: []string{"terminal:view"}}, nil
}

func (*viewerAttachStore) RefreshTerminalInteraction(context.Context, domain.TerminalSession, time.Duration) error {
	return nil
}

func (*viewerAttachStore) TerminalGrid(context.Context, domain.TerminalSession) (terminalview.Grid, error) {
	return terminalview.Grid{}, nil
}

func (*viewerAttachStore) ListTerminalOutput(context.Context, domain.TerminalSession, int64, int) ([]domain.TerminalOutput, string, error) {
	return nil, "open", nil
}

func (s *viewerAttachStore) UpsertTerminalViewer(_ context.Context, _ domain.TerminalSession, id string, viewer terminalview.Viewer, _ time.Duration) (terminalview.Grid, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.viewers == nil {
		s.viewers = make(map[string]terminalview.Viewer)
	}
	s.viewers[id] = viewer
	return terminalview.Grid{}, nil
}

func (s *viewerAttachStore) RemoveTerminalViewer(_ context.Context, _ domain.TerminalSession, id string) (terminalview.Grid, error) {
	s.mu.Lock()
	delete(s.viewers, id)
	s.mu.Unlock()
	close(s.released)
	return terminalview.Grid{}, nil
}

func TestTerminalViewerAttachRejectsInvalidFirstFrame(t *testing.T) {
	store := &viewerAttachStore{released: make(chan struct{})}
	server := &Server{store: store, logger: slog.Default(), terminalStreams: newTerminalStreams()}
	listener := httptest.NewServer(http.HandlerFunc(server.connectTerminal))
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(listener.URL, "http")+"?ticket=t&kind=agent&protocol=3", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	_, _, err = connection.Read(ctx) // reset
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","columns":80,"rows":24}`)); err != nil {
		t.Fatal(err)
	}
	_, _, err = connection.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusProtocolError {
		t.Fatalf("close status %v, error %v", websocket.CloseStatus(err), err)
	}
}

func TestTerminalViewerAttachAllowsReadOnlyUpdateAndReleasesOnClose(t *testing.T) {
	store := &viewerAttachStore{released: make(chan struct{})}
	server := &Server{store: store, logger: slog.Default(), terminalStreams: newTerminalStreams()}
	listener := httptest.NewServer(http.HandlerFunc(server.connectTerminal))
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(listener.URL, "http")+"?ticket=t&kind=agent&protocol=3", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	_, _, err = connection.Read(ctx) // reset
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range []string{
		`{"type":"viewer","role":"secondary","visible":true,"columns":0,"rows":0}`,
		`{"type":"viewer","role":"secondary","visible":false,"columns":55,"rows":39}`,
	} {
		if err := connection.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	// A read-only attachment can update its sizing lease, but not send input.
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"type":"input","data":"x"}`)); err != nil {
		t.Fatal(err)
	}
	_, _, err = connection.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("close status %v, error %v", websocket.CloseStatus(err), err)
	}
	select {
	case <-store.released:
	case <-time.After(time.Second):
		t.Fatal("viewer lease not released")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.viewers) != 0 {
		t.Fatalf("viewers still attached: %+v", store.viewers)
	}
}

func (s *viewerOutputStore) TerminalGrid(context.Context, domain.TerminalSession) (terminalview.Grid, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.grid, nil
}

func (s *viewerOutputStore) setGrid(grid terminalview.Grid) {
	s.mu.Lock()
	s.grid = grid
	s.mu.Unlock()
}

func (*viewerOutputStore) ListTerminalOutput(_ context.Context, _ domain.TerminalSession, after int64, _ int) ([]domain.TerminalOutput, string, error) {
	if after == 0 {
		return []domain.TerminalOutput{{Sequence: 1, Data: []byte("ready")}}, "open", nil
	}
	return nil, "open", nil
}

func TestTerminalViewerSizePrecedesReplayAndChangesAcrossReplicas(t *testing.T) {
	store := &viewerOutputStore{}
	server := &Server{store: store, logger: slog.Default(), terminalStreams: newTerminalStreams()}
	terminal := domain.TerminalSession{ID: "term", OrgID: "org", SessionID: "session", WorkerEpoch: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			result <- err
			return
		}
		defer connection.CloseNow()
		var writeMu sync.Mutex
		result <- server.writeTerminalOutput(ctx, connection, terminal, 0, true, &writeMu, true)
	}))
	defer listener.Close()
	readCtx, stopRead := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopRead()
	connection, _, err := websocket.Dial(readCtx, "ws"+strings.TrimPrefix(listener.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	store.setGrid(terminalview.Grid{Columns: 120, Rows: 40})
	server.HandleTerminalSizeNotify(terminal.ID)
	for index, expected := range []string{"size", "ready", "output", "replay_complete"} {
		_, payload, err := connection.Read(readCtx)
		if err != nil {
			t.Fatal(err)
		}
		var frame terminalServerMessage
		if err := json.Unmarshal(payload, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type != expected {
			t.Fatalf("frame %d = %s; want %s", index, frame.Type, expected)
		}
		if frame.Type == "size" && (frame.Columns != 120 || frame.Rows != 40) {
			t.Fatalf("initial size %+v", frame)
		}
		if frame.Type == "output" && frame.Data != base64.StdEncoding.EncodeToString([]byte("ready")) {
			t.Fatalf("output %+v", frame)
		}
	}
	store.setGrid(terminalview.Grid{Columns: 55, Rows: 39})
	server.HandleTerminalSizeNotify(terminal.ID)
	_, payload, err := connection.Read(readCtx)
	if err != nil {
		t.Fatal(err)
	}
	var size terminalServerMessage
	if err := json.Unmarshal(payload, &size); err != nil {
		t.Fatal(err)
	}
	if size.Type != "size" || size.Columns != 55 || size.Rows != 39 {
		t.Fatalf("handoff size %+v", size)
	}
	cancel()
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("output pump did not stop")
	}
}
