package supervisor_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/daemon/supervisor"
)

type fakeListener struct {
	mu     sync.Mutex
	conns  []net.Conn
	closed bool
	ready  chan struct{}
}

func newFakeListener() *fakeListener {
	return &fakeListener{ready: make(chan struct{}, 1)}
}

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
			return nil, net.ErrClosed
		}
		if len(fl.conns) > 0 {
			c := fl.conns[0]
			fl.conns = fl.conns[1:]
			fl.mu.Unlock()
			return c, nil
		}
		fl.mu.Unlock()
		select {
		case <-fl.ready:
		default:
		}
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

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const testGrace = 30 * time.Millisecond

const comfortWait = testGrace * 5

func TestNeverFiresPreConnect(t *testing.T) {
	t.Parallel()

	fired := make(chan struct{})
	cb := func() { close(fired) }

	s := supervisor.New(testGrace, cb, noopLogger())
	ln := newFakeListener()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

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

func TestFiresOnceAfterGrace(t *testing.T) {
	t.Parallel()

	fireCount := 0
	var mu sync.Mutex
	fired := make(chan struct{})
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

	serverConn, clientConn := makePipe()
	ln.enqueue(serverConn)

	_ = clientConn.Close()

	select {
	case <-fired:
	case <-time.After(comfortWait * 2):
		t.Fatal("callback did not fire after client disconnected and grace elapsed")
	}

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

	serverConn1, clientConn1 := makePipe()
	ln.enqueue(serverConn1)
	time.Sleep(5 * time.Millisecond)

	_ = clientConn1.Close()

	serverConn2, clientConn2 := makePipe()
	ln.enqueue(serverConn2)

	time.Sleep(comfortWait)

	select {
	case <-fired:
		t.Fatal("callback fired even though a client reconnected before grace elapsed")
	default:
	}

	_ = clientConn2.Close()

	select {
	case <-fired:
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

func makePipe() (net.Conn, net.Conn) {
	s, c := net.Pipe()
	return s, c
}
