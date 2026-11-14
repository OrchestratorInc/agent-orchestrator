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
	HostName           = "dev.ao.browser_bridge"
	Transport          = "chrome-extension"
	Client             = "ao-chrome-extension"
	HostProtocol       = 1
	ExtensionPublicKey = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAsxXchrJiAI4KtL7ACga37qexOAN/fx5a+Txw3TTmdxj49EXbC3eu2s+oSWTNZ0gffYBAGt+Pxosx1nk8SachWxmluPZmqmhuo8DUWS6aBVMjTFrLmCS1El9LMlCO6clwm6v/xJIri1sn4ZGEFIoUuEYcWNG/xF6/7MMCqNUJ/apIqJjPGdfUl61CpuT0su0PS854383aZRzCpULZCI1v69FBvskcJrPkjj7VVgi428tPttCgai2qz5jwOVhXJg+WdnZRtjAkla1Xs+mj4qvBlzUd3i7/AQkDio2Z8ZGWgqyo89In+nvmT4au8lra0bKelS+uOjgkA/hbmzwRfmbR2QIDAQAB"
)

var ExtensionID = mustExtensionID(ExtensionPublicKey)

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

func AllowedOrigin() string {
	return "chrome-extension://" + ExtensionID + "/"
}

type Paths struct {
	Root string
}

func NewPaths(dataDir string) Paths {
	return Paths{Root: filepath.Join(dataDir, "browser-chrome")}
}

func (p Paths) Endpoint() string { return filepath.Join(p.Root, "endpoint.json") }

func (p Paths) Extension() string { return filepath.Join(p.Root, "extension") }

func (p Paths) HostManifestCopy() string { return filepath.Join(p.Root, HostName+".json") }

type Endpoint struct {
	Address  string `json:"address"`
	Token    string `json:"token"`
	PID      int    `json:"pid"`
	Protocol int    `json:"protocol"`
}

func WriteEndpoint(path string, endpoint Endpoint) error {
	data, err := json.Marshal(endpoint)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o600)
}

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
