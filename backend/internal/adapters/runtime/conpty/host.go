// Package conpty - host.go implements the serve engine for the pty-host
// detached process. It owns the agent's PTY (via the ptyConn seam), exposes
// it over a loopback TCP socket using the B1 binary protocol, replays
// scrollback to new clients, fans output to all connected clients, and shuts
// down gracefully (PTY dispose first, then clients, then listener).
//
// This file is cross-platform; build-tagged files provide the native PTY.
package conpty

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

const (
	initialConPTYColumns = 220
	initialConPTYRows    = 50
	// Each client gets a bounded writer queue. Output waits for a full queue to
	// drain, so every viewer sees every byte and a burst pauses the program the
	// way a real terminal does, instead of dropping the viewer and replaying a
	// truncated ring on re-attach. A viewer that stops reading for
	// slowClientGrace is still dropped, so one stalled viewer cannot hold the
	// PTY; status probes, attaches and input never wait on a viewer's queue.
	hostClientWriteBuffer = 256
)

// hostIdleExit is how long a pty-host whose child has exited waits with no
// attached client before shutting itself down (issue #5948: a daemon crash
// must not leave an exited-child host alive forever). Package var: tests
// shorten it.
var hostIdleExit = 5 * time.Minute

// slowClientGrace is how long output waits on a viewer that frees no queue
// space before dropping it (a variable so tests can shorten it).
var slowClientGrace = 3 * time.Second

// ptyConn is the host's handle to the running agent's pseudo-terminal.
// The real impl (conptyConn) lives in host_conpty_windows.go; tests use a fake.
type ptyConn interface {
	io.Reader // PTY output (raw bytes from the terminal)
	io.Writer // PTY input (keystrokes to the terminal)
	Resize(cols, rows int) error
	Close() error          // dispose the platform PTY
	Done() <-chan struct{} // closed when the child process exits
	ExitCode() (int, bool) // (code, true) once exited; (0, false) while running
	PID() int
}

// ServeConfig carries everything the host needs.
type ServeConfig struct {
	SessionID string
	Listener  net.Listener // caller provides (loopback); engine owns Accept loop
	PTY       ptyConn
	Ring      *Ring
	// LazySurface defers the rendered surface until a styled-output read
	// first asks for it, then keeps it current; see asyncSurface.
	LazySurface bool
}

// Serve runs the host event loop until the listener closes or Shutdown is
// invoked via the returned ShutdownFunc. It pumps PTY output into the ring
// and broadcasts to all clients, accepts new clients (replaying ring snapshot),
// and dispatches client messages. On PTY exit it broadcasts a status update
// but stays alive (keep-alive, mirroring tmux behavior) until no viewer has
// been attached for hostIdleExit. Returns when shut down.
func Serve(ctx context.Context, cfg ServeConfig) error {
	return newHost(cfg).run(ctx)
}

func newHost(cfg ServeConfig) *host {
	return &host{
		cfg:     cfg,
		clients: make(map[net.Conn]*clientState),
		surface: newAsyncSurface(initialConPTYColumns, initialConPTYRows,
			func(cols, rows int) surfaceSink { return newRenderedSurface(cols, rows) }, cfg.Ring.Replay, cfg.LazySurface),
		modes:     newModeTracker(),
		shutdownC: make(chan struct{}),
	}
}

// clientState is the host's per-connection bookkeeping. cols/rows record the
// grid this client last asked for (sized reports whether it ever asked), so the
// host can size the shared PTY to the largest attached client (see
// applyLargestLocked). A connection that never sends a resize stays sized=false
// and never influences the shared grid.
type clientState struct {
	cols, rows int
	sized      bool

	out       chan []byte
	drained   chan struct{} // signalled when the writer frees queue space
	done      chan struct{}
	closeOnce sync.Once
}

func newClientState() *clientState {
	return &clientState{
		out:     make(chan []byte, hostClientWriteBuffer),
		drained: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
}

func (c *clientState) hasRoom() bool { return len(c.out) < cap(c.out) }

// enqueue is deliberately non-blocking. A slow client is disposable; the
// shared PTY and every other viewer are not.
func (c *clientState) enqueue(frame []byte) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.out <- frame:
		return true
	case <-c.done:
		return false
	default:
		return false
	}
}

