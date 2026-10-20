package conpty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type replayInputHost struct {
	addr     string
	host     *host
	listener net.Listener
	accepted atomic.Int32
	inputs   atomic.Int32
	handlers sync.WaitGroup
	acceptC  chan struct{}
}

func startReplayInputHost(t *testing.T) *replayInputHost {
	return startReplayInputHostWithOrdering(t, false)
}

func startReplayInputHostWithOrdering(t *testing.T, replayBeforeInput bool) *replayInputHost {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	pty := newFakePTY(12345)
	ring := NewRing()
	// A retained TUI repaint can be much larger than the TCP receive buffer.
	// Every new input connection receives this replay before its status data.
	ring.Append([]byte(strings.Repeat("r", 1<<20) + "\n"))
	f := &replayInputHost{addr: listener.Addr().String(), listener: listener, acceptC: make(chan struct{})}
	f.host = newHost(ServeConfig{SessionID: "input-replay", Listener: listener, PTY: pty, Ring: ring, LazySurface: true})
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := pty.ReadInput(buffer)
			if n > 0 {
				f.inputs.Add(int32(strings.Count(string(buffer[:n]), "\r")))
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer close(f.acceptC)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if replayBeforeInput {
				if tcp, ok := conn.(*net.TCPConn); ok {
					_ = tcp.SetWriteBuffer(16 * 1024)
				}
				conn = &replayBeforeReadConn{Conn: conn, written: make(chan struct{}), closed: make(chan struct{})}
			}
			f.handlers.Add(1)
			f.accepted.Add(1)
			go func() {
				defer f.handlers.Done()
				f.host.handleConn(conn)
			}()
		}
	}()
	t.Cleanup(func() { f.host.shutdown() })
	return f
}

// replayBeforeReadConn selects a valid host scheduling interleaving: the replay
// writer runs before the input reader. It never manufactures an I/O error; the
// real TCP write either succeeds, or reports the peer reset caused by closing
// an input socket with unread replay. On reset, the writer closes the socket
// before the reader is scheduled again.
type replayBeforeReadConn struct {
	net.Conn
	written    chan struct{}
	closed     chan struct{}
	writeOnce  sync.Once
	readOnce   sync.Once
	closeOnce  sync.Once
	writeError atomic.Bool
}

func (c *replayBeforeReadConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.writeOnce.Do(func() {
		c.writeError.Store(err != nil)
		close(c.written)
	})
	return n, err
}

func (c *replayBeforeReadConn) Read(p []byte) (int, error) {
	c.readOnce.Do(func() {
		<-c.written
		if c.writeError.Load() {
			<-c.closed
		}
	})
	return c.Conn.Read(p)
}

func (c *replayBeforeReadConn) Close() error {
	err := c.Conn.Close()
	c.closeOnce.Do(func() { close(c.closed) })
	return err
}

func (f *replayInputHost) waitConnectionsClosed(t *testing.T, count int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.accepted.Load() < count {
		if time.Now().After(deadline) {
			t.Fatalf("host accepted %d connections, want %d", f.accepted.Load(), count)
		}
		time.Sleep(time.Millisecond)
	}
	_ = f.listener.Close()
	<-f.acceptC // No Add can race the Wait below.
	done := make(chan struct{})
	go func() { f.handlers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("input connections did not finish")
	}
}

func TestShortLivedInputIsDeliveredDuringReplay(t *testing.T) {
	const attempts = 32
	for name, send := range map[string]func(string) error{
		"raw Enter":     func(addr string) error { return clientSendInput(context.Background(), addr, "\r") },
		"message Enter": func(addr string) error { return clientSendMessage(context.Background(), addr, "") },
	} {
		t.Run(name, func(t *testing.T) {
			f := startReplayInputHost(t)
			for i := 0; i < attempts; i++ {
				if err := send(f.addr); err != nil {
					t.Fatalf("input %d: %v", i, err)
				}
			}
			f.waitConnectionsClosed(t, attempts)
			if got := f.inputs.Load(); got != attempts {
				t.Fatalf("all %d input calls returned nil, but PTY received only %d Enters", attempts, got)
			}
		})
	}
}

func TestInputDoesNotReportSuccessWhenReplayWriterWinsRace(t *testing.T) {
	for name, send := range map[string]func(string) error{
		"raw Enter":     func(addr string) error { return clientSendInput(context.Background(), addr, "\r") },
		"message Enter": func(addr string) error { return clientSendMessage(context.Background(), addr, "") },
	} {
		t.Run(name, func(t *testing.T) {
			f := startReplayInputHostWithOrdering(t, true)
			if err := send(f.addr); err != nil {
				t.Fatalf("send input: %v", err)
			}
			f.waitConnectionsClosed(t, 1)
			if got := f.inputs.Load(); got != 1 {
				t.Fatalf("input reported success, but PTY received %d Enters after replay", got)
			}
		})
	}
}

