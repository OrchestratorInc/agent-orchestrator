package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/panes"
)

// PaneService relays split-pane requests to the desktop window
// (internal/service/panes).
type PaneService interface {
	Layout(sessionID string) (json.RawMessage, bool)
	Connected() bool
	Execute(ctx context.Context, sessionID, action string, args json.RawMessage) (panes.Result, error)
}

// PaneTerminalService types into and reads a session's own shell terminals
// (internal/service/shellterm).
type PaneTerminalService interface {
	TypeIntoSessionTerminal(ctx context.Context, sessionID domain.SessionID, handleID, text string, enter bool) error
	InterruptSessionTerminal(ctx context.Context, sessionID domain.SessionID, handleID string) error
	SessionTerminalOutput(ctx context.Context, sessionID domain.SessionID, handleID string, lines int) (string, error)
	CloseSessionTerminal(ctx context.Context, sessionID domain.SessionID, handleID string) error
}

// PaneInputRequest types into one of the session's terminal tabs.
type PaneInputRequest struct {
	Tab       string  `json:"tab" description:"Key of one of this session's terminal tabs, from ao pane."`
	Text      *string `json:"text,omitempty" description:"Text to type. Sent as a command (followed by Enter) unless enter is false."`
	Enter     *bool   `json:"enter,omitempty" description:"Press Enter after the text. Defaults to true."`
	Interrupt bool    `json:"interrupt,omitempty" description:"Send Ctrl-C instead of text."`
	Close     bool    `json:"close,omitempty" description:"Close the terminal tab (its shell may already have exited)."`
}

// PaneOutputQuery selects a terminal tab and how much of its screen to read.
type PaneOutputQuery struct {
	Tab   string `query:"tab" description:"Key of one of this session's terminal tabs, from ao pane."`
	Lines *int64 `query:"lines,omitempty" minimum:"1" maximum:"2000" description:"How many of the last lines to return. Defaults to 200."`
}

// PaneOutputResponse is what a terminal tab shows, as plain text.
type PaneOutputResponse struct {
	SessionID string `json:"sessionId"`
	Tab       string `json:"tab"`
	Output    string `json:"output"`
}

// PaneTab is one tab in a session's center pane.
type PaneTab struct {
	Key   string  `json:"key" description:"Stable tab key used by pane commands: session, a shell terminal's handle id, reviewer:<id>, reviewer-chat:<id> or file:<path>."`
	Kind  string  `json:"kind" enum:"session,terminal,reviewer,file" description:"What the tab shows."`
	Title string  `json:"title" description:"The tab's label."`
	Agent *string `json:"agent,omitempty" description:"Agent running in a terminal tab, when the window tracks it."`
}

// PaneBox is one pane of a split, as fractions of the center area.
type PaneBox struct {
	Key    string  `json:"key" description:"Key of the tab shown in this pane."`
	X      float64 `json:"x" description:"Left edge, 0 to 1."`
	Y      float64 `json:"y" description:"Top edge, 0 to 1."`
	Width  float64 `json:"width" description:"Width, 0 to 1."`
	Height float64 `json:"height" description:"Height, 0 to 1."`
}

// PaneGroup is a set of tabs shown together as a split.
type PaneGroup struct {
	ID    string    `json:"id" description:"Group id, for ungroup."`
	Focus string    `json:"focus" description:"Key of the group's focused pane."`
	Panes []PaneBox `json:"panes" description:"Every pane of the split with its position."`
}

// PaneLayout is what the desktop window shows for a session.
type PaneLayout struct {
	Enabled  bool        `json:"enabled" description:"Whether split panes are on in the window (a Developer Mode preview). Commands are refused while off."`
	Selected string      `json:"selected" description:"Key of the tab on screen (the focused pane when a split is shown)."`
	Shown    *string     `json:"shown,omitempty" description:"Id of the group on screen, when a split is shown."`
	Tabs     []PaneTab   `json:"tabs" description:"Every tab of the session."`
	Groups   []PaneGroup `json:"groups" description:"Every split group of the session."`
}

// PaneLayoutResponse is a session's current split layout.
type PaneLayoutResponse struct {
	SessionID string     `json:"sessionId"`
	Layout    PaneLayout `json:"layout"`
}

