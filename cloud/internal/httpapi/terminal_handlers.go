package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

const (
	// Browsers can take far longer than the handshake itself to actually open
	// the socket: a cold hostname costs a fresh TLS session, and Firefox burns
	// tens of seconds probing HTTP/3 — which it cannot carry a WebSocket over —
	// before falling back. A 30s ticket expired mid-fallback, so every upgrade
	// arrived already dead and the client retried forever. The ticket stays
	// single-use and session-scoped; only the window to redeem it is wider.
	terminalTicketTTL          = 5 * time.Minute
	terminalReadyTimeout       = 20 * time.Second
	terminalSessionTTL         = 30 * time.Minute
	agentTerminalTTL           = 24 * time.Hour
	terminalInteractionTTL     = 2 * time.Minute
	terminalInteractionRefresh = 30 * time.Second
	terminalPingInterval       = 20 * time.Second
	terminalPingTimeout        = 5 * time.Second
	// terminalPingMaxFailures is how many consecutive keepalive pings may miss
	// (e.g. because a large output Write is holding the connection's write mutex
	// across a flush stall) before the socket is treated as dead. Tolerating a
	// few misses prevents a reconnect storm during heavy replays while still
	// closing a genuinely unresponsive socket within a bounded window.
	terminalPingMaxFailures = 3
	// An agent ticket request waits up to agentTicketWait for the agent
	// terminal to open, checking every agentTicketPoll.
	agentTicketWait = 8 * time.Second
	agentTicketPoll = 250 * time.Millisecond
	// terminalRelayGapMaxWait bounds how long a client's live output waits on
	// a sequence gap when it cannot see the durable mirror's progress (the
	// worker stream ended or moved to another replica). Normally a gap is
	// skipped as soon as the mirror has finished with it and the replay log
	// still lacks it, however far behind the mirror runs.
	terminalRelayGapMaxWait = 10 * time.Second
	// terminalRepaintInterval limits how often one attachment asks the
	// terminal's program to redraw over output it skipped.
	terminalRepaintInterval = 5 * time.Second
	// terminalClientRepaintInterval bounds repaints a client asks for after
	// its screen reflowed, so a panel being toggled cannot flood the program.
	terminalClientRepaintInterval = time.Second
	// terminalSlowWrite is how long one output write to a client may take
	// before it is logged, at most every terminalSlowWriteLogInterval.
	terminalSlowWrite            = 2 * time.Second
	terminalSlowWriteLogInterval = 10 * time.Second
)

var errTerminalProcessUnavailable = errors.New("terminal process unavailable")

func (s *Server) createTerminalTicket(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and sessionId must be UUIDs.")
		return
	}
	var input struct {
		Kind       string `json:"kind"`
		TerminalID string `json:"terminalId,omitempty"`
	}
	if err := decodeJSONLimit(w, r, &input, maxWorkerControlBody); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.Kind != "workspace" && input.Kind != "agent" {
		writeError(w, r, http.StatusUnprocessableEntity, "TERMINAL_KIND_UNSUPPORTED", "Terminal kind must be agent or workspace.")
		return
	}
	if input.TerminalID != "" && requireUUID(input.TerminalID, "terminalId") != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "The terminal ID must be a UUID.")
		return
	}
	// A ticket wakes a paused sandbox, so it is subject to the plan's usage
	// limits like resume is. This early answer takes no lock and returns at
	// once for unbilled (Coder, Docker) and running sessions; the transaction
	// that wakes the sandbox checks again.
	if s.billingStore != nil {
		if err := s.billingStore.WakeAllowed(r.Context(), principalFrom(r), orgID, sessionID); err != nil {
			s.writeStoreError(w, r, err)
			return
		}
	}
	token, scopes, err := s.store.IssueTerminalTicket(
		r.Context(), principalFrom(r), orgID, sessionID, input.Kind, input.TerminalID, terminalTicketTTL,
	)
	if errors.Is(err, postgres.ErrWorkerUnavailable) && input.Kind == "agent" && input.TerminalID == "" {
		token, scopes, err = s.awaitAgentTicket(r, orgID, sessionID, err)
	}
	if errors.Is(err, postgres.ErrTerminalSessionExited) {
		writeError(w, r, http.StatusGone, "TERMINAL_SESSION_EXITED", "The coding-agent terminal has exited. Start a new session to continue.")
		return
	}
	if errors.Is(err, postgres.ErrForbidden) {
		writeError(w, r, http.StatusForbidden, "TERMINAL_POLICY_DENIED", "Terminal access is not allowed for this session.")
		return
	}
	if err != nil {
		s.writeWorkspaceStoreError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"ticket": token, "expiresIn": int(terminalTicketTTL.Seconds()),
		"scopes": scopes,
	})
}