func (c *clientState) close(conn net.Conn) {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = conn.Close()
	})
}

// host holds the mutable state for a single pty-host session.
type host struct {
	cfg     ServeConfig
	mu      sync.Mutex
	clients map[net.Conn]*clientState
	surface *asyncSurface
	// modes follows the DEC private modes the program negotiated, so a client
	// attaching after they scrolled out of the ring still receives them (see
	// modeTracker). record updates it together with the ring under mu, so when
	// handleConn snapshots the ring, the tracker is up to the same chunk.
	modes *modeTracker

	// curCols/curRows are the grid the host last applied to the shared PTY (0,0
	// = none applied yet). Guarded by mu; used to skip redundant resizes.
	curCols, curRows int

	shutdownOnce sync.Once
	shutdownC    chan struct{} // closed when Shutdown is called
}

// applyLargestLocked sizes the shared PTY to a SINGLE client's grid — the
// largest by area — and resizes only when that choice changes. There is one PTY
// with one grid, so when several clients view it at once (e.g. the desktop app
// and the phone) the largest wins: a small viewer can never shrink the grid a
// larger one needs, which is what produced the "stripped-down" desktop view when
// a phone attached.
//
// Crucially this matches ONE client's cols AND rows as a pair, rather than taking
// an independent max of each axis. A per-axis max would synthesize a grid no
// client actually has — a wide-but-short desktop (120x30) plus a narrow-but-tall
// phone (55x48) would yield 120x48 — and that phantom grid mis-renders for every
// client (the desktop draws its footer at a row it can't show; the phone gets
// columns it can't fit). Matching one client exactly keeps that client (the
// largest — normally the desktop) pixel-correct; only smaller clients scale.
//
// Called on every client resize and on every disconnect, so the grid follows a
// newly-attached larger client and falls back to the remaining largest one when
// it leaves. Callers must hold h.mu.
func (h *host) applyLargestLocked() {
	bestCols, bestRows, bestArea := 0, 0, 0
	for _, cs := range h.clients {
		if !cs.sized {
			continue
		}
		if area := cs.cols * cs.rows; area > bestArea {
			bestArea, bestCols, bestRows = area, cs.cols, cs.rows
		}
	}
	// No client has reported a size yet: leave the PTY at its current grid (the
	// initial size set when the ConPTY was created).
	if bestCols == 0 || bestRows == 0 {
		return
	}
	if bestCols == h.curCols && bestRows == h.curRows {
		return
	}
	h.curCols, h.curRows = bestCols, bestRows
	_ = h.cfg.PTY.Resize(bestCols, bestRows)
	h.surface.Resize(bestCols, bestRows)
}

// run is the main event loop.
func (h *host) run(ctx context.Context) error {
	// Pump PTY output to ring + broadcast.
	go h.pumpPTY()

	// Watch for ctx cancellation and trigger shutdown.
	go func() {
		select {
		case <-ctx.Done():
			h.shutdown()
		case <-h.shutdownC:
		}
	}()

	// runAcceptLoop accepts connections until the listener closes. A listener
	// close is normal (shutdown or external) and is treated as success.
	h.runAcceptLoop()
	return nil
}

// runAcceptLoop runs the Accept loop until the listener closes or returns an
// error. Listener-close errors are swallowed; they signal normal shutdown.
func (h *host) runAcceptLoop() {
	for {
		conn, err := h.cfg.Listener.Accept()
		if err != nil {
			return
		}
		go h.handleConn(conn)
	}
}

// shutdown is idempotent: disposes the PTY, closes clients, closes the
// listener. Mirrors the pty-host.ts shutdown() function.
// ponytail: 50ms sleep after pty.Close() gives the OS ConPTY helper
// (conpty_console_list_agent.exe) time to release cleanly; avoids the
// 0x800700e8 error dialog on Windows.
func (h *host) shutdown() {
	h.shutdownOnce.Do(func() {
		close(h.shutdownC)
		h.surface.stop()

		// 1. Dispose the PTY first (critical ordering).
		_ = h.cfg.PTY.Close()

		// 2. Brief grace so the OS ConPTY helper can clean up.
		time.Sleep(50 * time.Millisecond)

		// 3. Close all client connections.
		h.mu.Lock()
		clients := h.clients
		h.clients = make(map[net.Conn]*clientState)
		h.mu.Unlock()
		for conn, client := range clients {
			client.close(conn)
		}

		// 4. Close the listener to unblock Accept.
		_ = h.cfg.Listener.Close()
	})
}

