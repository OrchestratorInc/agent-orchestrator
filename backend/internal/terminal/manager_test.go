package terminal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeConn struct {
	in     chan clientMsg
	out    chan serverMsg
	pings  int32
	once   sync.Once
	closed chan struct{}
}

func (f *fakeConn) AllowsPanes() bool { return true }

type plainConn struct{ wsConn }

func newFakeConn() *fakeConn {
	return &fakeConn{in: make(chan clientMsg, 16), out: make(chan serverMsg, 64), closed: make(chan struct{})}
}

func (c *fakeConn) ReadJSON(ctx context.Context, v any) error {
	select {
	case m := <-c.in:
		*(v.(*clientMsg)) = m
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return context.Canceled
	}
}

func (c *fakeConn) WriteJSON(_ context.Context, v any) error {
	c.out <- v.(serverMsg)
	return nil
}

func (c *fakeConn) Ping(context.Context) error {
	atomic.AddInt32(&c.pings, 1)
	return nil
}

func (c *fakeConn) Close(string) error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func recv(t *testing.T, c *fakeConn, ch, typ string, d time.Duration) serverMsg {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-c.out:
			if m.Ch == ch && m.Type == typ {
				return m
			}
		case <-deadline:
			t.Fatalf("did not receive %s/%s within %s", ch, typ, d)
		}
	}
}

func TestServeOpenStreamsAndWritesTerminal(t *testing.T) {
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	src := &fakeSource{alive: true, spawner: sp}
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)

	pty.push([]byte("prompt$ "))
	data := recv(t, conn, chTerminal, msgData, time.Second)
	got, _ := base64.StdEncoding.DecodeString(data.Data)
	if string(got) != "prompt$ " {
		t.Fatalf("streamed data = %q", got)
	}

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("whoami\n"))}
	eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == "whoami\n" })

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgResize, Rows: 30, Cols: 100}
	eventually(t, time.Second, func() bool {
		rs := pty.resizeCalls()
		return len(rs) == 1 && rs[0] == [2]uint16{30, 100}
	})
}

type fixedSessionInputLease bool

func (l fixedSessionInputLease) AcquireSessionInput(domain.SessionID) (func(), bool) {
	if !l {
		return nil, false
	}
	return func() {}, true
}

type observedTerminalInputLease struct {
	acquired chan struct{}
	released chan struct{}
}

func (l *observedTerminalInputLease) AcquireSessionInput(domain.SessionID) (func(), bool) {
	close(l.acquired)
	return func() { close(l.released) }, true
}

func TestServeRejectsTerminalInputWhileSessionGateIsClosed(t *testing.T) {
	pty := newFakePTY()
	src := &fakeSource{alive: true, spawner: &fakeSpawner{ptys: []*fakePTY{pty}}}
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))
	mgr.SetSessionInputLease(fixedSessionInputLease(false))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "worker-1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)
	conn.in <- clientMsg{Ch: chTerminal, ID: "worker-1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("unsafe\n"))}
	errFrame := recv(t, conn, chTerminal, msgError, time.Second)
	if errFrame.ID != "worker-1" || errFrame.Error == "" {
		t.Fatalf("error frame = %#v", errFrame)
	}
	time.Sleep(20 * time.Millisecond)
	if got := string(pty.writtenBytes()); got != "" {
		t.Fatalf("blocked input reached PTY: %q", got)
	}
}

func TestServeHoldsSessionInputLeaseThroughPTYWrite(t *testing.T) {
	pty := newFakePTY()
	pty.writeStarted = make(chan struct{}, 1)
	pty.writeUnblock = make(chan struct{})
	src := &fakeSource{alive: true, spawner: &fakeSpawner{ptys: []*fakePTY{pty}}}
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))
	lease := &observedTerminalInputLease{acquired: make(chan struct{}), released: make(chan struct{})}
	mgr.SetSessionInputLease(lease)
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "worker-1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)
	conn.in <- clientMsg{Ch: chTerminal, ID: "worker-1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("status\n"))}

	select {
	case <-lease.acquired:
	case <-time.After(time.Second):
		t.Fatal("input lease was not acquired")
	}
	select {
	case <-pty.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("PTY write did not start")
	}
	select {
	case <-lease.released:
		t.Fatal("input lease released before PTY write returned")
	default:
	}
	close(pty.writeUnblock)
	select {
	case <-lease.released:
	case <-time.After(time.Second):
		t.Fatal("input lease was not released after PTY write")
	}
	eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == "status\n" })
}

