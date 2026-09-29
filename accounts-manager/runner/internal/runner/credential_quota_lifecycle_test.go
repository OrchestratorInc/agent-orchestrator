package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func quotaRuntimeFixture(t *testing.T, count int) (*credentialRuntime, []*coreauth.Auth) {
	t.Helper()
	vault := newTestVault(t)
	runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil), context: t.Context()}
	t.Cleanup(runtime.Close)
	auths := make([]*coreauth.Auth, 0, count)
	for index := range count {
		operation := fmt.Sprintf("quota-lifetime-%d", index)
		if err := vault.Begin(t.Context(), operation, "codex", time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		fixture := vaultFixture()
		fixture.Metadata["access_token"] = fmt.Sprintf("quota-fixture-token-%d", index)
		fixture.Metadata["account_id"] = fmt.Sprintf("quota-account-%d", index)
		auth, err := runtime.completeObserved(t.Context(), operation, fixture, false, true)
		if err != nil {
			t.Fatal(err)
		}
		auths = append(auths, auth)
	}
	return runtime, auths
}

func TestCredentialQuotaFirstCallerCancellationPreservesWaiter(t *testing.T) {
	runtime, auths := quotaRuntimeFixture(t, 1)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var calls atomic.Int32
	runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return successfulCredentialCheck(request)
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	})
	firstContext, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	first := make(chan error, 1)
	go func() { _, err := runtime.quota(firstContext, auths[0]); first <- err }()
	<-started
	secondContext := &joinedRefreshContext{Context: t.Context(), joined: make(chan struct{})}
	second := make(chan error, 1)
	go func() {
		quota, err := runtime.quota(secondContext, auths[0])
		if err == nil && (len(quota.Groups) != 1 || len(quota.Groups[0].Buckets) != 1 || quota.Groups[0].Buckets[0].RemainingFraction != .75) {
			err = errors.New("surviving viewer lost its observed usage")
		}
		second <- err
	}()
	<-secondContext.joined
	cancelFirst()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Error("first caller cancellation was not local to that caller", err)
	}
	once.Do(func() { close(release) })
	if err := <-second; err != nil {
		t.Error("first caller cancellation poisoned a live viewer", err)
	}
	if calls.Load() != 1 {
		t.Fatal("surviving viewer repeated the shared provider request", calls.Load())
	}
}

func TestCredentialQuotaCloseJoinsBlockedTransport(t *testing.T) {
	runtime, auths := quotaRuntimeFixture(t, 1)
	parent, cancelParent := context.WithCancel(t.Context())
	defer cancelParent()
	runtime.context = parent
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		close(cancelled)
		<-release
		return nil, request.Context().Err()
	})
	result := make(chan error, 1)
	go func() { _, err := runtime.quota(t.Context(), auths[0]); result <- err }()
	<-started
	closed := make(chan struct{})
	go func() { runtime.Close(); close(closed) }()
	returned := false
	select {
	case <-closed:
		returned = true
		t.Error("runtime closed before the quota transport worker drained")
		cancelParent()
		<-cancelled
	case <-cancelled:
		select {
		case <-closed:
			returned = true
			t.Error("runtime acknowledged close before transport cleanup")
		default:
		}
	case <-time.After(time.Second):
		cancelParent()
		<-cancelled
		t.Error("close did not cancel the quota transport")
	}
	once.Do(func() { close(release) })
	if !returned {
		<-closed
	}
	if err := <-result; err == nil {
		t.Error("closed runtime published a quota result")
	}
	if _, err := runtime.quota(t.Context(), auths[0]); !errors.Is(err, errCredentialFenced) {
		t.Error("closed runtime admitted another usage request", err)
	}
}

func TestCredentialQuotaDeadlineDoesNotFenceAccount(t *testing.T) {
	runtime, auths := quotaRuntimeFixture(t, 1)
	runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	_, err := runtime.quota(t.Context(), auths[0])
	response := httptest.NewRecorder()
	writeCredentialQuotaError(response, err)
	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("usage deadline reported an account lifecycle conflict: HTTP %d, %v", response.Code, err)
	}
	if !runtime.vault.admitVerified(t.Context(), auths[0]) {
		t.Fatal("usage timeout changed account authorization")
	}
}

