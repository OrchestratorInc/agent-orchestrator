package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/dictation"
)

// DictationController exposes desktop-local model management and audio streaming.
type DictationController struct{ Svc *dictation.Service }

// Register mounts short-lived model management routes.
func (c *DictationController) Register(r chi.Router) {
	r.Get("/dictation", c.status)
	r.Post("/dictation", c.change)
}

// RegisterStreams mounts audio streaming outside the REST timeout.
func (c *DictationController) RegisterStreams(r chi.Router) { r.Get("/dictation/stream", c.stream) }
func (c *DictationController) available(w http.ResponseWriter, r *http.Request) bool {
	if c.Svc != nil {
		return true
	}
	apispec.NotImplemented(w, r, r.Method, "/api/v1/dictation")
	return false
}
func (c *DictationController) status(w http.ResponseWriter, r *http.Request) {
	if c.available(w, r) {
		writeDictation(w, r)(c.Svc.Status(r.Context()))
	}
}
func (c *DictationController) change(w http.ResponseWriter, r *http.Request) {
	if !c.available(w, r) {
		return
	}
	var request DictationRequest
	if err := decodeJSONStrict(r, &request); err != nil {
		envelope.WriteAPIError(w, r, 400, "bad_request", "INVALID_DICTATION_REQUEST", "Invalid dictation request", nil)
		return
	}
	var err error
	switch request.Action {
	case "install":
		err = c.Svc.Install(r.Context(), request.Model)
	case "select":
		err = c.Svc.Select(r.Context(), request.Model)
	case "delete":
		err = c.Svc.Delete(r.Context(), request.Model)
	case "cancel":
		c.Svc.CancelInstall()
	default:
		envelope.WriteAPIError(w, r, 400, "bad_request", "INVALID_DICTATION_ACTION", "Unknown dictation action", nil)
		return
	}
	if err != nil {
		envelope.WriteAPIError(w, r, 409, "conflict", "DICTATION_UNAVAILABLE", err.Error(), nil)
		return
	}
	writeDictation(w, r)(c.Svc.Status(r.Context()))
}
func (c *DictationController) stream(w http.ResponseWriter, r *http.Request) {
	if !c.available(w, r) {
		return
	}
	// Renderer origins are checked by the daemon's outer CORS and renderer-only middleware.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(64000)
	ctx, cancel := context.WithTimeout(r.Context(), 7*time.Minute)
	defer cancel()
	write := func(event dictation.Event) error {
		data, _ := json.Marshal(event)
		writeCtx, done := context.WithTimeout(ctx, 10*time.Second)
		defer done()
		return conn.Write(writeCtx, websocket.MessageText, data)
	}
	session, err := c.Svc.Open(ctx)
	if err != nil {
		_ = write(dictation.Event{Type: "error", Message: err.Error()})
		return
	}
	completed := false
	defer func() { session.Close(completed) }()
	failures := make(chan error, 1)
	go func() {
		for {
			kind, data, err := conn.Read(ctx)
			if err != nil {
				failures <- err
				return
			}
			if kind == websocket.MessageBinary {
				err = session.Audio(data)
			} else {
				var command struct {
					Type string `json:"type"`
				}
				err = json.Unmarshal(data, &command)
				if err == nil && command.Type == "stop" {
					err = session.Finish()
				} else {
					cancel()
					return
				}
			}
			if err != nil {
				failures <- err
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-failures:
			_ = write(dictation.Event{Type: "error", Message: err.Error()})
			return
		case event, ok := <-session.Events():
			if !ok {
				_ = write(dictation.Event{Type: "error", Message: "Local speech engine stopped. Try again."})
				return
			}
			if event.Type == "ready" {
				continue
			}
			if err := write(event); err != nil {
				return
			}
			if event.Type == "end" {
				completed = true
				return
			}
			if event.Type == "error" {
				return
			}
		}
	}
}

func writeDictation(w http.ResponseWriter, r *http.Request) func(dictation.Status, error) {
	return func(status dictation.Status, err error) {
		if err != nil {
			envelope.WriteError(w, r, err)
			return
		}
		envelope.WriteJSON(w, http.StatusOK, status)
	}
}
