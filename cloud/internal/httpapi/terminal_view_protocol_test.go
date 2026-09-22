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
