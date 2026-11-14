package conpty

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	initialConPTYColumns  = 220
	initialConPTYRows     = 50
	hostClientWriteBuffer = 256
)

var slowClientGrace = 3 * time.Second

type ptyConn interface {
	io.Reader
	io.Writer
	Resize(cols, rows int) error
	Close() error
	Done() <-chan struct{}
	ExitCode() (int, bool)
	PID() int
}

type ServeConfig struct {
	SessionID   string
	Listener    net.Listener
	PTY         ptyConn
	Ring        *Ring
	LazySurface bool
}

func Serve(ctx context.Context, cfg ServeConfig) error {
	return newHost(cfg).run(ctx)
}

func newHost(cfg ServeConfig) *host {
	var fg *foregroundTracker
	if reader, ok := cfg.PTY.(foregroundReader); ok {
		fg = newForegroundTracker(reader)
	}
	return &host{
		fg:      fg,
		cfg:     cfg,
		clients: make(map[net.Conn]*clientState),
		surface: newAsyncSurface(initialConPTYColumns, initialConPTYRows,
			func(cols, rows int) surfaceSink { return newRenderedSurface(cols, rows) }, cfg.Ring.Replay, cfg.LazySurface),
		modes:     newModeTracker(),
		da1C:      make(chan struct{}, 4),
		shutdownC: make(chan struct{}),
	}
}

type clientState struct {
	cols, rows   int
	sized        bool
	watcher      bool
	titleWatcher bool

	out       chan []byte
	drained   chan struct{}
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

type host struct {
	cfg        ServeConfig
	mu         sync.Mutex
	clients    map[net.Conn]*clientState
	surface    *asyncSurface
	modes      *modeTracker
	fg         *foregroundTracker
	fgOnce     sync.Once
	fgWatchers atomic.Int32
	da1C       chan struct{}
	title      string

	curCols, curRows int

	shutdownOnce sync.Once
	shutdownC    chan struct{}
}

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

func (h *host) run(ctx context.Context) error {
	go h.pumpPTY()
	if answersDeviceAttributes {
		go h.writeDA1Replies()
	}

	go func() {
		select {
		case <-ctx.Done():
			h.shutdown()
		case <-h.shutdownC:
		}
	}()

	h.runAcceptLoop()
	return nil
}

func (h *host) runAcceptLoop() {
	for {
		conn, err := h.cfg.Listener.Accept()
		if err != nil {
			return
		}
		go h.handleConn(conn)
	}
}

func (h *host) shutdown() {
	h.shutdownOnce.Do(func() {
		close(h.shutdownC)
		h.surface.stop()

		_ = h.cfg.PTY.Close()

		time.Sleep(50 * time.Millisecond)

		h.mu.Lock()
		clients := h.clients
		h.clients = make(map[net.Conn]*clientState)
		h.mu.Unlock()
		for conn, client := range clients {
			client.close(conn)
		}

		_ = h.cfg.Listener.Close()
	})
}

func (h *host) pumpPTY() {
	buf := make([]byte, 32*1024)
	var da1 da1Scanner
	var titles oscTitleScanner
	for {
		n, err := h.cfg.PTY.Read(buf)
		if n > 0 && answersDeviceAttributes {
			h.queueDA1Replies(da1.scan(buf[:n]))
		}
		if n > 0 {
			if raw, ok := titles.scan(buf[:n]); ok {
				h.setTitle(domain.NormalizeTerminalTitle(raw))
			}
		}
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			h.record(chunk)
		}
		if err != nil {
			break
		}
	}

	<-h.cfg.PTY.Done()

	h.cfg.Ring.FlushPartial()

	code, _ := h.cfg.PTY.ExitCode()
	pid := h.cfg.PTY.PID()
	h.broadcast(statusFrame(false, pid, &code))
}

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
	if h.fgWatchers.Load() > 0 {
		h.fg.noteOutput(time.Now())
	}
}

func (h *host) setTitle(title string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.title == title {
		return
	}
	h.title = title
	frame := titleFrame(title)
	for conn, client := range h.clients {
		if client.titleWatcher && !client.enqueue(frame) {
			delete(h.clients, conn)
			client.close(conn)
		}
	}
}