// agentTerminalProbe is the read-only readiness check awaitAgentTicket polls.
type agentTerminalProbe interface {
	AgentTerminalLive(ctx context.Context, principal domain.Principal, orgID, sessionID string) (bool, error)
}

// awaitAgentTicket holds an agent ticket request that found the agent not yet
// started, instead of answering 409 straight away. Each browser retry costs a
// full round trip on top of its poll interval, so on a distant client the
// ticket arrived up to ~0.5s after the terminal opened. Here the wait is a
// cheap read-only poll beside the database, and the ticket is minted as soon
// as the terminal is live. IssueTerminalTicket already ran once, so a paused
// sandbox is waking and the interaction lease is held. When the wait expires
// it answers the original 409 and the browser retries as before.
func (s *Server) awaitAgentTicket(r *http.Request, orgID, sessionID string, unavailable error) (string, []string, error) {
	probe, ok := s.store.(agentTerminalProbe)
	if !ok {
		return "", nil, unavailable
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentTicketWait)
	defer cancel()
	ticker := time.NewTicker(agentTicketPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", nil, unavailable
		case <-ticker.C:
		}
		live, err := probe.AgentTerminalLive(ctx, principalFrom(r), orgID, sessionID)
		if err != nil {
			if ctx.Err() != nil {
				return "", nil, unavailable
			}
			return "", nil, err
		}
		if live {
			return s.store.IssueTerminalTicket(
				r.Context(), principalFrom(r), orgID, sessionID, "agent", "", terminalTicketTTL,
			)
		}
	}
}

