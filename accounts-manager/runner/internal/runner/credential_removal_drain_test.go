package runner

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCredentialRemovalDrainsAccountWorkers(t *testing.T) {
	for _, kind := range []string{"refresh", "quota", "recheck"} {
		t.Run(kind, func(t *testing.T) {
			startedA, cancelledA, releaseA := make(chan struct{}), make(chan struct{}), make(chan struct{})
			startedB, releaseB := make(chan struct{}), make(chan struct{})
			var releaseAOnce, releaseBOnce sync.Once
			var runtime *credentialRuntime
			var auths []*coreauth.Auth
			work := func(ctx context.Context, isA bool) error {
				if isA {
					close(startedA)
					<-ctx.Done()
					close(cancelledA)
					<-releaseA
					return ctx.Err()
				}
				close(startedB)
				select {
				case <-releaseB:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			var request func(context.Context, *coreauth.Auth) error
			if kind == "refresh" {
				runtime, auths = refreshRuntimeFixture(t, 2, func(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
					if err := work(ctx, auth.ID == auths[0].ID); err != nil {
						return nil, err
					}
					auth.Metadata["access_token"] = "test-drain-refreshed-token"
					return auth, nil
				})
				request = func(ctx context.Context, auth *coreauth.Auth) error {
					_, err := runtime.Refresh(ctx, auth.ID)
					return err
				}
			} else {
				runtime, auths = quotaRuntimeFixture(t, 2)
				runtime.checkTransport = runnerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					if err := work(r.Context(), r.Header.Get("ChatGPT-Account-Id") == "quota-account-0"); err != nil {
						return nil, err
					}
					return successfulCredentialCheck(r)
				})
				request = func(ctx context.Context, auth *coreauth.Auth) error { _, err := runtime.quota(ctx, auth); return err }
				if kind == "recheck" {
					request = func(ctx context.Context, auth *coreauth.Auth) error {
						_, err := runtime.recheckCredential(ctx, auth)
						return err
					}
				}
			}
			t.Cleanup(func() {
				releaseAOnce.Do(func() { close(releaseA) })
				releaseBOnce.Do(func() { close(releaseB) })
			})
			resultA, resultB := make(chan error, 1), make(chan error, 1)
			go func() { resultA <- request(t.Context(), auths[0]) }()
			go func() { resultB <- request(t.Context(), auths[1]) }()
			<-startedA
			<-startedB
			removed := make(chan error, 1)
			go func() { removed <- runtime.Remove(t.Context(), auths[0].ID) }()
			returned := false
			select {
			case <-cancelledA:
				select {
				case err := <-removed:
					returned = true
					t.Error("removal acknowledged before the account worker drained", err)
				default:
				}
			case err := <-removed:
				returned = true
				t.Error("removal returned without cancelling and joining the account worker", err)
			case <-time.After(time.Second):
				t.Error("removal did not cancel the account worker")
			}
			retained, err := runtime.vault.List(t.Context())
			if err != nil || len(retained) != 2 {
				t.Error("credential erased before the account worker drained", err)
			}
			if runtime.vault.Admit(t.Context(), auths[0]) {
				t.Error("removing account remained authorized during drain")
			}
			releaseBOnce.Do(func() { close(releaseB) })
			if err := <-resultB; err != nil {
				t.Error("removing A interrupted account B", err)
			}
			releaseAOnce.Do(func() { close(releaseA) })
			if !returned {
				if err := <-removed; err != nil {
					t.Error("drained removal failed", err)
				}
			}
			if err := <-resultA; !errors.Is(err, errCredentialFenced) {
				t.Error("removed account worker reported success", err)
			}
			remaining, err := runtime.vault.List(t.Context())
			if err != nil || len(remaining) != 1 || remaining[0].ID != auths[1].ID {
				t.Fatal("removal did not preserve exactly account B", err)
			}
		})
	}
}

