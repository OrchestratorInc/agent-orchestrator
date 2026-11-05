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

	mu                    sync.Mutex
	pending               map[string]chan legacyACPResponse
	models                *legacySessionModelState
	toolCallToRPCID       map[string]string
	latestPermissionRPCID string
	pendingAnswers        map[string]map[string]string
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
		writer:          writer,
		reader:          sdkReader,
		output:          sdkWriter,
		pending:         make(map[string]chan legacyACPResponse),
		toolCallToRPCID: make(map[string]string),
		pendingAnswers:  make(map[string]map[string]string),
	}
	rewriter := &permissionResponseRewriter{
		inner:     writer,
		transport: transport,
	}
	return transport, rewriter, sdkReader
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

	if envelope.Method == "session/request_permission" && len(envelope.ID) > 0 {
		var req struct {
			ToolCall struct {
				ToolCallID string `json:"toolCallId"`
			} `json:"toolCall"`
		}
		_ = json.Unmarshal(envelope.Params, &req)
		rawID := string(bytes.TrimSpace(envelope.ID))
		t.mu.Lock()
		if req.ToolCall.ToolCallID != "" {
			t.toolCallToRPCID[req.ToolCall.ToolCallID] = rawID
		}
		t.latestPermissionRPCID = rawID
		t.mu.Unlock()
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

func (t *legacyACPTransport) registerAnswersForToolCall(toolCallID string, answers map[string]string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	rpcID := t.toolCallToRPCID[toolCallID]
	if rpcID == "" {
		rpcID = t.latestPermissionRPCID
	}
	if rpcID != "" {
		t.pendingAnswers[rpcID] = answers
	}
}

func (t *legacyACPTransport) hasPendingAnswers() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pendingAnswers) > 0
}

func (t *legacyACPTransport) rewriteResponse(data []byte) []byte {
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Result map[string]any  `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || len(envelope.ID) == 0 || envelope.Result == nil {
		return data
	}

	rpcID := string(bytes.TrimSpace(envelope.ID))
	t.mu.Lock()
	answers, ok := t.pendingAnswers[rpcID]
	if ok {
		delete(t.pendingAnswers, rpcID)
	}
	t.mu.Unlock()

	if !ok || len(answers) == 0 {
		return data
	}

	var rawMsg map[string]any
	if err := json.Unmarshal(data, &rawMsg); err != nil {
		return data
	}
	envelope.Result["answers"] = answers
	rawMsg["result"] = envelope.Result
	modifiedLine, err := json.Marshal(rawMsg)
	if err != nil {
		return data
	}
	return append(modifiedLine, '\n')
}

type permissionResponseRewriter struct {
	inner     *lockedWriteCloser
	transport *legacyACPTransport
}

func (r *permissionResponseRewriter) Write(data []byte) (int, error) {
	if r.transport != nil && r.transport.hasPendingAnswers() {
		data = r.transport.rewriteResponse(data)
	}
	return r.inner.Write(data)
}

func (r *permissionResponseRewriter) Close() error {
	return r.inner.Close()
}
