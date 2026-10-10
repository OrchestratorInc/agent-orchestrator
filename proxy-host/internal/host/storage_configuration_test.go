package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	"gopkg.in/yaml.v3"
)

func TestHelperBuildKeepsEveryOwnedPathUnderItsConfiguredDataDirectory(t *testing.T) {
	for _, name := range []string{"ordinary", "folder with spaces", "account#data?", "账户"} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), name)
			control := strings.Repeat("c", 32)
			inference := strings.Repeat("i", 32)
			routes, err := OpenRoutes(filepath.Join(root, "run", "routes.json"))
			if err != nil {
				t.Fatal(err)
			}
			service, err := Build(root, 12345, control, inference, routes)
			if err != nil || service == nil {
				t.Fatalf("explicit private storage path failed: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(root, "config.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var cfg config.Config
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.Host != "127.0.0.1" || cfg.Port != 12345 || cfg.AuthDir != filepath.Join(root, "auth") {
				t.Fatal("helper configuration escaped its explicit loopback or storage boundary")
			}
			if len(cfg.APIKeys) != 1 || cfg.APIKeys[0] != inference {
				t.Fatal("helper API identity was not separated from management identity")
			}
			if strings.Contains(string(data), control) {
				t.Fatal("management identity was copied into SDK configuration")
			}
			if !cfg.RemoteManagement.DisableControlPanel || !cfg.RemoteManagement.DisableAutoUpdatePanel || cfg.RemoteManagement.AllowRemote {
				t.Fatal("managed helper enabled an unrelated network management surface")
			}
			if cfg.MaxRetryCredentials != 1 || cfg.Routing.Strategy != "fill-first" || !cfg.CommercialMode || !cfg.WebsocketAuth {
				t.Fatal("helper startup dropped a required routing or listener constraint")
			}
			info, err := os.Stat(filepath.Join(root, "config.yaml"))
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatal("private SDK configuration is not owner-readable only")
			}
			info, err = os.Stat(cfg.AuthDir)
			if err != nil || info.Mode().Perm() != 0o700 {
				t.Fatal("account credential directory is not owner-accessible only")
			}
			if err := routes.Apply(Snapshot{Revision: 1, Routes: []Route{testRoute("session", "private-ticket", "codex", "private-auth-id")}}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, "run", "routes.json")); err != nil {
				t.Fatal("acknowledged routes were not saved below the configured data directory")
			}
		})
	}
}

func TestHelperBuildRepairsCredentialDirectoryPermissionsWithoutRemovingAccounts(t *testing.T) {
	root := t.TempDir()
	authDir := filepath.Join(root, "auth")
	if err := os.Mkdir(authDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(authDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(authDir, "retained-test-account.json")
	credential := []byte(`{"type":"codex","access_token":"fake-existing-token"}`)
	if err := os.WriteFile(path, credential, 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := Build(root, 12345, strings.Repeat("c", 32), strings.Repeat("i", 32), testRoutes(t))
	if err != nil || service == nil {
		t.Fatal(err)
	}
	info, err := os.Stat(authDir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("startup did not tighten the existing auth directory")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(credential) {
		t.Fatal("permission repair replaced or deleted a saved credential")
	}
}

func TestHelperBuildStorageFailureDoesNotProduceAnUsableService(t *testing.T) {
	for _, target := range []string{"auth", "config.yaml"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, target)
			if target == "auth" {
				if err := os.WriteFile(path, []byte("occupied-auth-path"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			service, err := Build(root, 12345, strings.Repeat("c", 32), strings.Repeat("i", 32), testRoutes(t))
			if err == nil || service != nil {
				t.Fatal("failed private configuration silently produced a runnable helper")
			}
			if target == "auth" {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "occupied-auth-path" {
					t.Fatal("startup removed an occupied credential storage path")
				}
			}
		})
	}
}