func titleFrame(title string) []byte {
	b, _ := json.Marshal(TitlePayload{Title: title})
	frame, _ := EncodeMessage(MsgTitleState, b)
	return frame
}

func (h *host) publishForeground(state ForegroundPayload) {
	frame := foregroundFrame(state)
	h.mu.Lock()
	for _, client := range h.clients {
		if client.watcher {
			client.enqueue(frame)
		}
	}
	h.mu.Unlock()
}

func foregroundFrame(state ForegroundPayload) []byte {
	b, _ := json.Marshal(state)
	frame, _ := EncodeMessage(MsgForegroundState, b)
	return frame
}

func (h *host) broadcast(msg []byte) {
	if !h.lockWithRoom() {
		return
	}
	h.enqueueAllLocked(msg)
	h.mu.Unlock()
}

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

func (h *host) enqueueAllLocked(msg []byte) {
	for _, client := range h.clients {
		if (client.watcher || client.titleWatcher) && msg[0] == MsgTerminalData {
			continue
		}
		client.enqueue(msg)
	}
}

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

func (h *host) removeClient(conn net.Conn, client *clientState) {
	h.mu.Lock()
	if h.clients[conn] == client {
		delete(h.clients, conn)
		if client.watcher {
			h.fgWatchers.Add(-1)
		}
		h.applyLargestLocked()
	}
	h.mu.Unlock()
	client.close(conn)
}

func (h *host) handleConn(conn net.Conn) {
	client := newClientState()
	go h.writeClient(conn, client)

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

func (h *host) handleClientMsg(conn net.Conn, msgType byte, payload []byte) {
	switch msgType {
	case MsgTerminalInput:
		if answersDeviceAttributes && da1Replies.Match(payload) {
			return
		}
		if h.fgWatchers.Load() > 0 {
			h.fg.noteInput(time.Now())
		}
		if _, alive := h.cfg.PTY.ExitCode(); !alive {
			if n, err := h.cfg.PTY.Write(payload); err != nil {
				fmt.Fprintf(os.Stderr, "pty-host [%s]: input: wrote %d of %d bytes: %v\n", h.cfg.SessionID, n, len(payload), err)
			}
		}

	case MsgResize:
		if _, alive := h.cfg.PTY.ExitCode(); !alive {
			var rp ResizePayload
			if err := json.Unmarshal(payload, &rp); err == nil && rp.Cols > 0 && rp.Rows > 0 {
				if h.fgWatchers.Load() > 0 {
					h.fg.noteResize(time.Now())
				}
				h.mu.Lock()
				if cs := h.clients[conn]; cs != nil {
					cs.cols, cs.rows, cs.sized = rp.Cols, rp.Rows, true
				}
				h.applyLargestLocked()
				h.mu.Unlock()
			}
		}

	case MsgGetOutputReq:
		lines := 50
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

	case MsgWatchForegroundReq:
		h.mu.Lock()
		if cs := h.clients[conn]; cs != nil && !cs.watcher {
			cs.watcher = true
			h.fgWatchers.Add(1)
		}
		h.mu.Unlock()
		h.fgOnce.Do(func() { go h.fg.run(h.shutdownC, h.publishForeground) })
		if state, ok := h.fg.current(); ok {
			h.sendTo(conn, foregroundFrame(state))
		}
		h.fg.wake()

	case MsgWatchTitleReq:
		h.mu.Lock()
		cs := h.clients[conn]
		if cs != nil {
			cs.titleWatcher = true
			cs.enqueue(titleFrame(h.title))
		}
		h.mu.Unlock()

	case MsgKillReq:
		go h.shutdown()
	}
}

func statusFrame(alive bool, pid int, exitCode *int) []byte {
	sp := StatusPayload{
		Alive: alive, PID: pid, ExitCode: exitCode,
		ProtocolVersion: conPTYHostProtocolVersion,
	}
	b, _ := json.Marshal(sp)
	frame, _ := EncodeMessage(MsgStatusRes, b)
	return frame
}
