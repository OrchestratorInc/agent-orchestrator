package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelcatalog"
	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeAgent struct {
	err   error
	delay time.Duration
}

type fakeAuthAgent struct {
	fakeAgent
	status    ports.AgentAuthStatus
	authErr   error
	authDelay time.Duration
}

type probeTrackingAgent struct {
	fakeAgent
	onProbe func()
}

type concurrentResolverAgent struct {
	fakeAgent
	active  atomic.Int32
	calls   atomic.Int32
	overlap atomic.Bool
}

type countingResolverAgent struct {
	fakeAgent
	calls atomic.Int32
}

type blockingSubsequentResolverAgent struct {
	fakeAgent
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type startupPresenceAgent struct {
	fakeAgent
	normalResolveCalls *atomic.Int32
	presenceCalls      *atomic.Int32
	authCalls          *atomic.Int32
}

type identityPendingAgent struct {
	fakeAgent
	normalResolveCalls *atomic.Int32
	presenceCalls      *atomic.Int32
}

type mutableInstallAgent struct {
	fakeAgent
	installed atomic.Bool
}

type invalidatingAgent struct {
	fakeAgent
	calls atomic.Int32
}

type mutableAuthAgent struct {
	fakeAgent
	status    *ports.AgentAuthStatus
	authCalls atomic.Int32
}

type blockingResolverAgent struct {
	fakeAgent
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type fakeModelCache struct {
	mu      sync.RWMutex
	records map[string]ports.CachedAgentModelCatalog
	puts    int
	putErr  error
}

type fakeSessionUsageLookup struct {
	records []domain.SessionRecord
	err     error
}

func (f fakeSessionUsageLookup) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return f.records, f.err
}

type fakeModelDiscoverer struct {
	mu                     sync.Mutex
	version                string
	catalog                ports.AgentModelCatalog
	err                    error
	discoverCalls          atomic.Int32
	successfulCalls        atomic.Int32
	lastRequest            ports.AgentModelDiscoveryRequest
	fingerprintRequests    atomic.Int32
	lastFingerprintRequest atomic.Pointer[ports.AgentModelDiscoveryRequest]
	delay                  time.Duration
	active                 atomic.Int32
	maxActive              atomic.Int32
	overlap                atomic.Bool
}

type blockingSubsequentModelDiscoverer struct {
	*fakeModelDiscoverer
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *fakeModelDiscoverer) Discover(ctx context.Context, request ports.AgentModelDiscoveryRequest) (ports.AgentModelCatalog, error) {
	f.discoverCalls.Add(1)
	active := f.active.Add(1)
	for {
		maximum := f.maxActive.Load()
		if active <= maximum || f.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	if active != 1 {
		f.overlap.Store(true)
	}
	defer f.active.Add(-1)
	if f.delay > 0 {
		timer := time.NewTimer(f.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ports.AgentModelCatalog{AgentID: request.AgentID, Models: []ports.AgentModelInfo{}}, ctx.Err()
		}
	}
	f.mu.Lock()
	f.lastRequest = request
	catalog := f.catalog
	discoverErr := f.err
	f.mu.Unlock()
	if catalog.AgentID == "" {
		catalog.AgentID = request.AgentID
	}
	if catalog.Models == nil {
		catalog.Models = []ports.AgentModelInfo{}
	}
	if catalog.FetchedAt.IsZero() {
		catalog.FetchedAt = time.Now().UTC()
	}
	if discoverErr == nil {
		f.successfulCalls.Add(1)
	}
	return catalog, discoverErr
}

func (f *blockingSubsequentModelDiscoverer) Discover(ctx context.Context, request ports.AgentModelDiscoveryRequest) (ports.AgentModelCatalog, error) {
	if f.discoverCalls.Load() > 0 {
		f.once.Do(func() { close(f.started) })
		select {
		case <-f.release:
		case <-ctx.Done():
			return ports.AgentModelCatalog{}, ctx.Err()
		}
	}
	return f.fakeModelDiscoverer.Discover(ctx, request)
}

func TestCatalogFreshnessUsesMachineLocalDateAndTimezone(t *testing.T) {
	lineIslands := time.FixedZone("LINT", 14*60*60)
	utcMinus12 := time.FixedZone("UTC-12", -12*60*60)
	last := time.Date(2026, 9, 7, 0, 30, 0, 0, lineIslands)
	if catalogNeedsRevalidation(last, time.Date(2026, 9, 7, 23, 59, 0, 0, lineIslands)) {
		t.Fatal("same local calendar day was stale")
	}
	if !catalogNeedsRevalidation(last, time.Date(2026, 9, 8, 0, 1, 0, 0, lineIslands)) {
		t.Fatal("midnight rollover did not stale catalog")
	}
	previousZone, previousOffset := last.Zone()
	if !catalogClockDiscontinuity(last, last.In(utcMinus12), previousZone, previousOffset) {
		t.Fatal("timezone move to a different machine-local date did not stale catalog")
	}
	sameOffsetDifferentZone := time.FixedZone("KIRITIMATI", 14*60*60)
	if !catalogClockDiscontinuity(last, last.In(sameOffsetDifferentZone), previousZone, previousOffset) {
		t.Fatal("timezone identity change with the same offset was not detected")
	}
}

func TestStartupPrefetchCreatesOneGlobalCatalogPerInstalledAgentWithoutAuthentication(t *testing.T) {
	discoverer := successfulModelDiscoverer()
	cache := &fakeModelCache{}
	svc := newService([]agentregistry.HarnessAgent{
		harnessAuthAgent("codex", "Codex", ports.AgentAuthStatusUnauthorized, nil),
		harnessAgent("gemini", "Gemini", ports.ErrAgentBinaryNotFound),
	}, cache, discoverer)
	svc.prefetchModelCatalogs(context.Background(), false)
	deadline := time.Now().Add(time.Second)
	for (discoverer.discoverCalls.Load() < 1 || discoverer.active.Load() != 0) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := discoverer.discoverCalls.Load(); got != 1 {
		t.Fatalf("discoveries = %d, want one global catalog for the installed agent", got)
	}
	if _, ok, err := cache.GetAgentModelCatalog(context.Background(), "codex", ""); err != nil || !ok {
		t.Fatalf("cached global codex catalog = (%v, %v), want persisted", ok, err)
	}
	if _, ok, err := cache.GetAgentModelCatalog(context.Background(), "gemini", ""); err != nil || ok {
		t.Fatalf("cached uninstalled gemini catalog = (%v, %v), want absent", ok, err)
	}
}

func TestStartupPrefetchDoesNotConsumeDiscoveryTimeoutWhileQueued(t *testing.T) {
	previousTimeout := modelCatalogLoadTimeout
	modelCatalogLoadTimeout = 35 * time.Millisecond
	t.Cleanup(func() { modelCatalogLoadTimeout = previousTimeout })
	discoverer := successfulModelDiscoverer()
	discoverer.delay = 20 * time.Millisecond
	svc := newService([]agentregistry.HarnessAgent{
		harnessAuthAgent("codex", "Codex", ports.AgentAuthStatusAuthorized, nil),
		harnessAuthAgent("gemini", "Gemini", ports.AgentAuthStatusAuthorized, nil),
	}, &fakeModelCache{}, discoverer)

	svc.prefetchModelCatalogs(context.Background(), false)
	if got := discoverer.successfulCalls.Load(); got != 2 {
		t.Fatalf("successful discoveries = %d, want one global catalog per installed agent", got)
	}
}

func TestModelDiscoveryIsGloballyBoundedAtTwoAndDeduplicatedPerAgent(t *testing.T) {
	discoverer := successfulModelDiscoverer()
	discoverer.delay = 40 * time.Millisecond
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, &fakeModelCache{}, discoverer)
	var wg sync.WaitGroup
	for _, projectID := range []string{"a", "a", "b", "c", "d"} {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = svc.Models(context.Background(), "codex", projectID, true) }()
	}
	wg.Wait()
	if got := discoverer.maxActive.Load(); got > 2 {
		t.Fatalf("max concurrent discoveries = %d, want <= 2", got)
	}
	if got := discoverer.discoverCalls.Load(); got != 1 {
		t.Fatalf("discoveries = %d, want one project-independent agent load", got)
	}
}

func TestManualRefreshBypassesPersistedRetryBackoff(t *testing.T) {
	now := time.Now().UTC()
	record := cachedModelRecord(t, "codex", "project-a", now.Add(-24*time.Hour), true)
	record.BinaryVersion = "v1"
	record.InputFingerprint = "v1"
	var catalog ports.AgentModelCatalog
	if err := json.Unmarshal([]byte(record.CatalogJSON), &catalog); err != nil {
		t.Fatal(err)
	}
	catalog.BinaryVersion = "v1"
	catalog.InputFingerprint = "v1"
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	record.CatalogJSON = string(data)
	record.RefreshState = "error"
	record.RetryCount = int64(modelCatalogMaxRetries + 1)
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{"codex\x00": record}}
	discoverer := successfulModelDiscoverer()
	discoverer.err = errors.New("offline")
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, discoverer)
	if _, err := svc.RevalidateModels(context.Background(), "codex", "project-a"); err != nil {
		t.Fatal(err)
	}
	if discoverer.discoverCalls.Load() != 0 {
		t.Fatal("automatic revalidation bypassed retry backoff")
	}
	if _, err := svc.Models(context.Background(), "codex", "project-a", true); err != nil {
		t.Fatal(err)
	}
	if discoverer.discoverCalls.Load() != 1 {
		t.Fatal("manual refresh did not bypass retry backoff")
	}
	updated, ok, err := cache.GetAgentModelCatalog(context.Background(), "codex", "")
	if err != nil || !ok {
		t.Fatalf("updated cache = (%#v, %v, %v)", updated, ok, err)
	}
	if updated.RetryCount != 1 || updated.RetryAt.IsZero() {
		t.Fatalf("retry state = count:%d at:%s, want manual refresh to start a new retry sequence", updated.RetryCount, updated.RetryAt)
	}
}

func TestChangedInputsAndQueuedInvalidationBypassPersistedRetryBackoff(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fingerprint string
		state       string
	}{
		{name: "changed fingerprint", fingerprint: "v2", state: "error"},
		{name: "queued invalidation", fingerprint: "v1", state: "queued"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			record := cachedModelRecord(t, "codex", "project-a", now, true)
			record.BinaryVersion = "v1"
			record.InputFingerprint = "v1"
			record.RetryAt = now.Add(time.Hour)
			record.RefreshState = tc.state
			record.RetryCount = 1
			cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{"codex\x00": record}}
			discoverer := successfulModelDiscoverer()
			discoverer.version = tc.fingerprint
			svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, discoverer)

			if _, err := svc.RevalidateModels(context.Background(), "codex", "project-a"); err != nil {
				t.Fatal(err)
			}
			if got := discoverer.discoverCalls.Load(); got != 1 {
				t.Fatalf("discoveries = %d, want changed or explicitly invalidated inputs to bypass backoff", got)
			}
		})
	}
}

func TestChangedInputsStartANewRetrySequence(t *testing.T) {
	now := time.Now().UTC()
	record := cachedModelRecord(t, "codex", "project-a", now, true)
	record.BinaryVersion = "v1"
	record.InputFingerprint = "v1"
	record.RefreshState = "error"
	record.RetryCount = int64(modelCatalogMaxRetries + 1)
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{"codex\x00": record}}
	discoverer := successfulModelDiscoverer()
	discoverer.version = "v2"
	discoverer.err = errors.New("offline")
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, discoverer)
	svc.now = func() time.Time { return now }

	if _, err := svc.RevalidateModels(context.Background(), "codex", "project-a"); err != nil {
		t.Fatal(err)
	}
	updated, ok, err := cache.GetAgentModelCatalog(context.Background(), "codex", "")
	if err != nil || !ok {
		t.Fatalf("updated cache = (%#v, %v, %v)", updated, ok, err)
	}
	if updated.RetryCount != 1 || updated.RetryAt.IsZero() {
		t.Fatalf("retry state = count:%d at:%s, want a new scheduled sequence", updated.RetryCount, updated.RetryAt)
	}
}

