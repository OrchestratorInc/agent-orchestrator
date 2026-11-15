package controllers

import (
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

type DeviceRoster interface {
	List() []mobilebridge.PushDevice
	Delete(installID string) error
}

type LiveSet interface {
	Live() map[string]bool
	LastSeen(installID string) (time.Time, bool)
}

type DeviceAccess interface {
	AccessDevices() []mobilebridge.AccessDevice
	ConnectedCredentials() map[string]bool
	AccountDevices() (tokenHash string, lastUsed time.Time)
	RevokeDevice(id string) (installID string, found bool, err error)
	UnlinkAccount() error
}

const AccountDevicesID = "account"

const (
	deviceKindDevice   = "device"
	deviceKindPassword = "password"
	deviceKindAccount  = "account"
)

type MobileDevicesController struct {
	Registry DeviceRoster
	Presence LiveSet
	Access   DeviceAccess
}

func writeRegistryUnavailable(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "DEVICE_REGISTRY_UNAVAILABLE",
		"The mobile device registry failed to load; device management is unavailable until the daemon restarts with a valid registry", nil)
}

func (c *MobileDevicesController) List(w http.ResponseWriter, r *http.Request) {
	if c.Registry == nil && c.Access == nil {
		writeRegistryUnavailable(w, r)
		return
	}
	var devices []mobilebridge.PushDevice
	if c.Registry != nil {
		devices = c.Registry.List()
	}
	live := map[string]bool{}
	if c.Presence != nil {
		live = c.Presence.Live()
	}
	lastSeen := func(d mobilebridge.PushDevice) time.Time {
		seen := d.LastSeenAt
		if c.Presence != nil {
			if at, ok := c.Presence.LastSeen(d.InstallID); ok && at.After(seen) {
				seen = at
			}
		}
		return seen
	}
	out := make([]MobileDeviceResponse, 0, len(devices)+1)
	owned := map[string]bool{}
	if c.Access != nil {
		push := make(map[string]mobilebridge.PushDevice, len(devices))
		for _, d := range devices {
			push[d.InstallID] = d
		}
		connected := c.Access.ConnectedCredentials()
		for _, d := range c.Access.AccessDevices() {
			row := MobileDeviceResponse{
				ID: d.ID, Kind: deviceKindDevice, InstallID: d.InstallID, Platform: d.Platform, DeviceName: d.Name,
				Live: connected[d.TokenHash], CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt,
			}
			if registration, ok := push[d.InstallID]; ok && d.InstallID != "" {
				owned[d.InstallID] = true
				row.Token = registration.Token
				row.NotificationsEnabled = registration.Token != ""
				row.Live = row.Live || live[d.InstallID]
				if row.DeviceName == "" {
					row.DeviceName = registration.DeviceName
				}
				if row.Platform == "" {
					row.Platform = registration.Platform
				}
				if seen := lastSeen(registration); seen.After(row.LastSeenAt) {
					row.LastSeenAt = seen
				}
			}
			out = append(out, row)
		}
		if hash, used := c.Access.AccountDevices(); hash != "" {
			out = append(out, MobileDeviceResponse{ID: AccountDevicesID, Kind: deviceKindAccount, Live: connected[hash], LastSeenAt: used})
		}
	}
	for _, d := range devices {
		if owned[d.InstallID] {
			continue
		}
		out = append(out, MobileDeviceResponse{
			ID:                   d.InstallID,
			Kind:                 deviceKindPassword,
			InstallID:            d.InstallID,
			Token:                d.Token,
			Platform:             d.Platform,
			DeviceName:           d.DeviceName,
			Live:                 live[d.InstallID],
			NotificationsEnabled: d.Token != "",
			CreatedAt:            d.CreatedAt,
			LastSeenAt:           lastSeen(d),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Live != out[j].Live {
			return out[i].Live
		}
		return out[i].LastSeenAt.After(out[j].LastSeenAt)
	})
	envelope.WriteJSON(w, http.StatusOK, MobileDevicesResponse{Devices: out})
}

func (c *MobileDevicesController) Remove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if c.Access != nil {
		if id == AccountDevicesID {
			if err := c.Access.UnlinkAccount(); err != nil {
				envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "DEVICE_DELETE_FAILED", "Could not remove device", nil)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		installID, found, err := c.Access.RevokeDevice(id)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "DEVICE_DELETE_FAILED", "Could not remove device", nil)
			return
		}
		if found {
			if installID != "" && c.Registry != nil {
				_ = c.Registry.Delete(installID)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	if c.Registry == nil {
		writeRegistryUnavailable(w, r)
		return
	}
	if err := c.Registry.Delete(id); err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "DEVICE_DELETE_FAILED", "Could not remove device", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
