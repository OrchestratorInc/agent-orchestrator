package workertransport

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
)

func receiveSequence(t *testing.T, output chan int64) int64 {
	t.Helper()
	select {
	case sequence := <-output:
		return sequence
	case <-time.After(5 * time.Second):
		t.Fatal("no output was published over HTTP")
	}
	return 0
}

func expectNoSequence(t *testing.T, output chan int64) {
	t.Helper()
	select {
	case sequence := <-output:
		t.Fatalf("unexpected HTTP publish of frame %d", sequence)
	case <-time.After(100 * time.Millisecond):
	}
}

// A stream write that succeeds only means the bytes left the worker. When the
// stream then breaks, every frame the control plane never acknowledged is
// resent through HTTP ahead of the frame that found the stream broken, in
// order. Before, those frames were lost and every later frame landed out of
// sequence.
func TestPublishTerminalOutputResendsUnacknowledgedFramesInOrder(t *testing.T) {
	received := make(chan worker.TerminalStreamFrame, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for range 3 {
			received <- readFrame(t, conn)
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, _, err := websocket.Dial(ctx, server.URL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	stream := &terminalStream{conn: conn, ctx: ctx}
	terminal := &terminalProcess{}
	terminal.stream.Store(stream)
	control := &outputSequenceControl{output: make(chan int64, 8)}
	supervisor := &Supervisor{Control: control, Logger: slog.Default()}

	for id := int64(1); id <= 3; id++ {
		supervisor.publishTerminalOutput(ctx, "terminal-1", terminal, id, []byte("frame"))
		if frame := <-received; frame.ID != id {
			t.Fatalf("stream frame = %+v, want id %d", frame, id)
		}
	}
	// The control plane stored frame 1 only; then the socket dies.
	stream.acknowledgeOutput(1)
	_ = conn.CloseNow()
	expectNoSequence(t, control.output)

	supervisor.publishTerminalOutput(ctx, "terminal-1", terminal, 4, []byte("frame"))
	for _, want := range []int64{2, 3, 4} {
		if got := receiveSequence(t, control.output); got != want {
			t.Fatalf("HTTP resend order: got frame %d, want %d", got, want)
		}
	}
	expectNoSequence(t, control.output)
}

// A stream that drops while the PTY is quiet must not strand the frames it
// wrote: they are resent through HTTP as soon as the stream retires, before
// any newer frame.
func TestRunTerminalStreamResendsUnacknowledgedFramesWhenStreamDrops(t *testing.T) {
	received := make(chan worker.TerminalStreamFrame, 1)
	var dials atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if dials.Add(1) > 1 {
			// The redial after the drop: hold it open until the test ends.
			<-r.Context().Done()
			return
		}
		received <- readFrame(t, conn)
		_ = conn.Close(websocket.StatusInternalError, "control plane restarting")
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	terminal := &terminalProcess{cancel: func() {}, cleanup: func() {}}
	control := &outputSequenceControl{output: make(chan int64, 8)}
	supervisor := &Supervisor{
		Control: control,
		Streams: &wsDialer{url: server.URL},
		Logger:  slog.Default(),
	}
	go supervisor.runTerminalStream(ctx, "terminal-1", terminal)
	deadline := time.Now().Add(5 * time.Second)
	for terminal.stream.Load() == nil {
		if time.Now().After(deadline) {
			t.Fatal("worker never dialed the stream")
		}
		time.Sleep(10 * time.Millisecond)
	}
	supervisor.publishTerminalOutput(ctx, "terminal-1", terminal, 7, []byte("unacked"))
	if frame := <-received; frame.ID != 7 {
		t.Fatalf("stream frame = %+v, want id 7", frame)
	}
	if got := receiveSequence(t, control.output); got != 7 {
		t.Fatalf("resent frame %d, want 7", got)
	}
}

// An acknowledgement covers every earlier frame, so nothing it acknowledged
// is resent.
func TestAcknowledgedStreamFramesAreNotResent(t *testing.T) {
	stream := &terminalStream{}
	stream.unacked = []streamOutput{{id: 1}, {id: 2}, {id: 3}}
	stream.acknowledgeOutput(2)
	unacked := stream.retire()
	if len(unacked) != 1 || unacked[0].id != 3 {
		t.Fatalf("unacknowledged after ack 2 = %+v, want only frame 3", unacked)
	}
	if stream.sendOutput(4, []byte("x")) {
		t.Fatal("retired stream accepted output")
	}
}

// A repaint must actually signal the program: the kernel and dtach only send
// SIGWINCH for a real size change, so the worker nudges the width and
// restores it.
func TestRepaintTerminalSignalsProgramAndKeepsSize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	control := &terminalFrameControl{output: make(chan []byte, 64)}
	supervisor := &Supervisor{
		Control: control, Workspace: t.TempDir(), Shell: "/bin/sh",
		terminals: make(map[string]*terminalProcess), Logger: slog.Default(),
	}
	supervisor.AgentCommand = workerexec.Command{
		Path: "/bin/sh",
		Args: []string{"-c", `trap 'printf "winch:%s" "$(stty size)"' WINCH; printf ready; while :; do sleep 0.05; done`},
		Dir:  supervisor.Workspace,
	}
	defer supervisor.closeAllTerminals()
	id := "00000000-0000-0000-0000-000000000042"
	if err := supervisor.openTerminal(ctx, worker.TerminalCommand{
		TerminalID: id, Kind: "agent", NextOutputSequence: 1, Columns: 100, Rows: 30,
	}); err != nil {
		t.Fatal(err)
	}
	waitForOutput(t, control.output, "ready")
	if err := supervisor.repaintTerminal(id); err != nil {
		t.Fatalf("repaint: %v", err)
	}
	waitForOutput(t, control.output, "winch:")
	supervisor.mu.Lock()
	terminal := supervisor.terminals[id]
	supervisor.mu.Unlock()
	rows, columns, err := pty.Getsize(terminal.pty)
	if err != nil {
		t.Fatal(err)
	}
	if columns != 100 || rows != 30 {
		t.Fatalf("size after repaint = %dx%d, want 100x30", columns, rows)
	}
}

func waitForOutput(t *testing.T, output chan []byte, want string) {
	t.Helper()
	var seen strings.Builder
	deadline := time.After(5 * time.Second)
	for !strings.Contains(seen.String(), want) {
		select {
		case data := <-output:
			seen.Write(data)
		case <-deadline:
			t.Fatalf("terminal output %q never contained %q", seen.String(), want)
		}
	}
}
