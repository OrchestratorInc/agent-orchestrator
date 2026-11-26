package agy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuthModeRoundTrip(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	if got := ReadAuthMode(dataDir); got != AuthModeNone {
		t.Fatalf("ReadAuthMode before write = %q, want none", got)
	}
	for _, mode := range []AuthMode{AuthModeADC, AuthModeGoogle} {
		if err := WriteAuthMode(dataDir, mode); err != nil {
			t.Fatalf("WriteAuthMode(%q): %v", mode, err)
		}
		if got := ReadAuthMode(dataDir); got != mode {
			t.Fatalf("ReadAuthMode = %q, want %q", got, mode)
		}
	}
	info, err := os.Stat(authModePath(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("auth-mode permissions = %v, want owner-only", perm)
	}
}

func TestWriteAuthModeRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	if err := WriteAuthMode("", AuthModeADC); err == nil {
		t.Fatal("WriteAuthMode with empty data dir succeeded")
	}
	if err := WriteAuthMode(t.TempDir(), AuthMode("api-key")); err == nil {
		t.Fatal("WriteAuthMode with unknown mode succeeded")
	}
}

func TestReadAuthModeIgnoresUnknownContent(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	path := authModePath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("something-else\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadAuthMode(dataDir); got != AuthModeNone {
		t.Fatalf("ReadAuthMode = %q, want none", got)
	}
}

func TestAugmentRuntimeEnvAppliesRecordedAuthMode(t *testing.T) {
	t.Parallel()

	t.Run("none leaves inherited env untouched", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{}
		New().AugmentRuntimeEnv(env, t.TempDir())
		if _, ok := env[adcAuthEnv]; ok {
			t.Fatalf("env = %#v, want no %s override", env, adcAuthEnv)
		}
	})
	t.Run("adc sets AGY_ADC_AUTH", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()
		if err := WriteAuthMode(dataDir, AuthModeADC); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{}
		New().AugmentRuntimeEnv(env, dataDir)
		if env[adcAuthEnv] != "true" {
			t.Fatalf("env[%s] = %q, want true", adcAuthEnv, env[adcAuthEnv])
		}
	})
	t.Run("google clears an inherited AGY_ADC_AUTH", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()
		if err := WriteAuthMode(dataDir, AuthModeGoogle); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{adcAuthEnv: "true"}
		New().AugmentRuntimeEnv(env, dataDir)
		value, ok := env[adcAuthEnv]
		if !ok || value != "" {
			t.Fatalf("env[%s] = %q (present=%v), want present and empty to override the inherited value", adcAuthEnv, value, ok)
		}
	})
}

func TestApplyAuthModeEnvToleratesNilEnv(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	if err := WriteAuthMode(dataDir, AuthModeADC); err != nil {
		t.Fatal(err)
	}
	ApplyAuthModeEnv(nil, dataDir)
}
