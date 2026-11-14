package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
)

type ControlDeps struct {
	RequestShutdown      func()
	RequestAgentShutdown func(appRunID string) bool
	IPv6Loopback         bool
}

func NewRouterWithControl(cfg config.Config, log *slog.Logger, termMgr *terminal.Manager, deps APIDeps, control ControlDeps) chi.Router {
	log = loggerOrDefault(log)
	deps = normalizeAPIDeps(deps, log)
	r := chi.NewRouter()
	api := newAPIWithLogger(cfg, deps, log)

	r.Use(middleware.RequestID)
	r.Use(caller.Middleware)
	r.Use(requestLogger(log, deps.Telemetry))
	r.Use(recoverTelemetry(log, deps.Telemetry))
	r.Use(rendererOriginMiddleware(cfg.AllowedOrigins))
	r.Use(mobileControlOriginMiddleware(cfg.AllowedOrigins))
	r.Use(corsMiddleware(cfg.AllowedOrigins))
	r.Use(requestSurfaceMiddleware(cfg.AllowedOrigins))
	r.Use(previewOriginMiddleware(api.sessions))

	r.NotFound(notFoundJSON)
	r.MethodNotAllowed(methodNotAllowedJSON)

	mountHealth(r, cfg, control.IPv6Loopback)
	mountTerminalMux(r, termMgr, log, cfg.AllowedOrigins)
	mountControl(r, control)
	mountTelemetry(r, cfg, deps.Telemetry)
	mountUsageFlush(r, deps.UsageFlush)
	mountTelemetryIdentity(r, deps.TelemetryIdentity)
	mountMobile(r, deps.Mobile)
	mountBrowserLive(r, deps.BrowserLive)
	mountAwake(r, &controllers.AwakeController{Svc: deps.Awake})
	mountMobileDevices(r, &controllers.MobileDevicesController{Registry: deps.DeviceRoster, Presence: deps.DeviceLive, Access: deps.DeviceAccess})
	api.Register(r)

	return r
}

func mountUsageFlush(r chi.Router, flush func(context.Context)) {
	if flush == nil {
		return
	}
	r.Post("/internal/telemetry/usage-flush", func(w http.ResponseWriter, req *http.Request) {
		if !localControlRequest(req) {
			envelope.WriteJSON(w, http.StatusForbidden, map[string]any{
				"status": "forbidden", "service": daemonmeta.ServiceName,
			})
			return
		}
		flush(req.Context())
		w.WriteHeader(http.StatusNoContent)
	})
}

func previewOriginMiddleware(sessions *controllers.SessionsController) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if sessions != nil && sessions.PreviewOrigin(w, r) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func mountHealth(r chi.Router, cfg config.Config, ipv6Loopback bool) {
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		envelope.WriteJSON(w, http.StatusOK, daemonProbePayload("ok", cfg, ipv6Loopback))
	})
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		envelope.WriteJSON(w, http.StatusOK, daemonProbePayload("ready", cfg, ipv6Loopback))
	})
}

