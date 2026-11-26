package agy

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

const (
	// Agy saves its Google account sign-in in the OS keyring under this
	// service/account pair (observed via `security dump-keychain` metadata).
	keychainService = "gemini"
	keychainAccount = "antigravity"
	// macOSSecurityExitItemNotFound is `security`'s errSecItemNotFound exit.
	macOSSecurityExitItemNotFound = 44
	keychainProbeTimeout          = 3 * time.Second
)

// Test seams for the local auth probe.
var (
	authGetenv         = os.Getenv
	authUserHomeDir    = os.UserHomeDir
	authGOOS           = runtime.GOOS
	authDataDir        = defaultDataDir
	keychainItemExists = macOSKeychainItemExists
)

// AuthStatus returns the plugin's local authentication status. Agy has no
// non-interactive status command, so the probe reads the evidence each sign-in
// method leaves behind:
//
//   - ADC (chosen in AO's login menu, or AGY_ADC_AUTH inherited by the
//     daemon): authorized when Application Default Credentials exist,
//     unauthorized when they do not.
//   - Google account: authorized when Agy's keyring entry exists. A missing
//     entry stays unknown because Agy falls back to undocumented file-based
//     token storage when the keyring is locked or unavailable, and an
//     unauthorized verdict would block agent switching and reviewer launches.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if _, err := p.ResolveBinary(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	return localAuthStatus(ctx)
}

func localAuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if adcSelected(ReadAuthMode(authDataDir())) {
		if adcCredentialsConfigured() {
			return ports.AgentAuthStatusAuthorized, nil
		}
		return ports.AgentAuthStatusUnauthorized, nil
	}
	if authGOOS == "darwin" {
		found, err := keychainItemExists(ctx, keychainService, keychainAccount)
		if err != nil && ctx.Err() != nil {
			return ports.AgentAuthStatusUnknown, ctx.Err()
		}
		if err == nil && found {
			return ports.AgentAuthStatusAuthorized, nil
		}
	}
	return ports.AgentAuthStatusUnknown, nil
}

// adcSelected reports whether Agy will launch in ADC mode: AO's recorded
// choice wins because AugmentRuntimeEnv applies it to every launch; without
// one, the daemon's inherited AGY_ADC_AUTH decides.
func adcSelected(mode AuthMode) bool {
	switch mode {
	case AuthModeADC:
		return true
	case AuthModeGoogle:
		return false
	default:
		return strings.TrimSpace(authGetenv(adcAuthEnv)) != ""
	}
}

// adcCredentialsConfigured reports whether Google Application Default
// Credentials are available, following the standard lookup order:
// GOOGLE_APPLICATION_CREDENTIALS, then gcloud's well-known file.
func adcCredentialsConfigured() bool {
	if path := strings.TrimSpace(authGetenv("GOOGLE_APPLICATION_CREDENTIALS")); path != "" {
		return nonEmptyRegularFile(path)
	}
	path := gcloudADCPath()
	return path != "" && nonEmptyRegularFile(path)
}

func gcloudADCPath() string {
	const name = "application_default_credentials.json"
	if configDir := strings.TrimSpace(authGetenv("CLOUDSDK_CONFIG")); configDir != "" {
		return filepath.Join(configDir, name)
	}
	if authGOOS == "windows" {
		if appData := strings.TrimSpace(authGetenv("APPDATA")); appData != "" {
			return filepath.Join(appData, "gcloud", name)
		}
		return ""
	}
	home, err := authUserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "gcloud", name)
}

func nonEmptyRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// macOSKeychainItemExists checks for a generic-password item by attributes
// only. Without -g/-w, `security` never reads the secret, so the lookup does
// not trigger a Keychain access prompt.
func macOSKeychainItemExists(ctx context.Context, service, account string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, keychainProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", service, "-a", account) //nolint:gosec // fixed argv; no user input.
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == macOSSecurityExitItemNotFound {
		return false, nil
	}
	return false, err
}