// pumpPTY reads PTY output continuously, appends to the ring, and broadcasts
// to clients. On PTY exit it flushes the partial line and sends a status
// update but does NOT close the listener (keep-alive).
func (h *host) pumpPTY() {
	buf := make([]byte, 32*1024)
	for {
		n, err := h.cfg.PTY.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			h.record(chunk)
		}
		if err != nil {
			break
		}
	}

	// PTY reader is done (process exited or PTY closed). Wait for the Done
	// signal so ExitCode is populated before we send the status broadcast.
	<-h.cfg.PTY.Done()

	h.cfg.Ring.FlushPartial()

	code, _ := h.cfg.PTY.ExitCode()
	pid := h.cfg.PTY.PID()
	h.broadcast(statusFrame(false, pid, &code))
	// Keep-alive: the host stays up so clients can still connect and read
	// scrollback — but only for hostIdleExit once nobody is watching. A
	// daemon crash must not leave an exited-child host alive forever (#5948).
	go h.idleExitWatch()
}

// idleExitWatch shuts the host down once the child has exited and no client
// has been attached for hostIdleExit. Checked at hostIdleExit granularity:
// shutdown fires within [hostIdleExit, 2*hostIdleExit) after the last viewer
// detaches — never early. ponytail: coarse window; a finer tick only buys
// minutes on a leak reaper. The window is read once: tests mutate the var
// between hosts, and watchers may briefly outlive their own Serve.
func (h *host) idleExitWatch() {
	window := hostIdleExit
	emptySince := time.Time{}
	for {
		select {
		case <-h.shutdownC:
			return
		case <-time.After(window):
		}
		h.mu.Lock()
		empty := len(h.clients) == 0
		h.mu.Unlock()
		if !empty {
			emptySince = time.Time{} // viewers attached; re-arm for after they leave
			continue
		}
		if emptySince.IsZero() {
			emptySince = time.Now() // first empty tick after the last detach
			continue
		}
		if time.Since(emptySince) >= window {
			h.shutdown()
			return
		}
	}
}

// record appends one PTY chunk to the ring, the mode tracker and the rendered
// surface and queues it to every client, all in one h.mu hold. handleConn takes
// its snapshot, restores modes and registers the new client in one h.mu hold as
// well, so a chunk is either inside a new client's snapshot or queued after it,
// never both, and Restore never sees a mode set by a chunk recorded after the
// snapshot.
// Recorded outside the lock, a chunk that switched to the alternate screen
// could land in the tracker between an attach's snapshot and its Restore,
// painting the shell output that preceded the program into the alternate
// buffer; and a chunk appended just before a snapshot reached the new client
// twice, in the snapshot and again live.
//
// The wait for a slow viewer (see broadcast) therefore happens before the
// chunk is recorded, not between recording it and queueing it: a client that
// attaches during the wait gets a snapshot without the chunk and receives it
// live, once.
func (h *host) record(chunk []byte) {
	frame, err := EncodeMessage(MsgTerminalData, chunk)
	if !h.lockWithRoom() {
		return
	}
	h.cfg.Ring.Append(chunk)
	_, _ = h.modes.Write(chunk)
	h.surface.Write(chunk)
	if err == nil {
		h.enqueueAllLocked(frame)
	}
	h.mu.Unlock()
}

// broadcast queues msg to all connected clients. Socket writes happen only in
// each client's writer goroutine, never while h.mu is held, so status probes,
// new attaches and other viewers never wait on a viewer's socket.
func (h *host) broadcast(msg []byte) {
	if !h.lockWithRoom() {
		return
	}
	h.enqueueAllLocked(msg)
	h.mu.Unlock()
}