func (s *Server) connectTerminal(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("ticket"))
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	terminalID := strings.TrimSpace(r.URL.Query().Get("terminalId"))
	if kind == "" {
		kind = "workspace"
	}
	after, err := strconv.ParseInt(defaultString(r.URL.Query().Get("after"), "0"), 10, 64)
	if token == "" || (kind != "workspace" && kind != "agent") ||
		(terminalID != "" && requireUUID(terminalID, "terminalId") != nil) || err != nil || after < 0 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "A valid ticket, kind, and after cursor are required.")
		return
	}
	ttl := terminalSessionTTL
	if kind == "agent" {
		ttl = agentTerminalTTL
	}
	terminal, err := s.store.OpenTerminal(r.Context(), token, kind, terminalID, ttl)
	if errors.Is(err, postgres.ErrInvalidTicket) {
		if s.logger != nil {
			s.logger.Warn(
				"terminal ticket rejected",
				"reason", err,
				"kind", kind,
				"request_id", requestID(r),
			)
		}
		writeError(w, r, http.StatusUnauthorized, "INVALID_TERMINAL_TICKET", "The terminal ticket is invalid, expired, or already used.")
		return
	}
	if err != nil {
		s.writeWorkspaceStoreError(w, r, err)
		return
	}

	// Browser clients may be hosted on a separate Cloud UI origin. The
	// cryptographically random, single-use ticket is the request's CSRF and
	// authorization boundary, so origin affinity is neither required nor used.
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode:    websocket.CompressionDisabled,
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.closeTerminal(r, terminal)
		return
	}
	if s.logger != nil {
		s.logger.Info("browser terminal attached",
			"session_id", terminal.SessionID,
			"terminal_id", terminal.ID,
			"kind", terminal.Kind,
			"worker_epoch", terminal.WorkerEpoch,
		)
	}
	connection.SetReadLimit(maxTerminalFrame)
	defer func() {
		if terminal.Kind != "agent" {
			s.closeTerminal(r, terminal)
		}
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if err := s.store.RefreshTerminalInteraction(ctx, terminal, terminalInteractionTTL); err != nil {
		if s.logger != nil {
			s.logger.Debug("start terminal interaction lease", "error", err, "terminal_id", terminal.ID)
		}
	}
	go s.refreshTerminalInteraction(ctx, terminal)
	structured := r.URL.Query().Get("protocol") == "2"
	if structured {
		// Replay this attachment from sequence zero and tell the client to discard
		// whatever it was showing. A workspace reconnect gets a fresh shell; an
		// agent terminal can be reopened across worker epochs, so a client cursor
		// from an earlier attachment cannot safely describe its current replay
		// window. A from-0 replay is always correct, and the
		// reset makes the client wipe stale content so the replay does not stack.
		// (A future epoch-aware CP can compare the client's `after` against this
		// epoch's output floor and resume within an epoch instead — see the cursor
		// note in cloud-terminal-mux.ts.)
		after = 0
		if err := writeTerminalMessage(ctx, connection, terminalServerMessage{Type: "reset"}); err != nil {
			return
		}
	}
	attachedAt := time.Now()
	readResult := make(chan error, 1)
	var writeMu sync.Mutex
	go func() {
		readResult <- s.readTerminalInput(ctx, connection, terminal, &writeMu)
	}()
	progress := &terminalWriteProgress{}
	writeResult := make(chan error, 1)
	go func() {
		writeResult <- s.writeTerminalOutputProgress(ctx, connection, terminal, after, structured, &writeMu, progress)
	}()
	pingResult := make(chan error, 1)
	go func() {
		pingResult <- keepTerminalAlive(ctx, connection, progress)
	}()

	// closeSource records which stream ended the socket. A browser terminal that
	// drops seconds after attach and reconnects (a blank flash to the user) leaves
	// its fingerprint here: read means the client sent a frame we rejected, write
	// means the output pump failed, ping means keepalive timed out, ctx means the
	// request was cancelled. Kept at Info because it is one line per socket close.
	var closeSource string
	select {
	case err = <-readResult:
		closeSource = "read"
	case err = <-writeResult:
		closeSource = "write"
	case err = <-pingResult:
		closeSource = "ping"
	case <-ctx.Done():
		err = ctx.Err()
		closeSource = "ctx"
	}
	cancel()
	if s.logger != nil {
		s.logger.Info("browser terminal stream closed",
			"session_id", terminal.SessionID, "terminal_id", terminal.ID,
			"kind", terminal.Kind, "worker_epoch", terminal.WorkerEpoch,
			"close_source", closeSource, "error", err,
			"ws_close_status", int(websocket.CloseStatus(err)),
			"connected_ms", time.Since(attachedAt).Milliseconds())
	}
	if err != nil && !errors.Is(err, context.Canceled) &&
		websocket.CloseStatus(err) == -1 {
		s.logger.Warn("terminal stream ended unexpectedly", "error", err, "terminal_id", terminal.ID)
	}
	status, reason := terminalStreamClose(err, terminal.Kind)
	_ = connection.Close(status, reason)
}