func TestCredentialQuotaLastWaiterCancelsWithoutPoisoningNextRead(t *testing.T) {
	runtime, auths := quotaRuntimeFixture(t, 1)
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var calls atomic.Int32
	runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-request.Context().Done()
			close(cancelled)
			<-release
		}
		return successfulCredentialCheck(request)
	})
	firstContext, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	first := make(chan error, 1)
	go func() { _, err := runtime.quota(firstContext, auths[0]); first <- err }()
	<-started
	secondContext, cancelSecond := context.WithCancel(t.Context())
	defer cancelSecond()
	joined := &joinedRefreshContext{Context: secondContext, joined: make(chan struct{})}
	second := make(chan error, 1)
	go func() { _, err := runtime.quota(joined, auths[0]); second <- err }()
	<-joined.joined
	cancelFirst()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
		t.Fatal("upstream cancelled while a live viewer remained")
	default:
	}
	cancelSecond()
	if err := <-second; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("last viewer left an active provider check")
	}
	if _, err := runtime.quota(t.Context(), auths[0]); err != nil {
		t.Fatal("new viewer joined the cancelled request", err)
	}
	once.Do(func() { close(release) })
	runtime.quotaWorkers.Wait()
	if _, err := runtime.quota(t.Context(), auths[0]); err != nil || calls.Load() != 2 {
		t.Fatal("late cancelled completion replaced the newer snapshot", err, calls.Load())
	}
}

func TestCredentialQuotaAccountMutationPreservesOtherAccount(t *testing.T) {
	for _, action := range []string{"remove", "disable"} {
		t.Run(action, func(t *testing.T) {
			runtime, auths := quotaRuntimeFixture(t, 2)
			startedA, startedB := make(chan struct{}), make(chan struct{})
			cancelledA, releaseB := make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(releaseB) }) })
			var callsA, callsB atomic.Int32
			runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("ChatGPT-Account-Id") == "quota-account-0" {
					callsA.Add(1)
					close(startedA)
					<-request.Context().Done()
					close(cancelledA)
					return nil, request.Context().Err()
				}
				if request.Header.Get("ChatGPT-Account-Id") != "quota-account-1" {
					t.Error("usage request selected an unexpected account")
				}
				callsB.Add(1)
				close(startedB)
				select {
				case <-releaseB:
					return successfulCredentialCheck(request)
				case <-request.Context().Done():
					return nil, request.Context().Err()
				}
			})
			resultA, resultB := make(chan error, 1), make(chan error, 1)
			go func() { _, err := runtime.quota(t.Context(), auths[0]); resultA <- err }()
			go func() { _, err := runtime.quota(t.Context(), auths[1]); resultB <- err }()
			<-startedA
			<-startedB
			var err error
			if action == "remove" {
				err = runtime.Remove(t.Context(), auths[0].ID)
			} else {
				err = runtime.SetEnabled(t.Context(), auths[0].ID, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-cancelledA:
			case <-time.After(time.Second):
				t.Fatal("account mutation left its provider check active")
			}
			if err := <-resultA; !errors.Is(err, errCredentialFenced) {
				t.Fatal("mutated account published an observation", err)
			}
			once.Do(func() { close(releaseB) })
			if err := <-resultB; err != nil {
				t.Fatal("A-only mutation cancelled account B", err)
			}
			if _, err := runtime.quota(t.Context(), auths[0]); !errors.Is(err, errCredentialFenced) {
				t.Fatal("old A admission survived account mutation", err)
			}
			if _, err := runtime.quota(t.Context(), auths[1]); err != nil || callsB.Load() != 1 || callsA.Load() != 1 {
				t.Fatal("A-only mutation damaged B's cache or caused fallback", err)
			}
		})
	}
}

func TestCredentialQuotaNewFingerprintRejectsLateOldResult(t *testing.T) {
	runtime, auths := quotaRuntimeFixture(t, 1)
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var oldCalls, newCalls atomic.Int32
	runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") == "Bearer quota-fixture-token-0" {
			oldCalls.Add(1)
			close(started)
			<-request.Context().Done()
			close(cancelled)
			<-release
			return successfulCredentialCheck(request)
		}
		if request.Header.Get("Authorization") != "Bearer quota-new-fixture-token" {
			t.Error("wrong generation reached quota provider")
		}
		newCalls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: request,
			Body: io.NopCloser(strings.NewReader(`{"rate_limit":{"primary_window":{"used_percent":10}}}`))}, nil
	})
	oldResult := make(chan error, 1)
	go func() { _, err := runtime.quota(t.Context(), auths[0]); oldResult <- err }()
	<-started
	updated := auths[0].Clone()
	updated.Metadata["access_token"] = "quota-new-fixture-token"
	if _, err := runtime.vault.Save(t.Context(), updated); err != nil {
		t.Fatal(err)
	}
	if err := runtime.vault.recordVerification(t.Context(), updated, true); err != nil {
		t.Fatal(err)
	}
	updated, err := runtime.manager.Register(t.Context(), updated)
	if err != nil {
		t.Fatal(err)
	}
	quota, err := runtime.quota(t.Context(), updated)
	if err != nil || len(quota.Groups) != 1 || quota.Groups[0].Buckets[0].RemainingFraction != .9 {
		t.Fatal("new credential generation did not receive its own observation", err)
	}
	<-cancelled
	if err := <-oldResult; !errors.Is(err, errCredentialFenced) {
		t.Fatal("old credential generation published an observation", err)
	}
	once.Do(func() { close(release) })
	runtime.quotaWorkers.Wait()
	cached, err := runtime.quota(t.Context(), updated)
	if err != nil || len(cached.Groups) != 1 || cached.Groups[0].Buckets[0].RemainingFraction != .9 || oldCalls.Load() != 1 || newCalls.Load() != 1 {
		t.Fatal("late old completion replaced newer credential observation", err)
	}
}