func TestEmptyModelDiscoveryPreservesRetrySequence(t *testing.T) {
	for _, reset := range []string{"manual refresh", "changed inputs"} {
		t.Run(reset, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			now := time.Now().UTC()
			cache := &fakeModelCache{}
			discoverer := &fakeModelDiscoverer{version: "v1", err: errors.New("offline")}
			svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, discoverer)
			svc.ctx = ctx
			svc.now = func() time.Time { return now }

			for attempt, delay := range []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 0} {
				got, err := svc.RevalidateModels(ctx, "claude-code", "")
				if err != nil {
					t.Fatal(err)
				}
				if len(got.Models) != 0 || got.RefreshState != "error" || got.Warning != "offline" {
					t.Fatalf("failed empty discovery = %#v", got)
				}
				record, ok, err := cache.GetAgentModelCatalog(ctx, "claude-code", "")
				if err != nil || !ok {
					t.Fatalf("cached failure = (%#v, %v, %v)", record, ok, err)
				}
				if record.RetryCount != int64(attempt+1) || discoverer.discoverCalls.Load() != int32(attempt+1) {
					t.Fatalf("attempt %d: retry count = %d, discoveries = %d", attempt+1, record.RetryCount, discoverer.discoverCalls.Load())
				}
				if delay == 0 {
					if !record.RetryAt.IsZero() || got.RetryAt != nil || got.RefreshRecommended {
						t.Fatalf("exhausted discovery still recommends retry: %#v", got)
					}
				} else {
					wantRetryAt := now.Add(delay)
					if !record.RetryAt.Equal(wantRetryAt) || got.RetryAt == nil || !got.RetryAt.Equal(wantRetryAt) || !got.RefreshRecommended {
						t.Fatalf("attempt %d: retryAt = %v, want %s", attempt+1, got.RetryAt, wantRetryAt)
					}
				}
				// Automatic reads must respect both the backoff and the stop limit.
				if _, err := svc.RevalidateModels(ctx, "claude-code", ""); err != nil {
					t.Fatal(err)
				}
				if discoverer.discoverCalls.Load() != int32(attempt+1) {
					t.Fatal("automatic revalidation ignored retry backoff or exhaustion")
				}
				now = now.Add(delay)
			}

			var err error
			if reset == "manual refresh" {
				_, err = svc.Models(ctx, "claude-code", "", true)
			} else {
				discoverer.version = "v2"
				_, err = svc.RevalidateModels(ctx, "claude-code", "")
			}
			if err != nil {
				t.Fatal(err)
			}
			record, ok, err := cache.GetAgentModelCatalog(ctx, "claude-code", "")
			if err != nil || !ok {
				t.Fatalf("reset cache = (%#v, %v, %v)", record, ok, err)
			}
			if record.RetryCount != 1 || !record.RetryAt.Equal(now.Add(time.Minute)) || record.InputFingerprint != discoverer.version || discoverer.discoverCalls.Load() != 5 {
				t.Fatalf("%s did not restart the retry sequence: %#v", reset, record)
			}
		})
	}
}

func TestOnlyManualModelRefreshPersistsLoadingState(t *testing.T) {
	for _, tc := range []struct {
		name     string
		refresh  func(*Service) (ports.AgentModelCatalog, error)
		wantPuts int
	}{
		{
			name: "automatic revalidation",
			refresh: func(svc *Service) (ports.AgentModelCatalog, error) {
				return svc.RevalidateModels(context.Background(), "codex", "project-a")
			},
			wantPuts: 1,
		},
		{
			name: "manual refresh",
			refresh: func(svc *Service) (ports.AgentModelCatalog, error) {
				return svc.Models(context.Background(), "codex", "project-a", true)
			},
			wantPuts: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &fakeModelCache{}
			discoverer := successfulModelDiscoverer()
			svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, discoverer)
			if _, err := svc.Models(context.Background(), "codex", "project-a", true); err != nil {
				t.Fatal(err)
			}
			cache.puts = 0

			if _, err := tc.refresh(svc); err != nil {
				t.Fatal(err)
			}
			if cache.puts != tc.wantPuts {
				t.Fatalf("cache updates = %d, want %d; automatic refresh must not publish a transient loading state", cache.puts, tc.wantPuts)
			}
		})
	}
}

func TestModelDiscoveryRetryBackoffStopsAfterBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	now := time.Now().UTC()
	record := cachedModelRecord(t, "codex", "project-a", now.Add(-24*time.Hour), true)
	record.BinaryVersion = "v1"
	record.InputFingerprint = "v1"
	var catalog ports.AgentModelCatalog
	if err := json.Unmarshal([]byte(record.CatalogJSON), &catalog); err != nil {
		t.Fatal(err)
	}
	catalog.BinaryVersion = "v1"
	catalog.InputFingerprint = "v1"
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	record.CatalogJSON = string(data)
	record.RefreshState = "error"
	record.RefreshError = "offline"
	record.RetryCount = int64(modelCatalogMaxRetries)
	record.RetryAt = now.Add(-time.Second)
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{"codex\x00": record}}
	discoverer := successfulModelDiscoverer()
	discoverer.err = errors.New("offline")
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, discoverer)
	svc.ctx = ctx
	svc.now = func() time.Time { return now }
	got, err := svc.RevalidateModels(context.Background(), "codex", "project-a")
	if err != nil {
		t.Fatal(err)
	}
	record, ok, err := cache.GetAgentModelCatalog(context.Background(), "codex", "")
	if err != nil || !ok {
		t.Fatalf("cached failure = (%#v, %v, %v)", record, ok, err)
	}
	if record.RetryCount != int64(modelCatalogMaxRetries+1) {
		t.Fatalf("retry count = %d, want %d", record.RetryCount, modelCatalogMaxRetries+1)
	}
	if !record.RetryAt.IsZero() {
		t.Fatalf("retry remained scheduled after bound: %s", record.RetryAt)
	}
	if got.RetryAt != nil {
		t.Fatalf("response retryAt = %s after retry bound, want absent", got.RetryAt)
	}
	if got.RefreshRecommended {
		t.Fatal("final failed retry recommended another automatic refresh")
	}
	wire, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), `"retryAt"`) {
		t.Fatalf("response serialized an absent retry time: %s", wire)
	}
	exhaustedCalls := discoverer.discoverCalls.Load()
	fingerprintRequests := discoverer.fingerprintRequests.Load()
	cached, err := svc.Models(context.Background(), "codex", "project-a", false)
	if err != nil {
		t.Fatal(err)
	}
	if cached.RefreshRecommended {
		t.Fatal("cached read recommended another refresh after retries were exhausted")
	}
	deadline := time.Now().Add(100 * time.Millisecond)
	for discoverer.fingerprintRequests.Load() == fingerprintRequests && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if discoverer.fingerprintRequests.Load() == fingerprintRequests {
		t.Fatal("cached read did not check whether exhausted discovery inputs changed")
	}
	if calls := discoverer.discoverCalls.Load(); calls != exhaustedCalls {
		t.Fatalf("discoveries = %d after exhausted cached read, want %d", calls, exhaustedCalls)
	}
}

func TestObsoleteRetryTimerDoesNotRunAfterManualRefreshSucceeds(t *testing.T) {
	previousDelay := modelCatalogRetryDelays[0]
	modelCatalogRetryDelays[0] = 80 * time.Millisecond
	t.Cleanup(func() { modelCatalogRetryDelays[0] = previousDelay })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cache := &fakeModelCache{}
	discoverer := successfulModelDiscoverer()
	discoverer.err = errors.New("offline")
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, discoverer)
	svc.ctx = ctx

	if _, err := svc.Models(context.Background(), "codex", "project-a", true); err != nil {
		t.Fatal(err)
	}
	discoverer.mu.Lock()
	discoverer.err = nil
	discoverer.mu.Unlock()
	if _, err := svc.Models(context.Background(), "codex", "project-a", true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * modelCatalogRetryDelays[0])
	if got := discoverer.discoverCalls.Load(); got != 2 {
		t.Fatalf("discoveries = %d, want obsolete retry timer to remain a no-op after success", got)
	}
	record, ok, err := cache.GetAgentModelCatalog(context.Background(), "codex", "")
	if err != nil || !ok || record.RefreshState != "idle" || record.RetryCount != 0 {
		t.Fatalf("fresh catalog state = (%#v, %v, %v), want successful manual refresh preserved", record, ok, err)
	}
}

type cancelAwareModelDiscoverer struct{ started chan struct{} }

func (d *cancelAwareModelDiscoverer) Discover(ctx context.Context, request ports.AgentModelDiscoveryRequest) (ports.AgentModelCatalog, error) {
	close(d.started)
	<-ctx.Done()
	return ports.AgentModelCatalog{AgentID: request.AgentID}, ctx.Err()
}
func (*cancelAwareModelDiscoverer) CatalogFingerprint(context.Context, ports.AgentModelDiscoveryRequest) string {
	return "v1"
}
func (*cancelAwareModelDiscoverer) Manual(agentID string) ports.AgentModelCatalog {
	return ports.AgentModelCatalog{AgentID: agentID, Models: []ports.AgentModelInfo{}}
}

func TestModelDiscoveryStopsOnServiceShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	discoverer := &cancelAwareModelDiscoverer{started: make(chan struct{})}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, nil, discoverer)
	svc.ctx = ctx
	done := make(chan error, 1)
	go func() { _, err := svc.Models(context.Background(), "codex", "project-a", true); done <- err }()
	<-discoverer.started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("discovery did not stop on service shutdown")
	}
}

func TestRefreshTimeoutPersistsFailureInsteadOfLeavingRefreshing(t *testing.T) {
	previousTimeout := modelCatalogLoadTimeout
	modelCatalogLoadTimeout = 10 * time.Millisecond
	t.Cleanup(func() { modelCatalogLoadTimeout = previousTimeout })
	serviceCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	now := time.Now().UTC()
	record := cachedModelRecord(t, "codex", "", now, false)
	record.BinaryVersion = "v1"
	record.LastSuccessAt = now
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{"codex\x00": record}}
	discoverer := &cancelAwareModelDiscoverer{started: make(chan struct{})}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, discoverer)
	svc.ctx = serviceCtx

	if _, err := svc.Models(context.Background(), "codex", "project-a", true); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := cache.GetAgentModelCatalog(context.Background(), "codex", "")
	if err != nil || !ok {
		t.Fatalf("cached timeout = (%#v, %v, %v)", stored, ok, err)
	}
	if stored.RefreshState != "error" || stored.RefreshError == "" {
		t.Fatalf("refresh state = (%q, %q), want durable error", stored.RefreshState, stored.RefreshError)
	}
}

func TestStartupPrefetchRecoversPersistedRefreshingCatalog(t *testing.T) {
	now := time.Now().UTC()
	record := cachedModelRecord(t, "codex", "", now, false)
	record.LastSuccessAt = now
	record.RefreshState = "refreshing"
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{"codex\x00": record}}
	discoverer := successfulModelDiscoverer()
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, discoverer)

	svc.prefetchModelCatalogs(context.Background(), false)
	if got := discoverer.discoverCalls.Load(); got != 1 {
		t.Fatalf("discoveries = %d, want persisted refreshing state recovered", got)
	}
	stored, ok, err := cache.GetAgentModelCatalog(context.Background(), "codex", "")
	if err != nil || !ok || stored.RefreshState != "idle" {
		t.Fatalf("recovered cache = (%#v, %v, %v), want idle", stored, ok, err)
	}
}

func (f *fakeModelDiscoverer) CatalogFingerprint(_ context.Context, request ports.AgentModelDiscoveryRequest) string {
	f.fingerprintRequests.Add(1)
	f.lastFingerprintRequest.Store(&request)
	return f.version
}

func (f *fakeModelDiscoverer) Manual(agentID string) ports.AgentModelCatalog {
	return ports.AgentModelCatalog{
		AgentID:          agentID,
		SelectionMode:    ports.ModelSelectionText,
		Models:           []ports.AgentModelInfo{},
		CustomModelEntry: ports.CustomModelEntryDirect,
		AllowCustom:      true,
		Source:           "manual",
		FetchedAt:        time.Now().UTC(),
	}
}

var testModelDiscoverer ports.AgentModelDiscoverer = modelcatalog.Discoverer{}

func successfulModelDiscoverer() *fakeModelDiscoverer {
	return &fakeModelDiscoverer{version: "v1", catalog: ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog,
		Models:        []ports.AgentModelInfo{{ID: "model-one", Label: "Model One"}},
		AllowCustom:   true,
		Source:        "cli",
	}}
}

func (f *fakeModelCache) GetAgentModelCatalog(_ context.Context, agentID, projectID string) (ports.CachedAgentModelCatalog, bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	record, ok := f.records[agentID+"\x00"+projectID]
	return record, ok, nil
}

func (f *fakeModelCache) ListAgentModelCatalogsByAgent(_ context.Context, agentID string) ([]ports.CachedAgentModelCatalog, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	records := make([]ports.CachedAgentModelCatalog, 0)
	for _, record := range f.records {
		if record.AgentID == agentID {
			records = append(records, record)
		}
	}
	return records, nil
}

