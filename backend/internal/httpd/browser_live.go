package httpd

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserstream"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/preview"
)

const (
	browserLiveReadLimit = 64 << 10
	browserFrameVersion  = byte(1)
	browserFrameJPEG     = byte(1)
	browserLiveHeartbeat = 30 * time.Second
)

type browserLiveBroker interface {
	Subscribe(context.Context, string) (*browserstream.Subscription, error)
	Send(context.Context, string, string, any) error
}

type browserLiveSessionReader interface {
	Get(context.Context, domain.SessionID) (domain.Session, error)
}

// BrowserLiveHub bridges authenticated mobile WebSockets to the private
// daemon↔Electron media link. It also owns the exclusive controller lease for
// each session and can synchronously revoke all streams when the desktop gate
// is disabled.
type BrowserLiveHub struct {
	broker   browserLiveBroker
	sessions browserLiveSessionReader
	enabled  func() bool
	log      *slog.Logger

	mu           sync.Mutex
	leases       map[string]context.CancelFunc
	loopbackBase string
}

// NewBrowserLiveHub creates the authenticated LAN WebSocket bridge.
func NewBrowserLiveHub(broker browserLiveBroker, sessions browserLiveSessionReader, enabled func() bool, log *slog.Logger) *BrowserLiveHub {
	return &BrowserLiveHub{broker: broker, sessions: sessions, enabled: enabled, log: loggerOrDefault(log), leases: map[string]context.CancelFunc{}}
}

// SetLoopbackBaseURL records the daemon's own loopback origin. The desktop
// browser loads workspace previews from it, so the hub needs it to hand the
// desktop a preview URL on behalf of a phone that reached the LAN listener.
func (h *BrowserLiveHub) SetLoopbackBaseURL(base string) {
	h.mu.Lock()
	h.loopbackBase = strings.TrimSpace(base)
	h.mu.Unlock()
}

// previewTarget seeds only the session's explicit preview selection, matching
// GET /preview. Resolve stored file selections against the current daemon origin
// and their selected root; unrelated repository entrypoints are never discovered.
func (h *BrowserLiveHub) previewTarget(session domain.Session) string {
	target := strings.TrimSpace(session.Metadata.PreviewURL)
	if entry, ok := preview.StoredEntryFromPreview(target, session.ID); ok {
		root := session.Metadata.WorkspacePath
		if entry.Scope == preview.StoredEntryScopeArtifact {
			root = session.Metadata.ArtifactDir
		}
		if _, exists := preview.EntryAtPath(root, entry.Path); !exists {
			return ""
		}
		h.mu.Lock()
		base := h.loopbackBase
		h.mu.Unlock()
		if base == "" {
			return ""
		}
		var resolved string
		var err error
		if entry.Scope == preview.StoredEntryScopeArtifact {
			resolved, err = preview.ArtifactFileURL(base, session.ID, entry.Path)
		} else {
			resolved, err = preview.FileURL(base, session.ID, entry.Path)
		}
		if err != nil {
			return ""
		}
		return resolved
	}
	if parsed, err := url.Parse(target); err == nil &&
		(parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
		return parsed.String()
	}
	return ""
}

// CloseAll revokes every active mobile browser lease.
func (h *BrowserLiveHub) CloseAll() {
	h.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(h.leases))
	for _, cancel := range h.leases {
		cancels = append(cancels, cancel)
	}
	h.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (h *BrowserLiveHub) reserve(sessionID string) (context.Context, func(), bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.leases[sessionID]; exists {
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.leases[sessionID] = cancel
	release := func() {
		h.mu.Lock()
		if current := h.leases[sessionID]; current != nil {
			delete(h.leases, sessionID)
			current()
		}
		h.mu.Unlock()
	}
	return ctx, release, true
}

func mountBrowserLive(r chi.Router, hub *BrowserLiveHub) {
	if hub == nil {
		return
	}
	r.Get("/api/v1/sessions/{id}/browser/live", hub.serve)
}

func (h *BrowserLiveHub) serve(w http.ResponseWriter, r *http.Request) {
	if !requestscope.IsLAN(r.Context()) {
		notFoundJSON(w, r)
		return
	}
	if h.enabled == nil || !h.enabled() {
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "BROWSER_REMOTE_DISABLED", "Browser viewing from mobile is disabled on the desktop", nil)
		return
	}
	// A failed native WebSocket handshake hides the HTTP error envelope from
	// mobile. Allow an authenticated HTTP retry to identify disabled sharing,
	// but never reserve a browser lease or start capture for a plain request.
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		envelope.WriteAPIError(w, r, http.StatusUpgradeRequired, "upgrade_required", "WEBSOCKET_REQUIRED", "A WebSocket connection is required", nil)
		return
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if sessionID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "SESSION_ID_REQUIRED", "session id is required", nil)
		return
	}
	session, err := h.sessions.Get(r.Context(), domain.SessionID(sessionID))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if session.IsTerminated {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SESSION_TERMINATED", "Session is terminated", nil)
		return
	}
	leaseCtx, release, ok := h.reserve(sessionID)
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "BROWSER_CONTROL_IN_USE", "Another mobile client controls this browser", nil)
		return
	}
	if h.enabled == nil || !h.enabled() {
		release()
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "BROWSER_REMOTE_DISABLED", "Browser viewing from mobile is disabled on the desktop", nil)
		return
	}
	defer release()

	sub, err := h.broker.Subscribe(r.Context(), sessionID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, browserstream.ErrUnavailable) {
			status = http.StatusServiceUnavailable
		}
		envelope.WriteAPIError(w, r, status, "unavailable", "BROWSER_HOST_UNAVAILABLE", "Desktop browser host is unavailable", nil)
		return
	}
	defer sub.Close()

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		h.log.Warn("browser live: websocket upgrade failed", "err", err)
		return
	}
	defer func() {
		if closeErr := c.Close(websocket.StatusNormalClosure, "browser stream closed"); closeErr != nil {
			h.log.Debug("browser live: websocket close failed", "err", closeErr)
		}
	}()
	c.SetReadLimit(browserLiveReadLimit)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		select {
		case <-leaseCtx.Done():
			cancel()
		case <-ctx.Done():
		}
	}()

	go h.watchPreview(ctx, sessionID)

	writeDone := make(chan error, 1)
	go func() { writeDone <- h.writeBrowserEvents(ctx, c, sub.Events, sub.Frames) }()
	readDone := make(chan error, 1)
	go func() { readDone <- h.readBrowserCommands(ctx, c, sessionID) }()
	select {
	case err = <-writeDone:
	case err = <-readDone:
	case <-ctx.Done():
		err = ctx.Err()
	}
	cancel()
	if err != nil && !errors.Is(err, context.Canceled) && websocket.CloseStatus(err) == -1 {
		h.log.Debug("browser live connection ended", "session", sessionID, "err", err)
	}
}

