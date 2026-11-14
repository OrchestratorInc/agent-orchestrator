package mobilebridge

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type PushDevice struct {
	InstallID  string    `json:"installId"`
	Token      string    `json:"token,omitempty"`
	Platform   string    `json:"platform,omitempty"`
	DeviceName string    `json:"deviceName,omitempty"`
	HostName   string    `json:"hostName,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type pushDevicesFile struct {
	Devices []PushDevice `json:"devices"`
}

var expoPushTokenRE = regexp.MustCompile(`^Expo(nent)?PushToken\[[^\]]+\]$`)

func ValidPushToken(tok string) bool {
	return expoPushTokenRE.MatchString(tok)
}

const LegacyInstallIDPrefix = "legacy-"

func PushDevicesPath(dataDir string) string {
	return filepath.Join(dataDir, "mobile", "push-devices.json")
}

type DeviceRegistry struct {
	mu      sync.RWMutex
	path    string
	devices map[string]PushDevice
}

func LoadRegistry(path string) (*DeviceRegistry, error) {
	reg := &DeviceRegistry{path: path, devices: map[string]PushDevice{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return reg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read push devices: %w", err)
	}
	var file pushDevicesFile
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, fmt.Errorf("parse push devices: %w", err)
	}
	migrated := false
	for _, d := range file.Devices {
		if d.Token == "" && d.InstallID == "" {
			continue
		}
		if d.InstallID == "" {
			d.InstallID = LegacyInstallIDPrefix + uuid.NewString()
			migrated = true
		}
		reg.devices[d.InstallID] = d
	}
	if migrated {
		reg.mu.Lock()
		err := reg.persistLocked()
		reg.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("persist migrated push devices: %w", err)
		}
	}
	return reg, nil
}

func (r *DeviceRegistry) Upsert(dev PushDevice) error {
	if dev.Token != "" && !ValidPushToken(dev.Token) {
		return fmt.Errorf("invalid push token %q", dev.Token)
	}
	if dev.InstallID == "" {
		return fmt.Errorf("install id required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	byInstall, hasInstall := r.devices[dev.InstallID]
	tokenKey, byToken, hasToken := r.findByTokenLocked(dev.Token, dev.InstallID)

	return r.commitLocked(func() {
		switch {
		case hasInstall && hasToken:
			merged := mergeIdentities(byInstall, byToken)
			dev.CreatedAt, dev.Token = carryOver(merged, dev)
			if dev.HostName == "" {
				dev.HostName = merged.HostName
			}
			delete(r.devices, tokenKey)
		case hasInstall:
			dev.CreatedAt, dev.Token = carryOver(byInstall, dev)
			if dev.HostName == "" {
				dev.HostName = byInstall.HostName
			}
		case hasToken:
			dev.CreatedAt, dev.Token = carryOver(byToken, dev)
			if dev.HostName == "" {
				dev.HostName = byToken.HostName
			}
			delete(r.devices, tokenKey)
		}
		r.devices[dev.InstallID] = dev
	})
}

func (r *DeviceRegistry) findByTokenLocked(token, skipInstallID string) (key string, dev PushDevice, ok bool) {
	if token == "" {
		return "", PushDevice{}, false
	}
	for k, existing := range r.devices {
		if k != skipInstallID && existing.Token == token {
			return k, existing, true
		}
	}
	return "", PushDevice{}, false
}

func mergeIdentities(primary, other PushDevice) PushDevice {
	out := primary
	if !other.CreatedAt.IsZero() && (out.CreatedAt.IsZero() || other.CreatedAt.Before(out.CreatedAt)) {
		out.CreatedAt = other.CreatedAt
	}
	if other.LastSeenAt.After(out.LastSeenAt) {
		out.LastSeenAt = other.LastSeenAt
	}
	if out.Token == "" {
		out.Token = other.Token
	}
	if out.DeviceName == "" {
		out.DeviceName = other.DeviceName
	}
	if out.HostName == "" {
		out.HostName = other.HostName
	}
	if out.Platform == "" {
		out.Platform = other.Platform
	}
	return out
}

func carryOver(existing, incoming PushDevice) (createdAt time.Time, token string) {
	created := incoming.CreatedAt
	if !existing.CreatedAt.IsZero() {
		created = existing.CreatedAt
	}
	token = incoming.Token
	if token == "" {
		token = existing.Token
	}
	return created, token
}

func (r *DeviceRegistry) Delete(installID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.devices[installID]; !ok {
		return nil
	}
	return r.commitLocked(func() { delete(r.devices, installID) })
}

func (r *DeviceRegistry) UnregisterToken(token string) error {
	if token == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, dev := range r.devices {
		if dev.Token != token {
			continue
		}
		if strings.HasPrefix(dev.InstallID, LegacyInstallIDPrefix) {
			return r.commitLocked(func() { delete(r.devices, key) })
		}
		dev.Token = ""
		return r.commitLocked(func() { r.devices[key] = dev })
	}
	return nil
}

func (r *DeviceRegistry) Unpair(id string) error {
	if id == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.devices[id]; ok {
		return r.commitLocked(func() { delete(r.devices, id) })
	}
	for key, dev := range r.devices {
		if dev.Token == id {
			return r.commitLocked(func() { delete(r.devices, key) })
		}
	}
	return nil
}

func (r *DeviceRegistry) List() []PushDevice {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PushDevice, 0, len(r.devices))
	for _, d := range r.devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].Token < out[j].Token
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (r *DeviceRegistry) commitLocked(mutate func()) error {
	devices := maps.Clone(r.devices)
	mutate()
	if err := r.persistLocked(); err != nil {
		r.devices = devices
		return err
	}
	return nil
}

func (r *DeviceRegistry) persistLocked() error {
	devices := make([]PushDevice, 0, len(r.devices))
	for _, d := range r.devices {
		devices = append(devices, d)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Token < devices[j].Token })

	b, err := json.MarshalIndent(pushDevicesFile{Devices: devices}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return fmt.Errorf("mkdir mobile dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".push-devices-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, r.path)
}
