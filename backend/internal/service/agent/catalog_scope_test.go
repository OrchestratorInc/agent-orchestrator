package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type scopeModelDiscoverer struct {
	*fakeModelDiscoverer
	scopeMu      sync.Mutex
	cheap        string
	identity     string
	conclusive   bool
	checkStarted chan struct{}
	checkRelease chan struct{}
	checkOnce    sync.Once
	attempts     atomic.Int32
	discover     func(context.Context, int32) (ports.AgentModelCatalog, error)
}

func (f *scopeModelDiscoverer) CatalogFingerprint(context.Context, ports.AgentModelDiscoveryRequest) string {
	f.scopeMu.Lock()
	defer f.scopeMu.Unlock()
	return f.cheap
}

func (f *scopeModelDiscoverer) CatalogIdentityFingerprint(ctx context.Context, _ ports.AgentModelDiscoveryRequest) (string, bool) {
	if f.checkStarted != nil {
		f.checkOnce.Do(func() { close(f.checkStarted) })
		select {
		case <-f.checkRelease:
		case <-ctx.Done():
			return "", false
		}
	}
	f.scopeMu.Lock()
	defer f.scopeMu.Unlock()
	return f.identity, f.conclusive
}

func (f *scopeModelDiscoverer) Discover(ctx context.Context, request ports.AgentModelDiscoveryRequest) (ports.AgentModelCatalog, error) {
	attempt := f.attempts.Add(1)
	f.scopeMu.Lock()
	identity := f.identity
	f.scopeMu.Unlock()
	var catalog ports.AgentModelCatalog
	var err error
	if f.discover != nil {
		catalog, err = f.discover(ctx, attempt)
	} else {
		catalog, err = f.fakeModelDiscoverer.Discover(ctx, request)
	}
	catalog.InputFingerprint = identity
	return catalog, err
}

func (f *scopeModelDiscoverer) setScope(cheap, identity string) {
	f.scopeMu.Lock()
	defer f.scopeMu.Unlock()
	if cheap != "" {
		f.cheap = cheap
	}
	if identity != "" {
		f.identity = identity
	}
}

func (f *scopeModelDiscoverer) setModels(err error, ids ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.catalog.Models = nil
	for _, id := range ids {
		f.catalog.Models = append(f.catalog.Models, ports.AgentModelInfo{ID: id})
	}
	f.err = err
}

// A zero attempt holds every discovery; otherwise only that attempt is held.
func (f *scopeModelDiscoverer) holdDiscovery(attempt int32, outgoing, replacement string) (<-chan struct{}, chan<- struct{}) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.discover = func(ctx context.Context, current int32) (ports.AgentModelCatalog, error) {
		id := replacement
		if attempt == 0 || current == attempt {
			id = outgoing
			once.Do(func() { close(started) })
			select {
			case <-release:
			case <-ctx.Done():
				return ports.AgentModelCatalog{}, ctx.Err()
			}
		}
		return ports.AgentModelCatalog{AgentID: "codex", Models: []ports.AgentModelInfo{{ID: id}}, Source: "cli", FetchedAt: time.Now().UTC()}, nil
	}
	return started, release
}

func (c *fakeModelCache) setWriteError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putErr = err
}

func scopedModelService(t *testing.T, id string) (*Service, *scopeModelDiscoverer, *fakeModelCache) {
	t.Helper()
	discoverer := &scopeModelDiscoverer{fakeModelDiscoverer: successfulModelDiscoverer(), cheap: "local-a", identity: "account-a", conclusive: true}
	cache := &fakeModelCache{}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent(id, id, nil)}, cache, nil, discoverer)
	ctx, cancel := context.WithCancel(context.Background())
	svc.ctx = ctx
	t.Cleanup(cancel)
	catalog, err := svc.Models(ctx, id, "", false)
	assertScopeCatalog(t, catalog, err, "model-one", "")
	return svc, discoverer, cache
}

func TestCatalogScopeAllRegisteredHarnessesRejectChangedInputs(t *testing.T) {
	for _, adapter := range agentregistry.Constructors() {
		id := adapter.Manifest().ID
		t.Run(id, func(t *testing.T) {
			svc, discoverer, _ := scopedModelService(t, id)
			discoverer.setScope("local-b", "")
			discoverer.setModels(errors.New("provider unavailable"))
			got, err := svc.Models(context.Background(), id, "", false)
			if err != nil || len(got.Models) != 0 || got.Source != "manual" || got.Warning == "" {
				t.Fatalf("changed scope catalog = %#v, %v; want empty warned manual fallback", got, err)
			}
		})
	}
}

