package agy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type authProbeFixture struct {
	env          map[string]string
	home         string
	dataDir      string
	goos         string
	keychainHit  bool
	keychainErr  error
	keychainHits int
}

// install replaces the package-level probe seams for one test. Tests using it
// must not run in parallel.
func (f *authProbeFixture) install(t *testing.T) {
	t.Helper()
	prevGetenv, prevHome, prevGOOS, prevDataDir, prevKeychain := authGetenv, authUserHomeDir, authGOOS, authDataDir, keychainItemExists
	t.Cleanup(func() {
		authGetenv, authUserHomeDir, authGOOS, authDataDir, keychainItemExists = prevGetenv, prevHome, prevGOOS, prevDataDir, prevKeychain
	})
	authGetenv = func(key string) string { return f.env[key] }
	authUserHomeDir = func() (string, error) { return f.home, nil }
	authGOOS = f.goos
	authDataDir = func() string { return f.dataDir }
	keychainItemExists = func(_ context.Context, service, account string) (bool, error) {
		f.keychainHits++
		if service != keychainService || account != keychainAccount {
			t.Errorf("keychain lookup = (%q, %q), want (%q, %q)", service, account, keychainService, keychainAccount)
		}
		return f.keychainHit, f.keychainErr
	}
}

func newAuthProbeFixture(t *testing.T) *authProbeFixture {
	t.Helper()
	return &authProbeFixture{env: map[string]string{}, home: t.TempDir(), dataDir: t.TempDir(), goos: "darwin"}
}

func writeGcloudADC(t *testing.T, home string) {
	t.Helper()
	path := filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"authorized_user"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func authStatus(t *testing.T) ports.AgentAuthStatus {
	t.Helper()
	status, err := (&Plugin{resolvedBinary: "agy"}).AuthStatus(context.Background())
	if err != nil {
		t.Fatalf("AuthStatus: %v", err)
	}
	return status
}

func TestAuthStatusAuthorizedWhenGoogleSignInIsInKeychain(t *testing.T) {
	f := newAuthProbeFixture(t)
	f.keychainHit = true
	f.install(t)

	if got := authStatus(t); got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want authorized", got)
	}
}

func TestAuthStatusUnknownWhenKeychainEntryMissing(t *testing.T) {
	// Agy may hold its token in file-based storage when the keyring is locked,
	// so a missing entry must not block launches as unauthorized.
	f := newAuthProbeFixture(t)
	f.install(t)

	if got := authStatus(t); got != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want unknown", got)
	}
}

func TestAuthStatusUnknownWhenKeychainProbeFails(t *testing.T) {
	f := newAuthProbeFixture(t)
	f.keychainErr = errors.New("security: timed out")
	f.install(t)

	if got := authStatus(t); got != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want unknown", got)
	}
}

func TestAuthStatusSkipsKeychainOffMacOS(t *testing.T) {
	f := newAuthProbeFixture(t)
	f.goos = "linux"
	f.keychainHit = true
	f.install(t)

	if got := authStatus(t); got != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want unknown", got)
	}
	if f.keychainHits != 0 {
		t.Fatalf("keychain probed %d times off macOS", f.keychainHits)
	}
}

func TestAuthStatusADCModeAuthorizedWithGcloudCredentials(t *testing.T) {
	f := newAuthProbeFixture(t)
	writeGcloudADC(t, f.home)
	if err := WriteAuthMode(f.dataDir, AuthModeADC); err != nil {
		t.Fatal(err)
	}
	f.install(t)

	if got := authStatus(t); got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want authorized", got)
	}
	if f.keychainHits != 0 {
		t.Fatal("ADC mode must not consult the Google account keychain entry")
	}
}

func TestAuthStatusADCModeUnauthorizedWithoutCredentials(t *testing.T) {
	f := newAuthProbeFixture(t)
	f.keychainHit = true // a Google sign-in does not satisfy ADC mode
	if err := WriteAuthMode(f.dataDir, AuthModeADC); err != nil {
		t.Fatal(err)
	}
	f.install(t)

	if got := authStatus(t); got != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("status = %q, want unauthorized", got)
	}
}

func TestAuthStatusADCHonorsGoogleApplicationCredentials(t *testing.T) {
	f := newAuthProbeFixture(t)
	keyFile := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(keyFile, []byte(`{"type":"service_account"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.env[adcAuthEnv] = "true" // inherited by the daemon, no AO-recorded mode
	f.env["GOOGLE_APPLICATION_CREDENTIALS"] = keyFile
	f.install(t)

	if got := authStatus(t); got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want authorized", got)
	}

	f.env["GOOGLE_APPLICATION_CREDENTIALS"] = filepath.Join(t.TempDir(), "missing.json")
	if got := authStatus(t); got != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("status with missing credentials file = %q, want unauthorized", got)
	}
}

func TestAuthStatusGoogleModeIgnoresInheritedADCEnv(t *testing.T) {
	f := newAuthProbeFixture(t)
	f.env[adcAuthEnv] = "true"
	f.keychainHit = true
	if err := WriteAuthMode(f.dataDir, AuthModeGoogle); err != nil {
		t.Fatal(err)
	}
	f.install(t)

	if got := authStatus(t); got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want authorized from Google keychain sign-in", got)
	}
}
