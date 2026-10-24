// rpc.go is codex's transport (codexappserver/rpc.go) with the two deltas of
// DESIGN-zcodeappserver.md section 3.3: protocol-level rejections are logged at
// Error with their data, and a handler may pick its own refusal code. The
// framing is the same: NDJSON, no "jsonrpc" field, numeric ids for our requests
// (seeded from the persistent host on reconnect), string ids `server-<n>` for
// requests the server raises at us. Multi-waiter parking for re-announced
// server requests lives in serverrequest.go, not here: answer still writes
// exactly one reply per frame id.
package zcodeappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
)

// frame is the union of every shape that crosses the wire.
type frame struct {
	ID     *json.RawMessage `json:"id,omitempty"`
	Method string           `json:"method,omitempty"`
	Params json.RawMessage  `json:"params,omitempty"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *rpcError        `json:"error,omitempty"`
}

// rpcError is the error object on a failed response.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("app-server error %d: %s", e.Code, e.Message)
}

// notification is a server->client message with no id.
type notification struct {
	Method string
	Params json.RawMessage
}

// serverRequest is a server->client message that expects a reply. Approvals and
// user-input requests arrive this way, and the provider blocks on them.
type serverRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

// serverRequestHandler answers a server->client request. Returning an error
// sends a JSON-RPC error back, which the provider treats as a refusal to answer
// rather than as a decision.
type serverRequestHandler func(context.Context, serverRequest) (any, error)

// serverRequestArrivedHook is called synchronously on the read loop, in wire
// order, before a server request's handler goroutine is spawned. It lets the
// conversation register a host-replayed request ahead of the session/read
// response the same loop delivers next, so reconciliation can never run ahead
// of a replayed frame (DESIGN 3.5). It must not block.
type serverRequestArrivedHook func(serverRequest)

// ErrConnClosed reports use of a connection whose process has exited.
var ErrConnClosed = errors.New("app-server connection closed")

// conn is one live stdio connection to an app-server process.
//
// It is transport only: framing, request/response correlation, and fan-out. It
// knows nothing about threads or turns, so it can be tested over an in-memory
// pipe with no child process.
type conn struct {
	w   io.WriteCloser
	log *slog.Logger

	nextID atomic.Int64

	// writeMu serializes writes. It is never held while waiting for a response.
	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[int64]chan frame
	closed  bool

	// notificationInput decouples provider reads from notification consumption.
	// The relay owns an ordered queue so a replay burst cannot block response
	// correlation or silently drop a lifecycle frame.
	notificationInput chan notification
	notifications     chan notification

	onServerRequest        serverRequestHandler
	onServerRequestArrived serverRequestArrivedHook

	// done closes when the read loop exits, so a caller waiting on a response
	// learns the process died instead of blocking forever.
	done chan struct{}
	// readErr is set before done closes when the loop failed for a reason other
	// than clean EOF.
	readErr error
}

const (
	maxNotificationQueuedBytes = 32 << 20
	notificationQueueOverhead  = 64
)

func newConn(w io.WriteCloser, r io.Reader, log *slog.Logger, onReq serverRequestHandler) *conn {
	return newConnAt(w, r, log, onReq, nil, 0)
}

func newConnAt(w io.WriteCloser, r io.Reader, log *slog.Logger, onReq serverRequestHandler, onArrived serverRequestArrivedHook, nextID int64) *conn {
	c := &conn{
		w:                      w,
		log:                    log,
		pending:                make(map[int64]chan frame),
		notificationInput:      make(chan notification),
		notifications:          make(chan notification),
		onServerRequest:        onReq,
		onServerRequestArrived: onArrived,
		done:                   make(chan struct{}),
	}
	c.nextID.Store(nextID)
	go c.relayNotifications()
	go c.readLoop(r)
	return c
}

// relayNotifications keeps provider reads independent from a slow consumer
// without discarding protocol frames. The host bounds a detached replay by
// bytes; this queue preserves that replay's order while the conversation pump
// catches up.
func (c *conn) relayNotifications() {
	defer close(c.notifications)
	var queued []notification
	queuedBytes := 0
	input := c.notificationInput
	for input != nil || len(queued) > 0 {
		receive := input
		if queuedBytes >= maxNotificationQueuedBytes {
			receive = nil
		}
		var output chan notification
		var next notification
		if len(queued) > 0 {
			output = c.notifications
			next = queued[0]
		}
		select {
		case n, ok := <-receive:
			if !ok {
				input = nil
				continue
			}
			queued = append(queued, n)
			queuedBytes += notificationBytes(n)
		case output <- next:
			queuedBytes -= notificationBytes(next)
			queued[0] = notification{}
			queued = queued[1:]
			if len(queued) == 0 {
				queued = nil
			}
		}
	}
}

func notificationBytes(n notification) int {
	return notificationQueueOverhead + len(n.Method) + len(n.Params)
}

// notifications the caller consumes. Closed when the connection ends.
func (c *conn) notifs() <-chan notification { return c.notifications }

// wait returns a channel closed when the connection ends.
func (c *conn) wait() <-chan struct{} { return c.done }

// err reports why the read loop stopped, or nil for a clean end.
func (c *conn) err() error {
	select {
	case <-c.done:
		return c.readErr
	default:
		return nil
	}
}

func (c *conn) readLoop(r io.Reader) {
	defer func() {
		c.mu.Lock()
		c.closed = true
		pending := c.pending
		c.pending = map[int64]chan frame{}
		c.mu.Unlock()

		// Unblock every in-flight request so no caller hangs on a dead process.
		for _, ch := range pending {
			close(ch)
		}
		close(c.notificationInput)
		close(c.done)
	}()

	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := readFrame(br)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.readErr = err
			}
			return
		}
		if len(line) == 0 {
			continue
		}

		var f frame
		if err := json.Unmarshal(line, &f); err != nil {
			// One unparseable frame must not kill the connection: the provider
			// may add fields or emit something this build does not model.
			c.log.Warn("app-server frame not parseable", "error", err)
			continue
		}

		switch {
		case f.ID != nil && f.Method != "":
			req := serverRequest{ID: *f.ID, Method: f.Method, Params: f.Params}
			if c.onServerRequestArrived != nil {
				c.onServerRequestArrived(req)
			}
			go c.answer(req)
		case f.ID != nil:
			c.deliver(f)
		case f.Method != "":
			c.notificationInput <- notification{Method: f.Method, Params: f.Params}
		}
	}
}

// readFrame reads one newline-delimited frame with no length cap, so a large
// diff or command output cannot truncate the stream mid-JSON.
func readFrame(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if len(buf) > 0 && errors.Is(err, io.EOF) {
				return trimEOL(buf), nil
			}
			return nil, err
		}
		return trimEOL(buf), nil
	}
}

func trimEOL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func (c *conn) deliver(f frame) {
	var id int64
	if err := json.Unmarshal(*f.ID, &id); err != nil {
		// zcode delta (DESIGN 3.3): the server answers a malformed line with
		// {id:"parse-error", error:{code:-32700}} and a frame the strict 4-way
		// union rejects with {id:"invalid-message", error:{code:-32600,
		// data:{issues}}}. Both mean WE wrote something the schema refused, which
		// is the single most likely Phase 2 bug class, so they are logged at Error
		// with the issues rather than dropped as an unknown id.
		if rejected, reason := protocolRejection(f); rejected {
			c.log.Error("app-server rejected a client frame", "id", reason,
				"code", f.Error.Code, "message", f.Error.Message, "data", string(f.Error.Data))
			return
		}
		c.log.Warn("response with non-numeric id", "id", string(*f.ID))
		return
	}

	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()

	if !ok {
		// A response for a request we already gave up on. Expected after a
		// context timeout; not an error.
		c.log.Debug("response for unknown request id", "id", id)
		return
	}
	ch <- f
}

// answer runs the server-request handler and writes the reply. It runs on its
// own goroutine so a slow decision (a user staring at an approval card) does not
// stall the read loop and starve streaming deltas.
func (c *conn) answer(req serverRequest) {
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		select {
		case <-c.done:
			cancel()
		case <-finished:
		}
	}()
	result, err := c.onServerRequest(ctx, req)
	close(finished)
	cancel()

	reply := map[string]any{"id": req.ID}
	if err != nil {
		// A handler that returns an *rpcError chooses its own code: -32601 is the
		// only refusal that lets session/requestRuntimePreferences fall back to
		// defaults instead of failing session/create (DESIGN 3.3). Anything else
		// keeps codex's generic -32000.
		var typed *rpcError
		if errors.As(err, &typed) {
			reply["error"] = typed
		} else {
			reply["error"] = rpcError{Code: -32000, Message: err.Error()}
		}
	} else {
		reply["result"] = result
	}
	if werr := c.write(reply); werr != nil {
		c.log.Error("failed to answer app-server request", "method", req.Method, "error", werr)
	}
}

func (c *conn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	b = append(b, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.w.Write(b); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	return nil
}

// request sends a client->server request and waits for its response. The caller's
// context bounds the wait; a late response is discarded by deliver.
func (c *conn) request(ctx context.Context, method string, params, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan frame, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, ErrConnClosed)
	}
	c.pending[id] = ch
	c.mu.Unlock()

	payload := map[string]any{"id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	if err := c.write(payload); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, ctx.Err())

	case f, ok := <-ch:
		if !ok {
			return fmt.Errorf("%s: %w", method, ErrConnClosed)
		}
		if f.Error != nil {
			return fmt.Errorf("%s: %w", method, f.Error)
		}
		if out != nil && len(f.Result) > 0 {
			if err := json.Unmarshal(f.Result, out); err != nil {
				return fmt.Errorf("%s: decode result: %w", method, err)
			}
		}
		return nil
	}
}

// closeWrite closes stdin, which is how app-server is asked to shut down
// gracefully. The read loop then ends on EOF.
func (c *conn) closeWrite() error { return c.w.Close() }

// protocolRejection recognises the two string ids the server uses for
// protocol-level rejections (`decodeLine`). The returned reason is the id.
func protocolRejection(f frame) (bool, string) {
	if f.ID == nil || f.Error == nil {
		return false, ""
	}
	var id string
	if err := json.Unmarshal(*f.ID, &id); err != nil {
		return false, ""
	}
	switch id {
	case "parse-error", "invalid-message":
		return true, id
	}
	return false, ""
}
