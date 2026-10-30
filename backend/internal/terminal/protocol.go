package terminal

// The wire protocol is a single multiplexed JSON stream tagged by channel
// ("ch"), mirroring the legacy Node mux server so the existing xterm client can
// connect unchanged. One socket carries every logical stream:
//
//	ch "terminal" — per-pane byte stream, keyed by an opaque runtime handle id
//	ch "subscribe" — the client opts into the session-state channel
//	ch "sessions"  — server-pushed session-state messages (CDC-fed)
//	ch "system"    — liveness; ws-level ping/pong also runs underneath
//
// Terminal payloads are base64 in the Data field: PTY output is arbitrary bytes
// and need not be valid UTF-8, which a raw JSON string could not carry.
const (
	chTerminal  = "terminal"
	chSubscribe = "subscribe"
	chSessions  = "sessions"
	chSystem    = "system"
)

// client message types (ch "terminal" unless noted).
const (
	msgOpen      = "open"
	msgData      = "data"
	msgResize    = "resize"
	msgClose     = "close"
	msgSubscribe = "subscribe" // ch "subscribe"
	msgPing      = "ping"      // ch "system"
)

// server message types.
const (
	msgOpened   = "opened"
	msgExited   = "exited"
	msgError    = "error"
	msgSnapshot = "snapshot" // ch "sessions"
	msgPong     = "pong"     // ch "system"
	// msgResize is reused as a SERVER frame too: the daemon pushes the shared
	// PTY's authoritative grid (Cols/Rows) to every attached client so followers
	// render the exact grid the PTY is using instead of their own fitted size.
)

// Client roles for a terminal open. A single PTY has one grid; when several
// clients view it at once the "primary" (the desktop, the real workspace) drives
// that grid and secondaries follow, rendering the authoritative size (scaled to
// fit their screen) rather than shrinking the PTY. An unset role is treated as
// primary, so existing clients keep driving the size.
const (
	roleSecondary = "secondary"
)

// clientMsg is one inbound frame. Fields are shared across channels; which are
// populated depends on Ch/Type.
type clientMsg struct {
	Ch   string `json:"ch"`
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	// Data is base64-encoded keystrokes for ch "terminal" / type "data".
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	// Force explicitly re-signals an unchanged grid for attach recovery. Normal
	// duplicate frames are ignored so they cannot trigger another TUI repaint.
	Force bool `json:"force,omitempty"`
	// Role is the client's role for a terminal "open" (see roleSecondary). Empty
	// means primary.
	Role string `json:"role,omitempty"`
}

// serverMsg is one outbound frame.
type serverMsg struct {
	Ch   string `json:"ch"`
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	// Data is base64-encoded PTY output for ch "terminal" / type "data".
	Data string `json:"data,omitempty"`
	// Cols/Rows carry the authoritative PTY grid for ch "terminal" / type
	// "resize" (server-pushed; see msgResize).
	Cols    uint16         `json:"cols,omitempty"`
	Rows    uint16         `json:"rows,omitempty"`
	Error   string         `json:"error,omitempty"`
	Session *sessionUpdate `json:"session,omitempty"`

	// raw holds a data frame's PTY output until the writer encodes it into
	// Data, so consecutive queued output for one terminal can be merged.
	raw []byte
}

// sessionUpdate is the ch "sessions" payload: a single CDC change projected to
// the fields a client needs to refresh its view. It deliberately omits the raw
// change_log payload blob; the client refetches detail over the REST surface.
type sessionUpdate struct {
	Seq       int64  `json:"seq"`
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId,omitempty"`
	EventType string `json:"eventType"`
}