// keepTerminalAlive sends protocol-level pings often enough to keep idle
// terminal connections active through the public load balancer. Browsers
// answer WebSocket pings automatically while readTerminalInput continuously
// reads the corresponding pong control frames.
//
// Ping and the output writer both serialize on the coder/websocket connection's
// internal writeFrameMu. When the renderer is slow to drain a large replay, a
// single output Write can hold that mutex across a multi-second flush stall, so
// the ping cannot acquire it within terminalPingTimeout and reports a spurious
// deadline error even though the socket is healthy. Closing on that first miss
// tore the socket down mid-replay, the client reconnected and replayed from the
// start, and the cycle repeated (a reconnect storm that garbles the terminal).
// Tolerate a few consecutive misses so a transient write-stall does not kill a
// live connection; a genuinely dead socket still closes after a few intervals.
//
// A program flooding its terminal keeps that mutex busy for as long as the
// flood lasts, so every ping misses even though the client is reading. A miss
// therefore only counts when no output write completed since the previous
// ping: completed writes prove the client is alive, while a dead client
// blocks the writer too and is still closed after terminalPingMaxFailures.
func keepTerminalAlive(ctx context.Context, connection *websocket.Conn, progress *terminalWriteProgress) error {
	ticker := time.NewTicker(terminalPingInterval)
	defer ticker.Stop()
	liveness := terminalLiveness{progress: progress}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, terminalPingTimeout)
			err := connection.Ping(pingCtx)
			cancel()
			if err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			if liveness.dead(err == nil) {
				return err
			}
		}
	}
}

// terminalWriteProgress counts output writes a client has accepted, so the
// keepalive can tell a busy socket from a dead one.
type terminalWriteProgress struct {
	writes atomic.Int64
}

func (p *terminalWriteProgress) wrote() {
	if p != nil {
		p.writes.Add(1)
	}
}

func (p *terminalWriteProgress) count() int64 {
	if p == nil {
		return 0
	}
	return p.writes.Load()
}

// terminalLiveness decides, one ping at a time, when a client is dead.
type terminalLiveness struct {
	progress *terminalWriteProgress
	seen     int64
	misses   int
}

// dead records one ping result and reports whether the socket should close:
// terminalPingMaxFailures consecutive misses with no output write completing
// in between.
func (l *terminalLiveness) dead(answered bool) bool {
	writes := l.progress.count()
	progressed := writes != l.seen
	l.seen = writes
	if answered || progressed {
		l.misses = 0
		return false
	}
	l.misses++
	return l.misses >= terminalPingMaxFailures
}

func (s *Server) refreshTerminalInteraction(ctx context.Context, terminal domain.TerminalSession) {
	ticker := time.NewTicker(terminalInteractionRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.store.RefreshTerminalInteraction(ctx, terminal, terminalInteractionTTL); err != nil {
				if !errors.Is(err, context.Canceled) && s.logger != nil {
					s.logger.Debug("refresh terminal interaction lease", "error", err, "terminal_id", terminal.ID)
				}
				return
			}
		}
	}
}

func terminalStreamClose(err error, kind string) (websocket.StatusCode, string) {
	if errors.Is(err, errTerminalProcessUnavailable) {
		// An agent harness can be permanently absent from an image, so surface
		// that as a stable policy failure. A workspace shell open can instead
		// race a worker restart/resume and must remain reconnectable.
		if kind == "workspace" {
			return websocket.StatusTryAgainLater, "workspace terminal is restarting"
		}
		return websocket.StatusPolicyViolation, "terminal process unavailable"
	}
	if err != nil && !errors.Is(err, context.Canceled) &&
		websocket.CloseStatus(err) == -1 {
		return websocket.StatusInternalError, "terminal stream interrupted"
	}
	return websocket.StatusNormalClosure, "terminal closed"
}

