package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// cdpMsg is one Chrome DevTools Protocol message (command, response or event).
type cdpMsg struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// cdpClient is a minimal flat-session CDP client over the browser WebSocket.
// Event handlers run on the read goroutine: they must not call Call.
type cdpClient struct {
	conn    *websocket.Conn
	done    chan struct{} // closed when the connection is gone
	nextID  atomic.Int64
	wmu     sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan cdpMsg
	subs    map[int64]func(cdpMsg)
	subSeq  int64
}

func dialCDP(ctx context.Context, url string) (*cdpClient, error) {
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(256 << 20)
	c := &cdpClient{conn: conn, done: make(chan struct{}), pending: map[int64]chan cdpMsg{}, subs: map[int64]func(cdpMsg){}}
	go c.readLoop()
	return c, nil
}

func (c *cdpClient) readLoop() {
	defer close(c.done)
	for {
		_, data, err := c.conn.Read(context.Background())
		if err != nil {
			c.mu.Lock()
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		var m cdpMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		c.mu.Lock()
		fns := make([]func(cdpMsg), 0, len(c.subs))
		for _, fn := range c.subs {
			fns = append(fns, fn)
		}
		c.mu.Unlock()
		for _, fn := range fns {
			fn(m)
		}
	}
}

// maxMessage caps a message to Chrome. One over its 100 MB limit (a paste of
// a long text, a big 401 page sent back) closes the connection, which every
// view and the agent's tab tracking share.
const maxMessage = 64 << 20

func (c *cdpClient) write(ctx context.Context, id int64, sessionID, method string, params any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	b, _ := json.Marshal(cdpMsg{ID: id, Method: method, Params: raw, SessionID: sessionID})
	if len(b) > maxMessage {
		return fmt.Errorf("%s: %d-byte message, over the %d-byte limit", method, len(b), maxMessage)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	// coder/websocket closes the connection when a Write's context is cancelled
	// mid-write. This connection is shared by every viewer and the agent's tab
	// tracking, so a viewer disconnecting must never cancel a write on it.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return c.conn.Write(wctx, websocket.MessageText, b)
}

// Call sends a command and waits for its result.
func (c *cdpClient) Call(ctx context.Context, sessionID, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan cdpMsg, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(ctx, id, sessionID, method, params); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, errors.New("cdp connection closed")
		}
		if m.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		return m.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Notify sends a command without waiting. Writes are serialized, so input
// events keep their order (Chrome handles one session's messages in order).
func (c *cdpClient) Notify(sessionID, method string, params any) {
	_ = c.write(context.Background(), c.nextID.Add(1), sessionID, method, params)
}

// On registers an event handler and returns its removal function.
func (c *cdpClient) On(fn func(cdpMsg)) func() {
	c.mu.Lock()
	c.subSeq++
	id := c.subSeq
	c.subs[id] = fn
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.subs, id)
		c.mu.Unlock()
	}
}
