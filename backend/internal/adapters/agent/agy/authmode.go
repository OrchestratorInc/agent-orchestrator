package agy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// adcAuthEnv selects Agy's Application Default Credentials sign-in. Agy reads
// it from its process environment at startup; its own logout guidance is
// "run `unset AGY_ADC_AUTH` ... and restart the CLI".
const adcAuthEnv = "AGY_ADC_AUTH"

// AuthMode is the Agy sign-in method the user picked in AO's login menu. It is
// AO-owned state: Agy keeps no persistent record of an ADC choice, so AO
// stores the selection under its data dir and applies it to every Agy launch.
type AuthMode string

const (
	// AuthModeNone means the user has not picked a method through AO; Agy
	// launches with whatever environment the daemon inherited.
	AuthModeNone AuthMode = ""
	// AuthModeGoogle uses Agy's saved Google account sign-in (OS keyring).
	AuthModeGoogle AuthMode = "google"
	// AuthModeADC uses Google Cloud Application Default Credentials.
	AuthModeADC AuthMode = "adc"
)

func authModePath(dataDir string) string {
	return filepath.Join(dataDir, "agy", "auth-mode")
}

// WriteAuthMode records the sign-in method chosen in AO's Agy login menu.
func WriteAuthMode(dataDir string, mode AuthMode) error {
	if strings.TrimSpace(dataDir) == "" {
		return errors.New("agy auth mode: data dir is required")
	}
	switch mode {
	case AuthModeGoogle, AuthModeADC:
	default:
		return fmt.Errorf("agy auth mode: unknown mode %q", mode)
	}
	path := authModePath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("agy auth mode: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(string(mode)+"\n"), 0o600); err != nil {
		return fmt.Errorf("agy auth mode: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("agy auth mode: %w", err)
	}
	return nil
}

// ReadAuthMode returns the recorded sign-in method, or AuthModeNone when none
// was recorded or the record is unreadable.
func ReadAuthMode(dataDir string) AuthMode {
	if strings.TrimSpace(dataDir) == "" {
		return AuthModeNone
	}
	raw, err := os.ReadFile(authModePath(dataDir))
	if err != nil {
		return AuthModeNone
	}
	switch mode := AuthMode(strings.TrimSpace(string(raw))); mode {
	case AuthModeGoogle, AuthModeADC:
		return mode
	default:
		return AuthModeNone
	}
}

// ApplyAuthModeEnv makes the sign-in method recorded under dataDir
// authoritative for an Agy launch env (worker sessions and reviewers alike).
func ApplyAuthModeEnv(env map[string]string, dataDir string) {
	if env == nil {
		return
	}
	applyAuthModeEnv(env, ReadAuthMode(dataDir))
}

// applyAuthModeEnv makes the recorded choice authoritative for an Agy launch.
// The runtime env is an overlay on the daemon's inherited environment, so the
// Google choice sets AGY_ADC_AUTH to empty (unset for Agy) rather than
// deleting the key, which would let an inherited value leak through.
func applyAuthModeEnv(env map[string]string, mode AuthMode) {
	switch mode {
	case AuthModeADC:
		env[adcAuthEnv] = "true"
	case AuthModeGoogle:
		env[adcAuthEnv] = ""
	}
}

// defaultDataDir mirrors the daemon's data-dir resolution (AO_DATA_DIR, else
// ~/.ao/data) for probes such as AuthStatus that do not receive the daemon's
// resolved data dir.
func defaultDataDir() string {
	if raw := strings.TrimSpace(os.Getenv("AO_DATA_DIR")); raw != "" {
		if abs, err := filepath.Abs(raw); err == nil {
			return abs
		}
		return raw
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ao", "data")
}