func TestServeBuffersInputUntilAttachReady(t *testing.T) {
	pty := newFakePTY()
	spawnStarted := make(chan struct{})
	releaseSpawn := make(chan struct{})
	src := &fakeSource{alive: true, attachFn: func(context.Context, uint16, uint16) (ports.Stream, error) {
		close(spawnStarted)
		<-releaseSpawn
		return pty, nil
	}}
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))
	lease := &observedTerminalInputLease{acquired: make(chan struct{}), released: make(chan struct{})}
	mgr.SetSessionInputLease(lease)
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	select {
	case <-spawnStarted:
	case <-time.After(time.Second):
		t.Fatal("spawn was not reached")
	}
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("status\n"))}
	select {
	case <-lease.acquired:
	case <-time.After(time.Second):
		t.Fatal("buffered input did not acquire a lease")
	}
	select {
	case <-lease.released:
		t.Fatal("buffered input released its lease before the pending PTY write")
	default:
	}
	close(releaseSpawn)

	recv(t, conn, chTerminal, msgOpened, time.Second)
	eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == "status\n" })
	select {
	case <-lease.released:
	case <-time.After(time.Second):
		t.Fatal("buffered input lease was not released after PTY write")
	}
}

func TestBeginInputDrainBlocksMuxWritesUntilReleased(t *testing.T) {
	pty := newFakePTY()
	mgr := NewManager(&fakeSource{alive: true, spawner: &fakeSpawner{ptys: []*fakePTY{pty}}}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)
	_, release := mgr.BeginInputDrain("t1")
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("lost work\n"))}
	conn.in <- clientMsg{Ch: chSystem, Type: msgPing}
	recv(t, conn, chSystem, msgPong, time.Second)
	if got := string(pty.writtenBytes()); got != "" {
		t.Fatalf("blocked terminal wrote %q", got)
	}

	release()
	release()
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("allowed\n"))}
	eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == "allowed\n" })
}

func TestHoldInputFencesInputBufferedDuringAttach(t *testing.T) {
	pty := newFakePTY()
	attached := make(chan struct{})
	src := &fakeSource{alive: true, attachFn: func(ctx context.Context, _, _ uint16) (ports.Stream, error) {
		select {
		case <-attached:
			return pty, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("draft"))}
	eventually(t, time.Second, func() bool { return !mgr.LastInputAt("t1").IsZero() })

	lastInputAt, release := mgr.HoldInput("t1")
	defer release()
	if lastInputAt.IsZero() {
		t.Fatal("HoldInput did not report the buffered keystroke")
	}
	close(attached)
	recv(t, conn, chTerminal, msgOpened, time.Second)
	time.Sleep(100 * time.Millisecond)
	if got := string(pty.writtenBytes()); got != "" {
		t.Fatalf("buffered input reached the pane while the fence was held: %q", got)
	}

	release()
	eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == "draft" })
}

func TestBufferedInputReplayUpdatesLastInputAt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		data  string
		typed bool
	}{
		{name: "human draft", data: "human draft", typed: true},
		{name: "terminal report", data: "\x1b[I"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pty := newFakePTY()
			attached := make(chan struct{})
			src := &fakeSource{alive: true, attachFn: func(ctx context.Context, _, _ uint16) (ports.Stream, error) {
				select {
				case <-attached:
					return pty, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}}
			mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))
			defer mgr.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := &connState{mgr: mgr, conn: newFakeConn(), ctx: ctx, cancel: cancel, out: newOutQueue(), terms: map[string]*attachment{}, writers: map[string]*inputWriter{}}
			defer c.cleanup()
			c.openTerminal("t1", 24, 80, "")
			mgr.writeInput("t1", c.lookup("t1"), []byte(tc.data), func() {})
			replayedAt := time.Now()
			close(attached)
			eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == tc.data })
			lastInputAt, release := mgr.HoldInput("t1")
			defer release()
			if tc.typed && lastInputAt.Before(replayedAt) {
				t.Fatalf("last input at %v predates replay at %v; freshly replayed typing must get a new settle window", lastInputAt, replayedAt)
			}
			if !tc.typed && !lastInputAt.IsZero() {
				t.Fatalf("terminal report changed human input timestamp to %v", lastInputAt)
			}
		})
	}
}

func TestHeldFenceDoesNotStallTheConnectionsOtherTerminals(t *testing.T) {
	p1, p2 := newFakePTY(), newFakePTY()
	mgr := NewManager(&fakeSource{alive: true, spawner: &fakeSpawner{ptys: []*fakePTY{p1, p2}}}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)
	conn.in <- clientMsg{Ch: chTerminal, ID: "t2", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)

	_, release := mgr.HoldInput("t1")
	defer release()
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("he"))}
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("ld"))}
	conn.in <- clientMsg{Ch: chTerminal, ID: "t2", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("typed"))}
	eventually(t, time.Second, func() bool { return string(p2.writtenBytes()) == "typed" })
	conn.in <- clientMsg{Ch: chSystem, Type: msgPing}
	recv(t, conn, chSystem, msgPong, time.Second)
	if got := string(p1.writtenBytes()); got != "" {
		t.Fatalf("t1's keystrokes passed its held fence: %q", got)
	}
	release()
	release()
	eventually(t, time.Second, func() bool { return string(p1.writtenBytes()) == "held" })
}

func TestClosingATerminalStillWritesKeysQueuedBehindItsFence(t *testing.T) {
	pty := newFakePTY()
	mgr := NewManager(&fakeSource{alive: true, spawner: &fakeSpawner{ptys: []*fakePTY{pty}}}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)

	_, release := mgr.HoldInput("t1")
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("ls\r"))}
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgClose}
	conn.in <- clientMsg{Ch: chSystem, Type: msgPing}
	recv(t, conn, chSystem, msgPong, time.Second)
	release()
	eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == "ls\r" })
}

