package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	acpsdk "github.com/coder/acp-go-sdk"
)

// ACP 0.13.5 removed the legacy model selector from its generated API while
// deployed agents such as Kimi still advertise `models` and implement
// session/set_model. This narrow wire shim preserves that compatibility without
// downgrading or forking the SDK. Requests and responses still use the SDK;
// update notifications use synchronous delivery so replay backpressure reaches
// the provider socket instead of overflowing the SDK's notification queue.
type legacyACPTransport struct {
	writer *lockedWriteCloser
	reader *io.PipeReader
	output *io.PipeWriter
	nextID atomic.Uint64

	mu      sync.Mutex
	pending map[string]chan legacyACPResponse
	models  *legacySessionModelState
}

type lockedWriteCloser struct {
	mu    sync.Mutex
	inner io.WriteCloser
}

func (w *lockedWriteCloser) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inner.Write(data)
}

func (w *lockedWriteCloser) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inner.Close()
}

type legacySessionModelState struct {
	CurrentModelID string            `json:"currentModelId"`
	Available      []legacyModelInfo `json:"availableModels"`
}

type legacyModelInfo struct {
	ModelID     string  `json:"modelId"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

type legacyACPResponse struct {
	err error
}

func newLegacyACPTransport(stdin io.WriteCloser) (*legacyACPTransport, io.WriteCloser, io.Reader) {
	writer := &lockedWriteCloser{inner: stdin}
	sdkReader, sdkWriter := io.Pipe()
	transport := &legacyACPTransport{
		writer:  writer,
		reader:  sdkReader,
		output:  sdkWriter,
		pending: make(map[string]chan legacyACPResponse),
	}
	return transport, writer, sdkReader
}

func (t *legacyACPTransport) forward(source io.Reader, notification func(string, json.RawMessage) bool) {
	reader := &extensionMethodReader{reader: bufio.NewReader(source), maxFrame: maxACPFrameSize}
	defer func() { _ = t.output.Close() }()
	for {
		line, err := reader.readFrame()
		if len(line) > 0 && !t.intercept(line, notification) {
			if _, writeErr := t.output.Write(line); writeErr != nil {
				_ = t.output.CloseWithError(writeErr)
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = t.output.Close()
			} else {
				_ = t.output.CloseWithError(err)
			}
			return
		}
	}
}

func (t *legacyACPTransport) intercept(line []byte, notification func(string, json.RawMessage) bool) bool {
	var envelope struct {
		ID     json.RawMessage      `json:"id"`
		Method string               `json:"method"`
		Params json.RawMessage      `json:"params"`
		Result json.RawMessage      `json:"result"`
		Error  *acpsdk.RequestError `json:"error"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return false
	}
	if (len(envelope.ID) == 0 || bytes.Equal(envelope.ID, []byte("null"))) &&
		envelope.Method != "" && notification(envelope.Method, envelope.Params) {
		return true
	}

	var requestID string
	if len(envelope.ID) > 0 && json.Unmarshal(envelope.ID, &requestID) == nil &&
		strings.HasPrefix(requestID, "ao-legacy-") {
		t.mu.Lock()
		pending := t.pending[requestID]
		delete(t.pending, requestID)
		t.mu.Unlock()
		if pending != nil {
			var responseErr error
			if envelope.Error != nil {
				responseErr = envelope.Error
			}
			pending <- legacyACPResponse{err: responseErr}
		}
		return true
	}

	if len(envelope.Result) > 0 {
		var setup struct {
			Models *legacySessionModelState `json:"models"`
		}
		if json.Unmarshal(envelope.Result, &setup) == nil && setup.Models != nil {
			t.mu.Lock()
			state := *setup.Models
			state.Available = append([]legacyModelInfo(nil), setup.Models.Available...)
			t.models = &state
			t.mu.Unlock()
		}
	}
	return false
}

func (t *legacyACPTransport) modelState() *legacySessionModelState {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.models == nil {
		return nil
	}
	state := *t.models
	state.Available = append([]legacyModelInfo(nil), t.models.Available...)
	return &state
}

func (t *legacyACPTransport) setModel(ctx context.Context, sessionID, modelID string) error {
	requestID := fmt.Sprintf("ao-legacy-%d", t.nextID.Add(1))
	response := make(chan legacyACPResponse, 1)
	t.mu.Lock()
	t.pending[requestID] = response
	t.mu.Unlock()

	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  "session/set_model",
		"params": map[string]string{
			"sessionId": sessionID,
			"modelId":   modelID,
		},
	}
	encoded, err := json.Marshal(request)
	if err == nil {
		encoded = append(encoded, '\n')
		_, err = t.writer.Write(encoded)
	}
	if err != nil {
		t.removePending(requestID)
		return err
	}

	select {
	case result := <-response:
		return result.err
	case <-ctx.Done():
		t.removePending(requestID)
		return ctx.Err()
	}
}

func (t *legacyACPTransport) removePending(requestID string) {
	t.mu.Lock()
	delete(t.pending, requestID)
	t.mu.Unlock()
}
