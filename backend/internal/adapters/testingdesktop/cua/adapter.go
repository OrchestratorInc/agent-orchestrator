// Package cua controls one launch-owned window through Cua Driver 0.34.0.
// It never exposes Cua's MCP server or permits desktop-scope operations.
package cua

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// ErrRefused means validation prevented input dispatch.
var ErrRefused = errors.New("cua input refused")

// ErrProvider means a driver operation failed; its effect can be unknown.
var ErrProvider = errors.New("cua driver failed")

// Error retains stable refusal/provider codes without claiming an app effect.
type Error struct {
	Code   string
	Detail string
	cause  error
}

func (e *Error) Error() string { return "cua " + e.Code + ": " + e.Detail }

// Unwrap distinguishes pre-dispatch validation from provider failures.
func (e *Error) Unwrap() error { return e.cause }

func refuse(code, detail string) error {
	return &Error{Code: code, Detail: detail, cause: ErrRefused}
}

// DeliveryMode selects the type/key policy. Clicks always use background.
// Foreground can interrupt the current app and must be journaled before input.
type DeliveryMode string

// Supported delivery policies. Empty config selects background.
const (
	Background DeliveryMode = "background"
	Foreground DeliveryMode = "foreground"
)

// Config is supplied by the supervising daemon, never by worker tool arguments.
// DataDir is its resolved AO data directory, not the target's data directory.
type Config struct {
	DataDir      string
	AppPath      string
	DeliveryMode DeliveryMode
	CaptureTTL   time.Duration
	// Runner and ProcessStartedAt allow deterministic boundary tests. Production
	// leaves both nil. Process timestamps must match the target adapter exactly.
	Runner           Runner
	ProcessStartedAt func(context.Context, int) (time.Time, error)
}

type binding struct {
	target    domain.TestTargetIdentity
	session   string
	receipt   *captureReceipt
	lastTyped *typedFocus
	recording *windowRecording
}

type typedFocus struct {
	point  pixel
	bounds domain.TestWindowBounds
}

type captureReceipt struct {
	frame        domain.TestDesktopFrame
	providerID   string
	screenshotID string
	keyToken     string
	elements     []capturedElement
}

type captureState struct {
	PID      int                     `json:"pid"`
	WindowID int                     `json:"window_id"`
	Bounds   domain.TestWindowBounds `json:"window_bounds"`
	Width    int                     `json:"screenshot_width"`
	Height   int                     `json:"screenshot_height"`
	Scale    float64                 `json:"screenshot_scale"`
	Capture  string                  `json:"capture_id"`
	Elements []capturedElement       `json:"elements"`
}

type capturedElement struct {
	Role  string       `json:"role"`
	Token string       `json:"element_token"`
	Frame *pixelBounds `json:"screenshot_frame"`
}

type pixelBounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"w"`
	Height float64 `json:"h"`
}

// Adapter serializes observation and input. The supervising daemon owns one
// instance, calls Close on shutdown, and must journal DeliveryMode before input.
type Adapter struct {
	mu            sync.Mutex
	cfg           Config
	runner        Runner
	started       func(context.Context, int) (time.Time, error)
	root          string
	bindings      map[string]*binding
	driver        driverIdentity
	pendingDriver driverIdentity
	closed        bool
	now           func() time.Time
	startRecorder func(args, env []string, stdout, stderr string) (*recordingProcess, error)
	stagingDir    string
}

var _ ports.TestingDesktopControl = (*Adapter)(nil)

// New validates config without starting a process or triggering permissions.
func New(cfg Config) (*Adapter, error) {
	if !filepath.IsAbs(cfg.DataDir) {
		return nil, refuse("invalid_config", "DataDir must be an absolute resolved AO data directory")
	}
	if cfg.AppPath == "" {
		cfg.AppPath = "/Applications/CuaDriver.app"
	}
	if !filepath.IsAbs(cfg.AppPath) || filepath.Ext(cfg.AppPath) != ".app" {
		return nil, refuse("invalid_config", "AppPath must name the signed CuaDriver.app bundle")
	}
	if cfg.DeliveryMode == "" {
		cfg.DeliveryMode = Background
	}
	if cfg.DeliveryMode != Background && cfg.DeliveryMode != Foreground {
		return nil, refuse("invalid_config", "delivery mode must be background or explicit foreground")
	}
	if cfg.CaptureTTL == 0 {
		cfg.CaptureTTL = 30 * time.Second
	}
	if cfg.CaptureTTL < 0 || cfg.CaptureTTL > 30*time.Second {
		return nil, refuse("invalid_config", "CaptureTTL must be positive and at most 30 seconds")
	}
	runner := cfg.Runner
	if runner == nil {
		runner = commandRunner{}
	}
	started := cfg.ProcessStartedAt
	if started == nil {
		started = func(ctx context.Context, pid int) (time.Time, error) {
			if err := ctx.Err(); err != nil {
				return time.Time{}, err
			}
			return process.StartTime(pid)
		}
	}
	root := filepath.Join(cfg.DataDir, "testing", "cua")
	if len(filepath.Join(root, "driver.sock")) > 103 {
		return nil, refuse("invalid_config", "resolved Unix socket path exceeds the macOS limit")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Adapter{cfg: cfg, runner: runner, started: started, root: root,
		bindings: make(map[string]*binding), now: time.Now, startRecorder: startScreencapture,
		stagingDir: filepath.Join(home, "Library", "Group Containers", "group.com.apple.screencapture", "ScreenRecordings")}, nil
}

// DeliveryMode lets the service journal the configured policy before dispatch.
func (a *Adapter) DeliveryMode() DeliveryMode { return a.cfg.DeliveryMode }

// BindWindow pins a single layer-zero window of the exact live Electron PID.
// Ambiguous windows require the daemon to supply a specific WindowID; there is
// no largest-window, frontmost-app, or desktop fallback.
func (a *Adapter) BindWindow(ctx context.Context, target domain.TestTargetIdentity) (domain.TestTargetIdentity, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return target, refuse("closed", "adapter has been closed")
	}
	if target.ID == "" || target.LaunchID == "" || target.Generation <= 0 || target.ElectronPID <= 0 || target.ElectronStartedAt.IsZero() {
		return target, refuse("invalid_target", "target launch identity and Electron birth time are required")
	}
	if err := a.checkProcess(ctx, target); err != nil {
		return target, err
	}
	if existing := a.bindings[target.ID]; existing != nil {
		candidate := target
		candidate.WindowID = existing.target.WindowID
		if !sameTarget(candidate, existing.target) || (target.WindowID != "" && target.WindowID != candidate.WindowID) {
			return target, refuse("target_changed", "a target ID cannot be rebound to a different launch or window")
		}
		if _, err := a.liveWindow(ctx, existing); err != nil {
			return target, err
		}
		return existing.target, nil
	}
	if err := a.ensureDriver(ctx); err != nil {
		return target, err
	}
	b := &binding{target: target, session: "ao-" + uuid.NewString()}
	windows, err := a.windows(ctx, b)
	if err != nil {
		return target, err
	}
	var candidates []window
	for _, w := range windows {
		if w.Layer == 0 && validBounds(w.Bounds) && (target.WindowID == "" || target.WindowID == strconv.Itoa(w.ID)) {
			candidates = append(candidates, w)
		}
	}
	if len(candidates) != 1 {
		return target, refuse("window_ambiguous", "expected exactly one usable window owned by the target PID")
	}
	if err := a.checkProcess(ctx, target); err != nil {
		return target, err
	}
	b.target.WindowID = strconv.Itoa(candidates[0].ID)
	a.bindings[target.ID] = b
	return b.target, nil
}

// Screenshot captures only the bound window at original resolution. The
// service must preserve Frame, including CaptureHandle, when assigning the
// evidence ScreenshotID. No provider identifier or provider path is returned.
func (a *Adapter) Screenshot(ctx context.Context, target domain.TestTargetIdentity) (shot domain.TestScreenshot, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.screenshot(ctx, target)
}

func (a *Adapter) screenshot(ctx context.Context, target domain.TestTargetIdentity) (shot domain.TestScreenshot, err error) {
	b, err := a.bound(ctx, target)
	if err != nil {
		return shot, err
	}
	b.receipt = nil
	if _, err = a.liveWindow(ctx, b); err != nil {
		return shot, err
	}
	f, err := os.CreateTemp(filepath.Join(a.root, "captures"), "frame-*.png")
	if err != nil {
		return shot, fmt.Errorf("create capture file: %w", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		return shot, err
	}
	defer func() { err = errors.Join(err, os.Remove(path)) }()
	args := a.targetArgs(b)
	args["max_image_dimension"] = 0
	args["include_accessibility_tree"] = a.cfg.DeliveryMode == Foreground
	args["timeout_ms"] = 5000
	args["screenshot_out_file"] = path
	capturedAt := a.now().UTC() // conservative age includes the capture call
	var state captureState
	if err := a.call(ctx, "get_window_state", args, &state); err != nil {
		return shot, err
	}
	if state.PID != target.ElectronPID || strconv.Itoa(state.WindowID) != target.WindowID || state.Capture == "" || !validBounds(state.Bounds) {
		return shot, refuse("capture_target_mismatch", "capture did not prove the bound PID, window and frame")
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 || info.Size() > 64<<20 {
		return shot, refuse("capture_invalid", "capture file missing, empty or larger than 64 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return shot, err
	}
	dimensions, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || dimensions.Width != state.Width || dimensions.Height != state.Height || !coherentScale(state) {
		return shot, refuse("capture_invalid", "PNG dimensions and native point scale disagree")
	}
	w, err := a.liveWindow(ctx, b)
	if err != nil {
		return shot, err
	}
	if w.Bounds != state.Bounds {
		return shot, refuse("window_changed", "window moved or resized during capture")
	}
	frame := domain.TestDesktopFrame{Target: target, Bounds: state.Bounds, Width: state.Width, Height: state.Height,
		Scale: state.Scale, CaptureHandle: uuid.NewString(), CapturedAt: capturedAt}
	b.receipt = &captureReceipt{frame: frame, providerID: state.Capture, elements: state.Elements}
	if b.lastTyped != nil {
		if b.lastTyped.bounds != frame.Bounds {
			b.lastTyped = nil
		} else {
			b.receipt.keyToken = typedFieldToken(state.Elements, b.lastTyped.point)
		}
	}
	return domain.TestScreenshot{Frame: frame, MIMEType: "image/png", Data: data}, nil
}

func coherentScale(s captureState) bool {
	return (s.Scale == 1 || s.Scale == 2) && s.Width > 0 && s.Height > 0 &&
		math.Abs(float64(s.Width)-s.Bounds.Width*s.Scale) <= 1 && math.Abs(float64(s.Height)-s.Bounds.Height*s.Scale) <= 1
}

// Click admits one fresh screenshot and sends its immutable Cua capture ID.
func (a *Adapter) Click(ctx context.Context, target domain.TestTargetIdentity, frame domain.TestDesktopFrame, request domain.TestClickRequest) (domain.TestActionResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if request.Button != "" && request.Button != domain.TestMouseButtonLeft && request.Button != domain.TestMouseButtonRight && request.Button != domain.TestMouseButtonMiddle {
		return domain.TestActionResult{}, refuse("invalid_button", "unsupported mouse button")
	}
	b, receipt, err := a.admit(ctx, target, frame, request.ScreenshotID, &pixel{request.X, request.Y})
	if err != nil {
		return domain.TestActionResult{}, err
	}
	args := a.inputArgs(b)
	args["delivery_mode"] = string(Background)
	args["x"], args["y"] = request.X, request.Y
	args["capture_id"] = receipt.providerID
	if request.Button != "" {
		args["button"] = string(request.Button)
	}
	b.lastTyped = nil
	return a.dispatch(ctx, "click", args)
}

// Type uses pixel focus in background mode. Explicit foreground mode resolves
// the addressed field from this screenshot and focuses its exact AX token.
func (a *Adapter) Type(ctx context.Context, target domain.TestTargetIdentity, frame domain.TestDesktopFrame, request domain.TestTypeRequest) (domain.TestActionResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, receipt, err := a.admit(ctx, target, frame, request.ScreenshotID, &pixel{request.X, request.Y})
	if err != nil {
		return domain.TestActionResult{}, err
	}
	args := a.inputArgs(b)
	args["x"], args["y"], args["text"] = request.X, request.Y, request.Text
	b.lastTyped = nil
	if a.cfg.DeliveryMode == Foreground {
		token := typedFieldToken(receipt.elements, pixel{request.X, request.Y})
		if token == "" {
			return domain.TestActionResult{}, refuse("type_focus_unresolved", "fresh screenshot does not resolve exactly one text field at this point")
		}
		delete(args, "x")
		delete(args, "y")
		args["element_token"] = token
	}
	// Remember the addressed field, including uncertain partial delivery. A
	// subsequent key still requires a new screenshot and a fresh field token.
	b.lastTyped = &typedFocus{point: pixel{request.X, request.Y}, bounds: frame.Bounds}
	return a.dispatch(ctx, "type_text", args)
}

// Key sends only a validated simultaneous chord to the bound window.
func (a *Adapter) Key(ctx context.Context, target domain.TestTargetIdentity, frame domain.TestDesktopFrame, request domain.TestKeyRequest) (domain.TestActionResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key, modifiers, err := keyChord(request.Keys)
	if err != nil {
		return domain.TestActionResult{}, err
	}
	b, receipt, err := a.admit(ctx, target, frame, request.ScreenshotID, nil)
	if err != nil {
		return domain.TestActionResult{}, err
	}
	args := a.inputArgs(b)
	args["key"], args["modifiers"] = key, modifiers
	if a.cfg.DeliveryMode == Foreground && b.lastTyped != nil {
		if receipt.keyToken == "" {
			return domain.TestActionResult{}, refuse("key_focus_unresolved", "fresh snapshot cannot resolve the last typed field in this window")
		}
		// Electron reactivation can clear renderer focus. Re-focus only the
		// exact field at the prior Type point using this fresh window snapshot.
		args["element_token"] = receipt.keyToken
	}
	return a.dispatch(ctx, "press_key", args)
}

func typedFieldToken(elements []capturedElement, point pixel) string {
	token := ""
	for _, element := range elements {
		if (element.Role != "AXTextField" && element.Role != "AXTextArea") || element.Frame == nil || element.Token == "" {
			continue
		}
		frame := element.Frame
		if float64(point.x) >= frame.X && float64(point.x) < frame.X+frame.Width && float64(point.y) >= frame.Y && float64(point.y) < frame.Y+frame.Height {
			if token != "" {
				return ""
			}
			token = element.Token
		}
	}
	return token
}

type pixel struct{ x, y int }

func (a *Adapter) admit(ctx context.Context, target domain.TestTargetIdentity, frame domain.TestDesktopFrame, screenshotID string, p *pixel) (*binding, *captureReceipt, error) {
	b, err := a.bound(ctx, target)
	if err != nil {
		return nil, nil, err
	}
	r := b.receipt
	if screenshotID == "" || frame.ScreenshotID != screenshotID || r == nil || frame.CaptureHandle == "" || frame.CaptureHandle != r.frame.CaptureHandle ||
		!sameTarget(frame.Target, target) || frame.Bounds != r.frame.Bounds || frame.Width != r.frame.Width || frame.Height != r.frame.Height ||
		frame.Scale != r.frame.Scale || !frame.CapturedAt.Equal(r.frame.CapturedAt) || (r.screenshotID != "" && r.screenshotID != screenshotID) {
		return nil, nil, refuse("screenshot_mismatch", "input requires this window's current evidence screenshot receipt")
	}
	age := a.now().Sub(frame.CapturedAt)
	if age < 0 || age >= a.cfg.CaptureTTL {
		return nil, nil, refuse("screenshot_stale", "capture expired; take a new screenshot")
	}
	if p != nil && (p.x < 0 || p.y < 0 || p.x >= frame.Width || p.y >= frame.Height) {
		return nil, nil, refuse("coordinate_outside_window", "point lies outside original screenshot pixels")
	}
	r.screenshotID = screenshotID
	w, err := a.liveWindow(ctx, b)
	if err != nil {
		return nil, nil, err
	}
	if w.Bounds != frame.Bounds {
		return nil, nil, refuse("window_changed", "window moved or resized; take a new screenshot")
	}
	if age := a.now().Sub(frame.CapturedAt); age < 0 || age >= a.cfg.CaptureTTL {
		return nil, nil, refuse("screenshot_stale", "capture expired during validation; take a new screenshot")
	}
	// A dispatched or uncertain input consumes the receipt, including type/key
	// whose Cua schemas lack capture_id. No automatic retry can duplicate input.
	b.receipt = nil
	return b, r, nil
}

func (a *Adapter) dispatch(ctx context.Context, name string, args map[string]any) (domain.TestActionResult, error) {
	var result struct {
		Effect  string `json:"effect"`
		Summary string `json:"summary"`
	}
	if err := a.call(ctx, name, args, &result); err != nil {
		return domain.TestActionResult{}, err
	}
	if (result.Effect != "confirmed" && result.Effect != "unverifiable") || result.Summary == "" {
		return domain.TestActionResult{}, &Error{Code: "provider_protocol", Detail: "input result lacks an explicit delivery effect", cause: ErrProvider}
	}
	return domain.TestActionResult{Delivered: true, Detail: fmt.Sprintf("delivery_mode=%s; %s", args["delivery_mode"], result.Summary)}, nil
}

func (a *Adapter) bound(ctx context.Context, target domain.TestTargetIdentity) (*binding, error) {
	b := a.bindings[target.ID]
	if a.closed || b == nil || !sameTarget(b.target, target) {
		return nil, refuse("target_not_bound", "target does not match a live adapter binding")
	}
	if err := a.checkProcess(ctx, target); err != nil {
		return nil, err
	}
	if err := a.checkDriver(ctx); err != nil {
		return nil, err
	}
	return b, nil
}

func (a *Adapter) checkProcess(ctx context.Context, target domain.TestTargetIdentity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	actual, err := a.started(ctx, target.ElectronPID)
	if err != nil || !actual.Equal(target.ElectronStartedAt) {
		return refuse("process_changed", "Electron PID is missing or its birth timestamp changed")
	}
	return nil
}

func sameTarget(x, y domain.TestTargetIdentity) bool {
	return x.ID == y.ID && x.LaunchID == y.LaunchID && x.Generation == y.Generation && x.ElectronPID == y.ElectronPID &&
		x.ElectronStartedAt.Equal(y.ElectronStartedAt) && x.DaemonPID == y.DaemonPID && x.DaemonStartedAt.Equal(y.DaemonStartedAt) &&
		x.DataDir == y.DataDir && x.WindowID == y.WindowID
}

func validBounds(b domain.TestWindowBounds) bool {
	return !math.IsNaN(b.X) && !math.IsNaN(b.Y) && !math.IsInf(b.X, 0) && !math.IsInf(b.Y, 0) &&
		b.Width > 0 && b.Height > 0 && !math.IsInf(b.Width, 0) && !math.IsInf(b.Height, 0)
}

type window struct {
	PID            int                     `json:"pid"`
	ID             int                     `json:"window_id"`
	Layer          int                     `json:"layer"`
	Bounds         domain.TestWindowBounds `json:"bounds"`
	OnScreen       bool                    `json:"is_on_screen"`
	OnCurrentSpace *bool                   `json:"on_current_space"`
}

func (a *Adapter) windows(ctx context.Context, b *binding) ([]window, error) {
	var result struct {
		Windows []window `json:"windows"`
	}
	args := a.targetArgs(b)
	// list_windows does not accept window_id. PID is always injected.
	delete(args, "window_id")
	if err := a.call(ctx, "list_windows", args, &result); err != nil {
		return nil, err
	}
	for _, w := range result.Windows {
		if w.PID != b.target.ElectronPID || w.ID <= 0 {
			return nil, refuse("window_owner_mismatch", "provider returned a foreign or invalid window")
		}
	}
	return result.Windows, nil
}

func (a *Adapter) liveWindow(ctx context.Context, b *binding) (window, error) {
	if err := a.checkProcess(ctx, b.target); err != nil {
		return window{}, err
	}
	windows, err := a.windows(ctx, b)
	if err != nil {
		return window{}, err
	}
	for _, w := range windows {
		if strconv.Itoa(w.ID) == b.target.WindowID && w.Layer == 0 && validBounds(w.Bounds) {
			if err := a.checkProcess(ctx, b.target); err != nil {
				return window{}, err
			}
			return w, nil
		}
	}
	return window{}, refuse("window_missing", "bound window no longer exists")
}

func (a *Adapter) targetArgs(b *binding) map[string]any {
	args := map[string]any{"pid": b.target.ElectronPID, "session": b.session}
	if b.target.WindowID != "" {
		id, _ := strconv.Atoi(b.target.WindowID) // IDs originate in validated provider integers.
		args["window_id"] = id
	}
	return args
}

func (a *Adapter) inputArgs(b *binding) map[string]any {
	args := a.targetArgs(b)
	args["scope"], args["delivery_mode"] = "window", string(a.cfg.DeliveryMode)
	return args
}

func (a *Adapter) call(ctx context.Context, name string, args map[string]any, result any) error {
	if err := a.checkDriver(ctx); err != nil {
		return err
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return err
	}
	out, runErr := a.run(ctx, a.binary(), "--socket", a.socket(), "call", name, string(encoded))
	var status struct {
		Code    string          `json:"code"`
		Effect  string          `json:"effect"`
		Summary string          `json:"summary"`
		IsError bool            `json:"isError"`
		Reason  json.RawMessage `json:"reason"`
	}
	decodeErr := json.Unmarshal(out.Stdout, &status)
	if runErr != nil || decodeErr != nil || status.IsError || status.Effect == "refused" {
		code := status.Code
		if code == "" {
			code = "provider_failure"
		}
		detail := status.Summary
		if detail == "" {
			detail = string(status.Reason)
		}
		if detail == "" && runErr != nil {
			detail = runErr.Error()
		}
		return &Error{Code: code, Detail: detail, cause: errors.Join(ErrProvider, runErr, decodeErr, ctx.Err())}
	}
	if result != nil {
		if err := json.Unmarshal(out.Stdout, result); err != nil {
			return &Error{Code: "provider_protocol", Detail: "invalid structured result", cause: errors.Join(ErrProvider, err)}
		}
	}
	return nil
}