func TestClosedTerminalsDoNotKeepInputWriters(t *testing.T) {
	const terminals = 20
	ptys := make([]*fakePTY, terminals)
	for i := range ptys {
		ptys[i] = newFakePTY()
	}
	mgr := NewManager(&fakeSource{alive: true, spawner: &fakeSpawner{ptys: ptys}}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &connState{mgr: mgr, conn: newFakeConn(), ctx: ctx, cancel: cancel, out: newOutQueue(), terms: map[string]*attachment{}, writers: map[string]*inputWriter{}}
	defer c.cleanup()

	for i := range terminals {
		id := fmt.Sprintf("t%d", i)
		c.handle(clientMsg{Ch: chTerminal, ID: id, Type: msgOpen})
		c.handle(clientMsg{Ch: chTerminal, ID: id, Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("x"))})
		eventually(t, time.Second, func() bool { return string(ptys[i].writtenBytes()) == "x" })
		c.handle(clientMsg{Ch: chTerminal, ID: id, Type: msgClose})
	}
	eventually(t, time.Second, func() bool {
		c.closeTerminal("none")
		return len(c.writers) == 0
	})
}

func TestReopenedTerminalsWriterWaitsForTheRetiredOne(t *testing.T) {
	pty := newFakePTY()
	mgr := NewManager(&fakeSource{alive: true, spawner: &fakeSpawner{ptys: []*fakePTY{pty}}}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)
	var a *attachment
	eventually(t, time.Second, func() bool {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		for att := range mgr.attachments {
			a = att
		}
		return a != nil
	})

	retired := &inputWriter{queue: make(chan queuedInput), done: make(chan struct{}), retired: true}
	next := &inputWriter{queue: make(chan queuedInput, 1), done: make(chan struct{})}
	go next.run(mgr, "t1", retired)
	next.queue <- queuedInput{a: a, raw: []byte("after"), release: func() {}}
	time.Sleep(50 * time.Millisecond)
	if got := string(pty.writtenBytes()); got != "" {
		t.Fatalf("new writer wrote %q while the retired one was still draining", got)
	}
	close(retired.done)
	eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == "after" })
	close(next.queue)
	<-next.done
}

func TestKeystrokeReceivedDuringADrainIsDroppedEvenIfWrittenLater(t *testing.T) {
	pty := newFakePTY()
	mgr := NewManager(&fakeSource{alive: true, spawner: &fakeSpawner{ptys: []*fakePTY{pty}}}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)

	_, releaseHold := mgr.HoldInput("t1")
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("queued\n"))}
	conn.in <- clientMsg{Ch: chSystem, Type: msgPing}
	recv(t, conn, chSystem, msgPong, time.Second)
	drained := make(chan func())
	go func() {
		_, release := mgr.BeginInputDrain("t1")
		drained <- release
	}()
	time.Sleep(20 * time.Millisecond)
	releaseHold()
	releaseDrain := <-drained
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("during\n"))}
	conn.in <- clientMsg{Ch: chSystem, Type: msgPing}
	recv(t, conn, chSystem, msgPong, time.Second)
	releaseDrain()
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("after\n"))}
	eventually(t, time.Second, func() bool { return strings.HasSuffix(string(pty.writtenBytes()), "after\n") })
	if got := string(pty.writtenBytes()); strings.Contains(got, "during") {
		t.Fatalf("a keystroke received during the drain reached the pane: %q", got)
	}
}

func TestBeginInputDrainReturnsTheLastAcceptedWriteBarrier(t *testing.T) {
	pty := newFakePTY()
	mgr := NewManager(&fakeSource{alive: true, spawner: &fakeSpawner{ptys: []*fakePTY{pty}}}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("before\n"))}
	eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == "before\n" })

	first, release := mgr.BeginInputDrain("t1")
	if first.IsZero() {
		t.Fatal("accepted input did not produce a drain barrier")
	}
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("blocked\n"))}
	conn.in <- clientMsg{Ch: chSystem, Type: msgPing}
	recv(t, conn, chSystem, msgPong, time.Second)
	release()

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("after\n"))}
	eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == "before\nafter\n" })
	second, releaseSecond := mgr.BeginInputDrain("t1")
	defer releaseSecond()
	if !second.After(first) {
		t.Fatalf("second barrier = %s, want after first %s", second, first)
	}
}

func TestLastInputAtReadsWithoutBlockingInput(t *testing.T) {
	pty := newFakePTY()
	mgr := NewManager(&fakeSource{alive: true, spawner: &fakeSpawner{ptys: []*fakePTY{pty}}}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)
	if got := mgr.LastInputAt("t1"); !got.IsZero() {
		t.Fatalf("LastInputAt before any input = %s, want zero", got)
	}
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("first\n"))}
	eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == "first\n" })

	first := mgr.LastInputAt("t1")
	if first.IsZero() {
		t.Fatal("accepted input was not recorded")
	}
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("second\n"))}
	eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == "first\nsecond\n" })
	if !mgr.LastInputAt("t1").After(first) {
		t.Fatal("LastInputAt did not advance with the next accepted keystroke")
	}
}

