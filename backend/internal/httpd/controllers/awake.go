package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	awakesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/awake"
)

// AwakeService defines loopback-only desktop wake operations.
type AwakeService interface {
	Status() domain.AwakeState
	ObserveStatus(context.Context) domain.AwakeState
	Start(context.Context, domain.StartAwakeRequest) error
	End(context.Context, string) error
	Extend(context.Context, string, int) error
	StopAll(context.Context) error
	Resume(context.Context) error
	Recover(context.Context) error
	ClearHistory(context.Context) error
	UpdateSettings(context.Context, domain.AwakeSettings) error
	UpdateRules(context.Context, []domain.AwakeRule) error
	Downloads([]string) error
	AuthorizeLowPower(context.Context) error
	RevokeLowPower(context.Context) error
}

// AwakeController serves desktop wake routes.
type AwakeController struct{ Svc AwakeService }

// AwakeIDParam identifies a wake request in a route.
type AwakeIDParam struct {
	AwakeID string `path:"awakeId"`
}

// ExtendAwakeRequest adds time to a finite request.
type ExtendAwakeRequest struct {
	DurationSeconds int `json:"durationSeconds"`
}

// AwakeRulesRequest replaces activation rules.
type AwakeRulesRequest struct {
	Rules []domain.AwakeRule `json:"rules"`
}

// AwakeDownloadsRequest reports active AO download identities.
type AwakeDownloadsRequest struct {
	ActiveIDs []string `json:"activeIds"`
}

func (c *AwakeController) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	kind := "internal"
	if errors.Is(err, awakesvc.ErrValidation) {
		status = http.StatusBadRequest
		kind = "invalid_request"
	}
	envelope.WriteAPIError(w, r, status, kind, "AWAKE", err.Error(), nil)
}
func (c *AwakeController) available(w http.ResponseWriter, r *http.Request) bool {
	if c.Svc != nil {
		return true
	}
	envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "AWAKE", "Stay Awake is not available in this daemon", nil)
	return false
}
func (c *AwakeController) decode(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "invalid_request", "AWAKE", "invalid Stay Awake request", nil)
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "invalid_request", "AWAKE", "expected one Stay Awake request", nil)
		return false
	}
	return true
}
func (c *AwakeController) reply(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		c.fail(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, c.Svc.Status())
}

// Status returns current wake state.
func (c *AwakeController) Status(w http.ResponseWriter, r *http.Request) {
	if c.available(w, r) {
		c.reply(w, r, nil)
	}
}

// Start starts a wake request.
func (c *AwakeController) Start(w http.ResponseWriter, r *http.Request) {
	if !c.available(w, r) {
		return
	}
	var body domain.StartAwakeRequest
	if c.decode(w, r, &body) {
		c.reply(w, r, c.Svc.Start(r.Context(), body))
	}
}

// End ends one owned request.
func (c *AwakeController) End(w http.ResponseWriter, r *http.Request) {
	if c.available(w, r) {
		c.reply(w, r, c.Svc.End(r.Context(), chi.URLParam(r, "awakeId")))
	}
}

// Extend extends a finite request.
func (c *AwakeController) Extend(w http.ResponseWriter, r *http.Request) {
	if !c.available(w, r) {
		return
	}
	var body ExtendAwakeRequest
	if c.decode(w, r, &body) {
		c.reply(w, r, c.Svc.Extend(r.Context(), chi.URLParam(r, "awakeId"), body.DurationSeconds))
	}
}

// Stop releases this adapter's wake ownership.
func (c *AwakeController) Stop(w http.ResponseWriter, r *http.Request) {
	if c.available(w, r) {
		c.reply(w, r, c.Svc.StopAll(r.Context()))
	}
}

// Resume checks fresh host readings before clearing a safety stop.
func (c *AwakeController) Resume(w http.ResponseWriter, r *http.Request) {
	if c.available(w, r) {
		c.reply(w, r, c.Svc.Resume(r.Context()))
	}
}

// Settings updates desktop wake settings.
func (c *AwakeController) Settings(w http.ResponseWriter, r *http.Request) {
	if !c.available(w, r) {
		return
	}
	var body domain.AwakeSettings
	if c.decode(w, r, &body) {
		c.reply(w, r, c.Svc.UpdateSettings(r.Context(), body))
	}
}

// Rules replaces activation rules.
func (c *AwakeController) Rules(w http.ResponseWriter, r *http.Request) {
	if !c.available(w, r) {
		return
	}
	var body AwakeRulesRequest
	if c.decode(w, r, &body) {
		c.reply(w, r, c.Svc.UpdateRules(r.Context(), body.Rules))
	}
}

// Downloads updates typed download heartbeats.
func (c *AwakeController) Downloads(w http.ResponseWriter, r *http.Request) {
	if !c.available(w, r) {
		return
	}
	var body AwakeDownloadsRequest
	if c.decode(w, r, &body) {
		c.reply(w, r, c.Svc.Downloads(body.ActiveIDs))
	}
}

// Recover ends requests and restores journaled effects.
func (c *AwakeController) Recover(w http.ResponseWriter, r *http.Request) {
	if c.available(w, r) {
		c.reply(w, r, c.Svc.Recover(r.Context()))
	}
}

// ClearHistory removes ended sessions while preserving replay protection.
func (c *AwakeController) ClearHistory(w http.ResponseWriter, r *http.Request) {
	if c.available(w, r) {
		c.reply(w, r, c.Svc.ClearHistory(r.Context()))
	}
}

// Observe reads fresh host conditions.
func (c *AwakeController) Observe(w http.ResponseWriter, r *http.Request) {
	if c.available(w, r) {
		envelope.WriteJSON(w, http.StatusOK, c.Svc.ObserveStatus(r.Context()))
	}
}

// AuthorizeLowPower opens a user-initiated macOS authorization prompt.
func (c *AwakeController) AuthorizeLowPower(w http.ResponseWriter, r *http.Request) {
	if c.available(w, r) {
		c.reply(w, r, c.Svc.AuthorizeLowPower(r.Context()))
	}
}

// RevokeLowPower disables automation and requests explicit permission removal.
func (c *AwakeController) RevokeLowPower(w http.ResponseWriter, r *http.Request) {
	if c.available(w, r) {
		c.reply(w, r, c.Svc.RevokeLowPower(r.Context()))
	}
}