func TestCredentialQuotaPreservesFourCheckLimit(t *testing.T) {
	runtime, auths := quotaRuntimeFixture(t, 6)
	started, release := make(chan struct{}, len(auths)), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var active, maximum atomic.Int32
	runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for prior := maximum.Load(); current > prior; prior = maximum.Load() {
			if maximum.CompareAndSwap(prior, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
			return successfulCredentialCheck(request)
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	})
	results := make(chan error, len(auths))
	for _, auth := range auths {
		joined := &joinedRefreshContext{Context: t.Context(), joined: make(chan struct{})}
		go func() { _, err := runtime.quota(joined, auth); results <- err }()
		<-joined.joined
	}
	for range 4 {
		<-started
	}
	runtime.checkMu.Lock()
	capacity := cap(runtime.checkSlots)
	runtime.checkMu.Unlock()
	once.Do(func() { close(release) })
	for range auths {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if capacity != 4 || maximum.Load() != 4 {
		t.Fatal("shared usage workers bypassed the provider-check budget", capacity, maximum.Load())
	}
}

func TestCredentialQuotaServeJoinsBeforeVaultClosureAndRestarts(t *testing.T) {
	stateDir, _ := validStateFixture(t)
	port := reservePort(t)
	replaceInFile(t, filepath.Join(stateDir, configFileName), "43127", strconv.Itoa(port))
	state, err := LoadState(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := openCredentialVault(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	fixture := vaultFixture()
	fixture.Metadata["expired"] = time.Now().Add(365 * 24 * time.Hour).Format(time.RFC3339)
	fixture.LastRefreshedAt = time.Now()
	if err := vault.Begin(t.Context(), "quota-restart", "codex", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	auth, err := vault.Commit(t.Context(), "quota-restart", fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.recordVerification(t.Context(), auth, true); err != nil {
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	for attempt := range 2 {
		started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		var serveErr error
		var requestDone chan struct{}
		var requestErr error
		t.Cleanup(func() {
			cancel()
			once.Do(func() { close(release) })
			<-done
			if requestDone != nil {
				<-requestDone
			}
		})
		go func() {
			serveErr = serveWithCredentialSetup(ctx, stateDir, nil, func(runtime *credentialRuntime) {
				runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
					close(started)
					<-request.Context().Done()
					close(cancelled)
					<-release
					return successfulCredentialCheck(request)
				})
			})
			close(done)
		}()
		baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
		waitForRunnerHealth(t, baseURL)
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+credentialPath+"/quota?ref="+auth.Index, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+state.ManagementKey)
		requestDone = make(chan struct{})
		go func() {
			client := &http.Client{Timeout: 5 * time.Second}
			response, err := client.Do(request)
			if err == nil {
				_, err = io.Copy(io.Discard, response.Body)
				err = errors.Join(err, response.Body.Close())
				if response.StatusCode == http.StatusOK {
					err = errors.New("cancelled runner published a late observation")
				}
			}
			requestErr = err
			close(requestDone)
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			cancel()
			once.Do(func() { close(release) })
			<-done
			t.Fatalf("quota worker did not start on restart %d", attempt)
		}
		cancel()
		<-cancelled
		select {
		case <-done:
			once.Do(func() { close(release) })
			t.Fatalf("runner returned before quota cleanup on restart %d: %v", attempt, serveErr)
		default:
		}
		opened, openErr := openCredentialVault(stateDir)
		if openErr == nil {
			_ = opened.Close()
			t.Error("runner released the vault while quota cleanup was blocked")
		}
		once.Do(func() { close(release) })
		select {
		case <-done:
			if serveErr != nil {
				t.Fatal(serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("runner did not join released quota worker")
		}
		<-requestDone
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		reopened, err := openCredentialVault(stateDir)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := reopened.List(t.Context())
		_ = reopened.Close()
		if err != nil || len(stored) != 1 || stored[0].ID != auth.ID || stored[0].Metadata["access_token"] != fixture.Metadata["access_token"] {
			t.Fatal("quota shutdown altered the credential", err)
		}
	}
}
