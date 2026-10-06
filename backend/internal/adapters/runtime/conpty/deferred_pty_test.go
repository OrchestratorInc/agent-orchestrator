package conpty

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"slices"
	"sync"
	"testing"
	"time"
)

type startRecorder struct {
	mu    sync.Mutex
	sizes [][2]uint16
	conn  *fakePTY
	err   error
}

func (r *startRecorder) start(cols, rows uint16) (ptyConn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sizes = append(r.sizes, [2]uint16{cols, rows})
	if r.err != nil {
		return nil, r.err
	}
	return r.conn, nil
}

func (r *startRecorder) starts() [][2]uint16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.sizes)
}

func TestDeferredPTYStartsAtFirstGridAndReplaysEarlyInput(t *testing.T) {
	rec := &startRecorder{conn: newFakePTY(42)}
	d := newDeferredPTY(rec.start)

	if code, exited := d.ExitCode(); exited || code != 0 {
		t.Fatalf("ExitCode before start = %d, %v; want a running (not yet started) process", code, exited)
	}
	if pid := d.PID(); pid != 0 {
		t.Fatalf("PID before start = %d, want 0", pid)
	}
	// Keystrokes typed while the terminal is still sizing itself are kept.
	if _, err := d.Write([]byte("echo hi\r")); err != nil {
		t.Fatalf("early Write: %v", err)
	}
	if got := rec.starts(); len(got) != 0 {
		t.Fatalf("process started before any grid: %v", got)
	}

	inputC := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		n, _ := rec.conn.ReadInput(buf)
		inputC <- string(buf[:n])
	}()
	if err := d.Resize(93, 27); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if got, want := rec.starts(), [][2]uint16{{93, 27}}; !slices.Equal(got, want) {
		t.Fatalf("starts = %v, want %v", got, want)
	}
	select {
	case got := <-inputC:
		if got != "echo hi\r" {
			t.Fatalf("replayed input = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("early input was not replayed after the start")
	}

	// Later resizes resize the running process; they never start another.
	if err := d.Resize(120, 40); err != nil {
		t.Fatalf("second Resize: %v", err)
	}
	if got := rec.starts(); len(got) != 1 {
		t.Fatalf("starts after second resize = %v, want one", got)
	}
	if got := rec.conn.resizeSnapshot(); len(got) != 1 || got[0] != (ResizePayload{Cols: 120, Rows: 40}) {
		t.Fatalf("resizes forwarded to the process = %v", got)
	}
	if pid := d.PID(); pid != 42 {
		t.Fatalf("PID after start = %d, want 42", pid)
	}
	_ = d.Close()
}

// Input beyond the pre-start buffer is reported, not silently accepted.
func TestDeferredPTYReportsInputBeyondItsBuffer(t *testing.T) {
	d := newDeferredPTY((&startRecorder{conn: newFakePTY(42)}).start)
	if _, err := d.Write(make([]byte, maxDeferredInput-10)); err != nil {
		t.Fatalf("Write within the buffer: %v", err)
	}
	n, err := d.Write(make([]byte, 25))
	if n != 10 || !errors.Is(err, errDeferredInputFull) {
		t.Fatalf("Write past the buffer = %d, %v; want 10 accepted and errDeferredInputFull", n, err)
	}
	_ = d.Close()
}

func TestDeferredPTYClosedBeforeStartNeverStarts(t *testing.T) {
	rec := &startRecorder{conn: newFakePTY(42)}
	d := newDeferredPTY(rec.start)

	readC := make(chan error, 1)
	go func() {
		_, err := d.Read(make([]byte, 16))
		readC <- err
	}()
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-readC:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Read after close = %v, want EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read stayed blocked after Close")
	}
	select {
	case <-d.Done():
	default:
		t.Fatal("Done not closed after Close")
	}
	if err := d.Resize(93, 27); err != nil {
		t.Fatalf("Resize after Close: %v", err)
	}
	if got := rec.starts(); len(got) != 0 {
		t.Fatalf("a closed terminal started its process: %v", got)
	}
}