func nextTerminal(t *testing.T, c *fakeConn) serverMsg {
	t.Helper()
	select {
	case m := <-c.out:
		return m
	case <-time.After(time.Second):
		t.Fatal("no frame within 1s")
		return serverMsg{}
	}
}

func TestServeOpenDeadRuntimeReportsExitedAndAllowsReopen(t *testing.T) {
	sp := &fakeSpawner{ptys: []*fakePTY{newFakePTY()}}
	src := &fakeSource{alive: false, spawner: sp}
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	if m := nextTerminal(t, conn); m.Type != msgExited {
		t.Fatalf("first frame = %q, want exited", m.Type)
	}
	if got := sp.calls(); got != 0 {
		t.Fatalf("attach must never run against a dead runtime, got %d attaches", got)
	}

	src.setAlive(true)
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	if m := nextTerminal(t, conn); m.Type != msgOpened {
		t.Fatalf("re-open frame = %q, want opened (open was dropped, entry stuck)", m.Type)
	}
}

func TestServeExitAfterOpenClearsEntryAllowingReopen(t *testing.T) {
	p := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{p}}
	src := &fakeSource{alive: true, spawner: sp}
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)

	src.setAlive(false)
	p.Close()
	recv(t, conn, chTerminal, msgExited, time.Second)

	src.setAlive(true)
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, 2*time.Second)
}

func TestServeReopenAfterImmediateExitNeverStuck(t *testing.T) {
	for i := 0; i < 400; i++ {
		sp := &fakeSpawner{}
		src := &fakeSource{spawner: sp}
		src.setAlive(false)
		mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))

		conn := newFakeConn()
		ctx, cancel := context.WithCancel(context.Background())
		go mgr.Serve(ctx, conn)

		conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}

		recv(t, conn, chTerminal, msgExited, time.Second)

		conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
		recv(t, conn, chTerminal, msgExited, time.Second)

		cancel()
		mgr.Close()
	}
}

func TestServeRejectsOpenWithoutID(t *testing.T) {
	mgr := NewManager(&fakeSource{}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, Type: msgOpen}
	msg := recv(t, conn, chTerminal, msgError, time.Second)
	if msg.Error == "" {
		t.Fatal("expected an error message for open without id")
	}
}

func TestServeForwardsSessionChannelFromCDC(t *testing.T) {
	bc := cdc.NewBroadcaster()
	mgr := NewManager(&fakeSource{}, bc, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chSubscribe, Type: msgSubscribe}
	eventually(t, time.Second, func() bool {
		bc.Publish(cdc.Event{Seq: 9, ProjectID: "p1", SessionID: "s1", Type: cdc.EventSessionUpdated})
		select {
		case m := <-conn.out:
			return m.Ch == chSessions && m.Session != nil && m.Session.Seq == 9
		default:
			return false
		}
	})
}

type fakeShellAgents struct {
	mu   sync.Mutex
	subs int
}

func (f *fakeShellAgents) SubscribeShellAgents(fn func(json.RawMessage)) func() {
	f.mu.Lock()
	f.subs++
	f.mu.Unlock()
	fn(json.RawMessage(`[{"handleId":"h1"}]`))
	return func() {
		f.mu.Lock()
		f.subs--
		f.mu.Unlock()
	}
}

func TestServeForwardsShellAgentSnapshots(t *testing.T) {
	src := &fakeShellAgents{}
	mgr := NewManager(&fakeSource{}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	mgr.SetShellAgents(src)
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chShellAgents, Type: msgSubscribe}
	if msg := recv(t, conn, chShellAgents, msgSnapshot, time.Second); string(msg.Agents) != `[{"handleId":"h1"}]` {
		t.Fatalf("snapshot = %s", msg.Agents)
	}
	cancel()
	eventually(t, time.Second, func() bool {
		src.mu.Lock()
		defer src.mu.Unlock()
		return src.subs == 0
	})
}

type fakePanes struct {
	mu       sync.Mutex
	send     func(json.RawMessage)
	reports  []string
	results  []string
	closed   int
	connects int
}

func (f *fakePanes) ConnectPanes(send func(json.RawMessage)) PaneConn {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.send = send
	f.connects++
	return fakePaneConn{f}
}

type fakePaneConn struct{ f *fakePanes }

func (c fakePaneConn) Report(sessionID string, layout json.RawMessage) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	c.f.reports = append(c.f.reports, sessionID+"="+string(layout))
}

func (c fakePaneConn) Resolve(result json.RawMessage) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	c.f.results = append(c.f.results, string(result))
}

func (c fakePaneConn) Close() {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	c.f.closed++
}