// lockWithRoom takes h.mu once every client's queue has room for a frame and
// returns with it held, or returns false without it when the host shuts down.
// It is called only from the PTY read loop.
//
// When a client's queue is full, it waits (outside h.mu) for it to drain rather
// than dropping it: the read loop stops reading, the PTY fills, and the program
// pauses until the viewer catches up. A client that frees no space for
// slowClientGrace is dropped and re-attaches with a fresh snapshot.
func (h *host) lockWithRoom() bool {
	var waitingOn *clientState
	var deadline time.Time
	for {
		h.mu.Lock()
		var full *clientState
		var fullConn net.Conn
		for conn, client := range h.clients {
			if !client.hasRoom() {
				full, fullConn = client, conn
				break
			}
		}
		if full == nil {
			return true
		}
		if full != waitingOn {
			waitingOn, deadline = full, time.Now().Add(slowClientGrace)
		}
		if !time.Now().Before(deadline) {
			// Stalled: drop it so it cannot hold the PTY. A dropped client may
			// have been the largest viewer; the grid follows the rest.
			delete(h.clients, fullConn)
			h.applyLargestLocked()
			h.mu.Unlock()
			full.close(fullConn)
			waitingOn = nil
			continue
		}
		h.mu.Unlock()
		select {
		case <-full.drained:
		case <-full.done:
		case <-h.shutdownC:
			return false
		case <-time.After(time.Until(deadline)):
		}
	}
}

// enqueueAllLocked queues msg to every client. The caller holds h.mu and has
// seen every queue with room (lockWithRoom); only writers, which free space,
// touch the queues while h.mu is held, so these cannot fail.
func (h *host) enqueueAllLocked(msg []byte) {
	for _, client := range h.clients {
		client.enqueue(msg)
	}
}

// sendTo serializes a response behind that client's already-queued snapshot
// and terminal output. This also prevents concurrent response/broadcast writes
// from interleaving bytes and corrupting the frame stream.
func (h *host) sendTo(conn net.Conn, msg []byte) {
	h.mu.Lock()
	client := h.clients[conn]
	if client == nil {
		h.mu.Unlock()
		return
	}
	if !client.enqueue(msg) {
		delete(h.clients, conn)
		h.applyLargestLocked()
		h.mu.Unlock()
		client.close(conn)
		return
	}
	h.mu.Unlock()
}

// writeClient is the only goroutine that writes to conn. Keeping all writes
// here gives every client an ordered frame stream without putting socket
// back-pressure under the host's global lock.
//
// Frames already queued are written together in one call, so a burst of small
// PTY reads does not cost a syscall per read.
func (h *host) writeClient(conn net.Conn, client *clientState) {
	for {
		select {
		case <-client.done:
			return
		case frame := <-client.out:
			batch := net.Buffers{frame}
		drain:
			for len(batch) < cap(client.out) {
				select {
				case next := <-client.out:
					batch = append(batch, next)
				default:
					break drain
				}
			}
			select {
			case client.drained <- struct{}{}:
			default:
			}
			if _, err := batch.WriteTo(conn); err != nil {
				h.removeClient(conn, client)
				return
			}
		}
	}
}

// removeClient is idempotent; both the reader and writer can discover a dead
// connection. The identity guard prevents an obsolete goroutine from removing
// a hypothetical replacement registered under the same net.Conn key.
func (h *host) removeClient(conn net.Conn, client *clientState) {
	h.mu.Lock()
	if h.clients[conn] == client {
		delete(h.clients, conn)
		h.applyLargestLocked()
	}
	h.mu.Unlock()
	client.close(conn)
}

