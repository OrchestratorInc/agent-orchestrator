package runner

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

type memoryAuthenticator struct {
	provider string
	login    func(context.Context, *sdkconfig.Config, *sdkauth.LoginOptions) (*coreauth.Auth, error)
}

func (a memoryAuthenticator) Provider() string {
	if a.provider != "" {
		return a.provider
	}
	return "codex"
}
func (a memoryAuthenticator) RefreshLead() *time.Duration { return nil }
func (a memoryAuthenticator) Login(ctx context.Context, cfg *sdkconfig.Config, opts *sdkauth.LoginOptions) (*coreauth.Auth, error) {
	return a.login(ctx, cfg, opts)
}

func TestCredentialBrowserCommitBoundary(t *testing.T) {
	for _, outcome := range []string{"success", "success closed", "failure", "cancelled", "late result"} {
		t.Run(outcome, func(t *testing.T) {
			vault := newTestVault(t)
			manager := coreauth.NewManager(vault, nil, nil)
			runtime := &credentialRuntime{vault: vault, manager: manager}
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			provider := memoryAuthenticator{login: func(ctx context.Context, _ *sdkconfig.Config, opts *sdkauth.LoginOptions) (*coreauth.Auth, error) {
				if !opts.NoBrowser || opts.CallbackListener != listener || opts.AuthorizationURL == nil {
					t.Fatal("unsafe browser options")
				}
				if len(manager.List()) != 0 {
					t.Fatal("credential published before sign-in completed")
				}
				switch outcome {
				case "success closed":
					if err := listener.Close(); err != nil {
						t.Fatal(err)
					}
				case "failure":
					return nil, errors.New("provider-vault-secret")
				case "cancelled":
					if err := vault.Cancel(ctx, "browser"); err != nil {
						t.Fatal(err)
					}
				case "late result":
					cancel()
				}
				return vaultFixture(), nil
			}}
			auth, err := runtime.ConnectBrowser(ctx, "browser", provider, &sdkconfig.Config{}, listener, func(context.Context, string) error { return nil })
			if outcome == "success" || outcome == "success closed" {
				if err != nil || auth == nil || len(manager.List()) != 1 || !vault.Admit(ctx, auth) {
					t.Fatalf("durable login failed: %v", err)
				}
			} else {
				if err == nil || auth != nil || len(manager.List()) != 0 || strings.Contains(err.Error(), "vault-secret") {
					t.Fatal("failed login leaked or published a credential")
				}
				if _, err := vault.Commit(t.Context(), "browser", vaultFixture()); !errors.Is(err, errCredentialFenced) {
					t.Fatal("failed login was not durably cancelled")
				}
			}
			if conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second); err == nil {
				_ = conn.Close()
				t.Fatal("browser worker retained callback listener")
			}
			assertVaultHasNoSecret(t, vault.root.Name())
		})
	}
}
