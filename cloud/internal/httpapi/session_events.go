package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

var (
	// sessionEventFallbackPoll is how often a session event stream re-reads
	// PostgreSQL when the ao_session_event NOTIFY listener is running: only a
	// lost notification waits for it.
	sessionEventFallbackPoll = 3 * time.Second
	// sessionEventPoll is the stream's only cadence when no listener runs.
	sessionEventPoll = time.Second
	// sessionEventMinInterval batches a burst of wakes (assistant output
	// streams many small deltas) into at most one replay query per interval
	// for each open stream.
	sessionEventMinInterval = 100 * time.Millisecond
)

// HandleSessionEventNotify is the Postgres NOTIFY callback for
// ao_session_event; the payload is the session id whose event log just grew.
// Every replica runs its own listener, so each wakes only its own streams.
func (s *Server) HandleSessionEventNotify(sessionID string) {
	if s.sessionEventWaiters != nil {
		s.sessionEventWaiters.notify(sessionID)
	}
}

// EnableSessionEventNotify records that an ao_session_event listener is
// running, so event streams can rely on wakes and poll only as a fallback.
func (s *Server) EnableSessionEventNotify() {
	s.sessionEventNotify.Store(true)
}

// subscribeSessionEvents returns the session's wake channel and the stream's
// fallback poll interval. A server without a waiter registry (tests) gets a
// channel that never fires and the plain poll.
func (s *Server) subscribeSessionEvents(sessionID string) (<-chan struct{}, time.Duration, func()) {
	if s.sessionEventWaiters == nil {
		return nil, sessionEventPoll, func() {}
	}
	wake, cancel := s.sessionEventWaiters.subscribe(sessionID)
	if s.sessionEventNotify.Load() {
		return wake, sessionEventFallbackPoll, cancel
	}
	return wake, sessionEventPoll, cancel
}

// sessionLiveStore is the store surface behind a stream's live state. A store
// without it serves streams with no live frames.
type sessionLiveStore interface {
	JoinSessionViewers(ctx context.Context, principal domain.Principal, orgID, sessionID, connectionID string) error
	RefreshSessionViewer(ctx context.Context, orgID, connectionID string) error
	LeaveSessionViewers(ctx context.Context, orgID, connectionID string) error
	SessionLiveState(ctx context.Context, principal domain.Principal, orgID, sessionID string) (domain.SessionLiveState, error)
}

// HandleSessionLiveNotify is the Postgres NOTIFY callback for
// ao_session_live; the payload is the session whose live state changed.
func (s *Server) HandleSessionLiveNotify(sessionID string) {
	if s.sessionLiveWaiters != nil {
		s.sessionLiveWaiters.notify(sessionID)
	}
}

type sessionViewerResponse struct {
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatarUrl,omitempty"`
	Role      string `json:"role"`
}

type sessionLiveResponse struct {
	Type        string                  `json:"type"`
	Viewers     []sessionViewerResponse `json:"viewers"`
	Composer    composerLiveResponse    `json:"composer"`
	CanTakeOver bool                    `json:"canTakeOver"`
	// You is the user this stream is authenticated as, so a client compares
	// against the identity the server acts on, not one it remembers.
	You string `json:"you"`
	// ChatSettings is what the session's turns run with, set by its owner;
	// CanConfigure says whether this person is that owner.
	ChatSettings chatSettingsResponse `json:"chatSettings"`
	CanConfigure bool                 `json:"canConfigure"`
	// MergeRequestsVersion changes whenever a merge request in the project is
	// asked for or decided.
	MergeRequestsVersion string `json:"mergeRequestsVersion,omitempty"`
}

type chatSettingsResponse struct {
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	Mode            string `json:"mode,omitempty"`
	ApprovalMode    string `json:"approvalMode,omitempty"`
}

func toChatSettingsResponse(settings domain.ChatTurnSettings) chatSettingsResponse {
	return chatSettingsResponse{Model: settings.Model, ReasoningEffort: settings.ReasoningEffort, Mode: settings.Mode, ApprovalMode: settings.ApprovalMode}
}

func toSessionLiveResponse(state domain.SessionLiveState) sessionLiveResponse {
	response := sessionLiveResponse{
		Type:                 "session.live",
		Viewers:              make([]sessionViewerResponse, 0, len(state.Viewers)),
		Composer:             toComposerLiveResponse(state.Composer),
		CanTakeOver:          state.CanTakeOver,
		ChatSettings:         toChatSettingsResponse(state.ChatSettings),
		CanConfigure:         state.CanConfigure,
		MergeRequestsVersion: state.MergeRequestsVersion,
	}
	for _, viewer := range state.Viewers {
		response.Viewers = append(response.Viewers, sessionViewerResponse{UserID: viewer.UserID, Name: viewer.Name, AvatarURL: viewer.AvatarURL, Role: viewer.Role})
	}
	return response
}