func TestCatalogScopeChangedInputsSignInCannotRetainOldModels(t *testing.T) {
	svc, discoverer, _ := scopedModelService(t, "claude-code")
	discoverer.setScope("local-b", "account-b")
	discoverer.setModels(ports.ErrAgentModelDiscoverySignInRequired)
	got, err := svc.Models(context.Background(), "claude-code", "", false)
	if err != nil || len(got.Models) != 0 || got.InputFingerprint != "account-b" {
		t.Fatalf("sign-in fallback = %#v, %v", got, err)
	}
}

func TestCatalogScopeOpaqueIdentityChangeDropsIncompatibleFailureFallback(t *testing.T) {
	svc, discoverer, _ := scopedModelService(t, "claude-code")
	discoverer.setScope("", "account-b")
	discoverer.setModels(errors.New("offline"))
	got, err := svc.coalesceModelLoad(context.Background(), "claude-code", "", modelLoadCheck)
	if err != nil || len(got.Models) != 0 || got.InputFingerprint != "account-b" || got.Warning == "" {
		t.Fatalf("opaque changed-scope fallback = %#v, %v", got, err)
	}
}

func TestCatalogScopeInconclusiveIdentityPreservesSameScopeFallback(t *testing.T) {
	svc, discoverer, _ := scopedModelService(t, "claude-code")
	discoverer.scopeMu.Lock()
	discoverer.conclusive = false
	discoverer.scopeMu.Unlock()
	got, err := svc.coalesceModelLoad(context.Background(), "claude-code", "", modelLoadCheck)
	if err != nil || len(got.Models) != 1 || got.Models[0].ID != "model-one" || discoverer.attempts.Load() != 1 {
		t.Fatalf("inconclusive identity = %#v, %v; attempts %d", got, err, discoverer.attempts.Load())
	}
	discoverer.setModels(errors.New("offline"))
	got, err = svc.RevalidateModels(context.Background(), "claude-code", "")
	if err != nil || len(got.Models) != 1 || got.Models[0].ID != "model-one" || !got.Stale || got.InputFingerprint != "account-a" {
		t.Fatalf("same-scope failure fallback = %#v, %v", got, err)
	}
}

func TestCatalogScopeWarmReadsAndUnchangedIdentityDoNotRediscover(t *testing.T) {
	svc, discoverer, _ := scopedModelService(t, "codex")
	for range 10 {
		got, err := svc.Models(context.Background(), "codex", "", false)
		assertScopeCatalog(t, got, err, "model-one", "")
		_, err = svc.coalesceModelLoad(context.Background(), "codex", "", modelLoadCheck)
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := discoverer.attempts.Load(); got != 1 {
		t.Fatalf("discovery attempts = %d, want 1", got)
	}
}

type modelWaiterContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *modelWaiterContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

type scopeModelResult struct {
	catalog ports.AgentModelCatalog
	err     error
}

func startScopeModelRead(load func() (ports.AgentModelCatalog, error)) <-chan scopeModelResult {
	done := make(chan scopeModelResult, 1)
	go func() {
		catalog, err := load()
		done <- scopeModelResult{catalog, err}
	}()
	return done
}

func awaitScopeModelRead(t *testing.T, done <-chan scopeModelResult) (ports.AgentModelCatalog, error) {
	t.Helper()
	select {
	case result := <-done:
		return result.catalog, result.err
	case <-time.After(3 * time.Second):
		t.Fatal("model catalog read timed out")
		return ports.AgentModelCatalog{}, context.DeadlineExceeded
	}
}

func readScopeModel(t *testing.T, svc *Service, ctx context.Context, refresh bool, id, identity string) {
	t.Helper()
	catalog, err := svc.Models(ctx, "codex", "", refresh)
	assertScopeCatalog(t, catalog, err, id, identity)
}

func assertScopeCatalog(t *testing.T, catalog ports.AgentModelCatalog, err error, id, identity string) {
	t.Helper()
	if err != nil || len(catalog.Models) != 1 || catalog.Models[0].ID != id || (identity != "" && catalog.InputFingerprint != identity) {
		t.Fatalf("catalog = %#v, %v; want model %q, identity %q", catalog, err, id, identity)
	}
}

func awaitModelSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("model catalog synchronization timed out")
	}
}

