package daemon

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/panes"
	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
)

// paneSource links desktop windows on the terminal mux to the split-pane
// relay, translating the mux's raw JSON frames to the relay's types.
type paneSource struct{ broker *panes.Broker }

func (s paneSource) ConnectPanes(send func(json.RawMessage)) terminal.PaneConn {
	window := s.broker.Connect(func(cmd panes.Command) {
		if raw, err := json.Marshal(cmd); err == nil {
			send(raw)
		}
	})
	return paneConn{window}
}

type paneConn struct{ window *panes.Window }

func (c paneConn) Report(sessionID string, layout json.RawMessage) {
	c.window.Report(sessionID, layout)
}

func (c paneConn) Resolve(raw json.RawMessage) {
	var result panes.Result
	if json.Unmarshal(raw, &result) == nil && result.ID != "" {
		c.window.Resolve(result)
	}
}

func (c paneConn) Close() { c.window.Close() }
