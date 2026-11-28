// Package supervisor_test exercises the Supervisor watchdog via in-process
// net.Pipe connections so no real OS sockets are needed.
package supervisor_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/daemon/supervisor"
)

// fakeListener queues pre-made conns and blocks (or returns a closed error)
// once the queue is drained. Close() unblocks any pending Accept().
type fakeListener struct {
	mu     sync.Mutex
	conns  []net.Conn
	closed bool
	ready  chan struct{} // closed when a conn is enqueued or the listener is closed
}

func newFakeListener() *fakeListener {
	return &fakeListener{ready: make(chan struct{}, 1)}
}

// enqueue adds a conn for the next Accept() call.
func (fl *fakeListener) enqueue(c net.Conn) {
	fl.mu.Lock()
	fl.conns = append(fl.conns, c)
	fl.mu.Unlock()
	select {
	case fl.ready <- struct{}{}:
	default:
	}
}

func (fl *fakeListener) Accept() (net.Conn, error) {
	for {
		fl.mu.Lock()
		if fl.closed {
			fl.mu.Unlock()
			return nil, net.ErrClosed // signals Serve to stop
		}
		if len(fl.conns) > 0 {
			c := fl.conns[0]
			fl.conns = fl.conns[1:]
			fl.mu.Unlock()
			return c, nil
		}
		fl.mu.Unlock()
		// drain the ready channel so we can block below
		select {
		case <-fl.ready:
		default:
		}
		// wait for a new conn or a close signal
		<-fl.ready
	}
}

func (fl *fakeListener) Close() error {
	fl.mu.Lock()
	fl.closed = true
	fl.mu.Unlock()
	select {
	case fl.ready <- struct{}{}:
	default:
	}
	return nil
}

func (fl *fakeListener) Addr() net.Addr { return &net.UnixAddr{Name: "fake", Net: "unix"} }

// noopLogger returns a slog.Logger that discards all output.
func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const testGrace = 30 * time.Millisecond

// comfortWait is how long we wait when asserting the callback did NOT fire.
// It must be strictly greater than testGrace so a real timer would have fired.
const comfortWait = testGrace * 5

// TestNeverFiresPreConnect: start Serve with no connections, wait well past
// grace, assert callback was NOT called.
func TestNeverFiresPreConnect(t *testing.T) {
	t.Parallel()

	fired := make(chan struct{})
	cb := func() { close(fired) }

	s := supervisor.New(testGrace, cb, noopLogger())
	ln := newFakeListener()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	// wait comfortably past grace with no connections ever accepted
	time.Sleep(comfortWait)

	select {
	case <-fired:
		t.Fatal("callback fired before any client ever connected")
	default:
	}

	cancel()
	_ = ln.Close()
	<-done
}

// TestFiresOnceAfterGrace: connect one client, close it, assert the callback
// fires exactly once within a reasonable window.
func TestFiresOnceAfterGrace(t *testing.T) {
	t.Parallel()

	fireCount := 0
	var mu sync.Mutex
	fired := make(chan struct{})
	cb := func() {
		mu.Lock()
		fireCount++
		mu.Unlock()
		// close is safe even if called once, but use a Once-guarded close via
		// a sync.Once in the real impl; here we just close the channel once
		select {
		case fired <- struct{}{}:
		default:
		}
	}

	s := supervisor.New(testGrace, cb, noopLogger())
	ln := newFakeListener()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	// create a pipe, enqueue the server-side end
	serverConn, clientConn := makePipe()
	ln.enqueue(serverConn)

	// close the client side to signal disconnect
	_ = clientConn.Close()

	// wait for the callback within a bounded window
	select {
	case <-fired:
		// good
	case <-time.After(comfortWait * 2):
		t.Fatal("callback did not fire after client disconnected and grace elapsed")
	}

	// close and wait a bit more to make sure it only fires once
	time.Sleep(comfortWait)
	mu.Lock()
	count := fireCount
	mu.Unlock()
	if count != 1 {
		t.Fatalf("expected callback to fire exactly once, got %d", count)
	}

	cancel()
	_ = ln.Close()
	<-done
}

// TestReconnectWithinGraceCancels: connect, disconnect (arms grace), reconnect
// before grace elapses, wait past grace, assert callback NOT called. Then
// disconnect again and assert it DOES fire.
func TestReconnectWithinGraceCancels(t *testing.T) {
	t.Parallel()

	fireCount := 0
	var mu sync.Mutex
	fired := make(chan struct{}, 1)
	cb := func() {
		mu.Lock()
		fireCount++
		mu.Unlock()
		select {
		case fired <- struct{}{}:
		default:
		}
	}

	s := supervisor.New(testGrace, cb, noopLogger())
	ln := newFakeListener()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	// --- first connection ---
	serverConn1, clientConn1 := makePipe()
	ln.enqueue(serverConn1)
	// small sleep so the server-side accept loop picks up the first conn
	time.Sleep(5 * time.Millisecond)

	// disconnect first client: this arms grace
	_ = clientConn1.Close()

	// reconnect immediately (well within grace period) before grace elapses
	serverConn2, clientConn2 := makePipe()
	ln.enqueue(serverConn2)

	// wait well past grace: grace should have been cancelled by the reconnect
	time.Sleep(comfortWait)

	select {
	case <-fired:
		t.Fatal("callback fired even though a client reconnected before grace elapsed")
	default:
	}

	// now disconnect the second client: grace re-arms, callback should fire
	_ = clientConn2.Close()

	select {
	case <-fired:
		// good
	case <-time.After(comfortWait * 2):
		t.Fatal("callback did not fire after second client disconnected and grace elapsed")
	}

	mu.Lock()
	count := fireCount
	mu.Unlock()
	if count != 1 {
		t.Fatalf("expected exactly one callback fire (process-lifetime once), got %d", count)
	}

	cancel()
	_ = ln.Close()
	<-done
}

// makePipe returns a server-side and client-side net.Conn pair via net.Pipe.
func makePipe() (net.Conn, net.Conn) {
	s, c := net.Pipe()
	return s, c
}
