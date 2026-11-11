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

// AccessDevice is one device that paired with this machine and holds its own
// token. Only the token's SHA-256 is stored.
type AccessDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Platform  string `json:"platform,omitempty"`
	TokenHash string `json:"tokenHash"`
	// InstallID is the phone's row in the push-device registry, recorded when
	// the device registers for push with this token.
	InstallID  string    `json:"installId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

// The credentials several devices share, as keys of the last-used times.
const (
	SharedPassword = "password"
	SharedAccount  = "account"
)

// DeviceTokenPrefix marks a device token, so it is never mistaken for an
// account token (64 hex characters) or a pairing password.
const DeviceTokenPrefix = "aod_"

// maxAccessDevices bounds the registry: a holder of the pairing password can
// exchange it for device tokens without limit otherwise.
const maxAccessDevices = 64

// touchInterval is how stale a stored last-seen time may be before a request
// rewrites the file.
const touchInterval = 5 * time.Minute

// ErrTooManyDevices means the registry is full; revoke a device to pair another.
var ErrTooManyDevices = errors.New("too many paired devices; revoke one first")

type accessDevicesFile struct {
	Devices []AccessDevice `json:"devices"`
	// SharedLastUsed is when the pairing password and the account token last
	// authenticated a request.
	SharedLastUsed map[string]time.Time `json:"sharedLastUsed,omitempty"`
}

// AccessDevicesPath is the registry's location beside the Connect Mobile
// config (~/.ao/mobile/access-devices.json).
func AccessDevicesPath(dataDir string) string {
	return filepath.Join(dataDir, "mobile", "access-devices.json")
}

// AccessDevices is the registry of device tokens the remote listener accepts.
type AccessDevices struct {
	mu   sync.RWMutex
	path string
	// writeErr is set when a damaged or unreadable file could not be moved
	// aside. Every write then fails, so none can replace the file.
	writeErr error
	devices  map[string]AccessDevice
	byHash   map[string]string
	shared   map[string]time.Time
}

// LoadAccessDevices reads the registry. A missing file is an empty registry.
// A damaged or unreadable one is moved aside, so the next write cannot replace
// it, and reported. The returned registry is empty and usable: every device
// then pairs again.
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

// moveAside renames the registry file to .bad. If that fails, the registry
// refuses every write.
func (r *AccessDevices) moveAside(cause error) error {
	if err := os.Rename(r.path, r.path+".bad"); err != nil {
		r.writeErr = fmt.Errorf("%w; moving it aside failed: %w", cause, err)
		return r.writeErr
	}
	return cause
}

// Issue registers a new device and returns it with its token, which is shown
// to the caller once and never stored.
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

// Touch records that the device just made a request. The file is rewritten
// only when the stored time is older than touchInterval.
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

// TouchShared records that a shared credential just authenticated a request,
// with the same write limit as Touch.
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

// SharedLastUsed is when a shared credential last authenticated a request, or
// the zero time.
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

// Revoke removes a device, so its token stops authenticating. It reports
// whether the device existed.
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

// cleanLabel trims a client-supplied name to printable text of at most limit
// bytes.
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