func TestServeRelaysPaneCommandsLayoutsAndResults(t *testing.T) {
	src := &fakePanes{}
	mgr := NewManager(&fakeSource{}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	mgr.SetPanes(src)
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chPanes, Type: msgSubscribe}
	conn.in <- clientMsg{Ch: chPanes, Type: msgSubscribe}
	conn.in <- clientMsg{Ch: chPanes, Type: msgLayout, SessionID: "s1", Layout: json.RawMessage(`{"selected":"session"}`)}
	eventually(t, time.Second, func() bool {
		src.mu.Lock()
		defer src.mu.Unlock()
		return len(src.reports) == 1
	})
	src.mu.Lock()
	send, connects, report := src.send, src.connects, src.reports[0]
	src.mu.Unlock()
	if connects != 1 || report != `s1={"selected":"session"}` {
		t.Fatalf("connects = %d, report = %s", connects, report)
	}

	send(json.RawMessage(`{"id":"pane-1","sessionId":"s1","action":"split"}`))
	if msg := recv(t, conn, chPanes, msgCommand, time.Second); string(msg.Command) != `{"id":"pane-1","sessionId":"s1","action":"split"}` {
		t.Fatalf("command = %s", msg.Command)
	}
	conn.in <- clientMsg{Ch: chPanes, Type: msgResult, Result: json.RawMessage(`{"id":"pane-1","ok":true}`)}
	eventually(t, time.Second, func() bool {
		src.mu.Lock()
		defer src.mu.Unlock()
		return len(src.results) == 1 && src.results[0] == `{"id":"pane-1","ok":true}`
	})

	cancel()
	eventually(t, time.Second, func() bool {
		src.mu.Lock()
		defer src.mu.Unlock()
		return src.closed == 1
	})
}

func TestServeIgnoresPanesFromAConnectionThatIsNotAPage(t *testing.T) {
	src := &fakePanes{}
	mgr := NewManager(&fakeSource{}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	mgr.SetPanes(src)
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, plainConn{conn})

	conn.in <- clientMsg{Ch: chPanes, Type: msgSubscribe}
	conn.in <- clientMsg{Ch: chPanes, Type: msgLayout, SessionID: "s1", Layout: json.RawMessage(`{"selected":"session"}`)}
	conn.in <- clientMsg{Ch: chSystem, Type: msgPing}
	recv(t, conn, chSystem, msgPong, time.Second)
	src.mu.Lock()
	defer src.mu.Unlock()
	if src.connects != 0 || len(src.reports) != 0 {
		t.Fatalf("connects = %d, reports = %v; a non-page connection must not reach the panes relay", src.connects, src.reports)
	}
}

func TestServeSystemPingGetsPong(t *testing.T) {
	mgr := NewManager(&fakeSource{}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chSystem, Type: msgPing}
	recv(t, conn, chSystem, msgPong, time.Second)
}

func TestServeHeartbeatPings(t *testing.T) {
	mgr := NewManager(&fakeSource{}, nil, testLogger(), WithHeartbeat(10*time.Millisecond))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	eventually(t, time.Second, func() bool { return atomic.LoadInt32(&conn.pings) >= 2 })
}

func TestServeClosesConnOnReadEnd(t *testing.T) {
	mgr := NewManager(&fakeSource{}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	go mgr.Serve(ctx, conn)

	cancel()
	select {
	case <-conn.closed:
	case <-time.After(time.Second):
		t.Fatal("Serve must close the conn when the context is cancelled")
	}
}

func TestServePerClientAttachIsolation(t *testing.T) {
	p1, p2 := newFakePTY(), newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{p1, p2}}
	src := &fakeSource{alive: true, spawner: sp}
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	connA, connB := newFakeConn(), newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, connA)
	go mgr.Serve(ctx, connB)

	connA.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, connA, chTerminal, msgOpened, time.Second)
	eventually(t, time.Second, func() bool { return sp.calls() == 1 })

	connB.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, connB, chTerminal, msgOpened, time.Second)
	eventually(t, time.Second, func() bool { return sp.calls() == 2 })

	p1.push([]byte("for-A"))
	data := recv(t, connA, chTerminal, msgData, time.Second)
	got, _ := base64.StdEncoding.DecodeString(data.Data)
	if string(got) != "for-A" {
		t.Fatalf("conn A data = %q", got)
	}
	select {
	case m := <-connB.out:
		t.Fatalf("conn B received %s/%s for conn A's PTY output", m.Ch, m.Type)
	default:
	}

	connA.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgClose}
	eventually(t, time.Second, func() bool {
		select {
		case <-p1.closed:
			return true
		default:
			return false
		}
	})
	select {
	case <-p2.closed:
		t.Fatal("closing conn A's terminal must not close conn B's PTY")
	default:
	}
}

func TestServeOpenAppliesInitialSize(t *testing.T) {
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	src := &fakeSource{alive: true, spawner: sp}
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen, Rows: 40, Cols: 120}
	recv(t, conn, chTerminal, msgOpened, time.Second)
	eventually(t, time.Second, func() bool {
		rs := pty.resizeCalls()
		return len(rs) == 1 && rs[0] == [2]uint16{40, 120}
	})
}