// Keep offering the current preview while a phone is watching. The page may
// be generated after the WebSocket opens, or the first offer may arrive while
// an agent command is active. Electron only accepts offers into an empty tab.
func (h *BrowserLiveHub) watchPreview(ctx context.Context, sessionID string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if session, err := h.sessions.Get(ctx, domain.SessionID(sessionID)); err == nil && !session.IsTerminated {
			if target := h.previewTarget(session); target != "" {
				previewCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if err := h.broker.Send(previewCtx, sessionID, "preview", map[string]string{"url": target}); err != nil {
					h.log.Debug("browser live: preview offer failed", "session", sessionID, "err", err)
				}
				cancel()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *BrowserLiveHub) writeBrowserEvents(ctx context.Context, c *websocket.Conn, events, frames <-chan browserstream.Event) error {
	heartbeat := time.NewTicker(browserLiveHeartbeat)
	defer heartbeat.Stop()
	for {
		var event browserstream.Event
		var ok bool
		// Drain queued controls before another frame so navigation state is
		// delivered promptly even while JPEG traffic is under backpressure.
		select {
		case event, ok = <-events:
		default:
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-heartbeat.C:
				pingCtx, cancel := context.WithTimeout(ctx, browserLiveHeartbeat/2)
				err := c.Ping(pingCtx)
				cancel()
				if err != nil {
					return err
				}
				continue
			case event, ok = <-events:
			case event, ok = <-frames:
			}
		}
		if !ok {
			return nil
		}
		if event.Frame != nil {
			frame := event.Frame
			packet := make([]byte, 22+len(frame.JPEG))
			packet[0], packet[1] = browserFrameVersion, browserFrameJPEG
			binary.BigEndian.PutUint64(packet[2:10], frame.Sequence)
			binary.BigEndian.PutUint64(packet[10:18], uint64(frame.Captured.UnixMilli()))
			binary.BigEndian.PutUint16(packet[18:20], frame.Width)
			binary.BigEndian.PutUint16(packet[20:22], frame.Height)
			copy(packet[22:], frame.JPEG)
			if err := writeBrowserMessage(ctx, c, websocket.MessageBinary, packet); err != nil {
				return err
			}
			continue
		}
		message := map[string]any{"type": event.Type}
		if len(event.Payload) > 0 {
			message["payload"] = event.Payload
		}
		if event.Code != "" {
			message["code"] = event.Code
		}
		if event.Message != "" {
			message["message"] = event.Message
		}
		payload, marshalErr := json.Marshal(message)
		if marshalErr != nil {
			return marshalErr
		}
		if err := writeBrowserMessage(ctx, c, websocket.MessageText, payload); err != nil {
			return err
		}
	}
}

func writeBrowserMessage(ctx context.Context, c *websocket.Conn, messageType websocket.MessageType, payload []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, browserLiveHeartbeat/2)
	defer cancel()
	return c.Write(writeCtx, messageType, payload)
}

type browserLiveCommand struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func (h *BrowserLiveHub) readBrowserCommands(ctx context.Context, c *websocket.Conn, sessionID string) error {
	for {
		messageType, payload, err := c.Read(ctx)
		if err != nil {
			return err
		}
		if messageType != websocket.MessageText {
			return c.Close(websocket.StatusUnsupportedData, "commands must be JSON text")
		}
		var command browserLiveCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return c.Close(websocket.StatusInvalidFramePayloadData, "invalid browser command")
		}
		if command.Type != "input" && command.Type != "navigate" && command.Type != "tab" {
			return c.Close(websocket.StatusPolicyViolation, "unsupported browser command")
		}
		commandCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = h.broker.Send(commandCtx, sessionID, command.Type, command.Payload)
		cancel()
		if err != nil {
			return err
		}
	}
}