// PaneCommandRequest asks the desktop window to change a session's splits.
type PaneCommandRequest struct {
	Action string  `json:"action" enum:"split,remove,ungroup,focus,close" description:"split shows tab beside another; remove takes a tab out of its group; ungroup dissolves a group; focus brings a tab on screen; close closes a terminal tab."`
	Tab    *string `json:"tab,omitempty" description:"Tab key. For split, also terminal (open a new shell terminal) or file:<workspace-relative path> (open the file)."`
	Beside *string `json:"beside,omitempty" description:"For split: key of the tab to split beside. Defaults to the tab on screen."`
	Side   *string `json:"side,omitempty" enum:"left,right,top,bottom" description:"For split: which side of beside the tab goes. Defaults to right."`
	Group  *string `json:"group,omitempty" description:"For ungroup: group id. Defaults to the group on screen."`
}

// PaneSessionLookup finds the session a pane request names, for its
// capability verifier.
type PaneSessionLookup interface {
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
}

// PanesController serves split-pane layouts and requests for agents, and
// lets an agent drive its own session's terminal tabs.
//
// Like `ao browser`, every route requires the session's capability
// (X-AO-Browser-Capability, injected into the owning agent only): a session
// id alone is not proof, and without it any local process could type into
// another session's terminals.
type PanesController struct {
	Svc          PaneService
	Terminals    PaneTerminalService
	Sessions     PaneSessionLookup
	Capabilities SessionCapabilityValidator
}

// authorize checks that the caller holds the capability of the session it
// names. It writes the error response and returns false when it does not.
func (c *PanesController) authorize(w http.ResponseWriter, r *http.Request, sessionID domain.SessionID) bool {
	if c.Sessions == nil || c.Capabilities == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return false
	}
	sess, err := c.Sessions.Get(r.Context(), sessionID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return false
	}
	if sess.IsTerminated {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SESSION_TERMINATED", "Session is terminated", nil)
		return false
	}
	if !c.Capabilities.Valid(sessionID, strings.TrimSpace(r.Header.Get(browserCapabilityHeader)), sess.Metadata.BrowserCapabilityVerifier) {
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "PANE_CAPABILITY_INVALID",
			"Pane commands need the owning session's capability; run ao pane from inside that session", nil)
		return false
	}
	return true
}

// Register mounts the pane routes.
func (c *PanesController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/panes", c.layout)
	r.Post("/sessions/{sessionId}/panes", c.command)
	r.Post("/sessions/{sessionId}/panes/input", c.input)
	r.Get("/sessions/{sessionId}/panes/output", c.output)
}

func (c *PanesController) input(w http.ResponseWriter, r *http.Request) {
	if c.Terminals == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/{sessionId}/panes/input")
		return
	}
	sessionID := domain.SessionID(chi.URLParam(r, "sessionId"))
	if !c.authorize(w, r, sessionID) {
		return
	}
	var in PaneInputRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	var err error
	switch {
	case in.Close:
		envelope.SetTelemetryField(r, "pane_action", "close")
		err = c.Terminals.CloseSessionTerminal(r.Context(), sessionID, in.Tab)
	case in.Interrupt:
		envelope.SetTelemetryField(r, "pane_action", "interrupt")
		err = c.Terminals.InterruptSessionTerminal(r.Context(), sessionID, in.Tab)
	case in.Text == nil:
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PANE_COMMAND", "Pass text to type, or interrupt", nil)
		return
	default:
		enter := in.Enter == nil || *in.Enter
		envelope.SetTelemetryField(r, "pane_action", "type")
		err = c.Terminals.TypeIntoSessionTerminal(r.Context(), sessionID, in.Tab, *in.Text, enter)
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *PanesController) output(w http.ResponseWriter, r *http.Request) {
	if c.Terminals == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/sessions/{sessionId}/panes/output")
		return
	}
	sessionID := domain.SessionID(chi.URLParam(r, "sessionId"))
	if !c.authorize(w, r, sessionID) {
		return
	}
	tab := r.URL.Query().Get("tab")
	lines := 0
	if raw := r.URL.Query().Get("lines"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_LINES", "lines must be a positive integer", nil)
			return
		}
		lines = parsed
	}
	output, err := c.Terminals.SessionTerminalOutput(r.Context(), sessionID, tab, lines)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, PaneOutputResponse{SessionID: string(sessionID), Tab: tab, Output: output})
}