func (f *fakeModelCache) UpsertAgentModelCatalog(_ context.Context, record ports.CachedAgentModelCatalog) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return f.putErr
	}
	if f.records == nil {
		f.records = map[string]ports.CachedAgentModelCatalog{}
	}
	f.records[record.AgentID+"\x00"+record.ProjectID] = record
	f.puts++
	return nil
}

func (f *concurrentResolverAgent) ResolveBinary(ctx context.Context) (string, error) {
	f.calls.Add(1)
	if f.active.Add(1) != 1 {
		f.overlap.Store(true)
	}
	defer f.active.Add(-1)
	select {
	case <-time.After(5 * time.Millisecond):
		return "agent", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (f *countingResolverAgent) ResolveBinary(ctx context.Context) (string, error) {
	f.calls.Add(1)
	return f.fakeAgent.ResolveBinary(ctx)
}

func (f *blockingSubsequentResolverAgent) ResolveBinary(ctx context.Context) (string, error) {
	if f.calls.Add(1) == 1 {
		return f.fakeAgent.ResolveBinary(ctx)
	}
	f.once.Do(func() { close(f.started) })
	select {
	case <-f.release:
		return f.fakeAgent.ResolveBinary(ctx)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (f *mutableInstallAgent) ResolveBinary(context.Context) (string, error) {
	if !f.installed.Load() {
		return "", ports.ErrAgentBinaryNotFound
	}
	return "agent", nil
}

func (f *invalidatingAgent) InvalidateBinaryResolution() {
	f.calls.Add(1)
}

func (f *mutableAuthAgent) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	f.authCalls.Add(1)
	return *f.status, nil
}

func (f *blockingResolverAgent) ResolveBinary(ctx context.Context) (string, error) {
	f.once.Do(func() { close(f.started) })
	select {
	case <-f.release:
		return "agent", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (f fakeAgent) GetConfigSpec(context.Context) (ports.ConfigSpec, error) {
	return ports.ConfigSpec{}, nil
}

func (f fakeAgent) GetLaunchCommand(ctx context.Context, _ ports.LaunchConfig) ([]string, error) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return []string{"agent"}, nil
}

func (f fakeAgent) ResolveBinary(ctx context.Context) (string, error) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if f.err != nil {
		return "", f.err
	}
	return "agent", nil
}

func (f probeTrackingAgent) ResolveBinary(ctx context.Context) (string, error) {
	if f.onProbe != nil {
		f.onProbe()
	}
	return f.fakeAgent.ResolveBinary(ctx)
}

func (f fakeAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryInCommand, nil
}

func (f fakeAgent) GetAgentHooks(context.Context, ports.WorkspaceHookConfig) error {
	return nil
}

func (f fakeAgent) GetRestoreCommand(context.Context, ports.RestoreConfig) ([]string, bool, error) {
	return nil, false, nil
}

func (f fakeAgent) SessionInfo(context.Context, ports.SessionRef) (ports.SessionInfo, bool, error) {
	return ports.SessionInfo{}, false, nil
}

func (f fakeAuthAgent) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if f.authDelay > 0 {
		select {
		case <-time.After(f.authDelay):
		case <-ctx.Done():
			return ports.AgentAuthStatusUnknown, ctx.Err()
		}
	}
	return f.status, f.authErr
}

func (f startupPresenceAgent) ResolveBinary(context.Context) (string, error) {
	f.normalResolveCalls.Add(1)
	return "", errors.New("normal resolution must not run during startup")
}

func (f startupPresenceAgent) ResolveBinaryPresence(context.Context) (string, error) {
	f.presenceCalls.Add(1)
	return "/usr/local/bin/muse", nil
}

func (f startupPresenceAgent) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	f.authCalls.Add(1)
	return ports.AgentAuthStatusAuthorized, nil
}

func (f identityPendingAgent) ResolveBinary(ctx context.Context) (string, error) {
	f.normalResolveCalls.Add(1)
	return f.fakeAgent.ResolveBinary(ctx)
}

func (f identityPendingAgent) ResolveBinaryPresence(context.Context) (string, error) {
	f.presenceCalls.Add(1)
	return "", ports.ErrAgentBinaryIdentityUnknown
}

func TestListReturnsInitialSupportedInventoryWithoutProbing(t *testing.T) {
	probed := false
	svc := NewWithAgents([]agentregistry.HarnessAgent{
		{
			Harness: domain.AgentHarness("codex"),
			Manifest: adapters.Manifest{
				ID:   "codex",
				Name: "Codex",
			},
			Agent: probeTrackingAgent{onProbe: func() { probed = true }},
		},
	})

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if probed {
		t.Fatal("List ran a live probe")
	}
	if len(got.Supported) != 1 || got.Supported[0].ID != "codex" {
		t.Fatalf("supported = %#v, want codex", got.Supported)
	}
	if len(got.Installed) != 0 || len(got.Authorized) != 0 {
		t.Fatalf("inventory = %#v, want only supported entries before refresh", got)
	}
	if got.Installed == nil {
		t.Fatal("Installed = nil, want empty slice")
	}
	if got.Authorized == nil {
		t.Fatal("Authorized = nil, want empty slice")
	}
}

func TestFindInstalledBinary_ResolvesWithoutRefreshingInventory(t *testing.T) {
	svc := NewWithAgents([]agentregistry.HarnessAgent{
		harnessAgent("missing", "Missing", ports.ErrAgentBinaryNotFound),
		harnessAgent("codex", "Codex", nil),
	})

	got, ok := svc.FindInstalledBinary(context.Background())
	if !ok {
		t.Fatal("FindInstalledBinary() found no binary, want Codex")
	}
	if got.ID != "codex" || got.Label != "Codex" {
		t.Fatalf("FindInstalledBinary() = %#v, want Codex", got)
	}

	inventory, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(inventory.Installed) != 1 || inventory.Installed[0].ID != "codex" || len(inventory.Authorized) != 0 {
		t.Fatalf("inventory = %#v, want coordinator installation snapshot without auth", inventory)
	}
}

func TestFindInstalledBinaryUsesPresenceResolverWithoutAuthOrNormalResolution(t *testing.T) {
	var normalResolveCalls atomic.Int32
	var presenceCalls atomic.Int32
	var authCalls atomic.Int32
	svc := NewWithAgents([]agentregistry.HarnessAgent{
		{
			Harness:  domain.AgentHarness("muse"),
			Manifest: adapters.Manifest{ID: "muse", Name: "Muse"},
			Agent: startupPresenceAgent{
				fakeAgent:          fakeAgent{},
				normalResolveCalls: &normalResolveCalls,
				presenceCalls:      &presenceCalls,
				authCalls:          &authCalls,
			},
		},
	})

	got, ok := svc.FindInstalledBinary(context.Background())
	if !ok || got.ID != "muse" {
		t.Fatalf("FindInstalledBinary() = (%#v, %v), want Muse", got, ok)
	}
	if got := presenceCalls.Load(); got != 1 {
		t.Fatalf("presence resolver calls = %d, want 1", got)
	}
	if got := normalResolveCalls.Load(); got != 0 {
		t.Fatalf("normal resolver calls = %d, want 0", got)
	}
	if got := authCalls.Load(); got != 0 {
		t.Fatalf("auth calls = %d, want 0", got)
	}
}

func TestFindInstalledBinaryKeepsNameOnlyGooseMatchUnknown(t *testing.T) {
	var normalResolveCalls atomic.Int32
	var presenceCalls atomic.Int32
	svc := NewWithAgents([]agentregistry.HarnessAgent{{
		Harness:  domain.AgentHarness("goose"),
		Manifest: adapters.Manifest{ID: "goose", Name: "Goose"},
		Agent: identityPendingAgent{
			fakeAgent:          fakeAgent{err: ports.ErrAgentBinaryNotFound},
			normalResolveCalls: &normalResolveCalls,
			presenceCalls:      &presenceCalls,
		},
	}})

	if _, ok := svc.FindInstalledBinary(context.Background()); ok {
		t.Fatal("FindInstalledBinary() treated an unverified name match as installed")
	}
	if got := presenceCalls.Load(); got != 1 {
		t.Fatalf("startup presence calls = %d, want 1", got)
	}
	if got := normalResolveCalls.Load(); got != 0 {
		t.Fatalf("startup normal resolution calls = %d, want 0", got)
	}

	readiness, err := svc.CachedReadiness(context.Background())
	if err != nil {
		t.Fatalf("CachedReadiness: %v", err)
	}
	installation := readiness.Agents[0].Installation
	if installation.State != domain.AgentInstallationUnknown || installation.ReasonCode != domain.AgentReadinessReasonInstallIdentityPending {
		t.Fatalf("startup installation = %#v, want identity-pending unknown", installation)
	}

	inventory, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := normalResolveCalls.Load(); got != 1 {
		t.Fatalf("fresh normal resolution calls = %d, want 1", got)
	}
	if len(inventory.Installed) != 0 {
		t.Fatalf("Pressly-only inventory Installed = %#v, want empty", inventory.Installed)
	}
}

func TestPresslyGooseDoesNotSatisfyStartupOrFreshInventory(t *testing.T) {
	var normalResolveCalls atomic.Int32
	var presenceCalls atomic.Int32
	// The Goose adapter's help-level Pressly classification is covered in its
	// own tests. Inject the resulting identity-unknown observation here so this
	// service contract cannot inspect host-wide fallback paths.
	pressly := identityPendingAgent{
		fakeAgent:          fakeAgent{err: ports.ErrAgentBinaryNotFound},
		normalResolveCalls: &normalResolveCalls,
		presenceCalls:      &presenceCalls,
	}
	svc := NewWithAgents([]agentregistry.HarnessAgent{{
		Harness:  domain.AgentHarness("goose"),
		Manifest: adapters.Manifest{ID: "goose", Name: "Goose"},
		Agent:    pressly,
	}})
	if _, ok := svc.FindInstalledBinary(context.Background()); ok {
		t.Fatal("Pressly-only PATH match satisfied the process-free startup check")
	}
	if got := presenceCalls.Load(); got != 1 {
		t.Fatalf("startup presence calls = %d, want 1", got)
	}
	if got := normalResolveCalls.Load(); got != 0 {
		t.Fatalf("startup normal resolution calls = %d, want 0", got)
	}

	inventory, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := normalResolveCalls.Load(); got != 1 {
		t.Fatalf("fresh normal resolution calls = %d, want 1", got)
	}
	if len(inventory.Installed) != 0 {
		t.Fatalf("Pressly-only fresh inventory Installed = %#v, want empty", inventory.Installed)
	}
}

func TestRefreshUsesNormalResolutionInsteadOfStartupPresenceShortcut(t *testing.T) {
	var normalResolveCalls atomic.Int32
	var presenceCalls atomic.Int32
	var authCalls atomic.Int32
	svc := NewWithAgents([]agentregistry.HarnessAgent{{
		Harness:  domain.AgentHarness("muse"),
		Manifest: adapters.Manifest{ID: "muse", Name: "Muse"},
		Agent: startupPresenceAgent{
			fakeAgent:          fakeAgent{},
			normalResolveCalls: &normalResolveCalls,
			presenceCalls:      &presenceCalls,
			authCalls:          &authCalls,
		},
	}})

	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := normalResolveCalls.Load(); got != 1 {
		t.Fatalf("normal resolver calls = %d, want 1", got)
	}
	if got := presenceCalls.Load(); got != 0 {
		t.Fatalf("presence resolver calls = %d, want startup-only", got)
	}
	if got := authCalls.Load(); got != 0 {
		t.Fatalf("auth calls = %d, want none after install resolution failure", got)
	}
}

func TestDefaultCatalogDisplaysPrimeAgent(t *testing.T) {
	got, err := New().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range got.Supported {
		if info.ID == "prime-agent" {
			if info.Label != "Prime Agent" {
				t.Fatalf("prime-agent label = %q, want Prime Agent", info.Label)
			}
			return
		}
	}
	t.Fatal("default catalog does not contain prime-agent")
}

func TestDefaultCatalogDisplaysFX(t *testing.T) {
	got, err := New().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range got.Supported {
		if info.ID == "fx" {
			if info.Label != "fx" {
				t.Fatalf("fx label = %q, want fx", info.Label)
			}
			return
		}
	}
	t.Fatal("default catalog does not contain fx")
}

func TestRefreshReportsInstalledAgentsAndIgnoresDetectorErrors(t *testing.T) {
	svc := NewWithAgents([]agentregistry.HarnessAgent{
		harnessAgent("codex", "Codex", nil),
		harnessAgent("missing", "Missing", ports.ErrAgentBinaryNotFound),
		harnessAgent("broken", "Broken", errors.New("unexpected detector failure")),
	})

	got, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(got.Supported) != 3 {
		t.Fatalf("supported = %#v, want 3 agents", got.Supported)
	}
	if len(got.Installed) != 1 || got.Installed[0].ID != "codex" {
		t.Fatalf("installed = %#v, want only codex", got.Installed)
	}
}

func TestRefreshReportsAuthorizedInstalledAgents(t *testing.T) {
	svc := NewWithAgents([]agentregistry.HarnessAgent{
		harnessAuthAgent("codex", "Codex", ports.AgentAuthStatusAuthorized, nil),
		harnessAuthAgent("fx", "fx", ports.AgentAuthStatusConfigured, nil),
		harnessAuthAgent("claude-code", "Claude Code", ports.AgentAuthStatusUnauthorized, nil),
		harnessAgent("opencode", "OpenCode", nil),
		harnessAuthAgent("broken-auth", "Broken Auth", ports.AgentAuthStatusAuthorized, errors.New("probe failed")),
	})

	got, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(got.Supported) != 5 || len(got.Installed) != 5 {
		t.Fatalf("inventory = %#v, want supported=5 installed=5", got)
	}
	if len(got.Authorized) != 1 || got.Authorized[0].ID != "codex" {
		t.Fatalf("authorized = %#v, want only codex", got.Authorized)
	}

	byID := map[string]Info{}
	for _, info := range got.Installed {
		byID[info.ID] = info
	}
	if byID["codex"].AuthStatus != ports.AgentAuthStatusAuthorized {
		t.Fatalf("codex authStatus = %q", byID["codex"].AuthStatus)
	}
	if byID["fx"].AuthStatus != ports.AgentAuthStatusConfigured {
		t.Fatalf("fx authStatus = %q, want configured", byID["fx"].AuthStatus)
	}
	if byID["claude-code"].AuthStatus != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("claude-code authStatus = %q", byID["claude-code"].AuthStatus)
	}
	if byID["opencode"].AuthStatus != ports.AgentAuthStatusUnknown {
		t.Fatalf("opencode authStatus = %q", byID["opencode"].AuthStatus)
	}
	if byID["broken-auth"].AuthStatus != ports.AgentAuthStatusUnknown {
		t.Fatalf("broken-auth authStatus = %q", byID["broken-auth"].AuthStatus)
	}
}