// handleConn manages the lifecycle of a single client connection.
func (h *host) handleConn(conn net.Conn) {
	client := newClientState()
	go h.writeClient(conn, client)

	// Scrollback replay: take the ring snapshot, queue it for the conn's writer,
	// and add the conn to the broadcast set, all under a SINGLE h.mu hold.
	// record() appends to the ring and queues to clients in one h.mu hold too,
	// so any PTY chunk is either already in this snapshot or queued strictly
	// after it, never both. The writer goroutine keeps the socket itself
	// outside this critical section.
	//
	// The snapshot is prefixed with the modes the program has set that the
	// snapshot itself no longer carries. The ring is bounded, so the
	// alternate-screen / mouse / bracketed-paste handshake a full-screen
	// program prints once at startup is the first thing it forgets; without
	// the prefix a late attacher renders the replay in its normal buffer with
	// mouse reporting off and cannot scroll the program (#5039). tmux re-sends
	// these on every attach, and the mux layer relies on the runtime doing so
	// (internal/terminal/attachment.go).
	//
	// Restore re-scans the snapshot under h.mu, so PTY output waits for it.
	// The scan is the tracker's byte loop (BenchmarkModeTracker: ~410 MB/s,
	// no allocation), linear in the replay: about half a millisecond for
	// 200 KB, on a path that already held this lock while EncodeMessage
	// copied the same bytes.
	h.mu.Lock()
	snap := h.cfg.Ring.Replay()
	if prefix := h.modes.Restore(snap); len(prefix) > 0 {
		snap = append(prefix, snap...)
	}
	if len(snap) > 0 {
		snapFrame, err := EncodeMessage(MsgTerminalData, snap)
		if err != nil || !client.enqueue(snapFrame) {
			h.mu.Unlock()
			client.close(conn)
			return
		}
	}
	h.clients[conn] = client
	h.mu.Unlock()

	defer h.removeClient(conn, client)

	parser := NewMessageParser(func(msgType byte, payload []byte) {
		h.handleClientMsg(conn, msgType, payload)
	})

	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			parser.Feed(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// handleClientMsg dispatches a decoded client message. Mirrors handleClientMessage
// from pty-host.ts.
func (h *host) handleClientMsg(conn net.Conn, msgType byte, payload []byte) {
	switch msgType {
	case MsgTerminalInput:
		if _, alive := h.cfg.PTY.ExitCode(); !alive {
			if n, err := h.cfg.PTY.Write(payload); err != nil {
				fmt.Fprintf(os.Stderr, "pty-host [%s]: input: wrote %d of %d bytes: %v\n", h.cfg.SessionID, n, len(payload), err)
			}
		}

	case MsgResize:
		if _, alive := h.cfg.PTY.ExitCode(); !alive {
			var rp ResizePayload
			if err := json.Unmarshal(payload, &rp); err == nil && rp.Cols > 0 && rp.Rows > 0 {
				// Record this client's requested grid, then size the shared PTY to
				// the largest client (see applyLargestLocked) rather than blindly
				// applying this one — otherwise a small viewer shrinks every viewer.
				h.mu.Lock()
				if cs := h.clients[conn]; cs != nil {
					cs.cols, cs.rows, cs.sized = rp.Cols, rp.Rows, true
				}
				h.applyLargestLocked()
				h.mu.Unlock()
			}
			// Malformed resize: ignore (matches TS behavior).
		}

	case MsgGetOutputReq:
		lines := 50 // default matches TS
		var req GetOutputReq
		if err := json.Unmarshal(payload, &req); err == nil && req.Lines > 0 {
			lines = req.Lines
		}
		text := h.cfg.Ring.Tail(lines)
		if frame, err := EncodeMessage(MsgGetOutputRes, []byte(text)); err == nil {
			h.sendTo(conn, frame)
		}

	case MsgGetStyledOutputReq:
		lines := 50
		var req GetOutputReq
		if err := json.Unmarshal(payload, &req); err == nil && req.Lines > 0 {
			lines = req.Lines
		}
		text := h.surface.Tail(lines)
		if frame, err := EncodeMessage(MsgGetStyledOutputRes, []byte(text)); err == nil {
			h.sendTo(conn, frame)
		}

	case MsgStatusReq:
		code, exited := h.cfg.PTY.ExitCode()
		alive := !exited
		pid := h.cfg.PTY.PID()
		var codePtr *int
		if exited {
			codePtr = &code
		}
		h.sendTo(conn, statusFrame(alive, pid, codePtr))

	case MsgKillReq:
		// Trigger graceful shutdown; returns immediately (idempotent).
		go h.shutdown()
	}
}

// statusFrame builds a MsgStatusRes frame.
func statusFrame(alive bool, pid int, exitCode *int) []byte {
	sp := StatusPayload{
		Alive: alive, PID: pid, ExitCode: exitCode,
		ProtocolVersion: conPTYHostProtocolVersion,
	}
	b, _ := json.Marshal(sp)
	frame, _ := EncodeMessage(MsgStatusRes, b) // b is small JSON, never overflows uint32
	return frame
}