func TestDeferredPTYFailedStartReportsExit(t *testing.T) {
	rec := &startRecorder{err: errors.New("no such shell")}
	d := newDeferredPTY(rec.start)

	if err := d.Resize(93, 27); err == nil {
		t.Fatal("Resize hid the start failure")
	}
	if code, exited := d.ExitCode(); !exited || code != -1 {
		t.Fatalf("ExitCode after failed start = %d, %v; want -1, true", code, exited)
	}
	if _, err := d.Read(make([]byte, 16)); !errors.Is(err, io.EOF) {
		t.Fatalf("Read after failed start = %v, want EOF", err)
	}
	select {
	case <-d.Done():
	default:
		t.Fatal("Done not closed after a failed start")
	}
}

// The host starts a deferred process at the grid of the first client that
// reports one, so a shell's first output is laid out for its viewer.
func TestServeStartsDeferredProcessAtFirstClientGrid(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	rec := &startRecorder{conn: newFakePTY(77)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeConfig{SessionID: "deferred", Listener: ln, PTY: newDeferredPTY(rec.start), Ring: NewRing()})
	}()

	c := newTestClient(t, ln.Addr().String())
	defer c.close()
	if err := c.send(MsgStatusReq, nil); err != nil {
		t.Fatalf("status request: %v", err)
	}
	typ, payload := c.readFrame(t)
	var status StatusPayload
	if typ != MsgStatusRes || json.Unmarshal(payload, &status) != nil || !status.Alive {
		t.Fatalf("status before start = type %d %s; want alive", typ, payload)
	}
	if got := rec.starts(); len(got) != 0 {
		t.Fatalf("process started before a client reported a grid: %v", got)
	}

	resize, _ := json.Marshal(ResizePayload{Cols: 93, Rows: 27})
	if err := c.send(MsgResize, resize); err != nil {
		t.Fatalf("resize: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(rec.starts()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got, want := rec.starts(), [][2]uint16{{93, 27}}; !slices.Equal(got, want) {
		t.Fatalf("starts = %v, want %v", got, want)
	}

	if _, err := rec.conn.WriteOutput([]byte("user@host ~ % ")); err != nil {
		t.Fatalf("write output: %v", err)
	}
	typ, payload = c.readFrame(t)
	if typ != MsgTerminalData || string(payload) != "user@host ~ % " {
		t.Fatalf("first output frame = type %d %q", typ, payload)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestPtyHostArgsCarryHostOptions(t *testing.T) {
	argv := []string{"/bin/zsh", "-l"}
	opts := HostOptions{StartOnAttach: true, LazySurface: true}
	shell := ptyHostArgs("shellterm-1", "/tmp/ws", argv, opts)
	if want := []string{"pty-host", "--start=attach", "--surface=lazy", "shellterm-1", "/tmp/ws", "/bin/zsh", "-l"}; !slices.Equal(shell, want) {
		t.Fatalf("shell args = %q, want %q", shell, want)
	}
	got, rest := splitHostOptionArgs(shell[1:])
	if got != opts || !slices.Equal(rest, shell[3:]) {
		t.Fatalf("split shell = %+v, %q", got, rest)
	}

	// Agents and hosts spawned by older daemons keep the legacy positional
	// argv: they start immediately and keep their rendered surface current.
	agent := ptyHostArgs("session-1", "/tmp/ws", argv, HostOptions{})
	if want := []string{"pty-host", "session-1", "/tmp/ws", "/bin/zsh", "-l"}; !slices.Equal(agent, want) {
		t.Fatalf("agent args = %q, want %q", agent, want)
	}
	got, rest = splitHostOptionArgs(agent[1:])
	if got != (HostOptions{}) || !slices.Equal(rest, agent[1:]) {
		t.Fatalf("split agent = %+v, %q", got, rest)
	}
}