func TestCatalogScopeManualRefreshJoiningNonDiscoveryStillDiscovers(t *testing.T) {
	for _, mode := range []string{"identity check", "backoff revalidation"} {
		t.Run(mode, func(t *testing.T) {
			svc, discoverer, cache := scopedModelService(t, "codex")
			started, release := make(chan struct{}), make(chan struct{})
			background := func() { _, _ = svc.coalesceModelLoad(context.Background(), "codex", "", modelLoadCheck) }
			if mode == "identity check" {
				discoverer.checkStarted, discoverer.checkRelease = started, release
			} else {
				blocking := &blockedModelScopeReadCache{fakeModelCache: cache, started: started, release: release}
				record, _, _ := cache.GetAgentModelCatalog(context.Background(), "codex", "")
				record.RefreshState, record.RetryCount = "error", 1
				record.RetryAt = time.Now().Add(time.Hour)
				if err := cache.UpsertAgentModelCatalog(context.Background(), record); err != nil {
					t.Fatal(err)
				}
				svc.cache = blocking
				background = func() { _, _ = svc.RevalidateModels(context.Background(), "codex", "") }
			}
			checkDone := make(chan struct{})
			go func() {
				defer close(checkDone)
				background()
			}()
			awaitModelSignal(t, started)
			waiter := &modelWaiterContext{Context: context.Background(), entered: make(chan struct{})}
			refreshDone := startScopeModelRead(func() (ports.AgentModelCatalog, error) { return svc.Models(waiter, "codex", "", true) })
			awaitModelSignal(t, waiter.entered)
			close(release)
			awaitModelSignal(t, checkDone)
			got, err := awaitScopeModelRead(t, refreshDone)
			assertScopeCatalog(t, got, err, "model-one", "")
			if discoverer.attempts.Load() != 2 || got.RefreshState != "idle" {
				t.Fatalf("manual refresh = %#v; attempts %d", got, discoverer.attempts.Load())
			}
		})
	}
}

type modelScopeQueuedCache struct {
	*fakeModelCache
	queued     chan struct{}
	once       sync.Once
	generation atomic.Int64
}

func (c *modelScopeQueuedCache) UpsertAgentModelCatalog(ctx context.Context, record ports.CachedAgentModelCatalog) error {
	err := c.fakeModelCache.UpsertAgentModelCatalog(ctx, record)
	if err == nil && record.RefreshState == "queued" {
		c.generation.Store(record.Generation)
		c.once.Do(func() { close(c.queued) })
	}
	return err
}

func TestCatalogScopeInvalidationDuringDiscoveryRunsNewGeneration(t *testing.T) {
	svc, discoverer, cache := scopedModelService(t, "codex")
	queuedCache := &modelScopeQueuedCache{fakeModelCache: cache, queued: make(chan struct{})}
	svc.cache = queuedCache
	started, release := discoverer.holdDiscovery(2, "outgoing-model", "post-invalidation-model")
	firstDone := make(chan struct{})
	go func() { defer close(firstDone); _, _ = svc.RevalidateModels(context.Background(), "codex", "") }()
	awaitModelSignal(t, started)
	record, _, _ := cache.GetAgentModelCatalog(context.Background(), "codex", "")
	invalidationDone := make(chan struct{})
	go func() { defer close(invalidationDone); svc.invalidateModelCatalog(record) }()
	awaitModelSignal(t, queuedCache.queued)
	close(release)
	awaitModelSignal(t, firstDone)
	awaitModelSignal(t, invalidationDone)
	latest, ok, err := svc.cachedCatalog(context.Background(), "codex", "")
	if err != nil || !ok || latest.RefreshState != "idle" || len(latest.Catalog.Models) != 1 || latest.Catalog.Models[0].ID != "post-invalidation-model" || latest.Generation <= queuedCache.generation.Load() || discoverer.attempts.Load() < 3 {
		t.Fatalf("invalidation catalog = %#v, %v; attempts %d", latest, err, discoverer.attempts.Load())
	}
}