func (s *Server) readTerminalInput(
	ctx context.Context,
	connection *websocket.Conn,
	terminal domain.TerminalSession,
	writeMu *sync.Mutex,
) error {
	operate := terminalScope(terminal.Scopes, "terminal:operate")
	resizes := newTerminalResizes()
	go s.deliverTerminalResizes(ctx, terminal, resizes)
	var clientRepaintAt time.Time
	for {
		_, data, err := connection.Read(ctx)
		if err != nil {
			return err
		}
		if !operate {
			return connection.Close(websocket.StatusPolicyViolation, "terminal is read-only")
		}
		if len(data) == 0 || len(data) > maxTerminalFrame {
			return connection.Close(websocket.StatusMessageTooBig, "terminal input is too large")
		}
		var message struct {
			Type    string `json:"type"`
			InputID string `json:"inputId,omitempty"`
			Data    string `json:"data,omitempty"`
			Columns uint16 `json:"columns,omitempty"`
			Rows    uint16 `json:"rows,omitempty"`
			// Repaint marks a resize back to the size the program already has,
			// sent after the client's screen reflowed at sizes in between (a
			// burst of panel toggles): the program redraws for the client.
			Repaint bool `json:"repaint,omitempty"`
		}
		if json.Unmarshal(data, &message) == nil {
			if message.Type == "resize" {
				if message.Columns == 0 || message.Rows == 0 {
					// A zero-dimension resize carries no size to apply and must NOT
					// tear the socket down. A pane that fits while its element is not
					// yet laid out (hidden behind a connecting cover, mounted before
					// layout) proposes a 0x0 grid; closing on it made the client
					// reconnect, which replays from sequence 0 with a screen reset,
					// which shows as a blank flash, whereupon the pane fits at 0x0
					// again: an endless reconnect+flash loop. Ignore the frame and
					// keep the socket; the next real fit sends valid dimensions and
					// the PTY keeps whatever size it already had until then.
					if s.logger != nil {
						s.logger.Debug("ignoring zero-dimension terminal resize",
							"terminal_id", terminal.ID)
					}
					continue
				}
				resizes.request(message.Columns, message.Rows)
				if message.Repaint && time.Since(clientRepaintAt) >= terminalClientRepaintInterval {
					clientRepaintAt = time.Now()
					s.requestTerminalRepaint(terminal, "client reflowed")
				}
				continue
			}
			if message.Type == "input" {
				data = []byte(message.Data)
			}
		}
		if len(data) == 0 {
			continue
		}
		// Fast path: this single control-plane task holds the worker's terminal
		// stream, so hand the keystroke to it in memory and skip
		// the durable queue's insert + NOTIFY + claim round trip (~15-20ms of
		// intra-region Postgres latency off the hot path). Falls back to the
		// durable path when the worker stream is absent or its buffer is full,
		// so delivery is never dropped silently.
		if s.terminalStreamEnabled && s.terminalStreams.pushInput(terminal.ID, data) {
			// Delivered in memory. The open terminal WebSocket already refreshes
			// the interaction lease on its own timer, so no durable row is
			// needed to keep the session from idle-pausing.
			if s.terminalRelayEnabled && s.logger != nil {
				s.logger.Debug("terminal relay input forwarded",
					"terminal_id", terminal.ID, "bytes", len(data))
			}
		} else if err := retryTerminalRequest(ctx, func() error {
			return s.store.QueueTerminalInput(ctx, terminal, message.InputID, data)
		}); err != nil {
			return err
		}
		if message.InputID != "" {
			writeMu.Lock()
			err := writeTerminalMessage(ctx, connection, terminalServerMessage{
				Type: "input_ack", InputID: message.InputID,
			})
			writeMu.Unlock()
			if err != nil {
				return err
			}
		}
	}
}

// terminalResizes holds the newest size one client asked for. Delivery runs
// apart from the input loop, so keystrokes never wait behind a resize, and a
// burst of resizes (a dragged side panel) collapses to the latest size while
// an earlier one is still in flight: each delivered resize costs the program a
// full redraw.
type terminalResizes struct {
	mu      sync.Mutex
	columns uint16
	rows    uint16
	pending bool
	wake    chan struct{}
}

func newTerminalResizes() *terminalResizes {
	return &terminalResizes{wake: make(chan struct{}, 1)}
}

func (r *terminalResizes) request(columns, rows uint16) {
	r.mu.Lock()
	r.columns, r.rows, r.pending = columns, rows, true
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *terminalResizes) take() (uint16, uint16, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := r.pending
	r.pending = false
	return r.columns, r.rows, pending
}

