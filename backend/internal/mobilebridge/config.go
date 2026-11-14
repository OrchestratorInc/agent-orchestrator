package mobilebridge

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const DefaultPort = 3011

type State struct {
	Enabled                  bool   `json:"enabled"`
	Password                 string `json:"password"`
	LastPort                 int    `json:"lastPort"`
	RetiredPasswordHash      string `json:"retiredPasswordHash,omitempty"`
	AccountTokenHash         string `json:"accountTokenHash,omitempty"`
	AccountToken             string `json:"accountToken,omitempty"`
	AccountTokenOwner        string `json:"accountTokenOwner,omitempty"`
	RequireDeviceCredentials bool   `json:"requireDeviceCredentials,omitempty"`
	NoPublicTunnel           bool   `json:"noPublicTunnel"`
	LoopbackOnly             bool   `json:"loopbackOnly,omitempty"`
	SecurePairing            bool   `json:"securePairing"`
	ServeCleanupPending      bool   `json:"serveCleanupPending,omitempty"`
	KeepAwake                bool   `json:"keepAwake"`
	KeepAwakeLid             bool   `json:"keepAwakeLid,omitempty"`
	BrowserRemoteAccess      bool   `json:"browserRemoteAccess"`
}

func Path(dataDir string) string { return filepath.Join(dataDir, "mobile", "config.json") }

func Load(path string) (State, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read mobile config: %w", err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, fmt.Errorf("parse mobile config: %w", err)
	}
	return s, nil
}

func Save(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir mobile dir: %w", err)
	}
	//nolint:gosec // G117: persisting the rotating LAN password is the deliberate, documented purpose of State (see its doc comment).
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, b)
}

func writeFileAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

const pwAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func GeneratePassword() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = pwAlphabet[int(b)%len(pwAlphabet)]
	}
	return string(buf), nil
}

func HashPassword(pw string) string {
	sum := sha256.Sum256([]byte(pw))
	return hex.EncodeToString(sum[:])
}

func PasswordMatches(hash, pw string) bool {
	return subtle.ConstantTimeCompare([]byte(hash), []byte(HashPassword(pw))) == 1
}