func TestCatalogScopeChangedDuringHeldDiscoveryDoesNotReturnOutgoingModels(t *testing.T) {
	svc, discoverer, _ := scopedModelService(t, "codex")
	started, release := discoverer.holdDiscovery(2, "model-a", "model-b")
	leader := startScopeModelRead(func() (ports.AgentModelCatalog, error) {
		return svc.RevalidateModels(context.Background(), "codex", "")
	})
	awaitModelSignal(t, started)
	discoverer.setScope("local-b", "")
	waiterCtx := &modelWaiterContext{Context: context.Background(), entered: make(chan struct{})}
	waiter := startScopeModelRead(func() (ports.AgentModelCatalog, error) { return svc.Models(waiterCtx, "codex", "", false) })
	awaitModelSignal(t, waiterCtx.entered)
	close(release)
	for name, response := range map[string]<-chan scopeModelResult{"leader": leader, "waiter": waiter} {
		t.Run(name, func(t *testing.T) {
			got, err := awaitScopeModelRead(t, response)
			assertScopeCatalog(t, got, err, "model-b", "")
			if got.BinaryVersion != "local-b" {
				t.Fatalf("returned outgoing scope %#v", got)
			}
		})
	}
}

func TestCatalogScopeExplicitAuthInvalidationCannotRehydrateOutgoingCatalog(t *testing.T) {
	svc, discoverer, cache := scopedModelService(t, "codex")
	started, release := discoverer.holdDiscovery(0, "account-b-model", "account-b-model")
	queued := &modelScopeQueuedCache{fakeModelCache: cache, queued: make(chan struct{})}
	svc.cache = queued
	discoverer.setScope("", "account-b")
	svc.InvalidateAgentAuthentication("codex")
	pending, found, pendingErr := svc.cachedCatalog(context.Background(), "codex", "")
	if pendingErr != nil || !found || len(pending.Catalog.Models) != 0 {
		t.Errorf("auth invalidation did not synchronously clear models %#v, %v", pending, pendingErr)
	}
	awaitModelSignal(t, started)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	got, err := svc.Models(ctx, "codex", "", false)
	cancel()
	close(release)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("auth-invalidated read error %v", err)
	}
	if len(got.Models) > 0 && got.Models[0].ID == "model-one" {
		t.Fatalf("auth-invalidated GET rehydrated outgoing models %#v", got)
	}
}

func TestCatalogScopeEmptyFallbackWarmChecksDoNotRepeatResolver(t *testing.T) {
	for _, discoveryErr := range []error{errors.New("offline"), ports.ErrAgentModelDiscoverySignInRequired} {
		t.Run(discoveryErr.Error(), func(t *testing.T) {
			discoverer := &scopeModelDiscoverer{fakeModelDiscoverer: &fakeModelDiscoverer{err: discoveryErr}, cheap: "local-a", identity: "account-a", conclusive: true}
			resolver := &countingResolverAgent{}
			item := harnessAgent("codex", "Codex", nil)
			item.Agent = resolver
			svc := newService([]agentregistry.HarnessAgent{item}, &fakeModelCache{}, nil, discoverer)
			ctx, cancel := context.WithCancel(context.Background())
			svc.ctx = ctx
			t.Cleanup(cancel)
			initial, err := svc.Models(context.Background(), "codex", "", false)
			if err != nil || len(initial.Models) != 0 || initial.Warning == "" {
				t.Fatalf("empty fallback %#v, %v", initial, err)
			}
			cancel()
			for range 5 {
				got, err := svc.Models(context.Background(), "codex", "", false)
				if err != nil || len(got.Models) != 0 || got.Warning == "" {
					t.Fatalf("warm empty fallback %#v, %v", got, err)
				}
			}
			if got := resolver.calls.Load(); got != 1 {
				t.Fatalf("resolver calls %d, want only first cold resolution", got)
			}
			if initial.Metadata["binary"] != "agent" {
				t.Fatalf("fallback omitted binary metadata %#v", initial.Metadata)
			}
		})
	}
}

type blockedModelScopeReadCache struct {
	*fakeModelCache
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *blockedModelScopeReadCache) GetAgentModelCatalog(ctx context.Context, agentID, projectID string) (ports.CachedAgentModelCatalog, bool, error) {
	c.once.Do(func() {
		close(c.started)
		select {
		case <-c.release:
		case <-ctx.Done():
		}
	})
	if err := ctx.Err(); err != nil {
		return ports.CachedAgentModelCatalog{}, false, err
	}
	return c.fakeModelCache.GetAgentModelCatalog(ctx, agentID, projectID)
}

