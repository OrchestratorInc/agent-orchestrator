package agy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

type authProbeFixture struct {
	env          map[string]string
	home         string
	goos         string
	keychainHit  bool
	keychainErr  error
	keychainHits int
}

// install replaces the package-level probe seams for one test. Tests using it
// must not run in parallel.
func (f *authProbeFixture) install(t *testing.T) {
	t.Helper()
	prevGetenv, prevHome, prevGOOS, prevKeychain := authGetenv, authUserHomeDir, authGOOS, keychainItemExists
	t.Cleanup(func() {
		authGetenv, authUserHomeDir, authGOOS, keychainItemExists = prevGetenv, prevHome, prevGOOS, prevKeychain
	})
	authGetenv = func(key string) string { return f.env[key] }
	authUserHomeDir = func() (string, error) { return f.home, nil }
	authGOOS = f.goos
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
	return &authProbeFixture{env: map[string]string{}, home: t.TempDir(), goos: "darwin"}
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

func TestAuthStatusOnWindowsReadsCredentialManagerEntry(t *testing.T) {
	for _, tc := range []struct {
		name string
		hit  bool
		want ports.AgentAuthStatus
	}{
		{"found", true, ports.AgentAuthStatusAuthorized},
		{"missing", false, ports.AgentAuthStatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthProbeFixture(t)
			f.goos = "windows"
			f.keychainHit = tc.hit
			f.install(t)

			if got := authStatus(t); got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
			if f.keychainHits != 1 {
				t.Fatalf("credential store probed %d times, want 1", f.keychainHits)
			}
		})
	}
}

func TestCmdkeyListsTarget(t *testing.T) {
	const target = "gemini:antigravity"
	for _, tc := range []struct {
		name string
		out  string
		want bool
	}{
		{"filtered hit", "Currently stored credentials for gemini:antigravity:\r\n\r\n    Target: gemini:antigravity\r\n    Type: Generic \r\n    User: antigravity\r\n    Local machine persistence\r\n", true},
		{"unfiltered hit", "Currently stored credentials:\r\n\r\n    Target: LegacyGeneric:target=gemini:antigravity\r\n    Type: Generic \r\n    User: antigravity\r\n", true},
		{"localized labels", "Derzeit gespeicherte Anmeldeinformationen für gemini:antigravity:\r\n\r\n    Ziel: gemini:antigravity\r\n    Typ: Generisch\r\n", true},
		{"none", "\r\nCurrently stored credentials for gemini:antigravity:\r\n\r\n* NONE *\r\n", false},
		{"other target", "Currently stored credentials for gemini:anti*:\r\n\r\n    Target: gemini:antigravity-old\r\n    User: antigravity\r\n", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cmdkeyListsTarget(tc.out, target); got != tc.want {
				t.Fatalf("cmdkeyListsTarget = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAuthStatusKeepsInstalledMeansAuthorizedOnLinux(t *testing.T) {
	f := newAuthProbeFixture(t)
	f.goos = "linux"
	f.install(t)

	if got := authStatus(t); got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want authorized", got)
	}
	if f.keychainHits != 0 {
		t.Fatalf("keychain probed %d times on linux", f.keychainHits)
	}
}

func TestAuthStatusADCModeAuthorizedWithGcloudCredentials(t *testing.T) {
	f := newAuthProbeFixture(t)
	writeGcloudADC(t, f.home)
	f.env[adcAuthEnv] = "true"
	f.install(t)

	if got := authStatus(t); got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want authorized", got)
	}
	if f.keychainHits != 0 {
		t.Fatal("ADC mode must not consult the Google account keychain entry")
	}
}

func TestAuthStatusADCModeUnknownWithoutCredentialsFile(t *testing.T) {
	// ADC can still come from a Google Cloud host's metadata server, so a
	// missing file must not block launches as unauthorized.
	f := newAuthProbeFixture(t)
	f.keychainHit = true // a Google sign-in does not satisfy ADC mode
	f.env[adcAuthEnv] = "true"
	f.install(t)

	if got := authStatus(t); got != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want unknown", got)
	}
	if f.keychainHits != 0 {
		t.Fatal("ADC mode must not consult the Google account keychain entry")
	}
}

func TestAuthStatusADCHonorsGoogleApplicationCredentials(t *testing.T) {
	f := newAuthProbeFixture(t)
	keyFile := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(keyFile, []byte(`{"type":"service_account"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.env[adcAuthEnv] = "true"
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