func TestCredentialRemovalJoinsSupersededQuotaWorker(t *testing.T) {
	runtime, auths := quotaRuntimeFixture(t, 1)
	started := []chan struct{}{make(chan struct{}), make(chan struct{})}
	cancelled := []chan struct{}{make(chan struct{}), make(chan struct{})}
	release := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var once [2]sync.Once
	t.Cleanup(func() {
		for i := range release {
			once[i].Do(func() { close(release[i]) })
		}
	})
	var calls atomic.Int32
	runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		index := int(calls.Add(1)) - 1
		if index >= len(started) {
			return nil, errors.New("unexpected request after drain fence")
		}
		close(started[index])
		<-request.Context().Done()
		close(cancelled[index])
		<-release[index]
		return successfulCredentialCheck(request)
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := runtime.quota(ctx, auths[0]); first <- err }()
	<-started[0]
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	<-cancelled[0]
	second := make(chan error, 1)
	go func() { _, err := runtime.quota(t.Context(), auths[0]); second <- err }()
	<-started[1]
	runtime.quotaMu.Lock()
	current := runtime.quotas[auths[0].ID]
	runtime.quotaMu.Unlock()
	removeCtx, stopRemove := context.WithCancel(t.Context())
	defer stopRemove()
	removed := make(chan error, 1)
	go func() { removed <- runtime.Remove(removeCtx, auths[0].ID) }()
	<-cancelled[1]
	once[1].Do(func() { close(release[1]) })
	<-current.done
	stopRemove()
	if err := <-removed; !errors.Is(err, context.Canceled) {
		t.Error("removal skipped the abandoned worker outside the current cache", err)
	}
	if err := <-second; !errors.Is(err, errCredentialFenced) {
		t.Error("newer usage worker published after removal", err)
	}
	once[0].Do(func() { close(release[0]) })
	if err := runtime.Remove(t.Context(), auths[0].ID); err != nil {
		t.Fatal("removal did not recover after the older worker drained", err)
	}
	if calls.Load() != 2 {
		t.Fatal("drain started an extra upstream request")
	}
}

func TestCredentialRemovalShutdownCancelsAllBeforeJoin(t *testing.T) {
	runtime, auths := quotaRuntimeFixture(t, 3)
	started := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	cancelled := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	work := func(ctx context.Context, index int) error {
		close(started[index])
		<-ctx.Done()
		close(cancelled[index])
		<-release
		return ctx.Err()
	}
	runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		index := 0
		if request.Header.Get("ChatGPT-Account-Id") == "quota-account-1" {
			index = 1
		}
		return nil, work(request.Context(), index)
	})
	runtime.manager.RegisterExecutor(credentialRefreshExecutor{refresh: func(ctx context.Context, _ *coreauth.Auth) (*coreauth.Auth, error) {
		return nil, work(ctx, 2)
	}})
	results := make(chan error, 3)
	go func() { _, err := runtime.recheckCredential(t.Context(), auths[0]); results <- err }()
	go func() { _, err := runtime.quota(t.Context(), auths[1]); results <- err }()
	go func() { _, err := runtime.Refresh(t.Context(), auths[2].ID); results <- err }()
	for _, ready := range started {
		<-ready
	}
	closed := make(chan struct{})
	go func() { runtime.Close(); close(closed) }()
	for index, stopped := range cancelled {
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Error("shutdown waited for one worker before cancelling another kind", index)
		}
	}
	once.Do(func() { close(release) })
	<-closed
	for range 3 {
		if err := <-results; err == nil {
			t.Error("shutdown published a successful worker result")
		}
	}
	if _, err := runtime.recheckCredential(t.Context(), auths[0]); !errors.Is(err, errCredentialFenced) {
		t.Fatal("shutdown admitted a manual recheck", err)
	}
}

type removalAdmissionBarrier struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (b *removalAdmissionBarrier) MarshalJSON() ([]byte, error) {
	if b.calls.Add(1) == 2 {
		close(b.entered)
		<-b.release
	}
	return []byte(`"stable"`), nil
}

func TestCredentialRemovalRejectsPreviouslyValidatedQuota(t *testing.T) {
	runtime, auths := quotaRuntimeFixture(t, 1)
	auth := auths[0].Clone()
	auth.Metadata["admission_schedule"] = "stable"
	if _, err := runtime.vault.Save(t.Context(), auth); err != nil {
		t.Fatal(err)
	}
	if err := runtime.vault.recordVerification(t.Context(), auth, true); err != nil {
		t.Fatal(err)
	}
	barrier := &removalAdmissionBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(barrier.release) }) })
	// The second marshal is fingerprinting, after validation and outside the vault lock.
	auth.Metadata["admission_schedule"] = barrier
	var calls atomic.Int32
	runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return successfulCredentialCheck(request)
	})
	result := make(chan error, 1)
	go func() { _, err := runtime.quota(t.Context(), auth); result <- err }()
	select {
	case <-barrier.entered:
	case err := <-result:
		t.Fatal("fixture did not reach the admitted request schedule", err)
	}
	if err := runtime.Remove(t.Context(), auth.ID); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(barrier.release) })
	if err := <-result; !errors.Is(err, errCredentialFenced) || calls.Load() != 0 {
		t.Fatal("a previously validated request reached the provider after removal", calls.Load(), err)
	}
}
