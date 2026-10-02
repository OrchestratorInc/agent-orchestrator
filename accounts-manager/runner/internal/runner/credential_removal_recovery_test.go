package runner

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCredentialRemovalFenceSurvivesRestart(t *testing.T) {
	runtime, auth := trustedCredentialFixture(t, "codex")
	t.Cleanup(runtime.Close)
	vault := runtime.vault
	root := vault.root.Name()
	if err := vault.beginProviderLogin(t.Context(), "reconnect", auth.Provider, time.Now().Add(time.Minute), auth.ID, 1); err != nil {
		t.Fatal(err)
	}
	sealed := append([]byte(nil), vault.state.Records[auth.ID].Sealed...)
	if err := vault.beginRemoval(t.Context(), auth.ID); err != nil {
		t.Fatal(err)
	}
	for _, reopen := range []bool{false, true} {
		if reopen {
			if err := vault.Close(); err != nil {
				t.Fatal(err)
			}
			var err error
			vault, err = openCredentialVault(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = vault.Close() })
			runtime.vault = vault
			runtime.manager = coreauth.NewManager(vault, nil, nil)
			if err := runtime.Reload(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		if vault.Admit(t.Context(), auth) || vault.matchesCommitted(t.Context(), auth, false) || vault.admitsAccountWork(t.Context(), auth.ID) {
			t.Fatal("removing account remained admitted", reopen)
		}
		listed, err := vault.List(t.Context())
		if err != nil || len(listed) != 1 || listed[0].ID != auth.ID || listed[0].Index != auth.Index || !listed[0].Disabled {
			t.Fatal("retry could not resolve the fenced account", reopen, err)
		}
		if !bytes.Equal(sealed, vault.state.Records[auth.ID].Sealed) {
			t.Fatal("fencing erased or rewrote the credential before drain")
		}
		mutations := map[string]func() error{
			"save":         func() error { _, err := vault.Save(t.Context(), auth); return err },
			"enable":       func() error { return runtime.SetEnabled(t.Context(), auth.ID, true) },
			"disable":      func() error { return runtime.SetEnabled(t.Context(), auth.ID, false) },
			"label":        func() error { return runtime.SetLabel(t.Context(), auth.ID, "changed", 1) },
			"verification": func() error { return vault.recordVerification(t.Context(), auth, true) },
			"reconnect": func() error {
				return vault.beginProviderLogin(t.Context(), "new-reconnect", auth.Provider, time.Now().Add(time.Minute), auth.ID, 1)
			},
			"reconnect completion": func() error {
				_, err := runtime.complete(t.Context(), "reconnect", identityFixture("codex", "user-a", "account-a"), true)
				return err
			},
			"login replay": func() error {
				_, err := runtime.complete(t.Context(), "connect", identityFixture("codex", "user-a", "account-a"), true)
				return err
			},
		}
		for name, mutation := range mutations {
			if err := mutation(); !errors.Is(err, errCredentialFenced) {
				t.Error("mutation crossed removal fence", name, reopen, err)
			}
		}
		if generation, reconnect := vault.reconnectVersion(t.Context(), auth); generation != 0 || reconnect {
			t.Fatal("removing credential offered reconnect")
		}
		if verified, _ := vault.verification(t.Context(), auth); verified != "unverified" {
			t.Fatal("removing credential remained verified")
		}
		assertVaultHasNoSecret(t, root)
	}
	handler := &credentialHTTP{key: "fixture-management-key", runtime: runtime}
	for range 2 {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodDelete, credentialPath+"?ref="+auth.Index, http.NoBody)
		request.Header.Set("Authorization", "Bearer fixture-management-key")
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatal("private removal retry did not finish", response.Code)
		}
	}
	entry := vault.state.Records[auth.ID]
	if !entry.Deleted || len(entry.Sealed) != 0 || entry.Generation != 2 || vault.removingLocked(auth.ID) {
		t.Fatal("final removal did not atomically erase and retire the fence")
	}
}