func TestServeDeduplicatesResizeUnlessExplicitlyForced(t *testing.T) {
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	src := &fakeSource{alive: true, spawner: sp}
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen, Rows: 40, Cols: 120}
	recv(t, conn, chTerminal, msgOpened, time.Second)
	eventually(t, time.Second, func() bool { return len(pty.resizeCalls()) == 1 })

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgResize, Rows: 40, Cols: 120}
	conn.in <- clientMsg{Ch: chSystem, Type: msgPing}
	recv(t, conn, chSystem, msgPong, time.Second)
	if got := len(pty.resizeCalls()); got != 1 {
		t.Fatalf("duplicate normal resize calls = %d, want 1", got)
	}

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgResize, Rows: 40, Cols: 120, Force: true}
	eventually(t, time.Second, func() bool { return len(pty.resizeCalls()) == 2 })
}

func TestServePrimaryDrivesSharedGridSecondaryFollows(t *testing.T) {
	p1, p2 := newFakePTY(), newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{p1, p2}}
	src := &fakeSource{alive: true, spawner: sp}
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()

	primary, secondary := newFakeConn(), newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, primary)
	go mgr.Serve(ctx, secondary)

	primary.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen, Cols: 120, Rows: 40}
	if r := recv(t, primary, chTerminal, msgResize, time.Second); r.Cols != 120 || r.Rows != 40 {
		t.Fatalf("primary grid = %dx%d, want 120x40", r.Cols, r.Rows)
	}

	secondary.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen, Cols: 55, Rows: 48, Role: roleSecondary}
	if r := recv(t, secondary, chTerminal, msgResize, time.Second); r.Cols != 120 || r.Rows != 40 {
		t.Fatalf("secondary follows grid = %dx%d, want the primary's 120x40", r.Cols, r.Rows)
	}
	eventually(t, time.Second, func() bool {
		s := sp.spawnSizes()
		return len(s) == 2 && s[1] == [2]uint16{40, 120}
	})
	eventually(t, time.Second, func() bool {
		return len(p1.resizeCalls()) > 0 && len(p2.resizeCalls()) > 0
	})
	primaryResizeCount, secondaryResizeCount := len(p1.resizeCalls()), len(p2.resizeCalls())

	secondary.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgResize, Cols: 60, Rows: 50}
	secondary.in <- clientMsg{Ch: chSystem, Type: msgPing}
	recv(t, secondary, chSystem, msgPong, time.Second)
	if got := len(p1.resizeCalls()); got != primaryResizeCount {
		t.Fatalf("primary resize calls after secondary-only change = %d, want %d", got, primaryResizeCount)
	}
	if got := len(p2.resizeCalls()); got != secondaryResizeCount {
		t.Fatalf("secondary resize calls under unchanged shared grid = %d, want %d", got, secondaryResizeCount)
	}

	primary.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgClose}
	if r := recv(t, secondary, chTerminal, msgResize, 2*time.Second); r.Cols != 60 || r.Rows != 50 {
		t.Fatalf("after primary left, grid = %dx%d, want the secondary's 60x50", r.Cols, r.Rows)
	}
}

func TestManagerCloseKillsLiveAttachments(t *testing.T) {
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	src := &fakeSource{alive: true, spawner: sp}
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0))

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)
	eventually(t, time.Second, func() bool { return sp.calls() == 1 })

	mgr.Close()
	select {
	case <-pty.closed:
	case <-time.After(time.Second):
		t.Fatal("Manager.Close must close live attach PTYs")
	}
}

func newFloodedConn(ctx context.Context, cancel context.CancelFunc) *connState {
	c := &connState{
		ctx:    ctx,
		cancel: cancel,
		out:    newOutQueue(),
		terms:  map[string]*attachment{},
	}
	c.enqueue(serverMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: strings.Repeat("x", dataWatermark)})
	return c
}

func TestEnqueueDataBlocksUntilDrained(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newFloodedConn(ctx, func() { t.Error("a flood must not cancel the connection") })

	done := make(chan struct{})
	go func() {
		c.enqueue(serverMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: "more"})
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("data frame must wait while the queue is over the watermark")
	case <-time.After(50 * time.Millisecond):
	}

	c.out.drain()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("draining the queue must release the waiting data frame")
	}
}

func TestEnqueueControlFramesNeverWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newFloodedConn(ctx, func() { t.Error("a control frame must not cancel the connection") })

	done := make(chan struct{})
	go func() {
		c.enqueue(serverMsg{Ch: chTerminal, ID: "t1", Type: msgExited})
		c.enqueue(serverMsg{Ch: chSessions, Type: msgSnapshot})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("control frames must not wait behind a flood")
	}

	var types []string
	for _, m := range c.out.drain() {
		types = append(types, m.Type)
	}
	want := []string{msgData, msgExited, msgSnapshot}
	if !slices.Equal(types, want) {
		t.Fatalf("frames = %v, want %v (order must be preserved)", types, want)
	}
}

func TestEnqueueDataUnblocksOnConnClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := newFloodedConn(ctx, cancel)

	done := make(chan struct{})
	go func() {
		c.enqueue(serverMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: "more"})
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelling the connection must release a waiting data frame")
	}
}