// deliverTerminalResizes sends each coalesced size to the worker in order:
// straight down its stream when this replica holds it (the path keystrokes
// take), otherwise through the durable queue. A size that cannot be queued
// is dropped; the next resize or reattach sends a current one.
func (s *Server) deliverTerminalResizes(
	ctx context.Context,
	terminal domain.TerminalSession,
	resizes *terminalResizes,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-resizes.wake:
		}
		columns, rows, ok := resizes.take()
		if !ok {
			continue
		}
		if s.terminalStreamEnabled && s.terminalStreams != nil && s.terminalStreams.pushControl(
			terminal.ID, worker.TerminalStreamFrame{Type: "resize", Columns: columns, Rows: rows},
		) {
			continue
		}
		if err := retryTerminalRequest(ctx, func() error {
			return s.store.QueueTerminalResize(ctx, terminal, columns, rows)
		}); err != nil && ctx.Err() == nil && s.logger != nil {
			s.logger.Warn("queue terminal resize", "error", err, "terminal_id", terminal.ID)
		}
	}
}

// requestTerminalRepaint asks the terminal's program to redraw: this client
// skipped output (the replay log's head was pruned, or frames were lost), or
// the client's screen reflowed at sizes the program never saw. A full-screen program repaints its whole screen; a line-mode shell
// only loses the skipped scrollback. It is best effort and never blocks the
// output path.
func (s *Server) requestTerminalRepaint(terminal domain.TerminalSession, reason string) {
	if s.logger != nil {
		s.logger.Info("requesting terminal repaint",
			"terminal_id", terminal.ID, "kind", terminal.Kind, "reason", reason)
	}
	if s.terminalStreamEnabled && s.terminalStreams != nil && s.terminalStreams.pushControl(
		terminal.ID, worker.TerminalStreamFrame{Type: "repaint"},
	) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.store.QueueTerminalRepaint(ctx, terminal); err != nil && s.logger != nil {
			s.logger.Debug("queue terminal repaint", "error", err, "terminal_id", terminal.ID)
		}
	}()
}

func retryTerminalRequest(ctx context.Context, operation func() error) error {
	deadline := time.NewTimer(terminalReadyTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := operation()
		if err == nil {
			return nil
		}
		if !errors.Is(err, postgres.ErrWorkerUnavailable) &&
			!errors.Is(err, postgres.ErrConflict) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("terminal request queue did not become ready")
		case <-ticker.C:
		}
	}
}

func (s *Server) writeTerminalOutput(
	ctx context.Context,
	connection *websocket.Conn,
	terminal domain.TerminalSession,
	after int64,
	structured bool,
	writeMu *sync.Mutex,
) error {
	return s.writeTerminalOutputProgress(ctx, connection, terminal, after, structured, writeMu, nil)
}