func TestRefreshDoesNotWaitForSlowAgentProbe(t *testing.T) {
	svc := NewWithAgents([]agentregistry.HarnessAgent{
		harnessAgent("codex", "Codex", nil),
		{
			Harness: domain.AgentHarness("slow"),
			Manifest: adapters.Manifest{
				ID:   "slow",
				Name: "Slow",
			},
			Agent: fakeAgent{delay: time.Minute},
		},
	})
	svc.readiness.installTimeout = 20 * time.Millisecond

	start := time.Now()
	got, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("List took %s, want bounded by slow probe timeout", elapsed)
	}
	if len(got.Supported) != 2 {
		t.Fatalf("supported = %#v, want both agents", got.Supported)
	}
	if len(got.Installed) != 1 || got.Installed[0].ID != "codex" {
		t.Fatalf("installed = %#v, want only codex", got.Installed)
	}
}

func TestListRanksAgentsByRetainedSessionUsage(t *testing.T) {
	older := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	newer := older.Add(24 * time.Hour)
	svc := NewWithAgents([]agentregistry.HarnessAgent{
		harnessAgent("claude-code", "Claude Code", nil),
		harnessAgent("codex", "Codex", nil),
		harnessAgent("goose", "Goose", nil),
	})
	svc.sessions = fakeSessionUsageLookup{records: []domain.SessionRecord{
		{Harness: domain.AgentHarness("claude-code"), CreatedAt: newer},
		{Harness: domain.AgentHarness("codex"), CreatedAt: older},
		{Harness: domain.AgentHarness("codex"), CreatedAt: newer},
	}}

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if ids := []string{got.Supported[0].ID, got.Supported[1].ID, got.Supported[2].ID}; !reflect.DeepEqual(ids, []string{"codex", "claude-code", "goose"}) {
		t.Fatalf("supported order = %v, want frequency then unused fallback", ids)
	}
	if got.Supported[0].UsageCount != 2 || got.Supported[0].LastUsedAt == nil || !got.Supported[0].LastUsedAt.Equal(newer) {
		t.Fatalf("codex usage = %#v, want count 2 and latest use %s", got.Supported[0], newer)
	}
	if got.Supported[2].UsageCount != 0 || got.Supported[2].LastUsedAt != nil {
		t.Fatalf("unused agent usage = %#v, want empty usage metadata", got.Supported[2])
	}
}

func TestListReturnsSessionUsageReadFailure(t *testing.T) {
	svc := NewWithAgents([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)})
	svc.sessions = fakeSessionUsageLookup{err: errors.New("database unavailable")}

	_, err := svc.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "list sessions for agent usage") {
		t.Fatalf("List error = %v, want session usage context", err)
	}
}

func TestRefreshUsesSeparateTimeoutForAuthProbe(t *testing.T) {
	svc := NewWithAgents([]agentregistry.HarnessAgent{
		{
			Harness: domain.AgentHarness("claude-code"),
			Manifest: adapters.Manifest{
				ID:   "claude-code",
				Name: "Claude Code",
			},
			Agent: fakeAuthAgent{
				fakeAgent: fakeAgent{},
				status:    ports.AgentAuthStatusAuthorized,
				authDelay: 75 * time.Millisecond,
			},
		},
	})
	svc.readiness.installTimeout = 20 * time.Millisecond
	svc.readiness.authTimeout = 200 * time.Millisecond

	got, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(got.Authorized) != 1 || got.Authorized[0].ID != "claude-code" {
		t.Fatalf("authorized = %#v, want claude-code", got.Authorized)
	}
}

func TestRefreshForcesFreshReadinessChecks(t *testing.T) {
	probes := 0
	svc := NewWithAgents([]agentregistry.HarnessAgent{
		{
			Harness: domain.AgentHarness("codex"),
			Manifest: adapters.Manifest{
				ID:   "codex",
				Name: "Codex",
			},
			Agent: probeTrackingAgent{onProbe: func() { probes++ }},
		},
	})

	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if probes != 2 {
		t.Fatalf("probes = %d, want 2", probes)
	}
}

func TestRefreshFreshDetectsManualInstallWithoutInvalidation(t *testing.T) {
	agent := &mutableInstallAgent{}
	svc := NewWithAgents([]agentregistry.HarnessAgent{{
		Harness: domain.AgentHarness("codex"),
		Manifest: adapters.Manifest{
			ID:   "codex",
			Name: "Codex",
		},
		Agent: agent,
	}})

	initial, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(initial.Installed) != 0 {
		t.Fatalf("initial Installed = %#v, want empty", initial.Installed)
	}

	agent.installed.Store(true)
	fresh, err := svc.RefreshFresh(context.Background())
	if err != nil {
		t.Fatalf("RefreshFresh: %v", err)
	}
	if len(fresh.Installed) != 1 || fresh.Installed[0].ID != "codex" {
		t.Fatalf("fresh Installed = %#v, want codex", fresh.Installed)
	}
}

func TestProbeBypassesRefreshRateLimitForOneAgent(t *testing.T) {
	probes := 0
	svc := NewWithAgents([]agentregistry.HarnessAgent{
		{
			Harness: domain.AgentHarness("codex"),
			Manifest: adapters.Manifest{
				ID:   "codex",
				Name: "Codex",
			},
			Agent: probeTrackingAgent{fakeAgent: fakeAgent{}, onProbe: func() { probes++ }},
		},
		harnessAgent("missing", "Missing", ports.ErrAgentBinaryNotFound),
	})

	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	got, err := svc.Probe(context.Background(), "codex")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !got.Supported || !got.Installed || got.Agent.ID != "codex" {
		t.Fatalf("Probe = %#v, want supported installed codex", got)
	}
	if probes != 1 {
		t.Fatalf("probes = %d, want launch probe to reuse a launch-fresh snapshot", probes)
	}
	listed, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List after Probe: %v", err)
	}
	if len(listed.Installed) != 1 || listed.Installed[0].ID != "codex" {
		t.Fatalf("installed after Probe = %#v, want codex", listed.Installed)
	}
}

func TestProbeRechecksCachedUnauthorizedAuthentication(t *testing.T) {
	status := ports.AgentAuthStatusUnauthorized
	agent := &mutableAuthAgent{status: &status}
	svc := NewWithAgents([]agentregistry.HarnessAgent{{
		Harness:  domain.AgentHarness("codex"),
		Manifest: adapters.Manifest{ID: "codex", Name: "Codex"},
		Agent:    agent,
	}})

	initial, err := svc.EnsureAgentReadiness(context.Background(), "codex", domain.AgentReadinessPurposeLaunch)
	if err != nil {
		t.Fatalf("EnsureAgentReadiness: %v", err)
	}
	if initial.Authentication.State != domain.AgentAuthenticationUnauthorized {
		t.Fatalf("initial authentication = %q, want unauthorized", initial.Authentication.State)
	}

	status = ports.AgentAuthStatusAuthorized
	got, err := svc.Probe(context.Background(), "codex")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Agent.AuthStatus != ports.AgentAuthStatusAuthorized {
		t.Fatalf("Probe authStatus = %q, want authorized", got.Agent.AuthStatus)
	}
	if calls := agent.authCalls.Load(); calls != 2 {
		t.Fatalf("authentication checks = %d, want fresh check after cached unauthorized", calls)
	}
}

func TestProbeReportsUnsupportedAndMissingAgent(t *testing.T) {
	svc := NewWithAgents([]agentregistry.HarnessAgent{
		harnessAgent("missing", "Missing", ports.ErrAgentBinaryNotFound),
	})

	missing, err := svc.Probe(context.Background(), "missing")
	if err != nil {
		t.Fatalf("Probe missing: %v", err)
	}
	if !missing.Supported || missing.Installed {
		t.Fatalf("Probe missing = %#v, want supported but not installed", missing)
	}

	unsupported, err := svc.Probe(context.Background(), "unknown")
	if err != nil {
		t.Fatalf("Probe unknown: %v", err)
	}
	if unsupported.Supported || unsupported.Installed || unsupported.Agent.ID != "unknown" {
		t.Fatalf("Probe unknown = %#v, want unsupported unknown", unsupported)
	}
}

func TestModelsCachesDiscoveredCatalogGlobally(t *testing.T) {
	cache := &fakeModelCache{}
	svc := newService([]agentregistry.HarnessAgent{
		harnessAgent("codex", "Codex", nil),
	}, cache, successfulModelDiscoverer())

	first, err := svc.Models(context.Background(), "codex", "proj-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if first.SelectionMode != ports.ModelSelectionCatalog || len(first.Models) == 0 || !first.AllowCustom {
		t.Fatalf("first catalog = %#v", first)
	}
	if cache.puts != 1 {
		t.Fatalf("cache puts = %d, want 1", cache.puts)
	}

	second, err := svc.Models(context.Background(), "codex", "proj-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if cache.puts != 1 {
		t.Fatalf("cache puts after hit = %d, want 1", cache.puts)
	}
	if second.Source != first.Source || len(second.Models) != len(first.Models) {
		t.Fatalf("cached catalog = %#v, want %#v", second, first)
	}
}

func TestModelsReusesCacheWhileBinaryVersionMatches(t *testing.T) {
	cache := &fakeModelCache{}
	agent := &blockingSubsequentResolverAgent{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	discoverer := &fakeModelDiscoverer{version: "v1", catalog: ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog,
		Models:        []ports.AgentModelInfo{{ID: "model-one"}},
		AllowCustom:   true,
		Source:        "cli",
	}}
	svc := newService([]agentregistry.HarnessAgent{{
		Harness:  domain.AgentHarness("codex"),
		Manifest: adapters.Manifest{ID: "codex", Name: "Codex"},
		Agent:    agent,
	}}, cache, discoverer)
	ctx, cancel := context.WithCancel(context.Background())
	svc.ctx = ctx
	t.Cleanup(func() {
		cancel()
		close(agent.release)
	})

	_, err := svc.Models(context.Background(), "codex", "proj-1", false)
	if err != nil {
		t.Fatal(err)
	}
	record := cache.records["codex\x00"]
	var old ports.AgentModelCatalog
	if err := json.Unmarshal([]byte(record.CatalogJSON), &old); err != nil {
		t.Fatal(err)
	}
	old.FetchedAt = time.Now().Add(-30 * 24 * time.Hour)
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	record.CatalogJSON = string(data)
	cache.records["codex\x00"] = record

	cached, err := svc.Models(context.Background(), "codex", "proj-1", false)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-agent.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for background cache revalidation")
	}
	if discoverer.discoverCalls.Load() != 1 {
		t.Fatalf("discovery calls=%d, want cached result", discoverer.discoverCalls.Load())
	}
	if !cached.FetchedAt.Equal(old.FetchedAt) {
		t.Fatalf("FetchedAt=%s, want unchanged cached timestamp %s", cached.FetchedAt, old.FetchedAt)
	}
}

