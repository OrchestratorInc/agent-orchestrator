package mobilebridge

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// AccessDevice is a paired device. Only its token's SHA-256 is stored.
type AccessDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Platform  string `json:"platform,omitempty"`
	TokenHash string `json:"tokenHash"`
	// InstallID links the device to its push registration.
	InstallID  string    `json:"installId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

// Keys of the shared credentials' last-used times.
const (
	SharedPassword = "password"
	SharedAccount  = "account"
)

// DeviceTokenPrefix keeps a device token from looking like an account token.
const DeviceTokenPrefix = "aod_"

// maxAccessDevices bounds the tokens a holder of the pairing password can mint.
const maxAccessDevices = 64

// touchInterval limits how often last-seen times are written to disk.
const touchInterval = 5 * time.Minute

// ErrTooManyDevices is returned by Issue when the registry is full.
var ErrTooManyDevices = errors.New("too many paired devices; revoke one first")

type accessDevicesFile struct {
	Devices        []AccessDevice       `json:"devices"`
	SharedLastUsed map[string]time.Time `json:"sharedLastUsed,omitempty"`
}

// AccessDevicesPath returns the registry's path in dataDir.
func AccessDevicesPath(dataDir string) string {
	return filepath.Join(dataDir, "mobile", "access-devices.json")
}

// AccessDevices is the registry of device tokens the remote listener accepts.
type AccessDevices struct {
	mu   sync.RWMutex
	path string
	// writeErr refuses every write when a damaged file could not be moved aside.
	writeErr error
	devices  map[string]AccessDevice
	byHash   map[string]string
	shared   map[string]time.Time
}

// LoadAccessDevices reads the registry. A damaged or unreadable file is moved
// aside and reported, and the registry starts empty.
func LoadAccessDevices(path string) (*AccessDevices, error) {
	r := &AccessDevices{path: path, devices: map[string]AccessDevice{}, byHash: map[string]string{}, shared: map[string]time.Time{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		err = r.moveAside(fmt.Errorf("read access devices: %w", err))
		return r, err
	}
	var file accessDevicesFile
	if err := json.Unmarshal(raw, &file); err != nil {
		err = r.moveAside(fmt.Errorf("parse access devices: %w", err))
		return r, err
	}
	for _, device := range file.Devices {
		if device.ID == "" || device.TokenHash == "" {
			continue
		}
		r.devices[device.ID] = device
		r.byHash[device.TokenHash] = device.ID
	}
	for kind, at := range file.SharedLastUsed {
		r.shared[kind] = at
	}
	return r, nil
}

func (r *AccessDevices) moveAside(cause error) error {
	if err := os.Rename(r.path, r.path+".bad"); err != nil {
		r.writeErr = fmt.Errorf("%w; moving it aside failed: %w", cause, err)
		return r.writeErr
	}
	return cause
}

// Issue registers a new device and returns it with its token.
func (r *AccessDevices) Issue(name, platform string, now time.Time) (AccessDevice, string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return AccessDevice{}, "", err
	}
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return AccessDevice{}, "", err
	}
	token := DeviceTokenPrefix + hex.EncodeToString(secret)
	device := AccessDevice{
		ID:         "dev_" + hex.EncodeToString(id),
		Name:       cleanLabel(name, 64),
		Platform:   cleanLabel(strings.ToLower(platform), 32),
		TokenHash:  HashPassword(token),
		CreatedAt:  now.UTC(),
		LastSeenAt: now.UTC(),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.devices) >= maxAccessDevices {
		return AccessDevice{}, "", ErrTooManyDevices
	}
	r.devices[device.ID] = device
	r.byHash[device.TokenHash] = device.ID
	if err := r.persistLocked(); err != nil {
		delete(r.devices, device.ID)
		delete(r.byHash, device.TokenHash)
		return AccessDevice{}, "", err
	}
	return device, token, nil
}

// Match returns the device a token belongs to.
func (r *AccessDevices) Match(token string) (AccessDevice, bool) {
	if !strings.HasPrefix(token, DeviceTokenPrefix) {
		return AccessDevice{}, false
	}
	hash := HashPassword(token)
	r.mu.RLock()
	defer r.mu.RUnlock()
	device, ok := r.devices[r.byHash[hash]]
	return device, ok
}

// Touch updates the device's last-seen time.
func (r *AccessDevices) Touch(id string, now time.Time) {
	r.mu.RLock()
	device, ok := r.devices[id]
	r.mu.RUnlock()
	if !ok || now.Sub(device.LastSeenAt) < touchInterval {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if device, ok = r.devices[id]; !ok {
		return
	}
	device.LastSeenAt = now.UTC()
	r.devices[id] = device
	_ = r.persistLocked()
}

// TouchShared updates a shared credential's last-used time.
func (r *AccessDevices) TouchShared(kind string, now time.Time) {
	r.mu.RLock()
	last := r.shared[kind]
	r.mu.RUnlock()
	if now.Sub(last) < touchInterval {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shared[kind] = now.UTC()
	_ = r.persistLocked()
}

// SharedLastUsed returns when a shared credential last authenticated a request.
func (r *AccessDevices) SharedLastUsed(kind string) time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.shared[kind]
}

// LinkInstall records the push-registry row of the phone holding this token.
func (r *AccessDevices) LinkInstall(id, installID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	device, ok := r.devices[id]
	if !ok || device.InstallID == installID {
		return nil
	}
	previous := device.InstallID
	device.InstallID = installID
	r.devices[id] = device
	if err := r.persistLocked(); err != nil {
		device.InstallID = previous
		r.devices[id] = device
		return err
	}
	return nil
}

// Revoke removes a device and reports whether it existed.
func (r *AccessDevices) Revoke(id string) (AccessDevice, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	device, ok := r.devices[id]
	if !ok {
		return AccessDevice{}, false, nil
	}
	delete(r.devices, id)
	delete(r.byHash, device.TokenHash)
	if err := r.persistLocked(); err != nil {
		r.devices[id] = device
		r.byHash[device.TokenHash] = id
		return AccessDevice{}, true, err
	}
	return device, true, nil
}

// RevokeAll removes every device.
func (r *AccessDevices) RevokeAll() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.devices) == 0 {
		return nil
	}
	devices, byHash := r.devices, r.byHash
	r.devices, r.byHash = map[string]AccessDevice{}, map[string]string{}
	if err := r.persistLocked(); err != nil {
		r.devices, r.byHash = devices, byHash
		return err
	}
	return nil
}

// List returns the devices, oldest first.
func (r *AccessDevices) List() []AccessDevice {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AccessDevice, 0, len(r.devices))
	for _, device := range r.devices {
		out = append(out, device)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (r *AccessDevices) persistLocked() error {
	if r.writeErr != nil {
		return r.writeErr
	}
	file := accessDevicesFile{Devices: make([]AccessDevice, 0, len(r.devices)), SharedLastUsed: r.shared}
	for _, device := range r.devices {
		file.Devices = append(file.Devices, device)
	}
	sort.Slice(file.Devices, func(i, j int) bool { return file.Devices[i].ID < file.Devices[j].ID })
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(r.path, raw)
}

func cleanLabel(value string, limit int) string {
	value = strings.TrimSpace(strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, value))
	runes := []rune(value)
	for len(string(runes)) > limit {
		runes = runes[:len(runes)-1]
	}
	return strings.TrimSpace(string(runes))
}
