package runner

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCredentialVault(t *testing.T) {
	ctx := context.Background()
	t.Run("encrypted round trip and SDK refresh", func(t *testing.T) {
		root := privateVaultDir(t)
		vault, err := openCredentialVault(root)
		if err != nil {
			t.Fatal(err)
		}
		auth := vaultFixture()
		if err := vault.Begin(ctx, "connect-1", auth.Provider, time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		created, err := vault.Commit(ctx, "connect-1", auth)
		if err != nil {
			t.Fatal(err)
		}
		if created.ID == auth.ID || created.Index == "" || created.Attributes[vaultGenerationAttribute] == "" {
			t.Fatal("missing opaque identity or lifecycle generation")
		}
		assertVaultHasNoSecret(t, root)
		created.Metadata["access_token"] = "refreshed-vault-secret"
		if _, err := vault.Save(ctx, created); err != nil {
			t.Fatal(err)
		}
		if err := vault.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := openCredentialVault(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		records, err := reopened.List(ctx)
		if err != nil || len(records) != 1 {
			t.Fatalf("reopen count=%d err=%v", len(records), err)
		}
		if records[0].ID != created.ID || records[0].Index != created.Index || records[0].Metadata["access_token"] != "refreshed-vault-secret" {
			t.Fatal("credential or identity did not survive restart")
		}
		records[0].Metadata["access_token"] = "caller-mutated"
		again, _ := reopened.List(ctx)
		if again[0].Metadata["access_token"] == "caller-mutated" {
			t.Fatal("List exposes mutable internal state")
		}
		assertVaultHasNoSecret(t, root)
	})
	t.Run("cancel and expiry fence commits", func(t *testing.T) {
		vault := newTestVault(t)
		for _, operation := range []string{"cancelled", "expired"} {
			expires := time.Now().Add(time.Minute)
			if operation == "expired" {
				expires = time.Now().Add(-time.Second)
			}
			if err := vault.Begin(ctx, operation, "codex", expires); err != nil {
				t.Fatal(err)
			}
			if operation == "cancelled" {
				if err := vault.Cancel(ctx, operation); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := vault.Commit(ctx, operation, vaultFixture()); !errors.Is(err, errCredentialFenced) {
				t.Fatalf("late commit error=%v", err)
			}
		}
		items, _ := vault.List(ctx)
		if len(items) != 0 {
			t.Fatal("cancelled credential became active")
		}
	})
	t.Run("durable deletion rejects late refresh and replay", func(t *testing.T) {
		root := privateVaultDir(t)
		vault, err := openCredentialVault(root)
		if err != nil {
			t.Fatal(err)
		}
		_ = vault.Begin(ctx, "connect", "codex", time.Now().Add(time.Minute))
		created, err := vault.Commit(ctx, "connect", vaultFixture())
		if err != nil {
			t.Fatal(err)
		}
		if err := vault.Delete(ctx, created.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := vault.Save(ctx, created); !errors.Is(err, errCredentialFenced) {
			t.Fatalf("late refresh error=%v", err)
		}
		if err := vault.Close(); err != nil {
			t.Fatal(err)
		}
		vault, err = openCredentialVault(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = vault.Close() })
		if _, err := vault.Commit(ctx, "connect", vaultFixture()); !errors.Is(err, errCredentialFenced) {
			t.Fatalf("deleted operation replay error=%v", err)
		}
		if _, err := vault.Save(ctx, created); !errors.Is(err, errCredentialFenced) {
			t.Fatalf("post-restart refresh error=%v", err)
		}
		if err := vault.Delete(ctx, created.ID); err != nil {
			t.Fatal(err)
		}
		items, _ := vault.List(ctx)
		if len(items) != 0 {
			t.Fatal("deleted credential resurrected")
		}
	})
	t.Run("duplicate completion and conflicting retry", func(t *testing.T) {
		vault := newTestVault(t)
		_ = vault.Begin(ctx, "connect", "codex", time.Now().Add(time.Minute))
		first, err := vault.Commit(ctx, "connect", vaultFixture())
		if err != nil {
			t.Fatal(err)
		}
		second, err := vault.Commit(ctx, "connect", vaultFixture())
		if err != nil || second.ID != first.ID {
			t.Fatalf("duplicate commit changed identity: %v", err)
		}
		changed := vaultFixture()
		changed.Metadata["access_token"] = "different-secret"
		if _, err := vault.Commit(ctx, "connect", changed); !errors.Is(err, errCredentialConflict) {
			t.Fatalf("conflicting retry error=%v", err)
		}
		if err := vault.Cancel(ctx, "connect"); !errors.Is(err, errCredentialCommitted) {
			t.Fatalf("committed operation reported cancelled: %v", err)
		}
	})
	t.Run("pending operation cannot complete after restart", func(t *testing.T) {
		root := privateVaultDir(t)
		vault, err := openCredentialVault(root)
		if err != nil {
			t.Fatal(err)
		}
		_ = vault.Begin(ctx, "pending", "codex", time.Now().Add(time.Minute))
		_ = vault.Close()
		vault, err = openCredentialVault(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = vault.Close() })
		if _, err := vault.Commit(ctx, "pending", vaultFixture()); !errors.Is(err, errCredentialFenced) {
			t.Fatalf("orphaned operation error=%v", err)
		}
	})
	t.Run("one writer per store", func(t *testing.T) {
		root := privateVaultDir(t)
		first, err := openCredentialVault(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = first.Close() })
		if second, err := openCredentialVault(root); err == nil {
			_ = second.Close()
			t.Fatal("a second writer was admitted")
		}
	})
	t.Run("provider storage never writes plaintext", func(t *testing.T) {
		vault := newTestVault(t)
		_ = vault.Begin(ctx, "storage", "codex", time.Now().Add(time.Minute))
		auth := vaultFixture()
		delete(auth.Metadata, "access_token")
		wrote := false
		auth.Storage = &vaultTestProviderStorage{AccessToken: "provider-vault-secret", Wrote: &wrote}
		created, err := vault.Commit(ctx, "storage", auth)
		if err != nil || wrote {
			t.Fatalf("provider storage err=%v wrote=%v", err, wrote)
		}
		if created.Metadata["access_token"] != "provider-vault-secret" {
			t.Fatal("provider-only fields were lost")
		}
	})
	t.Run("cancelled contexts and wrong generations cannot write", func(t *testing.T) {
		vault := newTestVault(t)
		_ = vault.Begin(ctx, "connect", "codex", time.Now().Add(time.Minute))
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := vault.Commit(cancelled, "connect", vaultFixture()); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled commit error=%v", err)
		}
		created, err := vault.Commit(ctx, "connect", vaultFixture())
		if err != nil {
			t.Fatal(err)
		}
		created.Attributes[vaultGenerationAttribute] = "999"
		if _, err := vault.Save(ctx, created); !errors.Is(err, errCredentialFenced) {
			t.Fatalf("wrong generation error=%v", err)
		}
		if _, err := vault.Save(ctx, vaultFixture()); !errors.Is(err, errCredentialFenced) {
			t.Fatalf("unregistered SDK write error=%v", err)
		}
	})
	t.Run("failed persistence publishes no account", func(t *testing.T) {
		root := privateVaultDir(t)
		vault, err := openCredentialVault(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = vault.Close() })
		_ = vault.Begin(ctx, "connect", "codex", time.Now().Add(time.Minute))
		if err := os.Rename(filepath.Join(root, vaultFileName), filepath.Join(root, "saved.vault")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(root, vaultFileName), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := vault.Commit(ctx, "connect", vaultFixture()); err == nil {
			t.Fatal("failed disk write reported success")
		}
		items, err := vault.List(ctx)
		if err != nil || len(items) != 0 {
			t.Fatalf("uncommitted account published: count=%d err=%v", len(items), err)
		}
	})
	t.Run("plaintext scanner positive control", func(t *testing.T) {
		root := privateVaultDir(t)
		if err := os.WriteFile(filepath.Join(root, "bad.fixture"), []byte("access-vault-secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		leaks, err := vaultSecretFiles(root)
		if err != nil || len(leaks) != 1 || leaks[0] != "bad.fixture" {
			t.Fatalf("plaintext positive control failed: count=%d err=%v", len(leaks), err)
		}
	})
	for _, damage := range []string{"missing key", "bad key", "tampered data", "symlink"} {
		t.Run(damage, func(t *testing.T) {
			root := privateVaultDir(t)
			vault, err := openCredentialVault(root)
			if err != nil {
				t.Fatal(err)
			}
			_ = vault.Begin(ctx, "connect", "codex", time.Now().Add(time.Minute))
			if _, err := vault.Commit(ctx, "connect", vaultFixture()); err != nil {
				t.Fatal(err)
			}
			_ = vault.Close()
			switch damage {
			case "missing key":
				err = os.Remove(filepath.Join(root, vaultKeyName))
			case "bad key":
				err = os.WriteFile(filepath.Join(root, vaultKeyName), bytes.Repeat([]byte{0xff}, 32), 0o600)
			case "tampered data":
				var raw []byte
				raw, err = os.ReadFile(filepath.Join(root, vaultFileName))
				if err == nil {
					raw[len(raw)-1] ^= 0x80
					err = os.WriteFile(filepath.Join(root, vaultFileName), raw, 0o600)
				}
			case "symlink":
				saved := filepath.Join(root, "original.vault")
				if err = os.Rename(filepath.Join(root, vaultFileName), saved); err == nil {
					err = os.Symlink(saved, filepath.Join(root, vaultFileName))
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if opened, err := openCredentialVault(root); err == nil {
				_ = opened.Close()
				t.Fatal("unsafe storage accepted")
			}
			if damage == "missing key" {
				if _, err := os.Stat(filepath.Join(root, vaultKeyName)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("missing key was silently replaced")
				}
			}
		})
	}
}

func privateVaultDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	chmodPrivateDir(t, root)
	return root
}

func newTestVault(t *testing.T) *credentialVault {
	t.Helper()
	vault, err := openCredentialVault(privateVaultDir(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}

func vaultFixture() *coreauth.Auth {
	return &coreauth.Auth{ID: "upstream-email.json", Provider: "codex", Status: coreauth.StatusActive, Metadata: map[string]any{
		"access_token": "access-vault-secret", "refresh_token": "refresh-vault-secret", "email": "vault-private@example.invalid",
	}}
}

func assertVaultHasNoSecret(t *testing.T, root string) {
	t.Helper()
	leaks, err := vaultSecretFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaks) != 0 {
		t.Fatalf("plaintext markers in %v", leaks)
	}
}

func vaultSecretFiles(root string) ([]string, error) {
	var leaks []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, marker := range []string{"vault-secret", "vault-private@example.invalid", "upstream-email.json"} {
			if bytes.Contains(raw, []byte(marker)) {
				name, _ := filepath.Rel(root, path)
				leaks = append(leaks, name)
				break
			}
		}
		return nil
	})
	return leaks, err
}

type vaultTestProviderStorage struct {
	AccessToken string `json:"access_token"`
	Wrote       *bool  `json:"-"`
}

func (s *vaultTestProviderStorage) SaveTokenToFile(string) error {
	*s.Wrote = true
	return errors.New("plaintext writes are forbidden")
}