func TestModelsRediscoversWhenBinaryVersionChanges(t *testing.T) {
	cache := &fakeModelCache{}
	discoverer := &fakeModelDiscoverer{version: "v1", catalog: ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog,
		Models:        []ports.AgentModelInfo{{ID: "model-one"}},
		AllowCustom:   true,
		Source:        "cli",
	}}
	svc := newService([]agentregistry.HarnessAgent{{
		Harness:  domain.AgentHarness("codex"),
		Manifest: adapters.Manifest{ID: "codex", Name: "Codex"},
		Agent:    &countingResolverAgent{},
	}}, cache, discoverer)

	if _, err := svc.Models(context.Background(), "codex", "proj-1", false); err != nil {
		t.Fatal(err)
	}
	discoverer.version = "v2"
	discoverer.catalog.Models = []ports.AgentModelInfo{{ID: "model-two"}}
	discoverer.catalog.FetchedAt = time.Time{}
	got, err := svc.Models(context.Background(), "codex", "proj-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "model-one" {
		t.Fatalf("cache-first catalog=%#v, want model-one while v2 validates", got)
	}
	deadline := time.Now().Add(time.Second)
	for {
		record, ok, cacheErr := cache.GetAgentModelCatalog(context.Background(), "codex", "")
		if cacheErr != nil {
			t.Fatal(cacheErr)
		}
		if ok && record.BinaryVersion == "v2" {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("timed out waiting for asynchronously refreshed v2 catalog")
		}
		time.Sleep(time.Millisecond)
	}
	got, err = svc.Models(context.Background(), "codex", "proj-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.BinaryVersion != "v2" || len(got.Models) != 1 || got.Models[0].ID != "model-two" {
		t.Fatalf("catalog=%#v, want asynchronously refreshed v2 catalog", got)
	}
}

func TestModelsLeaderCancellationDoesNotCancelCoalescedLoad(t *testing.T) {
	agent := &blockingResolverAgent{started: make(chan struct{}), release: make(chan struct{})}
	svc := newService([]agentregistry.HarnessAgent{{
		Harness:  domain.AgentHarness("muse"),
		Manifest: adapters.Manifest{ID: "muse", Name: "Muse"},
		Agent:    agent,
	}}, nil, successfulModelDiscoverer())

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderErr := make(chan error, 1)
	go func() {
		_, err := svc.Models(leaderCtx, "muse", "proj-1", true)
		leaderErr <- err
	}()
	<-agent.started

	waiterResult := make(chan ports.AgentModelCatalog, 1)
	waiterErr := make(chan error, 1)
	go func() {
		catalog, err := svc.Models(context.Background(), "muse", "proj-1", true)
		waiterResult <- catalog
		waiterErr <- err
	}()
	cancelLeader()
	if err := <-leaderErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader error = %v, want context canceled", err)
	}
	close(agent.release)
	if err := <-waiterErr; err != nil {
		t.Fatalf("coalesced waiter: %v", err)
	}
	if got := <-waiterResult; got.AgentID != "muse" || len(got.Models) == 0 {
		t.Fatalf("coalesced waiter catalog = %#v", got)
	}
}

// A picker opened for an agent that has never been discovered must not wait on
// a slow CLI: it gets the capability fallback at once, marked refreshing, and
// the discovered catalog is persisted in the background for the next read.
// The saved catalog is listed without readiness; an update waits for the
// startup readiness pass before it discovers.
func TestModelUpdateWaitsForStartupReadiness(t *testing.T) {
	now := time.Now().UTC()
	record := cachedModelRecord(t, "codex", "", now, false)
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{"codex\x00": record}}
	discoverer := successfulModelDiscoverer()
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, discoverer)
	svc.readinessWarm = make(chan struct{})

	saved, err := svc.Models(context.Background(), "codex", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Models) != 1 || saved.Models[0].ID != "cached-model" {
		t.Fatalf("saved read = %#v, want the cached catalog before readiness", saved)
	}

	done := make(chan ports.AgentModelCatalog, 1)
	go func() {
		refreshed, _ := svc.revalidateModels(context.Background(), "codex", "")
		done <- refreshed
	}()
	time.Sleep(50 * time.Millisecond)
	if got := discoverer.discoverCalls.Load(); got != 0 {
		t.Fatalf("discoveries before readiness = %d, want 0", got)
	}
	close(svc.readinessWarm)
	select {
	case refreshed := <-done:
		if len(refreshed.Models) != 1 || refreshed.Models[0].ID != "model-one" {
			t.Fatalf("update after readiness = %#v, want the discovered catalog", refreshed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("update did not run after readiness finished")
	}
}

// Readiness that lasts through the whole wait limit must not eat into the
// discovery's own timeout: discovery still gets its full budget afterwards.
func TestModelUpdateKeepsFullDiscoveryBudgetAfterReadinessWait(t *testing.T) {
	previousWait, previousTimeout := modelUpdateReadinessWait, modelCatalogLoadTimeout
	modelUpdateReadinessWait = 60 * time.Millisecond
	modelCatalogLoadTimeout = 100 * time.Millisecond
	t.Cleanup(func() { modelUpdateReadinessWait, modelCatalogLoadTimeout = previousWait, previousTimeout })
	discoverer := successfulModelDiscoverer()
	discoverer.delay = 80 * time.Millisecond
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, &fakeModelCache{}, discoverer)
	svc.readinessWarm = make(chan struct{}) // never finishes

	got, err := svc.revalidateModels(context.Background(), "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "model-one" {
		t.Fatalf("catalog = %#v, want discovery to finish after the readiness wait", got)
	}
}

func TestModelRefreshBypassesStartupReadiness(t *testing.T) {
	for _, joinBackground := range []bool{false, true} {
		t.Run(fmt.Sprintf("joinBackground=%t", joinBackground), func(t *testing.T) {
			discoverer := successfulModelDiscoverer()
			svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, &fakeModelCache{}, discoverer)
			svc.readinessWarm = make(chan struct{}) // never finishes
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if joinBackground {
				// The first cache read starts a coalesced background load, which
				// must be woken when spawn asks for an explicit rediscovery.
				first, err := svc.Models(ctx, "codex", "", false)
				if err != nil || first.RefreshState != "refreshing" {
					t.Fatalf("first read = %#v, %v", first, err)
				}
			}
			got, err := svc.Models(ctx, "codex", "", true)
			if err != nil || len(got.Models) != 1 || got.Models[0].ID != "model-one" {
				t.Fatalf("explicit refresh = %#v, %v", got, err)
			}
			if calls := discoverer.discoverCalls.Load(); calls != 1 {
				t.Fatalf("discoveries = %d, want one coalesced discovery", calls)
			}
		})
	}
}

func TestModelRefreshIgnoresExpiredNotInstalledReadiness(t *testing.T) {
	now := time.Now().UTC()
	agent := &mutableInstallAgent{}
	discoverer := successfulModelDiscoverer()
	item := harnessAgent("gemini", "Gemini", nil)
	item.Agent = agent
	svc := newService([]agentregistry.HarnessAgent{item}, &fakeModelCache{}, discoverer)
	svc.readiness.now = func() time.Time { return now }
	if _, err := svc.readiness.EnsureInstallation(context.Background(), []string{"gemini"}, domain.AgentReadinessPurposeDisplay); err != nil {
		t.Fatal(err)
	}
	agent.installed.Store(true)
	now = now.Add(svc.readiness.displayTTL + time.Second)
	got, err := svc.Models(context.Background(), "gemini", "", true)
	if err != nil || len(got.Models) != 1 || got.Models[0].ID != "model-one" {
		t.Fatalf("refresh after external install = %#v, %v", got, err)
	}
	if discoverer.discoverCalls.Load() != 1 {
		t.Fatal("expired not-installed observation blocked discovery")
	}
}

func TestModelUpdateSkipsAgentReadinessFoundNotInstalled(t *testing.T) {
	now := time.Now().UTC()
	record := cachedModelRecord(t, "gemini", "", now, false)
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{"gemini\x00": record}}
	discoverer := successfulModelDiscoverer()
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("gemini", "Gemini", ports.ErrAgentBinaryNotFound)}, cache, discoverer)
	if _, err := svc.readiness.EnsureInstallation(context.Background(), []string{"gemini"}, domain.AgentReadinessPurposeDisplay); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Models(context.Background(), "gemini", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if discoverer.discoverCalls.Load() != 0 {
		t.Fatal("discovered an agent readiness found not installed")
	}
	if len(got.Models) != 1 || got.Models[0].ID != "cached-model" {
		t.Fatalf("catalog = %#v, want the saved catalog kept", got)
	}
}

func TestFirstModelsReadDoesNotWaitForSlowDiscovery(t *testing.T) {
	previousWait := modelCatalogFirstLoadWait
	modelCatalogFirstLoadWait = 20 * time.Millisecond
	t.Cleanup(func() { modelCatalogFirstLoadWait = previousWait })
	cache := &fakeModelCache{}
	discoverer := successfulModelDiscoverer()
	discoverer.delay = 300 * time.Millisecond
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("opencode", "OpenCode", nil)}, cache, discoverer)

	start := time.Now()
	first, err := svc.Models(context.Background(), "opencode", "proj-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= discoverer.delay {
		t.Fatalf("first read took %s, want it not to wait for discovery", elapsed)
	}
	if first.RefreshState != "refreshing" || first.Source != "manual" {
		t.Fatalf("first read = %#v, want the refreshing fallback", first)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok, _ := cache.GetAgentModelCatalog(context.Background(), "opencode", ""); ok {
			got, err := svc.Models(context.Background(), "opencode", "proj-1", false)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Models) != 1 || got.Models[0].ID != "model-one" {
				t.Fatalf("read after discovery = %#v, want the discovered catalog", got)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background discovery never persisted the catalog")
}

func TestFirstModelsReadReturnsFastDiscoveryDirectly(t *testing.T) {
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, &fakeModelCache{}, successfulModelDiscoverer())

	got, err := svc.Models(context.Background(), "codex", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "model-one" {
		t.Fatalf("first read = %#v, want the discovered catalog without a placeholder", got)
	}
}

func TestModelsDiscoversWithoutProjectContext(t *testing.T) {
	discoverer := &fakeModelDiscoverer{catalog: ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog,
		Models:        []ports.AgentModelInfo{{ID: "device/model"}},
		Source:        "cli",
	}}
	svc := newService([]agentregistry.HarnessAgent{
		harnessAgent("opencode", "OpenCode", nil),
	}, nil, discoverer)

	got, err := svc.Models(context.Background(), "opencode", "proj-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || discoverer.lastRequest.WorkingDir != "" || len(discoverer.lastRequest.Env) != 0 {
		t.Fatalf("catalog=%#v request=%#v, want device-wide discovery", got, discoverer.lastRequest)
	}
}

func TestGlobalModelDiscoveryUsesAODirectoryWithoutProject(t *testing.T) {
	discoveryDir := filepath.Join(t.TempDir(), "model-discovery")
	svc := NewWithDeps(Deps{ModelDiscoveryDir: discoveryDir})

	request, err := svc.modelDiscoveryRequest("deepseek-harness", "", "/usr/local/bin/dsh")
	if err != nil {
		t.Fatal(err)
	}
	if request.WorkingDir != discoveryDir {
		t.Fatalf("working directory = %q, want %q", request.WorkingDir, discoveryDir)
	}
	info, err := os.Stat(discoveryDir)
	if err != nil {
		t.Fatalf("stat model discovery directory: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("model discovery directory permissions = %o, want 700", got)
	}
}

// Model catalogs are device-wide: a picker opened in any project reads the one
// catalog per agent instead of waiting on a discovery of its own.
func TestModelsSharesOneDeviceCatalogAcrossProjects(t *testing.T) {
	cache := &fakeModelCache{}
	discoverer := successfulModelDiscoverer()
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, discoverer)

	for _, projectID := range []string{"proj-a", "proj-b", ""} {
		if _, err := svc.Models(context.Background(), "claude-code", projectID, false); err != nil {
			t.Fatal(err)
		}
	}
	if got := discoverer.discoverCalls.Load(); got != 1 {
		t.Fatalf("discoveries = %d, want one device-wide discovery", got)
	}
	if _, ok, err := cache.GetAgentModelCatalog(context.Background(), "claude-code", ""); err != nil || !ok {
		t.Fatalf("device catalog = found %v err %v, want persisted", ok, err)
	}
	for _, projectID := range []string{"proj-a", "proj-b"} {
		if _, ok, _ := cache.GetAgentModelCatalog(context.Background(), "claude-code", projectID); ok {
			t.Fatalf("cache wrote a project scope %s, want only the device catalog", projectID)
		}
	}
}

