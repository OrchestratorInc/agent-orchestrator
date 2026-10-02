package runner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCredentialRuntime(t *testing.T) {
	ctx := t.Context()
	vault := newTestVault(t)
	manager := coreauth.NewManager(vault, nil, nil)
	runtime := &credentialRuntime{vault: vault, manager: manager}
	if err := vault.Begin(ctx, "connect", "codex", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	created, err := runtime.Complete(ctx, "connect", vaultFixture())
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := manager.GetByID(created.ID)
	if !ok || !vault.Admit(ctx, loaded) {
		t.Fatal("committed credential not admitted")
	}
	if err := runtime.SetEnabled(ctx, created.ID, false); err != nil {
		t.Fatal(err)
	}
	if vault.Admit(ctx, loaded) {
		t.Fatal("disabled credential admitted through stale runtime")
	}
	if _, err := vault.Save(ctx, loaded); !errors.Is(err, errCredentialFenced) {
		t.Fatal("late refresh crossed disable fence")
	}
	if err := runtime.SetEnabled(ctx, created.ID, true); err != nil {
		t.Fatal(err)
	}
	if vault.Admit(ctx, loaded) {
		t.Fatal("re-enable revived stale generation")
	}
	loaded, ok = manager.GetByID(created.ID)
	if !ok || !vault.Admit(ctx, loaded) {
		t.Fatal("enabled generation unavailable")
	}
	uncommitted := loaded.Clone()
	uncommitted.Metadata["access_token"] = "uncommitted-vault-secret"
	if vault.Admit(ctx, uncommitted) {
		t.Fatal("uncommitted credential admitted")
	}
	uncommittedTransport := loaded.Clone()
	uncommittedTransport.ProxyURL = "https://uncommitted.example"
	if vault.Admit(ctx, uncommittedTransport) {
		t.Fatal("uncommitted transport admitted")
	}
	canonical := loaded.Clone()
	delete(canonical.Metadata, "disabled")
	if !vault.Admit(ctx, canonical) {
		t.Fatal("equivalent disable metadata changed admission")
	}
	if err := runtime.Remove(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.GetByID(created.ID); ok {
		t.Fatal("removed credential remains registered")
	}
	if _, err := manager.Register(ctx, uncommitted); err != nil {
		t.Fatal(err)
	}
	if vault.Admit(ctx, uncommitted) {
		t.Fatal("SDK publication bypassed removal")
	}
	if err := runtime.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.GetByID(created.ID); ok {
		t.Fatal("reload retained deleted credential")
	}
	assertVaultHasNoSecret(t, vault.root.Name())
}

func TestCredentialRuntimeCancellationAndRecovery(t *testing.T) {
	root := privateVaultDir(t)
	vault, err := openCredentialVault(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := vault.Begin(ctx, "pending", "codex", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := vault.Begin(ctx, "completed", "codex", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	created, err := vault.Commit(ctx, "completed", vaultFixture())
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	vault, err = openCredentialVault(root)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	manager := coreauth.NewManager(vault, nil, nil)
	runtime := &credentialRuntime{vault: vault, manager: manager}
	if err := runtime.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	loaded, ok := manager.GetByID(created.ID)
	if !ok || !vault.Admit(ctx, loaded) {
		t.Fatal("durable completion was not reconciled")
	}
	if _, err := runtime.Complete(ctx, "pending", vaultFixture()); !errors.Is(err, errCredentialFenced) {
		t.Fatal("interrupted operation resumed")
	}
	if len(manager.List()) != 1 {
		t.Fatal("interrupted operation published a credential")
	}
	if err := vault.Begin(ctx, "cancelled", "codex", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := vault.Cancel(ctx, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Complete(ctx, "cancelled", vaultFixture()); !errors.Is(err, errCredentialFenced) {
		t.Fatal("cancelled operation published a credential")
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	if vault.Admit(ctx, loaded) {
		t.Fatal("closed store admitted credential")
	}
}

func TestCredentialRuntimeConcurrentRemoval(t *testing.T) {
	ctx := context.Background()
	vault := newTestVault(t)
	manager := coreauth.NewManager(vault, nil, nil)
	runtime := &credentialRuntime{vault: vault, manager: manager}
	if err := vault.Begin(ctx, "connect", "codex", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	created, err := runtime.Complete(ctx, "connect", vaultFixture())
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Go(func() { <-start; _, _ = runtime.Complete(ctx, "connect", vaultFixture()) })
	group.Go(func() {
		<-start
		if err := runtime.Remove(ctx, created.ID); err != nil {
			t.Error(err)
		}
	})
	close(start)
	group.Wait()
	if _, ok := manager.GetByID(created.ID); ok {
		t.Fatal("completion resurrected removed runtime credential")
	}
	if vault.Admit(ctx, created) {
		t.Fatal("removed credential admitted")
	}
}
