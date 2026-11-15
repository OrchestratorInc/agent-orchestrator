package codexappserver

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

type frame struct {
	ID     *json.RawMessage `json:"id,omitempty"`
	Method string           `json:"method,omitempty"`
	Params json.RawMessage  `json:"params,omitempty"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *rpcError        `json:"error,omitempty"`
}

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

type notification struct {
	Method string
	Params json.RawMessage
}

type serverRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

type serverRequestHandler func(context.Context, serverRequest) (any, error)

var ErrConnClosed = errors.New("app-server connection closed")

type conn struct {
	w   io.WriteCloser
	log *slog.Logger

	nextID atomic.Int64

	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[int64]chan frame
	closed  bool

	notificationInput chan notification
	notifications     chan notification

	onServerRequest serverRequestHandler

	done    chan struct{}
	readErr error
}

const (
	maxNotificationQueuedBytes = 32 << 20
	notificationQueueOverhead  = 64
)

func newConn(w io.WriteCloser, r io.Reader, log *slog.Logger, onReq serverRequestHandler) *conn {
	return newConnAt(w, r, log, onReq, 0)
}

func newConnAt(w io.WriteCloser, r io.Reader, log *slog.Logger, onReq serverRequestHandler, nextID int64) *conn {
	c := &conn{
		w:                 w,
		log:               log,
		pending:           make(map[int64]chan frame),
		notificationInput: make(chan notification),
		notifications:     make(chan notification),
		onServerRequest:   onReq,
		done:              make(chan struct{}),
	}
	c.nextID.Store(nextID)
	go c.relayNotifications()
	go c.readLoop(r)
	return c
}

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

func (c *conn) notifs() <-chan notification { return c.notifications }

func (c *conn) wait() <-chan struct{} { return c.done }

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
			c.log.Warn("app-server frame not parseable", "error", err)
			continue
		}

		switch {
		case f.ID != nil && f.Method != "":
			go c.answer(serverRequest{ID: *f.ID, Method: f.Method, Params: f.Params})
		case f.ID != nil:
			c.deliver(f)
		case f.Method != "":
			c.notificationInput <- notification{Method: f.Method, Params: f.Params}
		}
	}
}

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
		c.log.Warn("response with non-numeric id", "id", string(*f.ID))
		return
	}

	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()

	if !ok {
		c.log.Debug("response for unknown request id", "id", id)
		return
	}
	ch <- f
}

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
		reply["error"] = rpcError{Code: -32000, Message: err.Error()}
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

func (c *conn) notify(method string, params any) error {
	payload := map[string]any{"method": method}
	if params != nil {
		payload["params"] = params
	}
	return c.write(payload)
}

func (c *conn) closeWrite() error { return c.w.Close() }
