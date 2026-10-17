package httpd

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/ptyexec"
	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
)

// stubSource attaches a throwaway shell command instead of a real mux pane, so
// the /mux path exercises the genuine upgrade + wsjson + Serve + creack/pty flow
// without needing a runtime. The pane reports alive until the first attach
// happens (the mux refuses to attach to a dead pane), then dead, so the
// command's exit is treated as the pane being gone (no re-attach).
type stubSource struct {
	argv     []string
	attached atomic.Bool
}

func (s *stubSource) Attach(ctx context.Context, _ ports.RuntimeHandle, rows, cols uint16) (ports.Stream, error) {
	s.attached.Store(true)
	return ptyexec.Spawn(ctx, s.argv, nil, rows, cols)
}

func (s *stubSource) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return !s.attached.Load(), nil
}

type terminalMuxFrame struct {
	Ch      string           `json:"ch"`
	ID      string           `json:"id"`
	Type    string           `json:"type"`
	Data    string           `json:"data"`
	Session *muxSessionFrame `json:"session"`
}

// muxSessionFrame is the payload of a ch "sessions" frame.
type muxSessionFrame struct {
	Seq       int64  `json:"seq"`
	SessionID string `json:"sessionId"`
}

func dialMux(t *testing.T, mgr *terminal.Manager) (*websocket.Conn, func()) {
	t.Helper()
	router := newTestRouter(config.Config{}, discardLogger(), mgr)
	ts := httptest.NewServer(router)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/mux"

	c, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		ts.Close()
		t.Fatalf("dial /mux: %v", err)
	}
	return c, func() {
		_ = c.Close(websocket.StatusNormalClosure, "test done")
		ts.Close()
	}
}

func readFrame(t *testing.T, c *websocket.Conn, ch, typ string, d time.Duration) terminalMuxFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	for {
		var f terminalMuxFrame
		if err := wsjson.Read(ctx, c, &f); err != nil {
			t.Fatalf("waiting for %s/%s: %v", ch, typ, err)
		}
		if f.Ch == ch && f.Type == typ {
			return f
		}
	}
}

func TestMuxUpgradeStreamsTerminal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY spawning not supported on Windows")
	}
	mgr := terminal.NewManager(
		&stubSource{argv: []string{"/bin/sh", "-c", "printf MUXOK; exit 0"}},
		nil, discardLogger(),
	)
	defer mgr.Close()

	c, done := dialMux(t, mgr)
	defer done()

	ctx := context.Background()
	if err := wsjson.Write(ctx, c, terminalMuxFrame{Ch: "terminal", ID: "t1", Type: "open"}); err != nil {
		t.Fatalf("write open: %v", err)
	}

	readFrame(t, c, "terminal", "opened", 3*time.Second)

	data := readFrame(t, c, "terminal", "data", 5*time.Second)
	got, _ := base64.StdEncoding.DecodeString(data.Data)
	if !strings.Contains(string(got), "MUXOK") {
		t.Fatalf("streamed data = %q, want it to contain MUXOK", got)
	}

	// The shell exits; the pane is reported gone (IsAlive=false) so we get exited.
	readFrame(t, c, "terminal", "exited", 5*time.Second)
}

func TestMuxSystemPingPong(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY spawning not supported on Windows")
	}
	mgr := terminal.NewManager(&stubSource{argv: []string{"/bin/sh"}}, nil, discardLogger())
	defer mgr.Close()

	c, done := dialMux(t, mgr)
	defer done()

	ctx := context.Background()
	if err := wsjson.Write(ctx, c, map[string]string{"ch": "system", "type": "ping"}); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	readFrame(t, c, "system", "pong", 3*time.Second)
}

// sessionFramesBeforePong sends a system ping and returns the ch "sessions"
// frames that arrive before its pong. The daemon handles one connection's frames
// in order and writes its queue in order, so the pong means every frame sent
// before the ping has been handled, and any frame the daemon queued before it
// handled the ping has already arrived.
func sessionFramesBeforePong(t *testing.T, c *websocket.Conn) []terminalMuxFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, c, map[string]string{"ch": "system", "type": "ping"}); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	var sessions []terminalMuxFrame
	for {
		var f terminalMuxFrame
		if err := wsjson.Read(ctx, c, &f); err != nil {
			t.Fatalf("waiting for pong: %v", err)
		}
		switch {
		case f.Ch == "system" && f.Type == "pong":
			return sessions
		case f.Ch == "sessions":
			sessions = append(sessions, f)
		}
	}
}

// TestMuxSubscribeFrame sends the subscribe frame as raw text through the real
// upgrade and JSON decode, the way the mobile client does, and counts the
// session frames the daemon pushes for one CDC event.
func TestMuxSubscribeFrame(t *testing.T) {
	tests := []struct {
		name  string
		frame string
		want  int
	}{
		// The frame packages/mobile/lib/mux.ts sends; mux.test.ts pins the same literal.
		{"typed frame subscribes", `{"ch":"subscribe","type":"subscribe","topics":["sessions","notifications"]}`, 1},
		// The frame mobile sent before it carried type. The daemon routes on type
		// and drops it with no error. It is deliberately not accepted: no build
		// ever sent it (subscribeSessions had no caller when this test was
		// written), so accepting it would heal no client and would loosen the one
		// check every other channel obeys.
		{"frame without type is ignored", `{"ch":"subscribe","topics":["sessions","notifications"]}`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bc := cdc.NewBroadcaster()
			mgr := terminal.NewManager(&stubSource{argv: []string{"/bin/sh"}}, bc, discardLogger())
			defer mgr.Close()
			c, done := dialMux(t, mgr)
			defer done()

			if err := c.Write(context.Background(), websocket.MessageText, []byte(tt.frame)); err != nil {
				t.Fatalf("write subscribe: %v", err)
			}
			sessionFramesBeforePong(t, c) // returns once the subscribe frame has been handled
			bc.Publish(cdc.Event{Seq: 9, ProjectID: "p1", SessionID: "s1", Type: cdc.EventSessionUpdated})

			got := sessionFramesBeforePong(t, c)
			if len(got) != tt.want {
				t.Fatalf("session frames after one CDC event = %d, want %d", len(got), tt.want)
			}
			if tt.want == 1 && (got[0].Session == nil || got[0].Session.Seq != 9 || got[0].Session.SessionID != "s1") {
				t.Fatalf("session frame = %+v, want seq 9 for session s1", got[0].Session)
			}
		})
	}
}