func TestCredentialRemovalRejectsReservedOperations(t *testing.T) {
	runtime, auth := trustedCredentialFixture(t, "codex")
	t.Cleanup(runtime.Close)
	key := removalOperationID(auth.ID)
	for _, pending := range []bool{false, true} {
		if pending {
			if err := runtime.vault.beginRemoval(t.Context(), auth.ID); err != nil {
				t.Fatal(err)
			}
		}
		for _, err := range []error{
			runtime.vault.Begin(t.Context(), key, auth.Provider, time.Now().Add(time.Minute)),
			runtime.vault.beginProviderLogin(t.Context(), key, auth.Provider, time.Now().Add(time.Minute), "", 0),
			runtime.vault.Cancel(t.Context(), key),
		} {
			if !errors.Is(err, errCredentialConflict) {
				t.Fatal("ordinary operation reused or cancelled a removal reservation", pending, err)
			}
		}
		if runtime.vault.removingLocked(auth.ID) != pending {
			t.Fatal("ordinary operation changed the durable removal marker")
		}
	}
}

func TestCredentialRemovalRejectsDuplicateKeyAndReplay(t *testing.T) {
	vault := newTestVault(t)
	runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil)}
	t.Cleanup(runtime.Close)
	fixture := &coreauth.Auth{Provider: "codex", Attributes: map[string]string{"api_key": "test-removal-key"}}
	if err := vault.Begin(t.Context(), "key", fixture.Provider, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	auth, err := runtime.Complete(t.Context(), "key", fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.beginRemoval(t.Context(), auth.ID); err != nil {
		t.Fatal(err)
	}
	if err := vault.Begin(t.Context(), "duplicate", fixture.Provider, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"key", "duplicate"} {
		if _, err := runtime.Complete(t.Context(), operation, fixture); !errors.Is(err, errCredentialFenced) {
			t.Fatal("duplicate or replay re-enabled a removing credential", operation, err)
		}
	}
	listed, err := vault.List(t.Context())
	if err != nil || len(listed) != 1 || !listed[0].Disabled || listed[0].ID != auth.ID {
		t.Fatal("duplicate key created an implicit fallback", err)
	}
}

func TestCredentialRemovalCancellationBeforeFenceIsNoOp(t *testing.T) {
	runtime, auth := trustedCredentialFixture(t, "codex")
	t.Cleanup(runtime.Close)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runtime.Remove(ctx, auth.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-admission cancellation was ignored", err)
	}
	if runtime.vault.removingLocked(auth.ID) || !runtime.vault.Admit(t.Context(), auth) {
		t.Fatal("unadmitted removal changed the account")
	}
	if err := runtime.Remove(t.Context(), "missing-account"); err != nil || !runtime.vault.Admit(t.Context(), auth) {
		t.Fatal("missing-account retry affected an unrelated account", err)
	}
}

func TestCredentialRemovalRejectsIncompleteJournal(t *testing.T) {
	for name, corrupt := range map[string]func(*vaultOperation){
		"status":               func(op *vaultOperation) { op.Status = "cancelled" },
		"account":              func(op *vaultOperation) { op.AccountID = "" },
		"foreign account":      func(op *vaultOperation) { op.AccountID = "unrelated" },
		"provider":             func(op *vaultOperation) { op.Provider = "" },
		"zero generation":      func(op *vaultOperation) { op.ExpectedGeneration = 0 },
		"foreign generation":   func(op *vaultOperation) { op.ExpectedGeneration++ },
		"expires":              func(op *vaultOperation) { op.ExpiresAt = time.Now().UTC() },
		"fingerprint":          func(op *vaultOperation) { op.Fingerprint = "unexpected" },
		"login":                func(op *vaultOperation) { op.ProviderLogin = true },
		"target":               func(op *vaultOperation) { op.TargetID = op.AccountID },
		"expected fingerprint": func(op *vaultOperation) { op.ExpectedFingerprint = "unexpected" },
	} {
		t.Run(name, func(t *testing.T) {
			runtime, auth := trustedCredentialFixture(t, "codex")
			root := runtime.vault.root.Name()
			if err := runtime.vault.beginRemoval(t.Context(), auth.ID); err != nil {
				t.Fatal(err)
			}
			vault := runtime.vault
			vault.mu.Lock()
			next := vault.cloneLocked()
			key := removalOperationID(auth.ID)
			op := next.Operations[key]
			corrupt(&op)
			next.Operations[key] = op
			err := vault.persistLocked(next)
			vault.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := vault.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := openCredentialVault(root)
			if reopened != nil {
				_ = reopened.Close()
			}
			if !errors.Is(err, errCredentialStorage) {
				t.Fatal("incomplete removal proof was accepted", err)
			}
		})
	}
}

func TestCredentialRemovalPersistenceFailureKeepsCredential(t *testing.T) {
	for _, phase := range []string{"fence", "erase"} {
		t.Run(phase, func(t *testing.T) {
			runtime, auth := trustedCredentialFixture(t, "codex")
			t.Cleanup(runtime.Close)
			vault := runtime.vault
			if phase == "erase" {
				if err := vault.beginRemoval(t.Context(), auth.ID); err != nil {
					t.Fatal(err)
				}
			}
			root := vault.root.Name()
			path, saved := filepath.Join(root, vaultFileName), filepath.Join(root, "saved.vault")
			if err := os.Rename(path, saved); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := runtime.Remove(t.Context(), auth.ID); !errors.Is(err, errCredentialStorage) {
				t.Fatal("failed removal write was acknowledged", err)
			}
			if entry := vault.state.Records[auth.ID]; entry.Deleted || len(entry.Sealed) == 0 {
				t.Fatal("failed persistence lost recoverable credential")
			}
			if vault.removingLocked(auth.ID) != (phase == "erase") {
				t.Fatal("failed write published an uncommitted journal state")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(saved, path); err != nil {
				t.Fatal(err)
			}
			if err := runtime.Remove(t.Context(), auth.ID); err != nil {
				t.Fatal("removal could not retry after storage recovery", err)
			}
		})
	}
}

func TestCredentialRemovalCancelledDrainRetriesAfterRestart(t *testing.T) {
	runtime, auths := quotaRuntimeFixture(t, 1)
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var calls atomic.Int32
	runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		<-request.Context().Done()
		close(cancelled)
		<-release
		return successfulCredentialCheck(request)
	})
	result := make(chan error, 1)
	go func() { _, err := runtime.quota(t.Context(), auths[0]); result <- err }()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	removed := make(chan error, 1)
	go func() { removed <- runtime.Remove(ctx, auths[0].ID) }()
	<-cancelled
	cancel()
	if err := <-removed; !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled drain acknowledged credential erasure", err)
	}
	if !runtime.vault.removingLocked(auths[0].ID) || runtime.vault.state.Records[auths[0].ID].Deleted {
		t.Fatal("cancelled response lost the durable fence or erased too early")
	}
	for range 5 {
		if _, err := runtime.quota(t.Context(), auths[0]); !errors.Is(err, errCredentialFenced) {
			t.Fatal("new worker crossed the drain admission boundary", err)
		}
		if _, err := runtime.Refresh(t.Context(), auths[0].ID); !errors.Is(err, errCredentialFenced) {
			t.Fatal("new refresh crossed the drain admission boundary", err)
		}
		if _, err := runtime.recheckCredential(t.Context(), auths[0]); !errors.Is(err, errCredentialFenced) {
			t.Fatal("new recheck crossed the drain admission boundary", err)
		}
	}
	once.Do(func() { close(release) })
	if err := <-result; !errors.Is(err, errCredentialFenced) {
		t.Fatal("late provider response survived cancellation", err)
	}
	runtime.Close()
	root := runtime.vault.root.Name()
	if err := runtime.vault.Close(); err != nil {
		t.Fatal(err)
	}
	vault, err := openCredentialVault(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	restarted := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil), context: t.Context()}
	t.Cleanup(restarted.Close)
	if err := restarted.Reload(t.Context()); err != nil || vault.Admit(t.Context(), auths[0]) {
		t.Fatal("restart lost the removal fence", err)
	}
	results := make(chan error, 10)
	for range 10 {
		go func() { results <- restarted.Remove(t.Context(), auths[0].ID) }()
	}
	for range 10 {
		if err := <-results; err != nil {
			t.Fatal("concurrent removal retry failed", err)
		}
	}
	entry := vault.state.Records[auths[0].ID]
	if calls.Load() != 1 || !entry.Deleted || entry.Generation != 2 || len(entry.Sealed) != 0 {
		t.Fatal("restart retried a worker or rotated the deleted generation twice")
	}
}
