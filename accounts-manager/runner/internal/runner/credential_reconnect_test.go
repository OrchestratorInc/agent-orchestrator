package runner

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func identityFixture(provider, user, account string) *coreauth.Auth {
	auth := vaultFixture()
	auth.Provider, auth.Label = provider, "Work"
	if provider == "codex" {
		payload, _ := json.Marshal(map[string]any{"sub": user, "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": account}})
		auth.Metadata["account_id"] = account
		auth.Metadata["id_token"] = "fixture." + base64.RawURLEncoding.EncodeToString(payload) + ".fixture"
	} else {
		auth.Metadata["account_uuid"], auth.Metadata["organization_uuid"] = user, account
	}
	return auth
}

func trustedCredentialFixture(t *testing.T, provider string) (*credentialRuntime, *coreauth.Auth) {
	t.Helper()
	vault := newTestVault(t)
	runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil)}
	if err := vault.beginProviderLogin(t.Context(), "connect", provider, time.Now().Add(time.Minute), "", 0); err != nil {
		t.Fatal(err)
	}
	auth, err := runtime.complete(t.Context(), "connect", identityFixture(provider, "user-a", "account-a"), true)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, auth
}

func TestCredentialReconnectPreservesIdentityAndFencesRefresh(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			runtime, previous := trustedCredentialFixture(t, provider)
			if err := runtime.vault.beginProviderLogin(t.Context(), "reconnect", provider, time.Now().Add(time.Minute), previous.ID, 1); err != nil {
				t.Fatal(err)
			}
			incoming := identityFixture(provider, "user-a", "account-a")
			incoming.Metadata["access_token"] = "reconnected-vault-secret"
			incoming.Metadata["email"] = "renamed@example.invalid"
			incoming.Label = "not the saved label"
			updated, err := runtime.complete(t.Context(), "reconnect", incoming, true)
			if err != nil {
				t.Fatal(err)
			}
			if updated.ID != previous.ID || updated.Index != previous.Index || updated.Label != previous.Label || updated.Attributes[vaultGenerationAttribute] != "2" || !runtime.vault.Admit(t.Context(), updated) {
				t.Fatal("replacement did not preserve the committed account")
			}
			if _, err := runtime.vault.Save(t.Context(), previous); !errors.Is(err, errCredentialFenced) {
				t.Fatal("stale refresh crossed replacement generation")
			}
			if runtime.vault.Admit(t.Context(), previous) {
				t.Fatal("old generation remains admitted")
			}
			repeated, err := runtime.complete(t.Context(), "reconnect", incoming, true)
			if err != nil || repeated.ID != updated.ID || len(runtime.manager.List()) != 1 {
				t.Fatal("replacement replay duplicated account")
			}
			root := runtime.vault.root.Name()
			if err := runtime.vault.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := openCredentialVault(root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			auths, err := reopened.List(t.Context())
			if err != nil || len(auths) != 1 || auths[0].ID != updated.ID || !reopened.Admit(t.Context(), auths[0]) {
				t.Fatal("replacement was not recovered after restart")
			}
			assertVaultHasNoSecret(t, root)
		})
	}
}

func TestCredentialReconnectRejectsUnverifiedIdentity(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			vault := newTestVault(t)
			if err := vault.Begin(t.Context(), "import", provider, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			auth, err := vault.Commit(t.Context(), "import", identityFixture(provider, "user-a", "account-a"))
			if err != nil {
				t.Fatal(err)
			}
			if err := vault.beginProviderLogin(t.Context(), "replace", provider, time.Now().Add(time.Minute), auth.ID, 1); !errors.Is(err, errCredentialIdentity) {
				t.Fatal("imported identity claims authorized replacement")
			}
			if err := vault.beginProviderLogin(t.Context(), "login", provider, time.Now().Add(time.Minute), "", 0); err != nil {
				t.Fatal(err)
			}
			if err := vault.Begin(t.Context(), "login", provider, time.Now().Add(time.Minute)); !errors.Is(err, errCredentialConflict) {
				t.Fatal("import adopted a provider login operation")
			}
			if _, err := vault.Commit(t.Context(), "login", identityFixture(provider, "user-a", "account-a")); !errors.Is(err, errCredentialConflict) {
				t.Fatal("plain import completed a trusted operation")
			}
		})
	}
}

func TestCredentialReconnectRejectsChangedState(t *testing.T) {
	for _, outcome := range []string{"user mismatch", "account mismatch", "missing identity", "refresh won", "removed", "disabled", "cancelled", "restarted"} {
		t.Run(outcome, func(t *testing.T) {
			runtime, previous := trustedCredentialFixture(t, "codex")
			if err := runtime.vault.beginProviderLogin(t.Context(), "reconnect", "codex", time.Now().Add(time.Minute), previous.ID, 1); err != nil {
				t.Fatal(err)
			}
			incoming := identityFixture("codex", "user-a", "account-a")
			incoming.Metadata["access_token"] = "replacement-vault-secret"
			switch outcome {
			case "user mismatch":
				incoming = identityFixture("codex", "user-b", "account-a")
			case "account mismatch":
				incoming = identityFixture("codex", "user-a", "account-b")
			case "missing identity":
				delete(incoming.Metadata, "id_token")
			case "refresh won":
				refreshed := previous.Clone()
				refreshed.Metadata["access_token"] = "newer-refresh-vault-secret"
				if _, err := runtime.vault.Save(t.Context(), refreshed); err != nil {
					t.Fatal(err)
				}
			case "removed":
				if err := runtime.Remove(t.Context(), previous.ID); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if err := runtime.SetEnabled(t.Context(), previous.ID, false); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				if err := runtime.vault.Cancel(t.Context(), "reconnect"); err != nil {
					t.Fatal(err)
				}
			case "restarted":
				root := runtime.vault.root.Name()
				if err := runtime.vault.Close(); err != nil {
					t.Fatal(err)
				}
				vault, err := openCredentialVault(root)
				if err != nil {
					t.Fatal(err)
				}
				defer vault.Close()
				runtime.vault = vault
			}
			if _, err := runtime.complete(t.Context(), "reconnect", incoming, true); err == nil {
				t.Fatal("invalid replacement committed")
			}
			auths, err := runtime.vault.List(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "removed" {
				if len(auths) != 0 {
					t.Fatal("reconnect resurrected a removed account")
				}
				return
			}
			if len(auths) != 1 || auths[0].ID != previous.ID || auths[0].Metadata["access_token"] == "replacement-vault-secret" {
				t.Fatal("failed replacement changed saved account")
			}
			if outcome == "refresh won" && auths[0].Metadata["access_token"] != "newer-refresh-vault-secret" {
				t.Fatal("replacement overwrote a newer refresh")
			}
		})
	}
}

func TestCredentialReconnectPreservesDisablement(t *testing.T) {
	runtime, previous := trustedCredentialFixture(t, "codex")
	if err := runtime.SetEnabled(t.Context(), previous.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := runtime.vault.beginProviderLogin(t.Context(), "reconnect", "codex", time.Now().Add(time.Minute), previous.ID, 2); err != nil {
		t.Fatal(err)
	}
	updated, err := runtime.complete(t.Context(), "reconnect", identityFixture("codex", "user-a", "account-a"), true)
	if err != nil || !updated.Disabled || updated.Attributes[vaultGenerationAttribute] != strconv.Itoa(3) || runtime.vault.Admit(t.Context(), updated) {
		t.Fatal("replacement enabled a disabled account")
	}
}