func TestModelsIgnoresUnknownProjectScope(t *testing.T) {
	svc := newService([]agentregistry.HarnessAgent{
		harnessAgent("codex", "Codex", nil),
	}, nil, testModelDiscoverer)

	if _, err := svc.Models(context.Background(), "codex", "missing", false); err != nil {
		t.Fatalf("Models: project-independent lookup failed: %v", err)
	}
}

func TestModelsUsesCapabilityAwareFallbackWhenDiscoveryCannotRun(t *testing.T) {
	for _, tc := range []struct {
		agent          string
		wantEntryMode  ports.CustomModelEntryMode
		wantSelection  ports.ModelSelectionMode
		wantAllowInput bool
	}{
		{agent: "qwen", wantEntryMode: ports.CustomModelEntryDirect, wantSelection: ports.ModelSelectionText, wantAllowInput: true},
		{agent: "opencode", wantEntryMode: ports.CustomModelEntryDirect, wantSelection: ports.ModelSelectionText, wantAllowInput: true},
		{agent: "grok", wantEntryMode: ports.CustomModelEntryDirect, wantSelection: ports.ModelSelectionText, wantAllowInput: true},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			emptyHome := t.TempDir()
			t.Setenv("HOME", emptyHome)
			t.Setenv("XDG_CONFIG_HOME", emptyHome)
			svc := NewWithAgents([]agentregistry.HarnessAgent{
				harnessAgent(tc.agent, tc.agent, ports.ErrAgentBinaryNotFound),
			})
			svc.discoverer = testModelDiscoverer
			got, err := svc.Models(context.Background(), tc.agent, "", false)
			if err != nil {
				t.Fatal(err)
			}
			if got.SelectionMode != tc.wantSelection || got.CustomModelEntry != tc.wantEntryMode || got.AllowCustom != tc.wantAllowInput || got.Source != "manual" || len(got.Models) != 0 {
				t.Fatalf("catalog = %#v, want capability-aware fallback", got)
			}
			if tc.agent != "qwen" && (!got.Stale || got.Warning == "") {
				t.Fatalf("catalog = %#v, want discovery warning on manual fallback", got)
			}
		})
	}
}

func TestModelsNormalizesCustomEntryPolicyInOldCache(t *testing.T) {
	for _, tc := range []struct {
		agent          string
		wantEntryMode  ports.CustomModelEntryMode
		wantAllowInput bool
	}{
		{agent: "codex", wantEntryMode: ports.CustomModelEntryDirect, wantAllowInput: true},
		{agent: "opencode", wantEntryMode: ports.CustomModelEntryDirect, wantAllowInput: true},
		{agent: "grok", wantEntryMode: ports.CustomModelEntryDirect, wantAllowInput: true},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			oldCatalog := map[string]any{
				"agentId": tc.agent, "selectionMode": "catalog", "models": []map[string]any{{"id": "cached-model", "label": "Cached model"}},
				"allowCustom": true, "source": "cli", "fetchedAt": time.Now().UTC(), "stale": false,
			}
			data, err := json.Marshal(oldCatalog)
			if err != nil {
				t.Fatal(err)
			}
			cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
				tc.agent + "\x00": {AgentID: tc.agent, CatalogJSON: string(data)},
			}}
			svc := newService([]agentregistry.HarnessAgent{harnessAgent(tc.agent, tc.agent, nil)}, cache, testModelDiscoverer)

			got, err := svc.Models(context.Background(), tc.agent, "", false)
			if err != nil {
				t.Fatal(err)
			}
			if got.CustomModelEntry != tc.wantEntryMode || got.AllowCustom != tc.wantAllowInput {
				t.Fatalf("catalog = %#v, want entry mode %q allowCustom=%v", got, tc.wantEntryMode, tc.wantAllowInput)
			}
		})
	}
}

func TestModelsSerializeBinaryResolutionPerAdapter(t *testing.T) {
	agent := &concurrentResolverAgent{}
	svc := newService([]agentregistry.HarnessAgent{{
		Harness:  domain.AgentHarness("codex"),
		Manifest: adapters.Manifest{ID: "codex", Name: "Codex"},
		Agent:    agent,
	}}, nil, testModelDiscoverer)

	start := make(chan struct{})
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.Models(context.Background(), "codex", "", true)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if agent.overlap.Load() {
		t.Fatal("ResolveBinary calls overlapped for the same adapter")
	}
	if got := agent.calls.Load(); got != 1 {
		t.Fatalf("ResolveBinary calls = %d, want one coalesced model load", got)
	}
}

func TestModelsReturnsDiscoveredCatalogWhenCacheWriteFails(t *testing.T) {
	cache := &fakeModelCache{putErr: errors.New("database unavailable")}
	svc := newService([]agentregistry.HarnessAgent{
		harnessAgent("codex", "Codex", nil),
	}, cache, successfulModelDiscoverer())

	got, err := svc.Models(context.Background(), "codex", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) == 0 || got.SelectionMode != ports.ModelSelectionCatalog {
		t.Fatalf("catalog = %#v, want discovered models", got)
	}
	if !strings.Contains(got.Warning, "could not update the model cache") {
		t.Fatalf("warning = %q, want cache warning", got.Warning)
	}
}

func TestModelsKeepsFullerCacheWhenRefreshReturnsPartialCatalog(t *testing.T) {
	lastSuccessfulValidation := time.Now().Add(-modelCatalogTrustWindow - time.Hour)
	cached := ports.AgentModelCatalog{
		AgentID:       "opencode",
		SelectionMode: ports.ModelSelectionCatalog,
		Models: []ports.AgentModelInfo{
			{ID: "configured/model", Label: "Configured"},
			{ID: "cli/one", Label: "CLI one"},
			{ID: "cli/two", Label: "CLI two"},
		},
		AllowCustom: true,
		Source:      "cli",
		FetchedAt:   lastSuccessfulValidation,
		ValidatedAt: lastSuccessfulValidation,
	}
	data, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"opencode\x00": {AgentID: "opencode", CatalogJSON: string(data)},
	}}
	discoverer := &fakeModelDiscoverer{
		catalog: ports.AgentModelCatalog{
			SelectionMode: ports.ModelSelectionCatalog,
			Models:        []ports.AgentModelInfo{{ID: "configured/model"}},
			AllowCustom:   true,
			Source:        "cli",
		},
		err: errors.New("transient model discovery failure"),
	}
	svc := newService([]agentregistry.HarnessAgent{
		harnessAgent("opencode", "OpenCode", nil),
	}, cache, discoverer)

	got, err := svc.Models(context.Background(), "opencode", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 3 || !got.Stale || got.Warning == "" {
		t.Fatalf("catalog = %#v, want fuller stale cache", got)
	}
	if !got.ValidatedAt.Equal(lastSuccessfulValidation) {
		t.Fatalf("validatedAt = %s, want last successful validation %s", got.ValidatedAt, lastSuccessfulValidation)
	}
	if !got.RefreshRecommended {
		t.Fatal("failed refresh did not remain eligible for automatic retry")
	}
}

func TestClaudeModelsUsesMatchingProviderCache(t *testing.T) {
	validatedAt := time.Now()
	cached := ports.AgentModelCatalog{
		AgentID: "claude-code", SelectionMode: ports.ModelSelectionCatalog,
		Models: []ports.AgentModelInfo{{ID: "us.anthropic.claude-opus-v1", Efforts: []string{"high"}}},
		Source: "provider", FetchedAt: validatedAt, ValidatedAt: validatedAt,
	}
	data, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"claude-code\x00": {
			AgentID: "claude-code", BinaryVersion: "same-fingerprint", CatalogJSON: string(data),
		},
	}}
	discoverer := &fakeModelDiscoverer{
		version: "same-fingerprint",
		catalog: ports.AgentModelCatalog{
			AgentID: "claude-code", SelectionMode: ports.ModelSelectionCatalog,
			Models: []ports.AgentModelInfo{}, Source: "manual",
		},
		err: errors.New("provider unavailable"),
	}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, discoverer)

	got, err := svc.Models(context.Background(), "claude-code", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if discoverer.discoverCalls.Load() != 0 {
		t.Fatalf("discovery calls = %d, want matching provider cache", discoverer.discoverCalls.Load())
	}
	if len(got.Models) != 1 || got.Models[0].ID != "us.anthropic.claude-opus-v1" || got.Stale {
		t.Fatalf("catalog = %#v, want fresh provider cache", got)
	}
}

func TestClaudeModelsRevalidationKeepsMatchingProviderCacheOnFailure(t *testing.T) {
	validatedAt := time.Now().Add(-24 * time.Hour)
	lastSuccessAt := validatedAt
	cached := ports.AgentModelCatalog{
		AgentID: "claude-code", SelectionMode: ports.ModelSelectionCatalog,
		Models:        []ports.AgentModelInfo{{ID: "us.anthropic.claude-opus-v1", Efforts: []string{"high"}}},
		Source:        "provider",
		FetchedAt:     validatedAt,
		ValidatedAt:   validatedAt,
		LastSuccessAt: &lastSuccessAt,
	}
	data, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"claude-code\x00": {
			AgentID: "claude-code", BinaryVersion: "same-fingerprint", CatalogJSON: string(data),
			LastSuccessAt: lastSuccessAt,
		},
	}}
	discoverer := &fakeModelDiscoverer{
		version: "same-fingerprint",
		catalog: ports.AgentModelCatalog{
			AgentID: "claude-code", SelectionMode: ports.ModelSelectionCatalog,
			Models: []ports.AgentModelInfo{}, Source: "manual",
		},
		err: errors.New("provider unavailable"),
	}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, discoverer)

	got, err := svc.RevalidateModels(context.Background(), "claude-code", "")
	if err != nil {
		t.Fatal(err)
	}
	if discoverer.discoverCalls.Load() != 1 {
		t.Fatalf("discovery calls = %d, want provider revalidation", discoverer.discoverCalls.Load())
	}
	if len(got.Models) != 1 || got.Models[0].ID != "us.anthropic.claude-opus-v1" || !got.Stale {
		t.Fatalf("catalog = %#v, want stale provider cache", got)
	}
}

