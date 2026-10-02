package runner

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func draftMigrationFixture(t *testing.T) (*State, *credentialVault) {
	t.Helper()
	root, authDir := validStateFixture(t)
	path := filepath.Join(root, configFileName)
	config, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writePrivateFile(t, path, string(config)+"codex-api-key:\n  - api-key: api-vault-secret\n    base-url: http://127.0.0.1:1/v1\n")
	writePrivateFile(t, filepath.Join(authDir, "draft.json"), `{"type":"codex","access_token":"token-vault-secret","refresh_token":"refresh-vault-secret","account_id":"account-a","disabled":true}`)
	state, err := LoadState(root)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := openCredentialVault(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	return state, vault
}

func TestCredentialMigration(t *testing.T) {
	for _, outcome := range []string{"complete", "interrupted", "removed", "changed"} {
		t.Run(outcome, func(t *testing.T) {
			state, vault := draftMigrationFixture(t)
			sources, _, raw, err := draftCredentialSources(state, vault)
			clear(raw)
			if err != nil || len(sources) != 2 {
				t.Fatal("draft source validation failed")
			}
			indexes := map[string]bool{}
			for _, source := range sources {
				indexes[source.Auth.Index] = true
			}
			if outcome != "complete" {
				if err := vault.migrateCredentials(t.Context(), sources); err != nil {
					t.Fatal(err)
				}
				if outcome == "removed" {
					auths, _ := vault.List(t.Context())
					for _, auth := range auths {
						if err := vault.Delete(t.Context(), auth.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err := vault.Close(); err != nil {
					t.Fatal(err)
				}
				vault, err = openCredentialVault(state.Root)
				if err != nil {
					t.Fatal(err)
				}
				defer vault.Close()
			}
			if outcome == "changed" {
				writePrivateFile(t, filepath.Join(state.Config.AuthDir, "draft.json"), `{"type":"codex","access_token":"replacement-vault-secret"}`)
				if err := migrateDraftCredentials(t.Context(), state, vault); !errors.Is(err, errCredentialConflict) {
					t.Fatal("changed migration source was admitted")
				}
				if _, err := os.Stat(filepath.Join(state.Config.AuthDir, "draft.json")); err != nil {
					t.Fatal("changed source was removed")
				}
				return
			}
			if err := migrateDraftCredentials(t.Context(), state, vault); err != nil {
				t.Fatal(err)
			}
			if err := requireEncryptedCredentialConfig(state); err != nil {
				t.Fatal(err)
			}
			if err := vault.Close(); err != nil {
				t.Fatal(err)
			}
			vault, err = openCredentialVault(state.Root)
			if err != nil {
				t.Fatal(err)
			}
			defer vault.Close()
			auths, err := vault.List(t.Context())
			want := 2
			if outcome == "removed" {
				want = 0
			}
			if err != nil || len(auths) != want {
				t.Fatalf("migrated count=%d want=%d err=%v", len(auths), want, err)
			}
			for _, auth := range auths {
				if !indexes[auth.Index] || auth.FileName != "" || auth.Attributes[coreauth.AttributePath] != "" {
					t.Fatal("migration changed public identity or retained a file writer")
				}
				if auth.Metadata["account_id"] == "account-a" && !auth.Disabled {
					t.Fatal("migration enabled a disabled account")
				}
			}
			state, err = LoadState(state.Root)
			if err != nil || migrateDraftCredentials(t.Context(), state, vault) != nil {
				t.Fatal("migration did not survive reload")
			}
			assertVaultHasNoSecret(t, state.Root)
		})
	}
}

func TestCredentialMigrationRejectsUnsafeSources(t *testing.T) {
	for _, outcome := range []string{"unsupported", "malformed", "symlink", "hardlink", "storage closed", "active runner"} {
		t.Run(outcome, func(t *testing.T) {
			if (outcome == "symlink" || outcome == "hardlink") && runtime.GOOS == "windows" {
				t.Skip("requires native link privileges")
			}
			state, vault := draftMigrationFixture(t)
			path := filepath.Join(state.Config.AuthDir, "draft.json")
			original, _ := os.ReadFile(path)
			switch outcome {
			case "unsupported":
				writePrivateFile(t, path, `{"type":"other","access_token":"private-vault-secret"}`)
			case "malformed":
				writePrivateFile(t, path, `{"type":"codex","access_token":123}`)
			case "symlink", "hardlink":
				outside := filepath.Join(t.TempDir(), "native.json")
				writePrivateFile(t, outside, string(original))
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				var err error
				if outcome == "symlink" {
					err = os.Symlink(outside, path)
				} else {
					err = os.Link(outside, path)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "storage closed":
				_ = vault.Close()
			case "active runner":
				listener, err := net.Listen("tcp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				raw, _ := json.Marshal(RuntimeRecord{Port: listener.Addr().(*net.TCPAddr).Port})
				writePrivateFile(t, filepath.Join(state.Root, runtimeFileName), string(raw))
			}
			err := migrateDraftCredentials(t.Context(), state, vault)
			if err == nil || strings.Contains(err.Error(), "vault-secret") {
				t.Fatal("unsafe migration succeeded or leaked a credential")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal("failed migration removed its source")
			}
			if auths, err := vault.List(t.Context()); err == nil && len(auths) != 0 {
				t.Fatal("failed validation partially committed migration")
			}
		})
	}
}
