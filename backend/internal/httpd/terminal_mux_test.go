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

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/runtime/ptyexec"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/config"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/terminal"
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
	Ch   string `json:"ch"`
	ID   string `json:"id"`
	Type string `json:"type"`
	Data string `json:"data"`
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
