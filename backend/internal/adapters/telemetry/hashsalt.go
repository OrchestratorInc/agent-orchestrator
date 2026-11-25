package telemetry

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// hashSaltFile holds this install's private key for hashed telemetry IDs
// (project_id_hash, session_id_hash). It is created once by whichever of the
// daemon or the desktop app gets there first and is never exported: the
// install ID rides every event, so salting with it would leave a project ID
// that is just a lowercased folder name reversible by hashing a list of repo
// names. The desktop app reads and creates the same file
// (frontend/src/shared/telemetry.ts).
const hashSaltFile = "telemetry_hash_salt"

var hashSaltPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// loadOrCreateHashSalt returns the install's hash key, creating it if absent.
// ok is false when it cannot be read or stored; hashed IDs are then omitted
// rather than sent unsalted.
func loadOrCreateHashSalt(dataDir string) (string, bool) {
	if dataDir == "" {
		return "", false
	}
	file := filepath.Join(dataDir, hashSaltFile)
	for attempt := 0; attempt < 3; attempt++ {
		raw, err := os.ReadFile(file)
		if err == nil {
			salt := strings.TrimSpace(string(raw))
			return salt, hashSaltPattern.MatchString(salt)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", false
		}
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return "", false
		}
		salt := hex.EncodeToString(buf)
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return "", false
		}
		// A complete temp file linked into place: the link fails if the desktop
		// won the race, and a reader never sees a half-written key.
		tmp, err := os.CreateTemp(dataDir, ".telemetry_hash_salt_*")
		if err != nil {
			return "", false
		}
		_, writeErr := tmp.WriteString(salt + "\n")
		closeErr := tmp.Close()
		if writeErr == nil && closeErr == nil {
			err = os.Link(tmp.Name(), file)
		}
		_ = os.Remove(tmp.Name())
		if writeErr != nil || closeErr != nil {
			return "", false
		}
		if err == nil {
			return salt, true
		}
		if !errors.Is(err, os.ErrExist) {
			return "", false
		}
	}
	return "", false
}

// hashTelemetryID is HMAC-SHA256(salt, value) in hex. The desktop computes the
// same value, so a project hashes identically on renderer and daemon events.
func hashTelemetryID(salt, value string) string {
	mac := hmac.New(sha256.New, []byte(salt))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
