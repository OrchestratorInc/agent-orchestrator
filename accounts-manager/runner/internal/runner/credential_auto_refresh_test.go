package runner

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCredentialAutoRefreshServeJoinsBeforeVaultClosure(t *testing.T) {
	stateDir, _ := validStateFixture(t)
	port := reservePort(t)
	replaceInFile(t, filepath.Join(stateDir, configFileName), "43127", strconv.Itoa(port))
	vault, err := openCredentialVault(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Begin(t.Context(), "refresh-close", "codex", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	auth := vaultFixture()
	auth.Metadata["expired"] = time.Now().Add(-time.Minute).Format(time.RFC3339)
	if _, err := vault.Commit(t.Context(), "refresh-close", auth); err != nil {
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	for attempt := range 2 {
		started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- serve(ctx, stateDir, func(manager *coreauth.Manager) {
				manager.RegisterExecutor(credentialRefreshExecutor{refresh: func(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
					close(started)
					<-ctx.Done()
					close(cancelled)
					<-release
					auth.Metadata["access_token"] = "late-vault-secret"
					return auth, nil
				}})
			})
		}()
		select {
		case <-started:
		case err := <-done:
			cancel()
			close(release)
			t.Fatalf("runner exited before refresh on restart %d: %v", attempt, err)
		case <-time.After(5 * time.Second):
			cancel()
			close(release)
			<-done
			t.Fatal("automatic refresh did not start")
		}
		waitForRunnerHealth(t, "http://127.0.0.1:"+strconv.Itoa(port))
		cancel()
		<-cancelled
		returned := false
		select {
		case err := <-done:
			returned = true
			t.Errorf("runner returned before automatic refresh worker completed: %v", err)
		case <-time.After(time.Second):
		}
		opened, openErr := openCredentialVault(stateDir)
		if openErr == nil {
			_ = opened.Close()
			t.Error("vault closed before automatic refresh pipeline drained")
		}
		close(release)
		if !returned {
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("runner did not join completed automatic refresh")
			}
		}
		reopened, err := openCredentialVault(stateDir)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := reopened.List(t.Context())
		_ = reopened.Close()
		if err != nil || len(stored) != 1 || stored[0].Metadata["access_token"] == "late-vault-secret" {
			t.Fatal("cancelled automatic result changed durable credentials", err)
		}
	}
}

type refreshPersistenceGate struct {
	coreauth.Store
	enabled   atomic.Bool
	entered   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
}

func (s *refreshPersistenceGate) Save(ctx context.Context, auth *coreauth.Auth) (string, error) {
	if s.enabled.Load() {
		close(s.entered)
		<-ctx.Done()
		close(s.cancelled)
		<-s.release
	}
	return s.Store.Save(ctx, auth)
}

func TestCredentialAutoRefreshJoinsPersistence(t *testing.T) {
	runtime, _ := refreshRuntimeFixture(t, 0, nil)
	store := &refreshPersistenceGate{Store: runtime.vault, entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	runtime.manager = coreauth.NewManager(store, nil, nil)
	var calls atomic.Int32
	runtime.manager.RegisterExecutor(credentialRefreshExecutor{refresh: func(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
		calls.Add(1)
		auth.Metadata["access_token"] = "late-vault-secret"
		return auth, nil
	}})
	if err := runtime.vault.Begin(t.Context(), "auto-persist", "codex", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	auth, err := runtime.Complete(t.Context(), "auto-persist", vaultFixture())
	if err != nil {
		t.Fatal(err)
	}
	store.enabled.Store(true)
	runtime.startAutoRefresh(t.Context())
	select {
	case <-store.entered:
	case <-time.After(5 * time.Second):
		close(store.release)
		t.Fatal("automatic refresh did not reach persistence")
	}
	waiter := &joinedRefreshContext{Context: t.Context(), joined: make(chan struct{})}
	waitResult := make(chan error, 1)
	go func() { _, err := runtime.Refresh(waiter, auth.ID); waitResult <- err }()
	<-waiter.joined
	closed := make(chan struct{})
	go func() { runtime.Close(); close(closed) }()
	<-store.cancelled
	select {
	case <-closed:
		t.Error("close returned while persistence was still blocked")
	default:
	}
	close(store.release)
	<-closed
	if !errors.Is(<-waitResult, errCredentialFenced) || calls.Load() != 1 {
		t.Fatal("manual check did not share the cancelled automatic refresh")
	}
	runtime.startAutoRefresh(t.Context())
	if _, err := runtime.Refresh(t.Context(), auth.ID); !errors.Is(err, errCredentialFenced) {
		t.Fatal("closed runtime admitted a new refresh")
	}
	stored, err := runtime.vault.List(t.Context())
	if err != nil || len(stored) != 1 || stored[0].Metadata["access_token"] == "late-vault-secret" {
		t.Fatal("cancelled persistence changed the vault", err)
	}
}

func TestCredentialAutoRefreshEligibility(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name   string
		change func(*coreauth.Auth)
		want   bool
	}{
		{"expired", func(a *coreauth.Auth) { a.Metadata["expired"] = now.Add(-time.Minute).Format(time.RFC3339) }, true},
		{"fresh", func(a *coreauth.Auth) { a.Metadata["expired"] = now.Add(365 * 24 * time.Hour).Format(time.RFC3339) }, false},
		{"disabled", func(a *coreauth.Auth) { a.Disabled = true }, false},
		{"no-refresh-token", func(a *coreauth.Auth) { delete(a.Metadata, "refresh_token") }, false},
		{"backoff", func(a *coreauth.Auth) { a.NextRefreshAfter = now.Add(time.Minute) }, false},
		{"reauth-required", func(a *coreauth.Auth) {
			a.Unavailable, a.Status, a.LastError = true, coreauth.StatusError, &coreauth.Error{Code: "unauthorized"}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := vaultFixture()
			tc.change(auth)
			if got := credentialNeedsRefresh(auth, now); got != tc.want {
				t.Fatalf("refresh eligibility=%v, want %v", got, tc.want)
			}
		})
	}
}