// writeTerminalOutputProgress is writeTerminalOutput that counts each output
// write the client accepted in progress (which may be nil).
func (s *Server) writeTerminalOutputProgress(
	ctx context.Context,
	connection *websocket.Conn,
	terminal domain.TerminalSession,
	after int64,
	structured bool,
	writeMu *sync.Mutex,
	progress *terminalWriteProgress,
) error {
	var slowWriteLoggedAt time.Time
	// wrote records an output write the client accepted, and logs (at most
	// every terminalSlowWriteLogInterval) one that took terminalSlowWrite or
	// longer: a client or network that stops reading is otherwise invisible.
	wrote := func(started time.Time, sequence int64) {
		progress.wrote()
		if took := time.Since(started); took >= terminalSlowWrite && s.logger != nil &&
			time.Since(slowWriteLoggedAt) >= terminalSlowWriteLogInterval {
			slowWriteLoggedAt = time.Now()
			s.logger.Info("terminal client write slow",
				"terminal_id", terminal.ID, "sequence", sequence, "took_ms", took.Milliseconds())
		}
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	startupDeadline := time.NewTimer(terminalReadyTimeout)
	defer startupDeadline.Stop()
	// With the stream enabled, a Postgres NOTIFY wakes this loop the moment a
	// new output row commits; the ticker stays as the missed-notification
	// fallback.
	var wake chan struct{}
	if s.terminalStreamEnabled {
		var cancelWake func()
		wake, cancelWake = s.terminalStreams.subscribeOutput(terminal.ID)
		defer cancelWake()
	}
	// The relay subscription is deliberately established before replay begins:
	// frames produced while Postgres is returning scrollback are buffered here
	// and compared against the same durable sequence before being written.
	var live chan terminalRelayOutput
	if s.terminalRelayEnabled && s.terminalStreams != nil {
		var cancelLive func()
		live, cancelLive = s.terminalStreams.subscribeRelayOutput(terminal.ID)
		defer cancelLive()
	}
	var notificationHints chan terminalRelayNotification
	if structured && s.terminalRelayEnabled && s.terminalStreams != nil {
		var cancelNotifications func()
		notificationHints, cancelNotifications = s.terminalStreams.subscribeRelayNotifications(terminal.ID)
		defer cancelNotifications()
	}
	replayComplete := false
	startingSent := false
	ready := false
	// skipped notes output this client will never see (a pruned replay head or
	// a sequence gap); the program is then asked to repaint, at most once per
	// terminalRepaintInterval.
	skipped := false
	var repaintedAt time.Time
	var gapSince time.Time
	// gapChecked notes that the replay log was read again after the mirror
	// finished with the current gap; a gap still open after that is lost.
	gapChecked := false
	// Poll once to establish state and replay existing output. Once relay mode
	// is live, only the ticker/NOTIFY fallback asks Postgres again; a hot output
	// frame must not wait behind a database round trip.
	pollDurable := true
	for {
		if pollDurable {
			frames, state, err := s.store.ListTerminalOutput(ctx, terminal, after, 100)
			if err != nil {
				return err
			}
			pollDurable = len(frames) == 100
			if structured && !ready && (!startingSent || state == "open") {
				writeMu.Lock()
				messageType := "starting"
				if state == "open" {
					messageType = "ready"
					ready = true
					if s.logger != nil {
						s.logger.Info("browser terminal ready",
							"session_id", terminal.SessionID,
							"terminal_id", terminal.ID,
							"kind", terminal.Kind,
							"worker_epoch", terminal.WorkerEpoch,
						)
					}
				} else {
					startingSent = true
				}
				err := writeTerminalMessage(ctx, connection, terminalServerMessage{
					Type:     messageType,
					Sequence: after,
				})
				writeMu.Unlock()
				if err != nil {
					return err
				}
			}
			for _, frame := range frames {
				if frame.Sequence != after+1 {
					skipped = true
				}
				var err error
				started := time.Now()
				writeMu.Lock()
				if structured {
					err = writeTerminalMessage(ctx, connection, terminalServerMessage{
						Type:     "output",
						Data:     base64.StdEncoding.EncodeToString(frame.Data),
						Sequence: frame.Sequence,
					})
				} else {
					// PTY reads are arbitrary byte chunks and can split a multi-byte
					// UTF-8 sequence. Legacy clients receive binary frames so partial
					// code points never make the WebSocket library reject the output.
					err = connection.Write(ctx, websocket.MessageBinary, frame.Data)
				}
				writeMu.Unlock()
				if err != nil {
					return err
				}
				wrote(started, frame.Sequence)
				after = frame.Sequence
				gapSince = time.Time{}
				gapChecked = false
			}
			if structured && ready && !replayComplete {
				writeMu.Lock()
				if err := writeTerminalMessage(ctx, connection, terminalServerMessage{
					Type: "replay_complete", Sequence: after,
				}); err != nil {
					writeMu.Unlock()
					return err
				}
				writeMu.Unlock()
				replayComplete = true
			}
			if skipped && state == "open" && time.Since(repaintedAt) >= terminalRepaintInterval {
				skipped = false
				repaintedAt = time.Now()
				s.requestTerminalRepaint(terminal, "skipped output")
			}
			if state == "failed" {
				return errTerminalProcessUnavailable
			}
			if state == "closed" {
				return connection.Close(websocket.StatusNormalClosure, "terminal process exited")
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-startupDeadline.C:
			if !ready {
				return errTerminalProcessUnavailable
			}
		case frame := <-live:
			// A direct frame uses the same sequence that the ordered durable
			// mirror will commit. A gap (a saturated live-client buffer, or a
			// lost frame) is left to the durable replay pass, which fills it in
			// byte order. The gap is skipped, with a repaint, only once it can
			// no longer fill: the mirror has finished with every frame before
			// this one and a fresh read of the log still lacks them. A mirror
			// running seconds behind under a flood is waited for, not skipped.
			if frame.sequence <= after {
				continue
			}
			if frame.sequence != after+1 {
				if gapSince.IsZero() {
					gapSince = time.Now()
				}
				mirrored, known := s.terminalStreams.mirroredThrough(terminal.ID)
				passed := known && mirrored >= frame.sequence-1
				if !passed && time.Since(gapSince) < terminalRelayGapMaxWait {
					pollDurable = true
					continue
				}
				if passed && !gapChecked {
					gapChecked = true
					pollDurable = true
					continue
				}
				if s.logger != nil {
					s.logger.Info("terminal live output skipped a gap",
						"terminal_id", terminal.ID, "after", after, "sequence", frame.sequence,
						"mirrored", mirrored, "mirror_known", known,
						"waited_ms", time.Since(gapSince).Milliseconds())
				}
				skipped = true
				pollDurable = true
			}
			gapSince = time.Time{}
			gapChecked = false
			started := time.Now()
			writeMu.Lock()
			var writeErr error
			if structured {
				writeErr = writeTerminalMessage(ctx, connection, terminalServerMessage{
					Type: "output", Data: base64.StdEncoding.EncodeToString(frame.data),
					Sequence: frame.sequence,
				})
			} else {
				writeErr = connection.Write(ctx, websocket.MessageBinary, frame.data)
			}
			writeMu.Unlock()
			if writeErr != nil {
				return writeErr
			}
			wrote(started, frame.sequence)
			after = frame.sequence
			if s.logger != nil {
				s.logger.Debug("terminal relay output delivered",
					"terminal_id", terminal.ID, "sequence", frame.sequence,
					"bytes", len(frame.data))
			}
		case hint := <-notificationHints:
			writeMu.Lock()
			writeErr := writeTerminalMessage(ctx, connection, terminalServerMessage{
				Type: "notification_hint", EventID: hint.eventID, EventType: hint.typeName,
				OccurredAt: hint.occurredAt, Payload: hint.payload,
			})
			writeMu.Unlock()
			if writeErr != nil {
				return writeErr
			}
		case <-wake:
			pollDurable = true
		case <-ticker.C:
			pollDurable = true
		}
	}
}

type terminalServerMessage struct {
	Type       string          `json:"type"`
	Data       string          `json:"data,omitempty"`
	Message    string          `json:"message,omitempty"`
	Sequence   int64           `json:"sequence,omitempty"`
	InputID    string          `json:"inputId,omitempty"`
	EventID    string          `json:"eventId,omitempty"`
	EventType  string          `json:"eventType,omitempty"`
	OccurredAt time.Time       `json:"occurredAt,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

func writeTerminalMessage(
	ctx context.Context,
	connection *websocket.Conn,
	message terminalServerMessage,
) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, data)
}

func (s *Server) closeTerminal(r *http.Request, terminal domain.TerminalSession) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Second)
	defer cancel()
	if err := s.store.CloseTerminal(ctx, terminal); err != nil {
		s.logger.Debug("close terminal session", "error", err, "terminal_id", terminal.ID)
	}
}

func terminalScope(scopes []string, expected string) bool {
	for _, scope := range scopes {
		if scope == expected {
			return true
		}
	}
	return false
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
