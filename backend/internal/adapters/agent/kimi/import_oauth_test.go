package kimi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const kimiImportOAuthConfig = `default_model = 'zai/glm'
[providers.'managed:kimi-code']
type = 'kimi'
base_url = 'https://api.kimi.com/coding/v1'
[providers.'managed:kimi-code'.oauth]
storage = 'file'
key = 'oauth/kimi-code'
[models.'zai/glm']
provider = 'managed:kimi-code'
model = 'kimi-for-coding'
`

func writeKimiImportOAuth(t *testing.T, home string, expires int64, refresh bool) []byte {
	t.Helper()
	// Generated dummy values only; no user credential, JWT or captured token.
	value := fmt.Sprintf("synthetic-%x", sha256.Sum256([]byte(t.Name())))
	token := map[string]any{"access_token": value, "expires_at": expires}
	if refresh {
		token["refresh_token"] = value + "-refresh"
	}
	data, err := json.Marshal(token)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "credentials"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "credentials", "kimi-code.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestKimiImportedOAuthSharesNativeRefreshAndCleansLinks(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), probe); err != nil {
		t.Skipf("native credential sharing needs symlink support: %v", err)
	}
	r := kimiImportFixture(t)
	if err := os.WriteFile(filepath.Join(r.ConfigDir, "config.toml"), []byte(kimiImportOAuthConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	credential := writeKimiImportOAuth(t, r.ConfigDir, time.Now().Add(-time.Hour).Unix(), true)
	ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(r.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	env, imported, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, ref.NativeSessionID, r.LaunchCWD, "", nil)
	if err != nil || !imported || env["KIMI_CODE_HOME"] != ref.ConfigDir || len(env) != 1 {
		t.Fatalf("OAuth-only native runtime refused: %v", err)
	}
	for _, name := range []string{"credentials", "oauth"} {
		link, err := os.Readlink(filepath.Join(ref.ConfigDir, name))
		if err != nil {
			t.Skipf("native credential sharing needs symlink support: %v", err)
		}
		if !sameKimiConfigPath(link, filepath.Join(r.ConfigDir, name)) {
			t.Fatal("private OAuth link does not share the source store")
		}
	}
	config, err := os.ReadFile(filepath.Join(ref.ConfigDir, "config.toml"))
	if err != nil || bytes.Contains(config, []byte("synthetic-")) || strings.Contains(string(config), "api_key_env") || !strings.Contains(string(config), "oauth") {
		t.Fatal("private config did not preserve only the OAuth reference")
	}
	// Native Kimi writes a temporary token and renames it in the shared directory.
	// The source and import must observe the same replacement and refresh lock.
	next := bytes.ReplaceAll(credential, []byte("synthetic-"), []byte("rotated-"))
	privateToken := filepath.Join(ref.ConfigDir, "credentials", "kimi-code.json")
	tmp := privateToken + ".tmp"
	if err := os.WriteFile(tmp, next, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, privateToken); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(r.ConfigDir, "credentials", "kimi-code.json"))
	if err != nil || !bytes.Equal(got, next) {
		t.Fatal("native refresh replacement did not reach the original login")
	}
	if err := os.Mkdir(filepath.Join(ref.ConfigDir, "oauth", "kimi-code.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(r.ConfigDir, "oauth", "kimi-code.lock")); err != nil || !info.IsDir() {
		t.Fatal("native refresh lock was not shared")
	}
	if err := New().CleanupWorkspace(t.Context(), ports.WorkspaceHookConfig{DataDir: r.DataDir, ImportConfigDir: ref.ConfigDir, NativeSessionID: ref.NativeSessionID}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"credentials", "oauth"} {
		if _, err := os.Lstat(filepath.Join(ref.ConfigDir, name)); !os.IsNotExist(err) {
			t.Fatal("session cleanup retained a native-auth link")
		}
	}
	after, err := os.ReadFile(r.TranscriptPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("OAuth runtime changed source history")
	}
	config, err = os.ReadFile(filepath.Join(r.ConfigDir, "config.toml"))
	if err != nil || string(config) != kimiImportOAuthConfig {
		t.Fatal("OAuth runtime changed source configuration")
	}
	if got, err := os.ReadFile(filepath.Join(r.ConfigDir, "credentials", "kimi-code.json")); err != nil || !bytes.Equal(got, next) {
		t.Fatal("session cleanup removed the user's native login")
	}
}

func TestKimiImportedOAuthRequiresUsableNativeLogin(t *testing.T) {
	for _, mode := range []string{"missing", "expired", "revoked", "corrupt", "logged out", "config removed"} {
		t.Run(mode, func(t *testing.T) {
			r := kimiImportFixture(t)
			// Prepare before logging out so both registration-time and reopen-time
			// refusals use the same actionable sign-in reason.
			ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(r.ConfigDir, "config.toml"), []byte(kimiImportOAuthConfig), 0o600); err != nil {
				t.Fatal(err)
			}
			if mode != "missing" {
				writeKimiImportOAuth(t, r.ConfigDir, time.Now().Add(-time.Hour).Unix(), false)
				if mode == "revoked" || mode == "corrupt" {
					data := []byte(`{"access_token":"","refresh_token":"","expires_at":0}`)
					if mode == "corrupt" {
						data = []byte(`not json`)
					}
					if err := os.WriteFile(filepath.Join(r.ConfigDir, "credentials", "kimi-code.json"), data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			switch mode {
			case "logged out":
				if err := os.WriteFile(filepath.Join(r.ConfigDir, "config.toml"), []byte("telemetry = false\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "config removed":
				if err := os.Remove(filepath.Join(r.ConfigDir, "config.toml")); err != nil {
					t.Fatal(err)
				}
			}
			fresh := r
			fresh.DataDir = t.TempDir()
			_, preparationErr := prepareKimiImport(t.Context(), fresh, "2.4.1")
			for _, err := range []error{
				preparationErr,
				CheckImportedOAuth(t.Context(), r.ConfigDir, "zai/glm"),
				func() error {
					_, _, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, ref.NativeSessionID, r.LaunchCWD, "", nil)
					return err
				}(),
			} {
				if err == nil || !strings.Contains(err.Error(), "sign in to Kimi") || !strings.Contains(err.Error(), "kimi login") {
					t.Fatal("missing native login did not produce an actionable sign-in refusal")
				}
			}
		})
	}
}