func TestMemcodeModelsRevalidatePreviouslyEmptyCatalogInBackground(t *testing.T) {
	oldCatalog := ports.AgentModelCatalog{AgentID: "memcode", SelectionMode: ports.ModelSelectionCatalog, Models: []ports.AgentModelInfo{}, Source: "manual"}
	data, err := json.Marshal(oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"memcode\x00": {AgentID: "memcode", BinaryVersion: "old", CatalogJSON: string(data)},
	}}
	discoverer := &fakeModelDiscoverer{version: "native-picker-v1", catalog: ports.AgentModelCatalog{
		AgentID: "memcode", SelectionMode: ports.ModelSelectionCatalog,
		Models: []ports.AgentModelInfo{{ID: "sonnet"}, {ID: "opus"}}, Source: "official-catalog",
	}}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("memcode", "Memcode", nil)}, cache, discoverer)
	got, err := svc.Models(context.Background(), "memcode", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 0 {
		t.Fatalf("catalog = %#v, want cached result on first read", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		cached, ok, err := svc.cachedCatalog(context.Background(), "memcode", "")
		if err != nil {
			t.Fatal(err)
		}
		if ok && len(cached.Catalog.Models) == 2 && cached.Catalog.Source == "official-catalog" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh did not replace the old catalog")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestClaudeModelsRejectProviderCacheWhenCredentialFingerprintChanges(t *testing.T) {
	cached := ports.AgentModelCatalog{
		AgentID: "claude-code", SelectionMode: ports.ModelSelectionCatalog,
		Models: []ports.AgentModelInfo{{ID: "us.anthropic.claude-opus-v1"}}, Source: "provider",
	}
	data, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"claude-code\x00": {
			AgentID: "claude-code", BinaryVersion: "credential-a", CatalogJSON: string(data),
		},
	}}
	discoverer := &fakeModelDiscoverer{
		version: "credential-b",
		catalog: ports.AgentModelCatalog{
			AgentID: "claude-code", SelectionMode: ports.ModelSelectionCatalog,
			Models: []ports.AgentModelInfo{}, Source: "manual",
		},
		err: errors.New("provider unavailable"),
	}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, discoverer)

	// The cache is served at once while the credential inputs are checked in
	// the background; it reports itself refreshing so open views re-read it.
	got, err := svc.Models(context.Background(), "claude-code", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "us.anthropic.claude-opus-v1" || got.RefreshState != "refreshing" {
		t.Fatalf("first read = %#v, want the cached catalog marked refreshing", got)
	}
	// A later read returns the catalog rediscovered for the current credential.
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err = svc.Models(context.Background(), "claude-code", "", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Models) == 0 && got.Source == "manual" && got.Stale {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("catalog = %#v, want current-credential fallback after the background check", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGlobalModelsNeverBorrowAnotherScopesCatalog(t *testing.T) {
	project := cachedModelRecord(t, "cursor", "project-a", time.Now().Add(-time.Hour), false)
	project.ProjectID = "project-a"
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{"cursor\x00project-a": project}}
	discoverer := &fakeModelDiscoverer{
		catalog: ports.AgentModelCatalog{AgentID: "cursor", SelectionMode: ports.ModelSelectionCatalog, Models: []ports.AgentModelInfo{{ID: "cursor/global"}}, Source: "catalog"},
	}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("cursor", "Cursor", nil)}, cache, discoverer)
	// Older builds wrote per-project rows. They are never read: the device
	// catalog discovers its own instead of showing a leftover project row.
	got, err := svc.Models(context.Background(), "cursor", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "cursor/global" {
		t.Fatalf("global read = %#v, want its own discovery", got)
	}
}

func TestModelsUsesGlobalCacheWhenDiscoveryFails(t *testing.T) {
	newer := cachedModelRecord(t, "cursor", "", time.Now().Add(-time.Hour), false)
	var newerCatalog ports.AgentModelCatalog
	if err := json.Unmarshal([]byte(newer.CatalogJSON), &newerCatalog); err != nil {
		t.Fatal(err)
	}
	newerCatalog.Models = []ports.AgentModelInfo{{ID: "cursor/latest", Label: "Latest"}}
	newerData, err := json.Marshal(newerCatalog)
	if err != nil {
		t.Fatal(err)
	}
	newer.CatalogJSON = string(newerData)

	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"cursor\x00": newer,
	}}
	discoverer := &fakeModelDiscoverer{err: errors.New("cursor model discovery timed out after 20s")}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("cursor", "Cursor", nil)}, cache, discoverer)

	got, err := svc.Models(context.Background(), "cursor", "project-c", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "cursor/latest" || !got.Stale {
		t.Fatalf("catalog = %#v, want newest agent-wide cache marked stale", got)
	}
	if !strings.Contains(got.Warning, "timed out") {
		t.Fatalf("warning = %q, want discovery failure", got.Warning)
	}
	stored, exists, err := cache.GetAgentModelCatalog(context.Background(), "cursor", "")
	if err != nil || !exists || stored.RefreshState != "error" {
		t.Fatalf("global fallback = (%#v, %v, %v), want durable retryable stale cache", stored, exists, err)
	}
}

func harnessAgent(id, label string, err error) agentregistry.HarnessAgent {
	return agentregistry.HarnessAgent{
		Harness: domain.AgentHarness(id),
		Manifest: adapters.Manifest{
			ID:   id,
			Name: label,
		},
		Agent: fakeAgent{err: err},
	}
}

func harnessAuthAgent(id, label string, status ports.AgentAuthStatus, err error) agentregistry.HarnessAgent {
	return agentregistry.HarnessAgent{
		Harness: domain.AgentHarness(id),
		Manifest: adapters.Manifest{
			ID:   id,
			Name: label,
		},
		Agent: fakeAuthAgent{fakeAgent: fakeAgent{}, status: status, authErr: err},
	}
}

func TestResolveAgentBinaryUsesRequestedAdapter(t *testing.T) {
	t.Parallel()

	svc := NewWithAgents([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)})

	path, err := svc.ResolveAgentBinary(context.Background(), "codex")
	if err != nil {
		t.Fatalf("ResolveAgentBinary(codex): %v", err)
	}
	if path != "agent" {
		t.Fatalf("ResolveAgentBinary(codex) = %q, want adapter-resolved path", path)
	}
}

func TestInvalidateAgentInstallationInvalidatesAdapterBinary(t *testing.T) {
	adapter := &invalidatingAgent{}
	svc := NewWithAgents([]agentregistry.HarnessAgent{{
		Harness:  domain.HarnessCodex,
		Manifest: adapters.Manifest{ID: "codex", Name: "Codex"},
		Agent:    adapter,
	}})

	svc.InvalidateAgentInstallation(string(domain.HarnessCodex))

	if got := adapter.calls.Load(); got != 1 {
		t.Fatalf("binary invalidation calls = %d, want 1", got)
	}
}

func TestModelsFingerprintsTheSameInputsDiscoveryReads(t *testing.T) {
	discoverer := &fakeModelDiscoverer{version: "v1", catalog: ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog,
		Models:        []ports.AgentModelInfo{{ID: "opus"}},
		Source:        "official-aliases",
	}}
	svc := newService([]agentregistry.HarnessAgent{
		harnessAgent("claude-code", "Claude Code", nil),
	}, &fakeModelCache{}, discoverer)

	if _, err := svc.Models(context.Background(), "claude-code", "proj-1", false); err != nil {
		t.Fatal(err)
	}
	// Fingerprinting and discovery must use the same inputs.
	fingerprinted := discoverer.lastFingerprintRequest.Load()
	if fingerprinted == nil {
		t.Fatal("catalog fingerprint was never requested")
		return
	}
	if !reflect.DeepEqual(*fingerprinted, discoverer.lastRequest) {
		t.Fatalf("fingerprint request = %#v, want the discovery request %#v", *fingerprinted, discoverer.lastRequest)
	}
	if fingerprinted.WorkingDir != "" || len(fingerprinted.Env) != 0 {
		t.Fatalf("fingerprint request = %#v, want no project inputs", *fingerprinted)
	}
}

func TestModelsAsksClientsToRevalidateAnAgedCatalog(t *testing.T) {
	cache := &fakeModelCache{}
	discoverer := &blockingSubsequentModelDiscoverer{
		fakeModelDiscoverer: &fakeModelDiscoverer{version: "v1", catalog: ports.AgentModelCatalog{
			SelectionMode: ports.ModelSelectionCatalog,
			Models:        []ports.AgentModelInfo{{ID: "model-one"}},
			Source:        "cli",
		}},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	defer close(discoverer.release)
	svc := newService([]agentregistry.HarnessAgent{
		harnessAgent("opencode", "OpenCode", nil),
	}, cache, discoverer)

	fresh, err := svc.Models(context.Background(), "opencode", "proj-1", false)
	if err != nil {
		t.Fatal(err)
	}
	// Just discovered: nothing to revalidate, so the client must not be nudged.
	if fresh.RefreshRecommended {
		t.Fatal("a freshly discovered catalog asked for revalidation")
	}

	record := cache.records["opencode\x00"]
	var aged ports.AgentModelCatalog
	if err := json.Unmarshal([]byte(record.CatalogJSON), &aged); err != nil {
		t.Fatal(err)
	}
	aged.ValidatedAt = time.Now().Add(-modelCatalogTrustWindow - time.Hour)
	lastSuccess := time.Now().Add(-24 * time.Hour)
	aged.LastSuccessAt = &lastSuccess
	data, err := json.Marshal(aged)
	if err != nil {
		t.Fatal(err)
	}
	record.CatalogJSON = string(data)
	cache.records["opencode\x00"] = record
	discoverer.mu.Lock()
	discoverer.catalog.Models = []ports.AgentModelInfo{{ID: "model-two"}}
	discoverer.mu.Unlock()

	// A CLI-backed catalog can drift with no change to the binary or its config,
	// so an aged cache hit is what replaces the manual "Refresh models" button.
	stale, err := svc.Models(context.Background(), "opencode", "proj-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !stale.RefreshRecommended {
		t.Fatalf("catalog validated %s ago did not ask for revalidation", time.Since(aged.ValidatedAt))
	}
	if len(stale.Models) != 1 || stale.Models[0].ID != "model-one" {
		t.Fatalf("models = %#v, want the cached catalog served immediately", stale.Models)
	}
	select {
	case <-discoverer.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for background cache revalidation")
	}
	if discoverer.discoverCalls.Load() != 1 {
		t.Fatalf("discovery calls = %d before releasing revalidation, want the cached catalog served immediately", discoverer.discoverCalls.Load())
	}
}

func TestRevalidateModelsRediscoversAnAgedCatalog(t *testing.T) {
	cache := &fakeModelCache{}
	discoverer := &fakeModelDiscoverer{version: "v1", catalog: ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog,
		Models:        []ports.AgentModelInfo{{ID: "model-one"}},
		Source:        "cli",
	}}
	svc := newService([]agentregistry.HarnessAgent{
		harnessAgent("opencode", "OpenCode", nil),
	}, cache, discoverer)

	if _, err := svc.Models(context.Background(), "opencode", "proj-1", false); err != nil {
		t.Fatal(err)
	}
	discoverer.catalog.Models = []ports.AgentModelInfo{{ID: "model-two"}}
	discoverer.catalog.FetchedAt = time.Time{}

	got, err := svc.RevalidateModels(context.Background(), "opencode", "proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if discoverer.discoverCalls.Load() != 2 {
		t.Fatalf("discovery calls = %d, want initial load plus revalidation", discoverer.discoverCalls.Load())
	}
	if len(got.Models) != 1 || got.Models[0].ID != "model-two" {
		t.Fatalf("revalidated catalog = %#v, want model-two", got)
	}
	if got.RefreshRecommended {
		t.Fatal("revalidated catalog still recommends refresh")
	}
}

func signInRequiredDiscoverer() *fakeModelDiscoverer {
	return &fakeModelDiscoverer{
		version: "v1",
		err:     fmt.Errorf("kiro model discovery: %w", ports.ErrAgentModelDiscoverySignInRequired),
	}
}

func (f *fakeModelDiscoverer) signIn(models ...ports.AgentModelInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = nil
	f.catalog = ports.AgentModelCatalog{SelectionMode: ports.ModelSelectionCatalog, Models: models, Source: "cli"}
}

func (f *fakeModelDiscoverer) signOut() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = fmt.Errorf("kiro model discovery: %w", ports.ErrAgentModelDiscoverySignInRequired)
}

func assertIdleWithoutRetries(t *testing.T, cache *fakeModelCache, agentID string) {
	t.Helper()
	record, ok, _ := cache.GetAgentModelCatalog(context.Background(), agentID, "")
	if !ok {
		t.Fatal("no catalog record was stored")
	}
	if record.RefreshState != "idle" || record.RefreshError != "" || record.RetryCount != 0 || !record.RetryAt.IsZero() {
		t.Fatalf("record = state %q error %q retries %d retryAt %s, want idle with no retry scheduled",
			record.RefreshState, record.RefreshError, record.RetryCount, record.RetryAt)
	}
}

