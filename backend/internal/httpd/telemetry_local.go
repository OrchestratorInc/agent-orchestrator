package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// TelemetryStatus is what `ao telemetry status` reports.
type TelemetryStatus struct {
	// AnalyticsSending is true when this daemon is exporting usage analytics
	// right now: remote export configured, not opted out, no DO_NOT_TRACK.
	AnalyticsSending bool `json:"analyticsSending"`
	// OptedOut is the Share usage analytics opt-out (desktop, daemon, phone).
	OptedOut bool `json:"optedOut"`
	// DoNotTrack is DO_NOT_TRACK as the daemon saw it at start.
	DoNotTrack bool `json:"doNotTrack"`
	// RemoteConfigured is whether this daemon was started to export at all
	// (the desktop app turns it on; a CLI-started daemon does not).
	RemoteConfigured bool `json:"remoteConfigured"`
	// ShareIdentity is "Include my account identity".
	ShareIdentity bool `json:"shareIdentity"`
	// ErrorReports is whether Share error events (Sentry) is allowed.
	ErrorReports bool `json:"errorReports"`
	// LocalLog is whether exported events are recorded for `ao telemetry log`.
	LocalLog      bool   `json:"localLog"`
	RetentionDays int    `json:"retentionDays"`
	InstallID     string `json:"installId,omitempty"`
}

// TelemetryLocal backs the loopback-only `ao telemetry` routes. Every field is
// required when the struct is supplied.
type TelemetryLocal struct {
	// List returns exported events newest first; a non-empty event keeps only
	// that name, filtered before limit applies.
	List        func(ctx context.Context, since time.Time, limit int, event string) ([]telemetryadapter.ExportedEvent, error)
	Record      func(ctx context.Context, ev telemetryadapter.ExportedEvent)
	Status      func(ctx context.Context) TelemetryStatus
	SetOptedOut func(ctx context.Context, optedOut bool) error
}

const (
	telemetryLogDefaultLimit = 100
	telemetryLogMaxLimit     = 1000
	telemetryExportedMaxBody = 64 << 10
)

var exportedEventNamePattern = regexp.MustCompile(`^[$A-Za-z0-9_.:-]{1,120}$`)

// mountTelemetryLocal serves the local telemetry log and switch. Like the
// identity hand-off, only a loopback caller without an Origin (the CLI and
// Electron main) may use it; the LAN listener never serves /internal/.
func mountTelemetryLocal(r chi.Router, local *TelemetryLocal) {
	if local == nil {
		return
	}
	forbidden := func(w http.ResponseWriter) {
		envelope.WriteJSON(w, http.StatusForbidden, map[string]any{"status": "forbidden", "service": daemonmeta.ServiceName})
	}
	r.Get("/internal/telemetry/status", func(w http.ResponseWriter, req *http.Request) {
		if !localControlRequest(req) {
			forbidden(w)
			return
		}
		envelope.WriteJSON(w, http.StatusOK, local.Status(req.Context()))
	})
	r.Get("/internal/telemetry/log", func(w http.ResponseWriter, req *http.Request) {
		if !localControlRequest(req) {
			forbidden(w)
			return
		}
		limit := telemetryLogDefaultLimit
		if raw := req.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > telemetryLogMaxLimit {
				envelope.WriteAPIError(w, req, http.StatusBadRequest, "bad_request", "INVALID_LIMIT", "limit must be 1-1000", nil)
				return
			}
			limit = n
		}
		since := time.Unix(0, 0)
		if raw := req.URL.Query().Get("since"); raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				envelope.WriteAPIError(w, req, http.StatusBadRequest, "bad_request", "INVALID_SINCE", "since must be an RFC 3339 time", nil)
				return
			}
			since = t
		}
		event := req.URL.Query().Get("event")
		if event != "" && !exportedEventNamePattern.MatchString(event) {
			envelope.WriteAPIError(w, req, http.StatusBadRequest, "bad_request", "INVALID_EVENT", "event must be an event name", nil)
			return
		}
		events, err := local.List(req.Context(), since, limit, event)
		if err != nil {
			envelope.WriteAPIError(w, req, http.StatusInternalServerError, "internal_error", "TELEMETRY_LOG_UNAVAILABLE", "telemetry log could not be read", nil)
			return
		}
		envelope.WriteJSON(w, http.StatusOK, map[string]any{"events": events})
	})
	r.Post("/internal/telemetry/opt-out", func(w http.ResponseWriter, req *http.Request) {
		if !localControlRequest(req) {
			forbidden(w)
			return
		}
		var body struct {
			OptedOut *bool `json:"optedOut"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil || body.OptedOut == nil {
			envelope.WriteAPIError(w, req, http.StatusBadRequest, "bad_request", "INVALID_JSON", `body must be {"optedOut": true|false}`, nil)
			return
		}
		if err := local.SetOptedOut(req.Context(), *body.OptedOut); err != nil {
			envelope.WriteAPIError(w, req, http.StatusInternalServerError, "internal_error", "TELEMETRY_OPT_OUT_NOT_SAVED", "the telemetry opt-out could not be saved", nil)
			return
		}
		envelope.WriteJSON(w, http.StatusOK, local.Status(req.Context()))
	})
	// The desktop renderer's events go straight to PostHog, so Electron main
	// forwards each one here as it is sent, to appear in the same local log.
	r.Post("/internal/telemetry/exported", func(w http.ResponseWriter, req *http.Request) {
		if !localControlRequest(req) {
			forbidden(w)
			return
		}
		var body struct {
			Event      string         `json:"event"`
			DistinctID string         `json:"distinctId"`
			Properties map[string]any `json:"properties"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, telemetryExportedMaxBody))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil || !exportedEventNamePattern.MatchString(body.Event) || len(body.DistinctID) > 200 {
			envelope.WriteAPIError(w, req, http.StatusBadRequest, "bad_request", "INVALID_EXPORTED_EVENT", "body must be an exported event", nil)
			return
		}
		local.Record(req.Context(), telemetryadapter.ExportedEvent{
			OccurredAt: time.Now().UTC(),
			Event:      strings.TrimSpace(body.Event),
			Surface:    "desktop",
			DistinctID: body.DistinctID,
			Properties: body.Properties,
		})
		w.WriteHeader(http.StatusNoContent)
	})
}