type pathAwareScopeDiscoverer struct{ *scopeModelDiscoverer }

func (f *pathAwareScopeDiscoverer) CatalogFingerprint(_ context.Context, request ports.AgentModelDiscoveryRequest) string {
	if request.Binary == "" {
		return "binary-missing"
	}
	return "binary-agent"
}

func TestCatalogScopePartialErrorWithoutDurableCacheDoesNotRediscover(t *testing.T) {
	for name, cache := range map[string]ports.AgentModelCatalogCache{
		"no-cache":      nil,
		"write-failure": &fakeModelCache{putErr: errors.New("database unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			base := &scopeModelDiscoverer{
				fakeModelDiscoverer: &fakeModelDiscoverer{
					catalog: ports.AgentModelCatalog{SelectionMode: ports.ModelSelectionCatalog, Models: []ports.AgentModelInfo{{ID: "partial-model", Efforts: []string{"low", "high"}}}, Source: "cli"},
					err:     errors.New("provider returned partial results"),
				},
				identity: "account-a", conclusive: true,
			}
			discoverer := &pathAwareScopeDiscoverer{scopeModelDiscoverer: base}
			svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, nil, discoverer)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			svc.ctx = ctx
			got, err := svc.Models(ctx, "codex", "", false)
			if err != nil || len(got.Models) != 1 || got.Models[0].ID != "partial-model" || len(got.Models[0].Efforts) != 2 || !got.Stale || got.Warning == "" || got.Metadata["binary"] != "agent" {
				t.Fatalf("partial-error catalog %#v, %v", got, err)
			}
			if attempts := base.attempts.Load(); attempts != 1 {
				t.Fatalf("partial-error discoveries %d, want 1", attempts)
			}
		})
	}
}

func TestCatalogScopeCompatibleWarmReadsDeferIdentityChecksToSweep(t *testing.T) {
	svc, discoverer, _ := scopedModelService(t, "claude-code")
	discoverer.checkStarted = make(chan struct{})
	discoverer.checkRelease = make(chan struct{})
	for range 5 {
		got, err := svc.Models(context.Background(), "claude-code", "", false)
		assertScopeCatalog(t, got, err, "model-one", "")
	}
	select {
	case <-discoverer.checkStarted:
		close(discoverer.checkRelease)
		t.Fatal("compatible warm read launched an identity probe")
	case <-time.After(30 * time.Millisecond):
	}
	done := make(chan struct{})
	go func() { defer close(done); svc.checkModelCatalogInputs(svc.ctx) }()
	awaitModelSignal(t, discoverer.checkStarted)
	close(discoverer.checkRelease)
	awaitModelSignal(t, done)
	if got := discoverer.attempts.Load(); got != 1 {
		t.Fatalf("unchanged-identity sweep discoveries %d, want 1", got)
	}
}

func TestCatalogScopeAuthFenceRejectsFailedWriteAndFailedReplacementFallback(t *testing.T) {
	svc, discoverer, cache := scopedModelService(t, "codex")
	cache.setWriteError(errors.New("database unavailable"))
	discoverer.setScope("", "account-b")
	discoverer.setModels(errors.New("provider unavailable"))
	svc.InvalidateAgentAuthentication("codex")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for range 3 {
		got, err := svc.Models(ctx, "codex", "", false)
		if err != nil || len(got.Models) != 0 || got.Warning == "" {
			t.Fatalf("failed-write auth fallback resurrected outgoing catalog %#v, %v", got, err)
		}
	}
	record, found, err := cache.GetAgentModelCatalog(ctx, "codex", "")
	if err != nil || !found {
		t.Fatalf("old durable row unreadable %v, %v", found, err)
	}
	var old ports.AgentModelCatalog
	if err := json.Unmarshal([]byte(record.CatalogJSON), &old); err != nil {
		t.Fatal(err)
	}
	if len(old.Models) != 1 || old.Models[0].ID != "model-one" {
		t.Fatalf("test did not retain old durable catalog %#v", old)
	}
	cache.setWriteError(nil)
	discoverer.setModels(nil, "account-b-model")
	got, err := svc.Models(ctx, "codex", "", true)
	assertScopeCatalog(t, got, err, "account-b-model", "account-b")
	warm, err := svc.Models(ctx, "codex", "", false)
	assertScopeCatalog(t, warm, err, "account-b-model", "")
}

