package acp

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"
)

// maxPendingSessionUpdates bounds the session/update notifications the ACP SDK
// has read but AO has not finished handling. The SDK queues notifications in a
// fixed 1,024-slot buffer and closes the connection when it overflows, so the
// limit leaves headroom for the other notifications it queues.
const maxPendingSessionUpdates = 512

var errSessionUpdateFlowClosed = errors.New("ACP session update flow closed")

// sessionUpdateFlow turns a burst of provider notifications into transport
// backpressure. A persistent host replays an in-flight prompt's whole journal on
// reconnect, faster than AO projects it; holding the excess in the socket makes
// the host wait instead of the SDK dropping the connection.
type sessionUpdateFlow struct {
	slots     chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newSessionUpdateFlow(limit int) *sessionUpdateFlow {
	return &sessionUpdateFlow{slots: make(chan struct{}, limit), done: make(chan struct{})}
}

// acquire reserves a slot for one session/update, waiting while the SDK already
// holds the limit. It reports false once the connection is gone.
func (f *sessionUpdateFlow) acquire() bool {
	select {
	case f.slots <- struct{}{}:
		return true
	case <-f.done:
		return false
	}
}

// release frees the slot of a session/update AO has finished handling.
func (f *sessionUpdateFlow) release() {
	if f == nil {
		return
	}
	select {
	case <-f.slots:
	default:
	}
}

func (f *sessionUpdateFlow) close() {
	if f == nil {
		return
	}
	f.closeOnce.Do(func() { close(f.done) })
}

// sessionUpdateFlowReader frames the SDK's input and reserves a flow slot for
// each session/update the SDK will hand to conversation.SessionUpdate.
type sessionUpdateFlowReader struct {
	reader     *bufio.Reader
	flow       *sessionUpdateFlow
	buffered   []byte
	pendingErr error
}

func newSessionUpdateFlowReader(reader io.Reader, flow *sessionUpdateFlow) io.Reader {
	return &sessionUpdateFlowReader{reader: bufio.NewReader(reader), flow: flow}
}

func (r *sessionUpdateFlowReader) Read(dst []byte) (int, error) {
	if len(r.buffered) == 0 {
		line, err := readACPFrame(r.reader, maxACPFrameSize)
		if len(line) == 0 {
			return 0, err
		}
		if dispatchesSessionUpdate(line) && !r.flow.acquire() {
			return 0, errSessionUpdateFlowClosed
		}
		r.buffered = line
		r.pendingErr = err
	}
	n := copy(dst, r.buffered)
	r.buffered = r.buffered[n:]
	if len(r.buffered) == 0 && r.pendingErr != nil {
		err := r.pendingErr
		r.pendingErr = nil
		return n, err
	}
	return n, nil
}

// dispatchesSessionUpdate mirrors the checks the SDK makes before it calls
// SessionUpdate, so every reserved slot is released by exactly one call.
func dispatchesSessionUpdate(line []byte) bool {
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(line, &envelope) != nil || envelope.Method != acpsdk.ClientMethodSessionUpdate {
		return false
	}
	if len(envelope.ID) > 0 && string(envelope.ID) != "null" {
		return false
	}
	var params acpsdk.SessionNotification
	return json.Unmarshal(envelope.Params, &params) == nil && params.Validate() == nil
}