func TestSignInRequiredDiscoveryStoresAnIdlePlaceholderThatLoadsAfterSignIn(t *testing.T) {
	cache := &fakeModelCache{}
	discoverer := signInRequiredDiscoverer()
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("kiro", "Kiro", nil)}, cache, discoverer)

	got, err := svc.Models(context.Background(), "kiro", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 0 || got.Stale || got.RefreshState != "idle" || !strings.Contains(got.Warning, "Kiro is not signed in") {
		t.Fatalf("signed-out catalog = %#v, want an idle, non-stale placeholder with a sign-in warning", got)
	}
	assertIdleWithoutRetries(t, cache, "kiro")

	// The placeholder has never succeeded, so cache-first readers are told to
	// revalidate; that is how the models appear once the user signs in.
	read, err := svc.Models(context.Background(), "kiro", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !read.RefreshRecommended {
		t.Fatal("signed-out placeholder does not ask readers to revalidate")
	}
	if !strings.Contains(read.Warning, "Kiro is not signed in") {
		t.Fatalf("cache-first warning = %q, want the sign-in warning", read.Warning)
	}

	discoverer.signIn(ports.AgentModelInfo{ID: "model-one"})
	got, err = svc.RevalidateModels(context.Background(), "kiro", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "model-one" || got.Warning != "" {
		t.Fatalf("catalog after sign-in = %#v, want model-one", got)
	}
}

func TestSignInRequiredDiscoveryKeepsTheCachedCatalogAndRetryBudget(t *testing.T) {
	cache := &fakeModelCache{}
	discoverer := signInRequiredDiscoverer()
	discoverer.signIn(ports.AgentModelInfo{ID: "model-one"})
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("kiro", "Kiro", nil)}, cache, discoverer)
	if _, err := svc.Models(context.Background(), "kiro", "", true); err != nil {
		t.Fatal(err)
	}

	// An earlier ordinary failure leaves the cached list stale with its error.
	discoverer.mu.Lock()
	discoverer.err = errors.New("kiro model discovery timed out after 20s")
	discoverer.mu.Unlock()
	if failed, err := svc.Models(context.Background(), "kiro", "", true); err != nil || !failed.Stale {
		t.Fatalf("failed refresh = %#v, %v; want a stale cached catalog", failed, err)
	}

	discoverer.signOut()
	// More skipped attempts than the failure retry budget allows.
	for range modelCatalogMaxRetries + 2 {
		got, err := svc.Models(context.Background(), "kiro", "", true)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Models) != 1 || got.Models[0].ID != "model-one" || got.Stale || got.RefreshState != "idle" {
			t.Fatalf("signed-out refresh = %#v, want the cached model-one catalog, not stale", got)
		}
		assertIdleWithoutRetries(t, cache, "kiro")
	}
	// Cache-first reads show the sign-in state, not the earlier timeout.
	read, err := svc.Models(context.Background(), "kiro", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if read.Stale || !strings.Contains(read.Warning, "Kiro is not signed in") || strings.Contains(read.Warning, "timed out") {
		t.Fatalf("cache-first read = stale %t warning %q, want the sign-in warning only", read.Stale, read.Warning)
	}

	discoverer.signIn(ports.AgentModelInfo{ID: "model-two"})
	got, err := svc.RevalidateModels(context.Background(), "kiro", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "model-two" {
		t.Fatalf("catalog after signing back in = %#v, want model-two", got)
	}
}

func TestSignInOutsideAOReloadsACatalogThatLoadedEarlierTheSameDay(t *testing.T) {
	cache := &fakeModelCache{}
	discoverer := signInRequiredDiscoverer()
	discoverer.signIn(ports.AgentModelInfo{ID: "model-one"})
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("kiro", "Kiro", nil)}, cache, discoverer)
	noon := time.Date(2026, 9, 24, 12, 0, 0, 0, time.Local)
	svc.now = func() time.Time { return noon }

	// Models load in the morning, then the user signs out and a refresh is skipped.
	if _, err := svc.Models(context.Background(), "kiro", "", true); err != nil {
		t.Fatal(err)
	}
	discoverer.signOut()
	if _, err := svc.Models(context.Background(), "kiro", "", true); err != nil {
		t.Fatal(err)
	}

	// The user signs back in from a terminal; AO's auth probe is never called.
	discoverer.signIn(ports.AgentModelInfo{ID: "model-two"})
	noon = noon.Add(time.Hour)
	read, err := svc.Models(context.Background(), "kiro", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !read.RefreshRecommended {
		t.Fatal("same-day catalog skipped for sign-in is not due for revalidation")
	}
	// The cache-first read revalidates in the background.
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := svc.Models(context.Background(), "kiro", "", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Models) == 1 && got.Models[0].ID == "model-two" && got.Warning == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("catalog after signing in outside AO = %#v, want model-two without the sign-in warning", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOrdinaryDiscoveryFailureStillSpendsTheRetryBudget(t *testing.T) {
	cache := &fakeModelCache{}
	discoverer := &fakeModelDiscoverer{version: "v1", err: errors.New("kiro model discovery: exit status 1")}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("kiro", "Kiro", nil)}, cache, discoverer)
	if _, err := svc.Models(context.Background(), "kiro", "", true); err != nil {
		t.Fatal(err)
	}
	record, _, _ := cache.GetAgentModelCatalog(context.Background(), "kiro", "")
	if record.RefreshState != "error" || record.RetryCount != 1 {
		t.Fatalf("record = state %q retries %d, want an error with one retry spent", record.RefreshState, record.RetryCount)
	}
}

func cachedModelRecord(t *testing.T, agentID, _ string, validatedAt time.Time, stale bool) ports.CachedAgentModelCatalog {
	t.Helper()
	catalog := ports.AgentModelCatalog{
		AgentID: agentID, SelectionMode: ports.ModelSelectionCatalog,
		Models: []ports.AgentModelInfo{{ID: "cached-model"}}, AllowCustom: true,
		Source: "cli", FetchedAt: validatedAt, ValidatedAt: validatedAt, Stale: stale,
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	return ports.CachedAgentModelCatalog{AgentID: agentID, CatalogJSON: string(data), FetchedAt: validatedAt}
}

func TestPrefetchModelCatalogsRefreshesAllHarnessesWithBoundedConcurrency(t *testing.T) {
	old := time.Now().Add(-24 * time.Hour)
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"claude-code\x00": cachedModelRecord(t, "claude-code", "", old, false),
		"muse\x00":        cachedModelRecord(t, "muse", "", old, false),
		"codex\x00":       cachedModelRecord(t, "codex", "", old, false),
	}}
	discoverer := &fakeModelDiscoverer{delay: 10 * time.Millisecond, catalog: ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog, Models: []ports.AgentModelInfo{{ID: "fresh-model"}}, Source: "cli",
	}}
	svc := newService([]agentregistry.HarnessAgent{
		harnessAgent("claude-code", "Claude Code", nil),
		harnessAgent("muse", "Muse", nil),
		harnessAgent("codex", "Codex", nil),
	}, cache, discoverer)

	svc.prefetchModelCatalogs(context.Background(), false)
	if got := discoverer.discoverCalls.Load(); got != 3 {
		t.Fatalf("discovery calls = %d, want Claude, Muse and Codex refreshed", got)
	}
	if got := discoverer.maxActive.Load(); got > 2 {
		t.Fatalf("concurrent discoveries = %d, want at most two", got)
	}
}

func TestPrefetchModelCatalogsSuppressesRecentSuccessfulValidation(t *testing.T) {
	recent := time.Now()
	record := cachedModelRecord(t, "claude-code", "", recent, false)
	record.LastSuccessAt = recent
	record.RefreshState = "idle"
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"claude-code\x00": record,
	}}
	discoverer := &fakeModelDiscoverer{catalog: ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog, Models: []ports.AgentModelInfo{{ID: "fresh-model"}}, Source: "cli",
	}}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, discoverer)

	svc.prefetchModelCatalogs(context.Background(), false)
	if got := discoverer.discoverCalls.Load(); got != 0 {
		t.Fatalf("discovery calls = %d, want recent global catalog preserved", got)
	}
}

func TestWarmModelCatalogsStartsAsynchronously(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"muse\x00": cachedModelRecord(t, "muse", "", old, false),
	}}
	discoverer := &fakeModelDiscoverer{delay: 100 * time.Millisecond, catalog: ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog, Models: []ports.AgentModelInfo{{ID: "fresh-model"}}, Source: "cli",
	}}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("muse", "Muse", nil)}, cache, discoverer)

	started := time.Now()
	svc.WarmModelCatalogs(context.Background())
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("WarmModelCatalogs blocked for %s", elapsed)
	}
	deadline := time.Now().Add(time.Second)
	for discoverer.discoverCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if discoverer.discoverCalls.Load() == 0 {
		t.Fatal("background warm did not start")
	}
	for discoverer.active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if discoverer.active.Load() != 0 {
		t.Fatal("background warm did not finish")
	}
}

// Catalog browsing must not wait on recency hints used only by task pickers.
type forbiddenCatalogSessionLookup struct{}

func (forbiddenCatalogSessionLookup) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	panic("catalog-only read scanned session history")
}

func TestModelCatalogServesPersistedModelsDuringBlockedRefresh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"fx\x00": cachedModelRecord(t, "fx", "", time.Now(), false),
	}}
	discoverer := &blockingSubsequentModelDiscoverer{
		fakeModelDiscoverer: successfulModelDiscoverer(),
		started:             make(chan struct{}), release: make(chan struct{}),
	}
	// The persisted catalog came from an earlier daemon process.
	discoverer.discoverCalls.Store(1)
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("fx", "FX", nil)}, cache, discoverer)
	svc.ctx = ctx
	svc.sessions = forbiddenCatalogSessionLookup{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = svc.ModelCatalog(ctx, "fx", "", true)
	}()
	defer func() { close(discoverer.release); <-done }()
	select {
	case <-discoverer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not start")
	}
	readCtx, stopRead := context.WithTimeout(ctx, time.Second)
	defer stopRead()
	got, err := svc.ModelCatalog(readCtx, "fx", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "cached-model" {
		t.Fatalf("catalog = %#v, want persisted models before refresh completes", got)
	}
}

func TestSettingsAndTaskModelsSharePersistentCatalog(t *testing.T) {
	for _, agentID := range []string{"fx", "opencode", "qwen"} {
		t.Run(agentID, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cache := &fakeModelCache{}
			discoverer := successfulModelDiscoverer()
			svc := newService([]agentregistry.HarnessAgent{harnessAgent(agentID, agentID, nil)}, cache, discoverer)
			svc.ctx = ctx
			if _, err := svc.Models(ctx, agentID, "", true); err != nil {
				t.Fatal(err)
			}
			// Settings must read the task catalog saved by a previous daemon,
			// even when no Settings-specific catalog has ever been discovered.
			restarted := newService(svc.agents, cache, discoverer)
			restarted.ctx = ctx
			restarted.sessions = forbiddenCatalogSessionLookup{}
			got, err := restarted.ModelCatalog(ctx, agentID, "", false)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Models) != 1 || got.Models[0].ID != "model-one" {
				t.Fatalf("Settings models = %#v", got.Models)
			}
			if calls := discoverer.discoverCalls.Load(); calls != 1 {
				t.Fatalf("discoveries = %d, want the persisted task catalog reused", calls)
			}
			discoverer.mu.Lock()
			discoverer.catalog.Models = []ports.AgentModelInfo{{ID: "refreshed-model"}}
			discoverer.mu.Unlock()
			if _, err := restarted.ModelCatalog(ctx, agentID, "", true); err != nil {
				t.Fatal(err)
			}
			got, err = svc.Models(ctx, agentID, "", false)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Models) != 1 || got.Models[0].ID != "refreshed-model" {
				t.Fatalf("task models after Settings refresh = %#v", got.Models)
			}
			if _, ok, _ := cache.GetAgentModelCatalog(ctx, agentID, "@browse:all"); ok {
				t.Fatal("Settings created a separate browsing catalog")
			}
		})
	}
}

func TestSharedCatalogRefreshRetainsOnlyFailedProviderModels(t *testing.T) {
	cache := &fakeModelCache{}
	discoverer := successfulModelDiscoverer()
	discoverer.catalog = ports.AgentModelCatalog{AgentID: "fx", Models: []ports.AgentModelInfo{
		{ID: "old", Provider: "gateway"}, {ID: "cached", Provider: "codex"},
	}}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("fx", "FX", nil)}, cache, discoverer)
	if _, err := svc.ModelCatalog(context.Background(), "fx", "", true); err != nil {
		t.Fatal(err)
	}
	discoverer.mu.Lock()
	discoverer.catalog = ports.AgentModelCatalog{AgentID: "fx", Models: []ports.AgentModelInfo{{ID: "new", Provider: "gateway"}},
		Metadata: map[string]string{"unavailableProviders": "codex"}, Warning: "Codex unavailable"}
	discoverer.mu.Unlock()
	for range 2 {
		got, err := svc.ModelCatalog(context.Background(), "fx", "", true)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Models) != 2 || got.Models[0].ID != "new" || got.Models[1].ID != "cached" {
			t.Fatalf("models = %#v", got.Models)
		}
		if got.Warning == "" {
			t.Fatal("missing partial refresh explanation")
		}
	}
}

func TestStartupPrefetchIgnoresRetiredSettingsCatalog(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	retired := cachedModelRecord(t, "fx", "", old, false)
	retired.ProjectID = "@browse:all"
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"fx\x00@browse:all": retired,
	}}
	discoverer := successfulModelDiscoverer()
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("fx", "FX", nil)}, cache, discoverer)

	svc.prefetchModelCatalogs(context.Background(), true)

	if calls := discoverer.discoverCalls.Load(); calls != 1 {
		t.Fatalf("discoveries = %d, want only the shared global catalog warmed", calls)
	}
	if _, ok, _ := cache.GetAgentModelCatalog(context.Background(), "fx", ""); !ok {
		t.Fatal("missing shared catalog")
	}
}