func mountControl(r chi.Router, deps ControlDeps) {
	if deps.RequestShutdown == nil {
		return
	}
	r.Post("/shutdown", func(w http.ResponseWriter, req *http.Request) {
		if !localControlRequest(req) {
			envelope.WriteJSON(w, http.StatusForbidden, map[string]any{
				"status":  "forbidden",
				"service": daemonmeta.ServiceName,
			})
			return
		}
		var in struct {
			StopAgents bool   `json:"stopAgents"`
			AppRunID   string `json:"appRunId"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1024))
		if err := decoder.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			envelope.WriteAPIError(w, req, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid shutdown request", nil)
			return
		}
		if in.StopAgents {
			if deps.RequestAgentShutdown == nil {
				envelope.WriteAPIError(w, req, http.StatusNotImplemented, "not_implemented", "AGENT_SHUTDOWN_UNAVAILABLE", "Agent shutdown is unavailable", nil)
				return
			}
			if !deps.RequestAgentShutdown(in.AppRunID) {
				envelope.WriteAPIError(w, req, http.StatusConflict, "conflict", "DAEMON_OWNER_CHANGED", "This desktop no longer owns the daemon", nil)
				return
			}
		}
		envelope.WriteJSON(w, http.StatusAccepted, map[string]any{
			"status":  "shutting_down",
			"service": daemonmeta.ServiceName,
			"pid":     os.Getpid(),
		})
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.AfterFunc(10*time.Millisecond, deps.RequestShutdown)
	})
}

func mountMobile(r chi.Router, c *controllers.MobileController) {
	if c == nil {
		return
	}
	r.Get("/api/v1/mobile/status", c.Status)
	r.Post("/api/v1/mobile/enable", c.Enable)
	r.Post("/api/v1/mobile/enable-lan-only", c.EnableLANOnly)
	r.Post("/api/v1/mobile/enable-tunnel-only", c.EnableTunnelOnly)
	r.Post("/api/v1/mobile/remote-access", c.StartRemoteAccess)
	r.Post("/api/v1/mobile/disable", c.Disable)
	r.Post("/api/v1/mobile/regenerate", c.Regenerate)
	r.Post("/api/v1/mobile/secure-pairing", c.SecurePairing)
	r.Post("/api/v1/mobile/keep-awake", c.KeepAwake)
	r.Post("/api/v1/remote-host/account-token", c.IssueAccountToken)
	r.Post("/api/v1/remote-host/devices", c.PairDevice)
	r.Post("/api/v1/mobile/pairing-code", c.NewPairingCode)
	r.Post("/api/v1/mobile/require-device-credentials", c.RequireDeviceCredentials)
	r.Post("/api/v1/mobile/browser-control", c.BrowserRemoteAccess)
}

func mountMobileDevices(r chi.Router, c *controllers.MobileDevicesController) {
	if c == nil {
		return
	}
	r.Get("/api/v1/mobile/devices", c.List)
	r.Delete("/api/v1/mobile/devices/{id}", c.Remove)
}

type cliInvokedRequest struct {
	Command     string `json:"command"`
	CommandPath string `json:"commandPath"`
	ActorType   string `json:"actorType"`
}

type cliUsageErrorRequest struct {
	Command     string `json:"command"`
	CommandPath string `json:"commandPath"`
	Error       string `json:"error"`
}

func mountTelemetry(r chi.Router, cfg config.Config, sink ports.EventSink) {
	if sink == nil {
		return
	}
	cliTelemetry := newCLITelemetryReservoir(cfg.DataDir)
	r.Post("/internal/telemetry/cli-invoked", func(w http.ResponseWriter, req *http.Request) {
		if !localControlRequest(req) {
			envelope.WriteJSON(w, http.StatusForbidden, map[string]any{
				"status":  "forbidden",
				"service": daemonmeta.ServiceName,
			})
			return
		}

		var body cliInvokedRequest
		dec := json.NewDecoder(req.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			envelope.WriteAPIError(w, req, http.StatusBadRequest, "bad_request", "INVALID_JSON", "request body must be valid JSON", nil)
			return
		}
		if body.CommandPath == "" {
			envelope.WriteAPIError(w, req, http.StatusBadRequest, "bad_request", "COMMAND_PATH_REQUIRED", "commandPath is required", nil)
			return
		}
		commandPath := telemetrymeta.NormalizeCommandPath(body.CommandPath)
		actorType := telemetrymeta.CLIActorType(body.ActorType, commandPath)
		if actorType == "system" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if telemetrymeta.IsRoutineInternalCLICommand(commandPath) {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		if now := time.Now(); cliTelemetry.reserveInvoked(now, actorType, commandPath) {
			sink.Emit(req.Context(), ports.TelemetryEvent{
				Name:       "ao.cli.invoked",
				Source:     "cli",
				OccurredAt: now.UTC(),
				Level:      ports.TelemetryLevelInfo,
				RequestID:  middleware.GetReqID(req.Context()),
				Payload: map[string]any{
					"command":      body.Command,
					"command_path": commandPath,
					"actor_type":   actorType,
				},
			})
		}
		if actorType == "user" {
			if now := time.Now(); cliTelemetry.reserveActive(now) {
				sink.Emit(req.Context(), ports.TelemetryEvent{
					Name:       "ao.cli.active",
					Source:     "cli",
					OccurredAt: now.UTC(),
					Level:      ports.TelemetryLevelInfo,
					RequestID:  middleware.GetReqID(req.Context()),
					Payload: map[string]any{
						"channel":      "cli",
						"command":      body.Command,
						"command_path": commandPath,
						"actor_type":   actorType,
					},
				})
			}
		}
		w.WriteHeader(http.StatusAccepted)
	})
	r.Post("/internal/telemetry/cli-usage-error", func(w http.ResponseWriter, req *http.Request) {
		if !localControlRequest(req) {
			envelope.WriteJSON(w, http.StatusForbidden, map[string]any{
				"status":  "forbidden",
				"service": daemonmeta.ServiceName,
			})
			return
		}

		var body cliUsageErrorRequest
		dec := json.NewDecoder(req.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			envelope.WriteAPIError(w, req, http.StatusBadRequest, "bad_request", "INVALID_JSON", "request body must be valid JSON", nil)
			return
		}
		if body.CommandPath == "" {
			envelope.WriteAPIError(w, req, http.StatusBadRequest, "bad_request", "COMMAND_PATH_REQUIRED", "commandPath is required", nil)
			return
		}

		sink.Emit(req.Context(), ports.TelemetryEvent{
			Name:       "ao.usage.cli_usage_errors",
			Source:     "cli",
			OccurredAt: time.Now().UTC(),
			Level:      ports.TelemetryLevelWarn,
			RequestID:  middleware.GetReqID(req.Context()),
			Payload: map[string]any{
				"component":    "cli",
				"operation":    "command_parse",
				"command":      body.Command,
				"command_path": body.CommandPath,
				"error_kind":   "usage",
				"fingerprint":  telemetrymeta.Fingerprint("cli", "command_parse", body.CommandPath, "usage"),
			},
		})
		w.WriteHeader(http.StatusAccepted)
	})
}

type telemetryIdentityRequest struct {
	CloudUserID string `json:"cloudUserId"`
	Sequence    int64  `json:"sequence,omitempty"`
}

func mountTelemetryIdentity(r chi.Router, ident ports.TelemetryIdentityStore) {
	if ident == nil {
		return
	}
	var (
		seqMu   sync.Mutex
		lastSeq int64
	)
	r.Post("/internal/telemetry/identity", func(w http.ResponseWriter, req *http.Request) {
		if !localControlRequest(req) {
			envelope.WriteJSON(w, http.StatusForbidden, map[string]any{"status": "forbidden", "service": daemonmeta.ServiceName})
			return
		}
		var body telemetryIdentityRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			envelope.WriteAPIError(w, req, http.StatusBadRequest, "bad_request", "INVALID_JSON", "request body must be valid JSON", nil)
			return
		}
		seqMu.Lock()
		defer seqMu.Unlock()
		if body.Sequence > 0 {
			if body.Sequence < lastSeq {
				envelope.WriteAPIError(w, req, http.StatusConflict, "conflict", "STALE_TELEMETRY_IDENTITY", "a newer telemetry identity was already applied", map[string]any{"lastSequence": lastSeq})
				return
			}
			lastSeq = body.Sequence
		}
		if err := ident.SetCloudUserID(req.Context(), body.CloudUserID); err != nil {
			if errors.Is(err, ports.ErrInvalidCloudUserID) {
				envelope.WriteAPIError(w, req, http.StatusBadRequest, "bad_request", "INVALID_CLOUD_USER_ID", "cloudUserId is not a valid user id", nil)
				return
			}
			envelope.WriteAPIError(w, req, http.StatusInternalServerError, "internal_error", "TELEMETRY_IDENTITY_NOT_PERSISTED", "telemetry identity could not be saved", nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func localControlRequest(r *http.Request) bool {
	if r.Header.Get("Origin") != "" {
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func daemonProbePayload(status string, cfg config.Config, ipv6Loopback bool) map[string]any {
	payload := map[string]any{
		"status":       status,
		"service":      daemonmeta.ServiceName,
		"pid":          os.Getpid(),
		"ipv6Loopback": ipv6Loopback,
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		payload["executablePath"] = exe
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		payload["workingDirectory"] = cwd
	}
	if cfg.StartupWorkingDirectory != "" {
		payload["startupWorkingDirectory"] = cfg.StartupWorkingDirectory
	}
	if appImage := os.Getenv("AO_APPIMAGE"); appImage != "" {
		payload["appImagePath"] = appImage
	}
	return payload
}

func mountAwake(r chi.Router, c *controllers.AwakeController) {
	r.Get("/api/v1/awake", c.Status)
	r.Get("/api/v1/awake/observations", c.Observe)
	r.Post("/api/v1/awake/sessions", c.Start)
	r.Post("/api/v1/awake/sessions/{awakeId}/end", c.End)
	r.Post("/api/v1/awake/sessions/{awakeId}/extend", c.Extend)
	r.Put("/api/v1/awake/settings", c.Settings)
	r.Put("/api/v1/awake/rules", c.Rules)
	r.Post("/api/v1/awake/low-power/authorize", c.AuthorizeLowPower)
	r.Post("/api/v1/awake/low-power/revoke", c.RevokeLowPower)
	r.Post("/api/v1/awake/stop", c.Stop)
	r.Post("/api/v1/awake/resume", c.Resume)
	r.Post("/api/v1/awake/recover", c.Recover)
	r.Delete("/api/v1/awake/history", c.ClearHistory)
	r.Post("/api/v1/awake/downloads", c.Downloads)
}
