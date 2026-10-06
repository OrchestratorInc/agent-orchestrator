package domain

import (
	"encoding/json"
	"time"
)

// TestScreenshotRequest captures the attempt's bound window without arguments.
type TestScreenshotRequest struct {
	_ struct{} `additionalProperties:"false"`
}

// TestMouseButton selects one button. Empty selects the default left button.
type TestMouseButton string

// Test mouse buttons accepted by the click tool.
const (
	TestMouseButtonLeft   TestMouseButton = "left"
	TestMouseButtonRight  TestMouseButton = "right"
	TestMouseButtonMiddle TestMouseButton = "middle"
)

// TestClickRequest uses pixels from an unscaled screenshot, with origin at its
// top-left. The service resolves ScreenshotID within the current attempt.
type TestClickRequest struct {
	_            struct{}        `additionalProperties:"false"`
	ScreenshotID string          `json:"screenshotId" minLength:"1"`
	X            int             `json:"x" minimum:"0"`
	Y            int             `json:"y" minimum:"0"`
	Button       TestMouseButton `json:"button,omitempty" enum:"left,right,middle" default:"left"`
}

// TestTypeRequest focuses a point in the bound window before entering text.
type TestTypeRequest struct {
	_            struct{} `additionalProperties:"false"`
	ScreenshotID string   `json:"screenshotId" minLength:"1"`
	X            int      `json:"x" minimum:"0"`
	Y            int      `json:"y" minimum:"0"`
	Text         string   `json:"text" maxLength:"16384"`
}

// TestKeyRequest sends one key or a simultaneous chord to the bound window.
type TestKeyRequest struct {
	_            struct{} `additionalProperties:"false"`
	ScreenshotID string   `json:"screenshotId" minLength:"1"`
	Keys         []string `json:"keys" minItems:"1" maxItems:"4"`
}

// TestReadLogsRequest selects bounded output from daemon-owned target logs.
// It accepts no filesystem path. An empty cursor starts at the log's beginning.
type TestReadLogsRequest struct {
	_        struct{} `additionalProperties:"false"`
	Cursor   string   `json:"cursor,omitempty" maxLength:"256"`
	MaxBytes int      `json:"maxBytes,omitempty" minimum:"1" maximum:"262144" default:"65536"`
}

// TestDaemonResource selects one of the two allowed target GET routes.
type TestDaemonResource string

// Test daemon resources are fixed routes, not arbitrary API paths.
const (
	TestDaemonProjects TestDaemonResource = "projects"
	TestDaemonSessions TestDaemonResource = "sessions"
)

// TestDaemonQueryRequest selects a read-only daemon query from a closed enum.
// Allowed resources are projects and sessions; no URL, path, method or body
// is accepted. The environment adapter supplies the exact target endpoint.
type TestDaemonQueryRequest struct {
	_        struct{}           `additionalProperties:"false"`
	Resource TestDaemonResource `json:"resource" enum:"projects,sessions"`
}

// TestWindowBounds is a native window rectangle in desktop points.
type TestWindowBounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// TestDesktopFrame maps captured pixels to one window and native rectangle.
// ScreenshotID is assigned after saving evidence. Input must reject changed
// window bounds, stale frames, foreign screenshot IDs and out-of-bounds pixels.
type TestDesktopFrame struct {
	ScreenshotID string             `json:"screenshotId" minLength:"1"`
	Target       TestTargetIdentity `json:"-"`
	Bounds       TestWindowBounds   `json:"bounds"`
	Width        int                `json:"width"`
	Height       int                `json:"height"`
	Scale        float64            `json:"scale"`
	// CaptureHandle is an in-memory adapter receipt. Preserve it when assigning
	// ScreenshotID after evidence storage; never accept it from worker input.
	CaptureHandle string    `json:"-"`
	CapturedAt    time.Time `json:"capturedAt"`
}

// TestScreenshot carries original encoded pixels, not a provider file path.
// JSON encodes Data as base64; the MCP adapter returns it as image content.
type TestScreenshot struct {
	Frame    TestDesktopFrame `json:"frame"`
	MIMEType string           `json:"mimeType"`
	Data     []byte           `json:"data"`
}

// TestActionResult reports delivery, not proof that the application changed.
// The worker must observe again to establish the action's effect.
type TestActionResult struct {
	Delivered bool   `json:"delivered"`
	Detail    string `json:"detail,omitempty"`
}

// TestLogResult contains a bounded log page and its continuation cursor.
type TestLogResult struct {
	Text       string `json:"text"`
	NextCursor string `json:"nextCursor,omitempty"`
	Truncated  bool   `json:"truncated"`
}

// TestDaemonQueryResult contains the JSON from one allowed target GET route.
type TestDaemonQueryResult struct {
	Resource TestDaemonResource `json:"resource" enum:"projects,sessions"`
	Data     json.RawMessage    `json:"data"`
}

// TestActionRecord is a journal entry written before dispatch and on completion.
// RequestID lets the service refuse a duplicate input without executing it twice.
// Inputs are validated tool data; headers, capability tokens and env are excluded.
type TestActionRecord struct {
	AttemptID              TestAttemptID   `json:"attemptId"`
	RequestID              string          `json:"requestId"`
	Tool                   string          `json:"tool"`
	Input                  json.RawMessage `json:"input"`
	State                  string          `json:"state"`
	Detail                 string          `json:"detail,omitempty"`
	DeliveryMode           string          `json:"deliveryMode,omitempty"`
	ConfiguredDeliveryMode string          `json:"configuredDeliveryMode,omitempty"`
	RecordingGap           string          `json:"recordingGap,omitempty"`
	Recording              json.RawMessage `json:"recording,omitempty"`
	At                     time.Time       `json:"at"`
}

// TestSubmitReportRequest saves the investigator's conclusion and replay steps.
// Markdown is limited to 64 KiB. Outcome uses the six TestOutcome wire labels.
type TestSubmitReportRequest struct {
	_        struct{}    `additionalProperties:"false"`
	Outcome  TestOutcome `json:"outcome" enum:"reproduced,not_reproduced,needs_information,environment_blocked,partial,cancelled"`
	Markdown string      `json:"markdown" maxLength:"65536"`
}

// TestSubmitReportResult links the retained report and its outcome.
type TestSubmitReportResult struct {
	Outcome    TestOutcome `json:"outcome"`
	EvidenceID string      `json:"evidenceId"`
}