func TestCatalogScopeOrdinaryInvalidationCannotBypassFailedAuthFence(t *testing.T) {
	svc, discoverer, cache := scopedModelService(t, "codex")
	queued := &modelScopeQueuedCache{fakeModelCache: cache, queued: make(chan struct{})}
	svc.cache = queued
	started, release := discoverer.holdDiscovery(0, "account-b-model", "account-b-model")
	cache.setWriteError(errors.New("database unavailable"))
	discoverer.setScope("", "account-b")
	svc.InvalidateAgentAuthentication("codex")
	awaitModelSignal(t, started)
	old, found, err := svc.cachedCatalog(context.Background(), "codex", "")
	if err != nil || !found || len(old.Catalog.Models) != 1 || old.Catalog.Models[0].ID != "model-one" {
		t.Fatalf("test did not retain outgoing row after failed auth clear %#v, %v", old, err)
	}
	cache.setWriteError(nil)
	svc.InvalidateModelCatalogs("codex")
	awaitModelSignal(t, queued.queued)
	safe, found, err := svc.cachedCatalog(context.Background(), "codex", "")
	if err != nil || !found || len(safe.Catalog.Models) != 0 {
		t.Errorf("ordinary invalidation resurrected fenced models %#v, %v", safe, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	interim, readErr := svc.Models(ctx, "codex", "", false)
	cancel()
	if readErr != nil && !errors.Is(readErr, context.DeadlineExceeded) {
		t.Errorf("fenced interim GET %v", readErr)
	}
	if len(interim.Models) > 0 && interim.Models[0].ID != "account-b-model" {
		t.Errorf("fenced interim GET returned outgoing models %#v", interim)
	}
	close(release)
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	final, err := svc.Models(ctx, "codex", "", false)
	assertScopeCatalog(t, final, err, "account-b-model", "account-b")
}

func TestCatalogScopeDelayedOutgoingInvalidationCannotBypassHistoricalAuthFence(t *testing.T) {
	svc, discoverer, cache := scopedModelService(t, "codex")
	outgoing, found, err := cache.GetAgentModelCatalog(context.Background(), "codex", "")
	if err != nil || !found {
		t.Fatalf("outgoing snapshot %v, %v", found, err)
	}
	discoverer.setScope("", "account-b")
	discoverer.setModels(nil, "account-b-model")
	svc.InvalidateAgentAuthentication("codex")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	fresh, err := svc.RevalidateModels(ctx, "codex", "")
	assertScopeCatalog(t, fresh, err, "account-b-model", "account-b")
	persisted, found, err := svc.cachedCatalog(ctx, "codex", "")
	if err != nil || !found || len(persisted.Catalog.Models) != 1 || persisted.Catalog.Models[0].ID != "account-b-model" {
		t.Fatalf("safe persisted replacement %#v, %v", persisted, err)
	}
	if !svc.markModelCatalogInvalidated(outgoing, false) {
		t.Fatal("delayed ordinary invalidation did not persist")
	}
	pending, found, err := svc.cachedCatalog(ctx, "codex", "")
	if err != nil || !found || len(pending.Catalog.Models) != 0 || pending.RefreshState != "queued" || pending.Generation <= persisted.Generation {
		t.Fatalf("delayed invalidation restored historical outgoing catalog %#v, %v", pending, err)
	}
	current, err := svc.Models(ctx, "codex", "", false)
	assertScopeCatalog(t, current, err, "account-b-model", "account-b")
}

type coldAuthScopeCache struct {
	*fakeModelCache
	failList       bool
	outgoingWrites atomic.Int32
}

func (c *coldAuthScopeCache) ListAgentModelCatalogsByAgent(ctx context.Context, agentID string) ([]ports.CachedAgentModelCatalog, error) {
	if c.failList {
		return nil, errors.New("catalog listing unavailable")
	}
	return c.fakeModelCache.ListAgentModelCatalogsByAgent(ctx, agentID)
}

func (c *coldAuthScopeCache) UpsertAgentModelCatalog(ctx context.Context, record ports.CachedAgentModelCatalog) error {
	var catalog ports.AgentModelCatalog
	if json.Unmarshal([]byte(record.CatalogJSON), &catalog) == nil {
		for _, model := range catalog.Models {
			if model.ID == "outgoing-account-a-model" {
				c.outgoingWrites.Add(1)
			}
		}
	}
	return c.fakeModelCache.UpsertAgentModelCatalog(ctx, record)
}

func TestCatalogScopeAuthInvalidationFencesDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name          string
		seed          bool
		noCache       bool
		failList      bool
		failWrites    bool
		futureHistory bool
	}{
		{name: "held discovery/durable cache", seed: true},
		{name: "held discovery/failed writes", seed: true, failWrites: true},
		{name: "first discovery/empty cache"},
		{name: "first discovery/no cache", noCache: true},
		{name: "first discovery/failed listing", failList: true},
		{name: "first discovery/failed writes", failWrites: true},
		{name: "first discovery/clock reversal", futureHistory: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			discoverer := &scopeModelDiscoverer{fakeModelDiscoverer: successfulModelDiscoverer(), cheap: "local-a", identity: "account-a", conclusive: true}
			cache := &coldAuthScopeCache{fakeModelCache: &fakeModelCache{}, failList: tc.failList}
			if tc.failWrites && !tc.seed {
				cache.setWriteError(errors.New("catalog writes unavailable"))
			}
			var catalogCache ports.AgentModelCatalogCache = cache
			if tc.noCache {
				catalogCache = nil
			}
			svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, catalogCache, nil, discoverer)
			ctx, cancel := context.WithCancel(context.Background())
			svc.ctx = ctx
			t.Cleanup(cancel)
			attempt := int32(1)
			load := func() (ports.AgentModelCatalog, error) { return svc.Models(ctx, "codex", "", false) }
			if tc.seed {
				readScopeModel(t, svc, ctx, false, "model-one", "account-a")
				attempt = 2
				load = func() (ports.AgentModelCatalog, error) { return svc.RevalidateModels(ctx, "codex", "") }
			}
			started, release := discoverer.holdDiscovery(attempt, "outgoing-account-a-model", "account-b-model")
			leader := startScopeModelRead(load)
			awaitModelSignal(t, started)
			if tc.failWrites && tc.seed {
				cache.setWriteError(errors.New("catalog writes unavailable"))
			}
			key := "codex\x00"
			svc.modelCallMu.Lock()
			minimum := svc.modelCalls[key].generation
			if tc.futureHistory {
				minimum = time.Now().Add(time.Hour).UnixNano()
				state := svc.modelGeneration[key]
				state.latest = minimum
				svc.modelGeneration[key] = state
			}
			svc.modelCallMu.Unlock()
			discoverer.setScope("", "account-b")
			svc.InvalidateAgentAuthentication("codex")
			if fence := svc.modelAuthenticationFence("codex", ""); fence <= minimum {
				t.Fatalf("authentication fence %d did not advance past discovery/history %d", fence, minimum)
			}
			if tc.seed && !tc.failWrites {
				pending, found, err := svc.cachedCatalog(ctx, "codex", "")
				if err != nil || !found || len(pending.Catalog.Models) != 0 {
					t.Errorf("held-fetch auth invalidation retained choices %#v, %v", pending, err)
				}
			}
			close(release)
			got, err := awaitScopeModelRead(t, leader)
			assertScopeCatalog(t, got, err, "account-b-model", "account-b")
			readCtx, stopRead := context.WithTimeout(ctx, 2*time.Second)
			defer stopRead()
			if cache.outgoingWrites.Load() != 0 {
				t.Fatal("outgoing-account discovery reached the cache writer")
			}
			for range 3 {
				readScopeModel(t, svc, readCtx, false, "account-b-model", "account-b")
			}
			cache.setWriteError(nil)
			if tc.seed {
				readScopeModel(t, svc, readCtx, true, "account-b-model", "account-b")
				stored, found, err := svc.cachedCatalog(ctx, "codex", "")
				if err != nil || !found || len(stored.Catalog.Models) != 1 || stored.Catalog.Models[0].ID != "account-b-model" || stored.Catalog.InputFingerprint != "account-b" {
					t.Fatalf("safe persisted auth catalog %#v, %v", stored, err)
				}
			} else {
				readScopeModel(t, svc, readCtx, false, "account-b-model", "account-b")
			}
		})
	}
}
