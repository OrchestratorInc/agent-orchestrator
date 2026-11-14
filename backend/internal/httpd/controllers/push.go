package controllers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

type PushRegistry interface {
	Upsert(dev mobilebridge.PushDevice) error
	UnregisterToken(token string) error
	Unpair(id string) error
}

type PushController struct {
	Registry PushRegistry
	Devices  *mobilebridge.AccessDevices
	Now      func() time.Time
}

func (c *PushController) Register(r chi.Router) {
	r.Post("/push/devices", c.register)
	r.Delete("/push/devices/{token}", c.unregister)
	r.Delete("/push/pairings/{id}", c.unpair)
}

func (c *PushController) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func (c *PushController) register(w http.ResponseWriter, r *http.Request) {
	if c.Registry == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/push/devices")
		return
	}
	var req RegisterPushDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if req.Token != "" && !mobilebridge.ValidPushToken(req.Token) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PUSH_TOKEN",
			"token must be a well-formed Expo push token", nil)
		return
	}
	if req.InstallID == "" && req.Token == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "MISSING_DEVICE_IDENTITY",
			"installId or token is required", nil)
		return
	}
	installID := req.InstallID
	if installID == "" {
		installID = mobilebridge.LegacyInstallIDPrefix + uuid.NewString()
	}
	now := c.now()
	dev := mobilebridge.PushDevice{
		InstallID:  installID,
		Token:      req.Token,
		Platform:   req.Platform,
		DeviceName: req.DeviceName,
		HostName:   req.HostName,
		CreatedAt:  now,
		LastSeenAt: now,
	}
	if err := c.Registry.Upsert(dev); err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "PUSH_REGISTER", err.Error(), nil)
		return
	}
	if credential := requestscope.CredentialFrom(r.Context()); c.Devices != nil && credential.Kind == requestscope.CredentialDevice && req.InstallID != "" {
		_ = c.Devices.LinkInstall(credential.DeviceID, req.InstallID)
	}
	envelope.WriteJSON(w, http.StatusOK, PushDeviceEnvelope{Device: PushDeviceResponse{
		Token:      dev.Token,
		Platform:   dev.Platform,
		DeviceName: dev.DeviceName,
		CreatedAt:  dev.CreatedAt,
		LastSeenAt: dev.LastSeenAt,
	}})
}

func (c *PushController) unregister(w http.ResponseWriter, r *http.Request) {
	if c.Registry == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/push/devices/{token}")
		return
	}
	token := chi.URLParam(r, "token")
	if err := c.Registry.UnregisterToken(token); err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "PUSH_UNREGISTER", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, UnregisterPushDeviceResponse{Token: token, Deleted: true})
}

func (c *PushController) unpair(w http.ResponseWriter, r *http.Request) {
	if c.Registry == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/push/pairings/{id}")
		return
	}
	id := chi.URLParam(r, "id")
	if err := c.Registry.Unpair(id); err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "DEVICE_UNPAIR_FAILED",
			"Could not unpair the device", nil)
		return
	}
	if credential := requestscope.CredentialFrom(r.Context()); c.Devices != nil && credential.Kind == requestscope.CredentialDevice {
		_, _, _ = c.Devices.Revoke(credential.DeviceID)
	}
	w.WriteHeader(http.StatusNoContent)
}