func TestLargestGridFallsBackToSecondaryWhenNoPrimaryHasASize(t *testing.T) {
	phone := &connState{}
	parkedDesktop := &connState{}
	members := map[*connState]*termMember{
		phone:         {cols: 45, rows: 30, primary: false},
		parkedDesktop: {cols: 0, rows: 0, primary: true},
	}
	if cols, rows := largestGrid(members); cols != 45 || rows != 30 {
		t.Fatalf("largestGrid = %dx%d, want the phone's 45x30", cols, rows)
	}

	members[parkedDesktop].cols, members[parkedDesktop].rows = 151, 38
	if cols, rows := largestGrid(members); cols != 151 || rows != 38 {
		t.Fatalf("largestGrid once the desktop measures = %dx%d, want 151x38", cols, rows)
	}
}

func TestTerminalReportsAreNotInput(t *testing.T) {
	for _, raw := range []string{"\x1b[I", "\x1b[O", "\x1b[?1;2c", "\x1b[12;40R", "\x1b]11;rgb:0000/0000/0000\x07", "\x1b[I\x1b[O",
		"\x1b[?2026;2$y", "\x1b[?0u", "\x1b[8;38;151t", "\x1bP>|xterm.js(5.5.0)\x1b\\"} {
		if !terminalReports.MatchString(raw) {
			t.Errorf("%q should be a terminal report", raw)
		}
	}
	for _, raw := range []string{"a", "\r", "\x1b[A", "hello\x1b[I", "\x03", "\x1b[13u", "\x1b[97;5u"} {
		if terminalReports.MatchString(raw) {
			t.Errorf("%q should count as input", raw)
		}
	}
}

func typeInto(t *testing.T, mgr *Manager, conn *fakeConn, id string) time.Time {
	t.Helper()
	conn.in <- clientMsg{Ch: chTerminal, ID: id, Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)
	conn.in <- clientMsg{Ch: chTerminal, ID: id, Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("half-typed draft"))}
	eventually(t, time.Second, func() bool { return !mgr.LastInputAt(id).IsZero() })
	return mgr.LastInputAt(id)
}

func hasLastInput(mgr *Manager, id string) bool {
	mgr.inputMu.Lock()
	defer mgr.inputMu.Unlock()
	_, ok := mgr.lastInputAt[id]
	return ok
}

func TestServeKeepsLastInputAtWhenTheLastViewerCloses(t *testing.T) {
	mgr := NewManager(&fakeSource{alive: true}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	connA, connB := newFakeConn(), newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, connA)
	go mgr.Serve(ctx, connB)

	typedAt := typeInto(t, mgr, connA, "t1")
	connB.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, connB, chTerminal, msgOpened, time.Second)

	connA.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgClose}
	connB.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgClose}
	eventually(t, time.Second, func() bool {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		return len(mgr.attachments) == 0
	})
	if got := mgr.LastInputAt("t1"); !got.Equal(typedAt) {
		t.Fatalf("LastInputAt after the last viewer closed = %v, want %v", got, typedAt)
	}
	got, release := mgr.BeginInputDrain("t1")
	release()
	if !got.Equal(typedAt) {
		t.Fatalf("drain barrier after the last viewer closed = %v, want %v", got, typedAt)
	}
}

func TestForgetTerminalInputDropsTheTimestamp(t *testing.T) {
	mgr := NewManager(&fakeSource{alive: true}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	typeInto(t, mgr, conn, "t1")
	typeInto(t, mgr, conn, "t2")
	forgetInput(t, mgr, "t1")
	if hasLastInput(mgr, "t1") {
		t.Fatal("t1 input timestamp kept after ForgetTerminalInput")
	}
	if !mgr.LastInputAt("t1").IsZero() {
		t.Fatal("LastInputAt(t1) is not zero after ForgetTerminalInput")
	}
	if !hasLastInput(mgr, "t2") {
		t.Fatal("ForgetTerminalInput(t1) dropped t2's timestamp")
	}
}

func TestForgetTerminalInputIgnoresUnknownTerminals(t *testing.T) {
	mgr := NewManager(&fakeSource{alive: true}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	forgetInput(t, mgr, "never-seen")
	forgetInput(t, mgr, "")
	if n := len(mgr.lastInputAt); n != 0 {
		t.Fatalf("lastInputAt has %d entries, want 0", n)
	}
	mgr.fencesMu.Lock()
	n := len(mgr.fences)
	mgr.fencesMu.Unlock()
	if n != 0 {
		t.Fatalf("fences has %d entries after ForgetTerminalInput, want 0", n)
	}
}

func TestForgetTerminalInputWaitsForTheWriteFence(t *testing.T) {
	mgr := NewManager(&fakeSource{alive: true}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	mgr.recordInputAt("t1", []byte("x"))
	_, release := mgr.HoldInput("t1")
	done := make(chan struct{})
	go func() {
		forgetInput(t, mgr, "t1")
		close(done)
	}()
	select {
	case <-done:
		release()
		t.Fatal("ForgetTerminalInput returned while the write fence was held")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ForgetTerminalInput did not return after the fence was released")
	}
	if hasLastInput(mgr, "t1") {
		t.Fatal("t1 input timestamp kept after ForgetTerminalInput")
	}
}

func TestForgetTerminalInputIsSafeAlongsideInputAndReaders(t *testing.T) {
	mgr := NewManager(&fakeSource{alive: true}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)
	typeInto(t, mgr, conn, "t1")

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("k"))}
				_ = mgr.LastInputAt("t1")
				_, release := mgr.HoldInput("t1")
				release()
				forgetInput(t, mgr, "t1")
			}
		}()
	}
	wg.Wait()
	eventually(t, time.Second, func() bool {
		forgetInput(t, mgr, "t1")
		return !hasLastInput(mgr, "t1")
	})
}

