package terminal

import "encoding/json"

const (
	chTerminal    = "terminal"
	chSubscribe   = "subscribe"
	chSessions    = "sessions"
	chShellAgents = "shell-agents"
	chPanes       = "panes"
	chSystem      = "system"
)

const (
	msgOpen      = "open"
	msgData      = "data"
	msgResize    = "resize"
	msgClose     = "close"
	msgSubscribe = "subscribe"
	msgPing      = "ping"
	msgLayout    = "layout"
	msgResult    = "result"
)

const (
	msgOpened   = "opened"
	msgExited   = "exited"
	msgError    = "error"
	msgSnapshot = "snapshot"
	msgPong     = "pong"
	msgCommand  = "command"
)

const (
	roleSecondary = "secondary"
)

type clientMsg struct {
	Ch        string          `json:"ch"`
	ID        string          `json:"id,omitempty"`
	Type      string          `json:"type"`
	Data      string          `json:"data,omitempty"`
	Cols      uint16          `json:"cols,omitempty"`
	Rows      uint16          `json:"rows,omitempty"`
	Force     bool            `json:"force,omitempty"`
	Role      string          `json:"role,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Layout    json.RawMessage `json:"layout,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
}

type serverMsg struct {
	Ch      string          `json:"ch"`
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Data    string          `json:"data,omitempty"`
	Cols    uint16          `json:"cols,omitempty"`
	Rows    uint16          `json:"rows,omitempty"`
	Error   string          `json:"error,omitempty"`
	Session *sessionUpdate  `json:"session,omitempty"`
	Agents  json.RawMessage `json:"agents,omitempty"`
	Command json.RawMessage `json:"command,omitempty"`

	raw []byte
}

type sessionUpdate struct {
	Seq       int64  `json:"seq"`
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId,omitempty"`
	EventType string `json:"eventType"`
}
