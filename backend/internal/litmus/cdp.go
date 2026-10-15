package litmus

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/coder/websocket"
)

// pageCDP connects to the engine's pinned app tab, even when target order changes.
func (e *Engine) pageCDP(ctx context.Context) (*pageCDP, error) {
	if e.connectPage != nil {
		return e.connectPage(ctx)
	}
	url, err := e.pageURL(ctx)
	if err != nil {
		return nil, err
	}
	conn, resp, err := websocket.Dial(ctx, url, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(32 << 20)
	return &pageCDP{conn: conn}, nil
}

func (e *Engine) pageURL(ctx context.Context) (string, error) {
	targetID, err := e.pinnedTargetID(ctx)
	if err != nil {
		return "", err
	}
	return e.targetURL(ctx, targetID)
}

// pinnedTargetID resolves the engine's app tab to the target ID that remains
// stable while the browser replaces its WebSocket connection.
func (e *Engine) pinnedTargetID(ctx context.Context) (string, error) {
	list, err := e.Run(ctx, pointTimeout, "tab", "list", "--json")
	if err != nil {
		return "", err
	}
	var reply struct {
		Data struct {
			Tabs []struct{ TabID, TargetID string }
		}
	}
	if err := json.Unmarshal([]byte(list), &reply); err != nil {
		return "", err
	}
	var targetID string
	for _, tab := range reply.Data.Tabs {
		if tab.TabID == e.tab {
			targetID = tab.TargetID
			break
		}
	}
	if targetID == "" {
		return "", fmt.Errorf("the engine's pinned app tab %q is missing", e.tab)
	}
	return targetID, nil
}

func (e *Engine) targetURL(ctx context.Context, targetID string) (string, error) {
	var targets []struct{ ID, Type, WebSocketDebuggerURL string }
	if err := getJSON(ctx, e.Endpoint+"/json/list", &targets); err != nil {
		return "", err
	}
	for _, t := range targets {
		if t.ID == targetID && t.Type == "page" && t.WebSocketDebuggerURL != "" {
			return t.WebSocketDebuggerURL, nil
		}
	}
	return "", fmt.Errorf("the engine's pinned app target %q is missing on the leg's debug port", targetID)
}

// recordingPageResolver pins the app target once while the engine is
// initialized, then returns a resolver for reconnects that only reads the
// debug endpoint. Reconnects must not take e.mu or invoke agent-browser:
// either can block Drive while a dead tab is being resolved.
func (e *Engine) recordingPageResolver(ctx context.Context) (string, func(context.Context) (string, error), error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	e.mu.Lock()
	targetID, err := e.pinnedTargetID(ctx)
	e.mu.Unlock()
	if err != nil {
		return "", nil, err
	}
	url, err := e.targetURL(ctx, targetID)
	if err != nil {
		return "", nil, err
	}
	return url, func(ctx context.Context) (string, error) {
		return e.targetURL(ctx, targetID)
	}, nil
}

type pageCDP struct {
	conn    *websocket.Conn
	id      int
	request func(context.Context, string, any, any) error // tests answer page protocol requests without a socket
}

func (p *pageCDP) call(ctx context.Context, method string, params, result any) error {
	if p.request != nil {
		return p.request(ctx, method, params, result)
	}
	p.id++
	request, err := json.Marshal(map[string]any{"id": p.id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := p.conn.Write(ctx, websocket.MessageText, request); err != nil {
		return err
	}
	for {
		_, data, err := p.conn.Read(ctx)
		if err != nil {
			return err
		}
		var reply struct {
			ID     int
			Result json.RawMessage
			Error  *struct{ Message string }
		}
		if err := json.Unmarshal(data, &reply); err != nil {
			return err
		}
		if reply.ID != p.id {
			continue // CDP events can arrive between the request and its reply.
		}
		if reply.Error != nil {
			return fmt.Errorf("%s: %s", method, reply.Error.Message)
		}
		if result != nil {
			return json.Unmarshal(reply.Result, result)
		}
		return nil
	}
}

func (p *pageCDP) evaluate(ctx context.Context, expression string, out any) error {
	var reply struct {
		Result           struct{ Value json.RawMessage }
		ExceptionDetails json.RawMessage
	}
	if err := p.call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true}, &reply); err != nil {
		return err
	}
	if len(reply.ExceptionDetails) > 0 {
		return fmt.Errorf("page evaluation failed: %s", reply.ExceptionDetails)
	}
	return json.Unmarshal(reply.Result.Value, out)
}