// liveFeed is one event stream's opt-in live state: it counts the stream's
// owner as present while it is open and writes a session.live frame whenever
// that state changes. Live frames carry no id, so they never move the
// stream's replay cursor. A nil feed is a stream that did not opt in.
type liveFeed struct {
	server       *Server
	store        sessionLiveStore
	request      *http.Request
	orgID        string
	sessionID    string
	connectionID string
	joined       bool
	wakeCh       <-chan struct{}
	unsubscribe  func()
	refresher    *time.Ticker
	last         []byte
	lastRead     time.Time
}

// openLiveFeed starts the feed when the request asked for one (live=1) and
// the store supports it. Failing to record presence only leaves this viewer
// out of the list; the stream itself still serves events.
func (s *Server) openLiveFeed(r *http.Request, orgID, sessionID string) *liveFeed {
	if r.URL.Query().Get("live") != "1" {
		return nil
	}
	store, ok := s.store.(sessionLiveStore)
	if !ok {
		return nil
	}
	feed := &liveFeed{
		server: s, store: store, request: r, orgID: orgID, sessionID: sessionID,
		connectionID: uuid.NewString(), unsubscribe: func() {},
		refresher: time.NewTicker(postgres.SessionViewerRefresh),
	}
	if s.sessionLiveWaiters != nil {
		feed.wakeCh, feed.unsubscribe = s.sessionLiveWaiters.subscribe(sessionID)
	}
	if err := store.JoinSessionViewers(r.Context(), principalFrom(r), orgID, sessionID, feed.connectionID); err != nil {
		s.logger.Warn("join session viewers", "error", err, "request_id", requestID(r))
	} else {
		feed.joined = true
	}
	return feed
}

func (f *liveFeed) wake() <-chan struct{} {
	if f == nil {
		return nil
	}
	return f.wakeCh
}

func (f *liveFeed) refresh() <-chan time.Time {
	if f == nil {
		return nil
	}
	return f.refresher.C
}

// touch keeps this stream counted as present.
func (f *liveFeed) touch() {
	if f == nil || !f.joined {
		return
	}
	if err := f.store.RefreshSessionViewer(f.request.Context(), f.orgID, f.connectionID); err != nil {
		f.server.logger.Warn("refresh session viewer", "error", err, "request_id", requestID(f.request))
	}
}

// throttle waits out sessionEventMinInterval since the last read, so a
// holder typing quickly costs each stream at most one read per interval; the
// wake channel folds the keystrokes in between. It returns false once the
// stream should end.
func (f *liveFeed) throttle() bool {
	wait := sessionEventMinInterval - time.Since(f.lastRead)
	if wait <= 0 {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-f.request.Context().Done():
		return false
	case <-f.server.drain:
		return false
	case <-timer.C:
		return true
	}
}

// write sends the current live state when it differs from the last frame
// sent. It returns false only when the client connection failed.
func (f *liveFeed) write(w http.ResponseWriter) bool {
	if f == nil {
		return true
	}
	f.lastRead = time.Now()
	state, err := f.store.SessionLiveState(f.request.Context(), principalFrom(f.request), f.orgID, f.sessionID)
	if err != nil {
		f.server.logger.Warn("read session live state", "error", err, "request_id", requestID(f.request))
		return true
	}
	frame := toSessionLiveResponse(state)
	frame.You = principalFrom(f.request).UserID
	payload, err := json.Marshal(frame)
	if err != nil || bytes.Equal(payload, f.last) {
		return true
	}
	if _, err := fmt.Fprintf(w, "event: session.live\ndata: %s\n\n", payload); err != nil {
		return false
	}
	f.last = payload
	return http.NewResponseController(w).Flush() == nil
}

// close stops the feed and removes this stream from the viewer list. The
// request context is already done, so the removal gets its own deadline.
func (f *liveFeed) close() {
	if f == nil {
		return
	}
	f.refresher.Stop()
	f.unsubscribe()
	if !f.joined {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.store.LeaveSessionViewers(ctx, f.orgID, f.connectionID); err != nil {
		f.server.logger.Warn("leave session viewers", "error", err, "request_id", requestID(f.request))
	}
}
