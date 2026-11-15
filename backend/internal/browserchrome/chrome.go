// Package browserchrome connects AO's browser commands to the user's own
// Google Chrome through the AO Browser Bridge extension.
//
// The extension talks to Chrome Native Messaging host "dev.ao.browser_bridge",
// which is the `ao browser-host` subcommand started by Chrome. The host relays
// frames between the extension and a dedicated daemon socket served by a
// browserruntime.Broker, so the daemon dispatches commands exactly as it does
// for the Electron Browser panel.
//
// Chrome only discovers native messaging hosts through a manifest file in its
// own per-user directory (or the Windows registry). That manifest is the one
// file AO writes outside ~/.ao: it is a pointer to the host wrapper under the
// AO data directory, written only when the user enables Chrome mode and
// removed when they disable it. Everything else stays under the data directory.
package browserchrome

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// HostName is the Chrome Native Messaging host name.
	HostName = "dev.ao.browser_bridge"
	// Transport names the Chrome runtime in browser status responses.
	Transport = "chrome-extension"
	// Client is the hello client name the host reports for the extension.
	Client = "ao-chrome-extension"
	// HostProtocol versions the extension-to-host frames.
	HostProtocol = 1
	// ExtensionPublicKey is the "key" in the extension manifest. It pins the
	// extension ID for unpacked installs; Chrome derives the ID from it.
	ExtensionPublicKey = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAsxXchrJiAI4KtL7ACga37qexOAN/fx5a+Txw3TTmdxj49EXbC3eu2s+oSWTNZ0gffYBAGt+Pxosx1nk8SachWxmluPZmqmhuo8DUWS6aBVMjTFrLmCS1El9LMlCO6clwm6v/xJIri1sn4ZGEFIoUuEYcWNG/xF6/7MMCqNUJ/apIqJjPGdfUl61CpuT0su0PS854383aZRzCpULZCI1v69FBvskcJrPkjj7VVgi428tPttCgai2qz5jwOVhXJg+WdnZRtjAkla1Xs+mj4qvBlzUd3i7/AQkDio2Z8ZGWgqyo89In+nvmT4au8lra0bKelS+uOjgkA/hbmzwRfmbR2QIDAQAB"
)

// ExtensionID is the Chrome extension ID derived from ExtensionPublicKey.
var ExtensionID = mustExtensionID(ExtensionPublicKey)

// ExtensionIDFromKey derives a Chrome extension ID from a base64 DER public
// key: the first 16 bytes of its SHA-256, each hex digit mapped to a-p.
func ExtensionIDFromKey(key string) (string, error) {
	der, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return "", fmt.Errorf("decode extension key: %w", err)
	}
	sum := sha256.Sum256(der)
	digits := hex.EncodeToString(sum[:16])
	id := make([]byte, len(digits))
	for i := range len(digits) {
		c := digits[i]
		if c >= '0' && c <= '9' {
			id[i] = 'a' + (c - '0')
		} else {
			id[i] = 'a' + 10 + (c - 'a')
		}
	}
	return string(id), nil
}

func mustExtensionID(key string) string {
	id, err := ExtensionIDFromKey(key)
	if err != nil {
		panic(err)
	}
	return id
}

// AllowedOrigin is the extension origin Chrome checks against the manifest.
func AllowedOrigin() string {
	return "chrome-extension://" + ExtensionID + "/"
}

// Paths are the Chrome-mode files under the AO data directory.
type Paths struct {
	Root string
}

// NewPaths returns the Chrome-mode paths for an AO data directory.
func NewPaths(dataDir string) Paths {
	return Paths{Root: filepath.Join(dataDir, "browser-chrome")}
}

// Endpoint is the daemon socket description the host reads.
func (p Paths) Endpoint() string { return filepath.Join(p.Root, "endpoint.json") }

// Extension is where the unpacked extension is installed for Chrome to load.
func (p Paths) Extension() string { return filepath.Join(p.Root, "extension") }

// HostManifestCopy is the manifest file kept under ~/.ao (Windows registers
// this path; macOS and Linux keep it for status and repair).
func (p Paths) HostManifestCopy() string { return filepath.Join(p.Root, HostName+".json") }

// Endpoint tells the host how to reach the daemon's Chrome runtime socket.
type Endpoint struct {
	Address  string `json:"address"`
	Token    string `json:"token"`
	PID      int    `json:"pid"`
	Protocol int    `json:"protocol"`
}

// WriteEndpoint atomically writes the endpoint file with owner-only access.
func WriteEndpoint(path string, endpoint Endpoint) error {
	data, err := json.Marshal(endpoint)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o600)
}

// ReadEndpoint reads the endpoint file written by the daemon.
func ReadEndpoint(path string) (Endpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Endpoint{}, fmt.Errorf("read browser endpoint: %w", err)
	}
	var endpoint Endpoint
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return Endpoint{}, fmt.Errorf("parse browser endpoint: %w", err)
	}
	if endpoint.Address == "" || endpoint.Token == "" {
		return Endpoint{}, errors.New("browser endpoint is incomplete")
	}
	return endpoint, nil
}

// RemoveEndpoint deletes the endpoint file if it still belongs to pid.
func RemoveEndpoint(path string, pid int) {
	endpoint, err := ReadEndpoint(path)
	if err == nil && endpoint.PID != pid {
		return
	}
	_ = os.Remove(path)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return cleanup(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func sameFile(a, b string) bool {
	left, err := os.Stat(a)
	if err != nil {
		return false
	}
	right, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(left, right)
}

func quoteShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