func (c *PanesController) layout(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/sessions/{sessionId}/panes")
		return
	}
	sessionID := chi.URLParam(r, "sessionId")
	if !c.authorize(w, r, domain.SessionID(sessionID)) {
		return
	}
	raw, ok := c.Svc.Layout(sessionID)
	if !ok {
		writePaneUnavailable(w, r, c.Svc.Connected())
		return
	}
	var layout PaneLayout
	if err := json.Unmarshal(raw, &layout); err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "PANE_LAYOUT_INVALID", "The AO window reported a layout that could not be read", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, PaneLayoutResponse{SessionID: sessionID, Layout: normalizePaneLayout(layout)})
}

func (c *PanesController) command(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/{sessionId}/panes")
		return
	}
	sessionID := chi.URLParam(r, "sessionId")
	if !c.authorize(w, r, domain.SessionID(sessionID)) {
		return
	}
	var in PaneCommandRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if msg := validatePaneCommand(in); msg != "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PANE_COMMAND", msg, nil)
		return
	}
	args, err := json.Marshal(in)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "PANE_COMMAND_ENCODE", "Could not encode the pane command", nil)
		return
	}
	envelope.SetTelemetryField(r, "pane_action", in.Action)
	result, err := c.Svc.Execute(r.Context(), sessionID, in.Action, args)
	switch {
	case errors.Is(err, panes.ErrNoWindow):
		writePaneUnavailable(w, r, false)
		return
	case errors.Is(err, panes.ErrTimeout):
		envelope.WriteAPIError(w, r, http.StatusGatewayTimeout, "timeout", "PANE_COMMAND_TIMEOUT", "The AO window did not answer in time; check `ao pane ls` before retrying", nil)
		return
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "PANE_COMMAND_FAILED", err.Error(), nil)
		return
	}
	if !result.OK {
		code := result.Code
		if code == "" {
			code = "PANE_COMMAND_REFUSED"
		}
		message := result.Message
		if message == "" {
			message = "The AO window refused the pane command"
		}
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", code, message, nil)
		return
	}
	var layout PaneLayout
	if len(result.Layout) > 0 {
		if err := json.Unmarshal(result.Layout, &layout); err != nil {
			envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "PANE_LAYOUT_INVALID", "The AO window reported a layout that could not be read", nil)
			return
		}
	}
	envelope.WriteJSON(w, http.StatusOK, PaneLayoutResponse{SessionID: sessionID, Layout: normalizePaneLayout(layout)})
}

func writePaneUnavailable(w http.ResponseWriter, r *http.Request, connected bool) {
	if !connected {
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "NO_DESKTOP_WINDOW", "No AO window is accepting pane requests: open AO with Developer Mode on (split panes are a Developer Mode preview)", nil)
		return
	}
	envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", panes.CodeNotShown, "This session is not open in the AO window; panes follow the session on screen", nil)
}

func validatePaneCommand(in PaneCommandRequest) string {
	tab := ""
	if in.Tab != nil {
		tab = strings.TrimSpace(*in.Tab)
	}
	switch in.Action {
	case "split":
		if tab == "" {
			return "split needs a tab: a tab key, terminal or file:<path>"
		}
		if in.Side != nil {
			switch *in.Side {
			case "left", "right", "top", "bottom":
			default:
				return "side must be left, right, top or bottom"
			}
		}
	case "remove", "focus", "close":
		if tab == "" {
			return in.Action + " needs a tab key"
		}
	case "ungroup":
	default:
		return "action must be split, remove, ungroup, focus or close"
	}
	return ""
}

// normalizePaneLayout keeps lists non-null on the wire.
func normalizePaneLayout(layout PaneLayout) PaneLayout {
	if layout.Tabs == nil {
		layout.Tabs = []PaneTab{}
	}
	if layout.Groups == nil {
		layout.Groups = []PaneGroup{}
	}
	for i := range layout.Groups {
		if layout.Groups[i].Panes == nil {
			layout.Groups[i].Panes = []PaneBox{}
		}
	}
	return layout
}
