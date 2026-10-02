package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestManagedBrowserCoordinator(t *testing.T) {
	for _, outcome := range []string{"success", "cancel", "failure", "shutdown"} {
		t.Run(outcome, func(t *testing.T) {
			vault := newTestVault(t)
			runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil)}
			coordinator := newOAuthCoordinator("", "management", nil, func(_, _ string) (net.Listener, error) { return net.Listen("tcp4", "127.0.0.1:0") })
			coordinator.useCredentials(t.Context(), runtime, &sdkconfig.Config{})
			defer coordinator.Close()
			finish := make(chan struct{})
			coordinator.authenticator = func(string) sdkauth.Authenticator {
				return memoryAuthenticator{login: func(ctx context.Context, _ *sdkconfig.Config, opts *sdkauth.LoginOptions) (*coreauth.Auth, error) {
					if err := opts.AuthorizationURL(ctx, "https://provider.example/authorize?state=private-state"); err != nil {
						return nil, err
					}
					select {
					case <-ctx.Done():
					case <-finish:
					}
					if outcome == "failure" {
						return nil, errors.New("provider-vault-secret")
					}
					return vaultFixture(), nil
				}}
			}
			call := func(method, path, body string, want int) []byte {
				t.Helper()
				request := httptest.NewRequest(method, "/ao/internal/oauth/"+path, strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer management")
				response := httptest.NewRecorder()
				coordinator.ServeHTTP(response, request)
				if response.Code != want {
					t.Fatalf("%s: status=%d want=%d", path, response.Code, want)
				}
				if strings.Contains(response.Body.String(), "provider-vault-secret") {
					t.Fatal("private provider error exposed")
				}
				return response.Body.Bytes()
			}
			var started struct {
				State string `json:"state"`
				URL   string `json:"authorizationUrl"`
			}
			if json.Unmarshal(call("POST", "start", `{"provider":"codex","mode":"callback"}`, 200), &started) != nil || started.State == "" || started.URL == "" {
				t.Fatal("missing browser instructions")
			}
			if len(runtime.manager.List()) != 0 {
				t.Fatal("pending account published")
			}
			switch outcome {
			case "cancel":
				call("DELETE", "session?state="+started.State, "", 204)
			case "shutdown":
				coordinator.Close()
			default:
				close(finish)
			}
			coordinator.workers.Wait()
			if outcome == "success" {
				if len(runtime.manager.List()) != 1 {
					t.Fatal("completed credential missing")
				}
				call("DELETE", "session?state="+started.State, "", 409)
			} else {
				if len(runtime.manager.List()) != 0 {
					t.Fatal("unsuccessful sign-in published a credential")
				}
				if _, err := vault.Commit(t.Context(), started.State, vaultFixture()); !errors.Is(err, errCredentialFenced) {
					t.Fatal("late callback was not fenced")
				}
			}
			assertVaultHasNoSecret(t, vault.root.Name())
		})
	}
}