func TestPersistentInputDrainingReplayDeliversEveryEnter(t *testing.T) {
	const attempts = 32
	f := startReplayInputHost(t)
	conn, err := net.Dial("tcp", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() { _, _ = io.Copy(io.Discard, conn) }()
	frame, err := EncodeMessage(MsgTerminalInput, []byte("\r"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < attempts; i++ {
		if _, err := conn.Write(frame); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for f.inputs.Load() < attempts && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	_ = conn.Close()
	f.waitConnectionsClosed(t, 1)
	if got := f.inputs.Load(); got != attempts {
		t.Fatalf("persistent input delivered %d Enters, want %d", got, attempts)
	}
}

type inputWritePTY struct {
	ptyConn
	write func([]byte) (int, error)
}

func (p inputWritePTY) Write(data []byte) (int, error) { return p.write(data) }

func TestInputWaitsForPTYAcceptance(t *testing.T) {
	f := startReplayInputHost(t)
	original := f.host.cfg.PTY
	entered, release := make(chan struct{}), make(chan struct{})
	f.host.cfg.PTY = inputWritePTY{original, func(data []byte) (int, error) {
		close(entered)
		<-release
		return original.Write(data)
	}}
	done := make(chan error, 1)
	go func() { done <- clientSendInput(context.Background(), f.addr, "\r") }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("host did not attempt PTY input")
	}
	select {
	case err := <-done:
		t.Fatalf("returned before PTY acceptance: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f.waitConnectionsClosed(t, 1)
	if f.inputs.Load() != 1 {
		t.Fatal("acknowledged input was not delivered")
	}
}

func TestInputDoesNotAcknowledgePTYWriteFailure(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(fmt.Sprintf("short=%t", short), func(t *testing.T) {
			f := startReplayInputHost(t)
			f.host.cfg.PTY = inputWritePTY{f.host.cfg.PTY, func([]byte) (int, error) {
				if short {
					return 0, nil
				}
				return 0, io.ErrClosedPipe
			}}
			if err := clientSendInput(context.Background(), f.addr, "\r"); err == nil {
				t.Fatal("failed PTY write reported success")
			}
			f.waitConnectionsClosed(t, 1)
		})
	}
}

func TestMessageFinishesPasteAfterCallerCancellation(t *testing.T) {
	for _, size := range []int{1025, 128 * 1024} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := startServe(t, 12345)
			defer f.cancel()
			defer f.host.shutdown()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			message := strings.Repeat("x", size)
			received := make(chan string, 1)
			go func() {
				var text strings.Builder
				buffer := make([]byte, 4096)
				for {
					n, err := f.pty.ReadInput(buffer)
					if n > 0 {
						text.Write(buffer[:n])
						cancel() // The first accepted chunk must still finish with Enter.
						if strings.Contains(text.String(), "\r") {
							received <- text.String()
							return
						}
					}
					if err != nil {
						received <- text.String()
						return
					}
				}
			}()
			if err := clientSendMessage(ctx, f.addr, message); err != nil {
				t.Fatalf("completion failed after cancellation: %v", err)
			}
			if got := <-received; got != message+"\r" {
				t.Fatalf("received %d bytes, want %d complete bytes ending in Enter", len(got), len(message)+1)
			}
		})
	}
}

func TestInputCancellationAndMissingAcknowledgment(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
			_, _ = io.Copy(io.Discard, conn) // Read input, but never acknowledge it.
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := clientSendInput(ctx, listener.Addr().String(), "\r"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing ack error = %v, want context deadline", err)
	}
	select {
	case conn := <-accepted:
		_ = conn.Close()
	case <-time.After(time.Second):
		t.Fatal("host never accepted connection")
	}

	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if err := clientSendMessage(ctx, listener.Addr().String(), "must not paste"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled message error = %v", err)
	}
}

func TestInputRejectsInvalidAndUnsolicitedExitStatus(t *testing.T) {
	for name, payload := range map[string]string{"invalid": "not JSON", "exit": `{"alive":false,"exitCode":0}`} {
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				frame, _ := EncodeMessage(MsgStatusRes, []byte(payload))
				_, _ = conn.Write(frame)
				_, _ = io.Copy(io.Discard, conn)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := clientSendInput(ctx, listener.Addr().String(), "\r"); err == nil {
				t.Fatal("unconfirmed input reported success")
			}
		})
	}
}
