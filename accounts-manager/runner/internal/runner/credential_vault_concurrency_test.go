package runner

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCredentialVaultCancellationBeforeStart(t *testing.T) {
	ctx := context.Background()
	root := privateVaultDir(t)
	vault, err := openCredentialVault(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Cancel(ctx, "cancel-before-start"); err != nil {
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	vault, err = openCredentialVault(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	if err := vault.Begin(ctx, "cancel-before-start", "codex", time.Now().Add(time.Minute)); !errors.Is(err, errCredentialConflict) {
		t.Fatalf("cancelled operation restarted: %v", err)
	}
	if err := vault.Cancel(ctx, "../invalid"); !errors.Is(err, errCredentialConflict) {
		t.Fatalf("invalid cancellation accepted: %v", err)
	}
}

func TestCredentialVaultConcurrentCompletionAndCancellation(t *testing.T) {
	ctx := context.Background()
	vault := newTestVault(t)
	committed := 0
	for i := range 24 {
		id := fmt.Sprintf("race-%d", i)
		if err := vault.Begin(ctx, id, "codex", time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			<-start
			done <- vault.Cancel(ctx, id)
		}()
		close(start)
		created, commitErr := vault.Commit(ctx, id, vaultFixture())
		cancelErr := <-done
		if commitErr == nil {
			committed++
			if created == nil || !errors.Is(cancelErr, errCredentialCommitted) {
				t.Fatalf("committed result misreported: %v", cancelErr)
			}
		} else if !errors.Is(commitErr, errCredentialFenced) || cancelErr != nil {
			t.Fatalf("unexpected race result: commit=%v cancel=%v", commitErr, cancelErr)
		}
	}
	items, err := vault.List(ctx)
	if err != nil || len(items) != committed {
		t.Fatalf("cancelled credentials were published: count=%d expected=%d err=%v", len(items), committed, err)
	}
}

func TestCredentialVaultConcurrentDeletionAndRefresh(t *testing.T) {
	ctx := context.Background()
	root := privateVaultDir(t)
	vault, err := openCredentialVault(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	var deleted []*coreauth.Auth
	for i := range 24 {
		id := fmt.Sprintf("refresh-%d", i)
		if err := vault.Begin(ctx, id, "codex", time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		created, err := vault.Commit(ctx, id, vaultFixture())
		if err != nil {
			t.Fatal(err)
		}
		deleted = append(deleted, created)
		start := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			<-start
			_, err := vault.Save(ctx, created)
			done <- err
		}()
		close(start)
		if err := vault.Delete(ctx, created.ID); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil && !errors.Is(err, errCredentialFenced) {
			t.Fatal(err)
		}
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	vault, err = openCredentialVault(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, auth := range deleted {
		if _, err := vault.Save(ctx, auth); !errors.Is(err, errCredentialFenced) {
			t.Fatalf("deleted credential accepted after restart: %v", err)
		}
	}
	items, err := vault.List(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("deleted credential resurrected: count=%d err=%v", len(items), err)
	}
	assertVaultHasNoSecret(t, root)
}
