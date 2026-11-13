package zcode

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const testSecret = "test-secret"

// sealCredential encrypts value with the same envelope the CLI uses, under the
// test secret, so fixtures exercise the real decrypt path.
func sealCredential(t *testing.T, value string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(testSecret))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	iv := make([]byte, gcm.NonceSize())
	sealed := gcm.Seal(nil, iv, []byte(value), nil)
	overhead := gcm.Overhead()
	body, tag := sealed[:len(sealed)-overhead], sealed[len(sealed)-overhead:]
	return "enc:v1:" +
		base64.RawURLEncoding.EncodeToString(iv) + "." +
		base64.RawURLEncoding.EncodeToString(tag) + "." +
		base64.RawURLEncoding.EncodeToString(body)
}

// jwtWithClaims builds an unsigned JWT whose payload carries the given claims.
func jwtWithClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// isolateAODataDir points the probe's AO-owned provider config lookup at an
// empty data dir, so a developer's real ~/.ao/data provider config can never
// turn an OAuth-branch case into an API-key hit.
func isolateAODataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dir)
	return dir
}

// writeStore points the auth probe at a fresh store directory holding entries.
func writeStore(t *testing.T, entries map[string]string) {
	t.Helper()
	isolateAODataDir(t)
	dir := t.TempDir()
	path := filepath.Join(dir, ".zcode", "v2", "credentials.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(zcodeDataBaseDirEnv, dir)
	t.Setenv(zcodeCredentialSecretEnv, testSecret)
}

func TestAuthStatus(t *testing.T) {
	now := time.Now()
	valid := jwtWithClaims(t, map[string]any{"exp": now.Add(30 * time.Minute).Unix(), "sub": "u"})
	expired := jwtWithClaims(t, map[string]any{"exp": now.Add(-time.Hour).Unix(), "sub": "u"})
	noExpiry := jwtWithClaims(t, map[string]any{"sub": "u"})

	t.Run("missing store is unauthorized", func(t *testing.T) {
		isolateAODataDir(t)
		t.Setenv(zcodeDataBaseDirEnv, t.TempDir())
		t.Setenv(zcodeCredentialSecretEnv, testSecret)
		status, err := New().AuthStatus(context.Background())
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if status != ports.AgentAuthStatusUnauthorized {
			t.Fatalf("status = %q, want unauthorized", status)
		}
	})

	t.Run("missing token is unauthorized", func(t *testing.T) {
		writeStore(t, map[string]string{"zcodejwttoken": "other"})
		status, err := New().AuthStatus(context.Background())
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if status != ports.AgentAuthStatusUnauthorized {
			t.Fatalf("status = %q, want unauthorized", status)
		}
	})

	t.Run("expired token is unauthorized", func(t *testing.T) {
		writeStore(t, map[string]string{zcodeAccessTokenKey: sealCredential(t, expired)})
		status, err := New().AuthStatus(context.Background())
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if status != ports.AgentAuthStatusUnauthorized {
			t.Fatalf("status = %q, want unauthorized", status)
		}
	})

	t.Run("valid token is authorized", func(t *testing.T) {
		writeStore(t, map[string]string{zcodeAccessTokenKey: sealCredential(t, valid)})
		status, err := New().AuthStatus(context.Background())
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if status != ports.AgentAuthStatusAuthorized {
			t.Fatalf("status = %q, want authorized", status)
		}
	})

	t.Run("plaintext token without envelope is authorized", func(t *testing.T) {
		writeStore(t, map[string]string{zcodeAccessTokenKey: valid})
		status, err := New().AuthStatus(context.Background())
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if status != ports.AgentAuthStatusAuthorized {
			t.Fatalf("status = %q, want authorized", status)
		}
	})

	t.Run("token without exp claim is configured", func(t *testing.T) {
		writeStore(t, map[string]string{zcodeAccessTokenKey: sealCredential(t, noExpiry)})
		status, err := New().AuthStatus(context.Background())
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if status != ports.AgentAuthStatusConfigured {
			t.Fatalf("status = %q, want configured", status)
		}
	})

	t.Run("tampered envelope is unknown", func(t *testing.T) {
		sealed := sealCredential(t, valid)
		tampered := sealed[:len(sealed)-4] + "AAAA"
		if tampered == sealed {
			t.Fatal("tampering failed to change the value")
		}
		writeStore(t, map[string]string{zcodeAccessTokenKey: tampered})
		status, _ := New().AuthStatus(context.Background())
		if status != ports.AgentAuthStatusUnknown {
			t.Fatalf("status = %q, want unknown", status)
		}
	})

	t.Run("sealed non-JWT is unknown", func(t *testing.T) {
		writeStore(t, map[string]string{zcodeAccessTokenKey: sealCredential(t, "garbage")})
		status, _ := New().AuthStatus(context.Background())
		if status != ports.AgentAuthStatusUnknown {
			t.Fatalf("status = %q, want unknown", status)
		}
	})
}

func TestDecryptZcodeCredentialRejectsWrongSecret(t *testing.T) {
	t.Setenv(zcodeCredentialSecretEnv, "other-secret")
	value := sealCredential(t, "payload")
	if _, err := decryptZcodeCredential(value); err == nil {
		t.Fatal("decrypt with the wrong key succeeded")
	}
}

func TestCredentialStoreBasePrefersExplicitEnv(t *testing.T) {
	t.Setenv(zcodeDataBaseDirEnv, "/opt/zcode-store")
	t.Setenv("AO_DATA_DIR", "/should/be/ignored")
	if got := zcodeCredentialStoreBase(); got != "/opt/zcode-store" {
		t.Fatalf("base = %q, want explicit env", got)
	}
}

func TestCredentialStoreBaseFallsBackToAODataDir(t *testing.T) {
	t.Setenv(zcodeDataBaseDirEnv, "")
	t.Setenv("AO_DATA_DIR", "/tmp/ao-x")
	if got := zcodeCredentialStoreBase(); got != filepath.Join("/tmp/ao-x", "harnesses", "zcode") {
		t.Fatalf("base = %q, want AO-owned harnesses dir", got)
	}
}

func TestAuthStatusReadsAOOwnedStore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(zcodeDataBaseDirEnv, "")
	t.Setenv("AO_DATA_DIR", dir)
	t.Setenv(zcodeCredentialSecretEnv, testSecret)
	path := filepath.Join(dir, "harnesses", "zcode", ".zcode", "v2", "credentials.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	valid := jwtWithClaims(t, map[string]any{"exp": time.Now().Add(30 * time.Minute).Unix(), "sub": "u"})
	data, err := json.Marshal(map[string]string{zcodeAccessTokenKey: sealCredential(t, valid)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := New().AuthStatus(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want authorized", status)
	}
}

func TestAugmentRuntimeLaunchEnv(t *testing.T) {
	plugin := New()

	t.Run("injects the AO-owned store", func(t *testing.T) {
		env := map[string]string{}
		plugin.AugmentRuntimeLaunchEnv(env, "/tmp/ao-data", "sess", "launch")
		if env[zcodeDataBaseDirEnv] != filepath.Join("/tmp/ao-data", "harnesses", "zcode") {
			t.Fatalf("env = %v", env)
		}
	})

	t.Run("explicit env wins", func(t *testing.T) {
		env := map[string]string{zcodeDataBaseDirEnv: "/opt/keep"}
		plugin.AugmentRuntimeLaunchEnv(env, "/tmp/ao-data", "sess", "launch")
		if env[zcodeDataBaseDirEnv] != "/opt/keep" {
			t.Fatalf("env = %v, want override preserved", env)
		}
	})

	t.Run("empty data dir leaves env untouched", func(t *testing.T) {
		env := map[string]string{}
		plugin.AugmentRuntimeLaunchEnv(env, "", "sess", "launch")
		if _, ok := env[zcodeDataBaseDirEnv]; ok {
			t.Fatalf("env = %v, want no injection", env)
		}
	})

	t.Run("daemon-environment override beats the data-dir default", func(t *testing.T) {
		t.Setenv(zcodeDataBaseDirEnv, "/opt/operator-store")
		env := map[string]string{}
		plugin.AugmentRuntimeLaunchEnv(env, "/tmp/ao-data", "sess", "launch")
		if env[zcodeDataBaseDirEnv] != "/opt/operator-store" {
			t.Fatalf("env = %v, want daemon-env override", env)
		}
	})
}

func TestZcodeTokenExpiry(t *testing.T) {
	exp := time.Now().Add(time.Hour).Truncate(time.Second).UTC()
	token := jwtWithClaims(t, map[string]any{"exp": exp.Unix()})
	got, has, err := zcodeTokenExpiry(token)
	if err != nil || !has {
		t.Fatalf("err=%v has=%v", err, has)
	}
	if !got.Equal(exp) {
		t.Fatalf("exp = %v, want %v", got, exp)
	}
	if _, has, err := zcodeTokenExpiry(jwtWithClaims(t, map[string]any{"sub": "u"})); err != nil || has {
		t.Fatalf("no-exp: err=%v has=%v", err, has)
	}
	if _, _, err := zcodeTokenExpiry("not-a-jwt"); err == nil {
		t.Fatal("non-JWT accepted")
	}
	if _, _, err := zcodeTokenExpiry(strings.Repeat("x.", 5)); err == nil {
		t.Fatal("malformed accepted")
	}
}