func forgetInput(t *testing.T, mgr *Manager, id string) {
	t.Helper()
	if err := mgr.ForgetTerminalInput(context.Background(), id); err != nil {
		t.Errorf("ForgetTerminalInput(%q): %v", id, err)
	}
}

func TestForgetTerminalInputStopsWaitingForTheFenceWhenTheContextEnds(t *testing.T) {
	mgr := NewManager(&fakeSource{alive: true}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	mgr.recordInputAt("t1", []byte("x"))
	_, release := mgr.HoldInput("t1")
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- mgr.ForgetTerminalInput(ctx, "t1") }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ForgetTerminalInput = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ForgetTerminalInput kept waiting on the held fence after its context ended")
	}
	if !hasLastInput(mgr, "t1") {
		t.Fatal("a cancelled ForgetTerminalInput dropped the timestamp without the fence")
	}

	release()
	mgr.fencesMu.Lock()
	n := len(mgr.fences)
	mgr.fencesMu.Unlock()
	if n != 0 {
		t.Fatalf("fences has %d entries after the cancelled wait and release, want 0", n)
	}
}

func TestForgetTerminalInputDropsLaterInputForTheDestroyedRuntime(t *testing.T) {
	pty := newFakePTY()
	mgr := NewManager(&fakeSource{alive: true, spawner: &fakeSpawner{ptys: []*fakePTY{pty}}}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	typeInto(t, mgr, conn, "t1")
	forgetInput(t, mgr, "t1")
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("late"))}
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgClose}
	eventually(t, time.Second, func() bool {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		return len(mgr.attachments) == 0
	})
	if hasLastInput(mgr, "t1") {
		t.Fatal("a keystroke written after ForgetTerminalInput re-created the timestamp")
	}
	if got := string(pty.writtenBytes()); strings.Contains(got, "late") {
		t.Fatalf("keystroke reached the destroyed runtime's pane: %q", got)
	}
}

func TestWriteInputDoesNotRecordInputRefusedByAnExitedAttachment(t *testing.T) {
	mgr := NewManager(&fakeSource{alive: true}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	a := newAttachment("t1", ports.RuntimeHandle{ID: "t1"}, mgr.src, nil, nil, nil, testLogger())
	a.markExited()
	released := false
	mgr.writeInput("t1", a, []byte("late"), func() { released = true })
	if !released {
		t.Fatal("the refused write kept its input lease")
	}
	if hasLastInput(mgr, "t1") {
		t.Fatal("a write refused by an exited attachment recorded a timestamp")
	}
}

func TestForgetTerminalInputInvalidatesInputQueuedBehindTheFence(t *testing.T) {
	pty := newFakePTY()
	mgr := NewManager(&fakeSource{alive: true, spawner: &fakeSpawner{ptys: []*fakePTY{pty}}}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	typeInto(t, mgr, conn, "t1")
	_, release := mgr.HoldInput("t1")
	for range 8 {
		conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("q"))}
	}
	conn.in <- clientMsg{Ch: chSystem, Type: msgPing}
	recv(t, conn, chSystem, msgPong, time.Second)
	done := make(chan error, 1)
	go func() { done <- mgr.ForgetTerminalInput(context.Background(), "t1") }()
	time.Sleep(10 * time.Millisecond)
	release()
	if err := <-done; err != nil {
		t.Fatalf("ForgetTerminalInput: %v", err)
	}
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgClose}
	eventually(t, time.Second, func() bool {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		return len(mgr.attachments) == 0
	})
	if hasLastInput(mgr, "t1") {
		t.Fatal("input queued behind the fence re-created the timestamp after ForgetTerminalInput")
	}
}

func TestForgottenViewExitsInsteadOfReattaching(t *testing.T) {
	first, second := newFakePTY(), newFakePTY()
	mgr := NewManager(&fakeSource{alive: true, spawner: &fakeSpawner{ptys: []*fakePTY{first, second}}}, nil, testLogger(), WithHeartbeat(0))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	typeInto(t, mgr, conn, "t1")
	forgetInput(t, mgr, "t1")
	_ = first.Close()
	recv(t, conn, chTerminal, msgExited, 2*time.Second)

	typeInto(t, mgr, conn, "t1")
	if !hasLastInput(mgr, "t1") {
		t.Fatal("a fresh view's input was not recorded after the old one was forgotten")
	}
}
