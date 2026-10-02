package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type credentialRefreshExecutor struct {
	coreauth.ProviderExecutor
	refresh func(context.Context, *coreauth.Auth) (*coreauth.Auth, error)
}

func (credentialRefreshExecutor) Identifier() string { return "codex" }
func (e credentialRefreshExecutor) Refresh(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return e.refresh(ctx, auth)
}

type joinedRefreshContext struct {
	context.Context
	joined chan struct{}
	once   sync.Once
}

func (c *joinedRefreshContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return c.Context.Done()
}

func refreshRuntimeFixture(t *testing.T, count int, refresh func(context.Context, *coreauth.Auth) (*coreauth.Auth, error)) (*credentialRuntime, []*coreauth.Auth) {
	t.Helper()
	vault := newTestVault(t)
	runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil), context: t.Context()}
	runtime.manager.RegisterExecutor(credentialRefreshExecutor{refresh: refresh})
	t.Cleanup(runtime.Close)
	auths := make([]*coreauth.Auth, 0, count)
	for i := 0; i < count; i++ {
		operation := fmt.Sprintf("refresh-%d", i)
		if err := vault.Begin(t.Context(), operation, "codex", time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		auth, err := runtime.Complete(t.Context(), operation, vaultFixture())
		if err != nil {
			t.Fatal(err)
		}
		auths = append(auths, auth)
	}
	return runtime, auths
}

func TestCredentialRefreshCoalescesAndCancelsWaiter(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	runtime, auths := refreshRuntimeFixture(t, 1, func(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		auth.Metadata["access_token"] = "refreshed-vault-secret"
		return auth, nil
	})
	first := make(chan error, 1)
	go func() { _, err := runtime.Refresh(t.Context(), auths[0].ID); first <- err }()
	<-started
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		ctx := &joinedRefreshContext{Context: t.Context(), joined: make(chan struct{})}
		go func() { _, err := runtime.Refresh(ctx, auths[0].ID); results <- err }()
		<-ctx.joined
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancelled := &joinedRefreshContext{Context: ctx, joined: make(chan struct{})}
	cancelResult := make(chan error, 1)
	go func() { _, err := runtime.Refresh(cancelled, auths[0].ID); cancelResult <- err }()
	<-cancelled.joined
	cancel()
	if !errors.Is(<-cancelResult, context.Canceled) {
		t.Fatal("caller cancellation was ignored")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("overlapping checks repeated provider refresh")
	}
	auth, _ := runtime.manager.GetByID(auths[0].ID)
	if !runtime.vault.Admit(t.Context(), auth) || auth.Metadata["access_token"] != "refreshed-vault-secret" {
		t.Fatal("refresh result was not durable")
	}
}

func TestCredentialRefreshBoundsIndependentChecks(t *testing.T) {
	started, release := make(chan struct{}, 5), make(chan struct{})
	var active, maximum atomic.Int32
	runtime, auths := refreshRuntimeFixture(t, 5, func(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); current > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		auth.Metadata["access_token"] = "refreshed-" + auth.ID
		return auth, nil
	})
	results := make(chan error, 5)
	for _, auth := range auths {
		go func() { _, err := runtime.Refresh(t.Context(), auth.ID); results <- err }()
	}
	for i := 0; i < 4; i++ {
		<-started
	}
	close(release)
	for i := 0; i < 5; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if maximum.Load() != 4 {
		t.Fatalf("independent refresh concurrency=%d", maximum.Load())
	}
}

func TestCredentialRefreshFencesLateResults(t *testing.T) {
	for _, outcome := range []string{"remove", "disable", "shutdown", "provider error"} {
		t.Run(outcome, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			runtime, auths := refreshRuntimeFixture(t, 1, func(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
				close(started)
				select {
				case <-ctx.Done():
				case <-release:
				}
				if outcome == "provider error" {
					return nil, errors.New("private-vault-secret")
				}
				auth.Metadata["access_token"] = "late-vault-secret"
				return auth, nil
			})
			result := make(chan error, 1)
			go func() { _, err := runtime.Refresh(t.Context(), auths[0].ID); result <- err }()
			<-started
			switch outcome {
			case "remove":
				if err := runtime.Remove(t.Context(), auths[0].ID); err != nil {
					t.Fatal(err)
				}
			case "disable":
				if err := runtime.SetEnabled(t.Context(), auths[0].ID, false); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				runtime.Close()
			}
			close(release)
			if err := <-result; !errors.Is(err, errCredentialFenced) {
				t.Fatal("late or failed refresh was acknowledged")
			}
			auths, err := runtime.vault.List(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, auth := range auths {
				if auth.Metadata["access_token"] == "late-vault-secret" {
					t.Fatal("late refresh was committed")
				}
			}
		})
	}
}

func TestCredentialRefreshReturnsWhenRuntimeContextEnds(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	runtime, auths := refreshRuntimeFixture(t, 1, func(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
		close(started)
		<-release
		auth.Metadata["access_token"] = "late-vault-secret"
		return auth, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runtime.context = ctx
	result := make(chan error, 1)
	go func() { _, err := runtime.Refresh(t.Context(), auths[0].ID); result <- err }()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, errCredentialFenced) {
			t.Error("cancelled runtime refresh acknowledged")
		}
	case <-time.After(time.Second):
		t.Error("caller waited for an uncooperative refresh after cancellation")
	}
	close(release)
	runtime.Close()
	committed, _ := runtime.vault.List(t.Context())
	if committed[0].Metadata["access_token"] == "late-vault-secret" {
		t.Fatal("late cancelled result committed")
	}
}

func TestCredentialRefreshCannotOverwriteNewRegistration(t *testing.T) {
	for _, change := range []string{"rename", "reconnect"} {
		t.Run(change, func(t *testing.T) {
			runtime, previous := trustedCredentialFixture(t, "codex")
			runtime.context = t.Context()
			t.Cleanup(runtime.Close)
			started, release := make(chan struct{}), make(chan struct{})
			runtime.manager.RegisterExecutor(credentialRefreshExecutor{refresh: func(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
				close(started)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-release:
				}
				auth.Metadata["access_token"] = "stale-vault-secret"
				return auth, nil
			}})
			result := make(chan error, 1)
			go func() { _, err := runtime.Refresh(t.Context(), previous.ID); result <- err }()
			<-started
			if change == "rename" {
				if err := runtime.SetLabel(t.Context(), previous.ID, "Production", 1); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := runtime.vault.beginProviderLogin(t.Context(), "replace", "codex", time.Now().Add(time.Minute), previous.ID, 1); err != nil {
					t.Fatal(err)
				}
				updated := identityFixture("codex", "user-a", "account-a")
				updated.Metadata["access_token"] = "replacement-vault-secret"
				if _, err := runtime.complete(t.Context(), "replace", updated, true); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			if !errors.Is(<-result, errCredentialFenced) {
				t.Fatal("superseded refresh acknowledged")
			}
			current, exists := runtime.manager.GetByID(previous.ID)
			if !exists || !runtime.vault.Admit(t.Context(), current) || current.Metadata["access_token"] == "stale-vault-secret" {
				t.Fatal("late SDK writer replaced the new runtime registration")
			}
		})
	}
}
