package runner

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestManagedReconnectCoordinator(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		for _, mode := range []string{"callback", "device"} {
			if provider != "codex" && mode == "device" {
				continue
			}
			for _, outcome := range []string{"success", "mismatch", "refresh", "cancel", "removed"} {
				t.Run(provider+"/"+mode+"/"+outcome, func(t *testing.T) {
					runtime, previous := trustedCredentialFixture(t, provider)
					coordinator := newOAuthCoordinator("", "management", nil, func(_, _ string) (net.Listener, error) { return net.Listen("tcp4", "127.0.0.1:0") })
					coordinator.useCredentials(t.Context(), runtime, &sdkconfig.Config{})
					defer coordinator.Close()
					finish := make(chan struct{})
					incoming := identityFixture(provider, "user-a", "account-a")
					if outcome == "mismatch" {
						incoming = identityFixture(provider, "user-b", "account-a")
					}
					incoming.Metadata["access_token"] = "reconnected-vault-secret"
					coordinator.authenticator = func(string) sdkauth.Authenticator {
						return memoryAuthenticator{provider: provider, login: func(ctx context.Context, _ *sdkconfig.Config, opts *sdkauth.LoginOptions) (*coreauth.Auth, error) {
							if err := opts.AuthorizationURL(ctx, "https://provider.example/authorize"); err != nil {
								return nil, err
							}
							select {
							case <-ctx.Done():
							case <-finish:
							}
							return incoming, nil
						}}
					}
					coordinator.startCodexDevice = func(ctx context.Context) (codexDeviceLogin, error) {
						done, result := make(chan error, 1), make(chan *coreauth.Auth, 1)
						go func() {
							select {
							case <-ctx.Done():
							case <-finish:
							}
							result <- incoming
							done <- nil
						}()
						return codexDeviceLogin{AuthorizationURL: codexDeviceVerificationURL, UserCode: "ABCD-EFGH", Done: done, Result: result}, nil
					}
					call := func(method, path string, body any, want int) []byte {
						t.Helper()
						raw, _ := json.Marshal(body)
						req := httptest.NewRequest(method, "/ao/internal/oauth/"+path, strings.NewReader(string(raw)))
						req.Header.Set("Authorization", "Bearer management")
						response := httptest.NewRecorder()
						coordinator.ServeHTTP(response, req)
						if response.Code != want {
							t.Fatalf("%s: status=%d want=%d", path, response.Code, want)
						}
						if strings.Contains(response.Body.String(), "vault-secret") {
							t.Fatal("credential leaked")
						}
						return response.Body.Bytes()
					}
					input := map[string]any{"provider": provider, "mode": mode, "targetRef": previous.Index, "generation": 1}
					var started struct{ State, TargetRef string }
					if json.Unmarshal(call("POST", "start", input, 200), &started) != nil || started.State == "" || started.TargetRef != previous.Index {
						t.Fatal("reconnect target lost")
					}
					call("POST", "start", map[string]string{"provider": provider, "mode": mode}, 409)
					switch outcome {
					case "cancel":
						call("DELETE", "session?state="+started.State, nil, 204)
					case "removed":
						if err := runtime.Remove(t.Context(), previous.ID); err != nil {
							t.Fatal(err)
						}
					case "refresh":
						refreshed := previous.Clone()
						refreshed.Metadata["access_token"] = "refreshed-vault-secret"
						if _, err := runtime.vault.Save(t.Context(), refreshed); err != nil {
							t.Fatal(err)
						}
					}
					close(finish)
					coordinator.workers.Wait()
					var status struct{ Status, FailureCode string }
					if json.Unmarshal(call("GET", "status?state="+started.State, nil, 200), &status) != nil {
						t.Fatal("invalid progress")
					}
					wantStatus, wantCode := "completed", ""
					switch outcome {
					case "mismatch":
						wantStatus, wantCode = "failed", "identity_mismatch"
					case "refresh", "removed":
						wantStatus, wantCode = "failed", "credential_changed"
					case "cancel":
						wantStatus, wantCode = "expired", "cancelled"
					}
					if status.Status != wantStatus || status.FailureCode != wantCode {
						t.Fatalf("progress=%+v want=%s/%s", status, wantStatus, wantCode)
					}
					auths, err := runtime.vault.List(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					if outcome == "removed" {
						if len(auths) != 0 {
							t.Fatal("removed account resurrected")
						}
						return
					}
					if len(auths) != 1 || auths[0].ID != previous.ID || auths[0].Index != previous.Index {
						t.Fatal("reconnect duplicated or changed selected account")
					}
					generation, supported := runtime.vault.reconnectVersion(t.Context(), auths[0])
					wantGeneration := uint64(1)
					if outcome == "success" {
						wantGeneration = 2
					}
					if generation != wantGeneration || !supported {
						t.Fatal("incorrect public reconnect capability")
					}
					assertVaultHasNoSecret(t, runtime.vault.root.Name())
				})
			}
		}
	}
}
