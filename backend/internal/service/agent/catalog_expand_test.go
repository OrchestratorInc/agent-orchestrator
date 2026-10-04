package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type expansionDiscoverer struct {
	*fakeModelDiscoverer
	requestsMu       sync.Mutex
	requests         []bool
	expansionErr     error
	baseStarted      chan struct{}
	releaseBase      chan struct{}
	baseBlockCall    int
	identity         string
	identityObserved chan struct{}
	beforeExpansion  func()
}

func (d *expansionDiscoverer) Discover(ctx context.Context, request ports.AgentModelDiscoveryRequest) (ports.AgentModelCatalog, error) {
	d.requestsMu.Lock()
	d.requests = append(d.requests, request.IncludeAdditional)
	blockCall := d.baseBlockCall
	if blockCall == 0 {
		blockCall = 1
	}
	first := len(d.requests) == blockCall
	beforeExpansion := d.beforeExpansion
	expansionErr := d.expansionErr
	d.requestsMu.Unlock()
	if first && d.baseStarted != nil {
		close(d.baseStarted)
		select {
		case <-d.releaseBase:
		case <-ctx.Done():
			return ports.AgentModelCatalog{}, ctx.Err()
		}
	}
	if request.IncludeAdditional && beforeExpansion != nil {
		beforeExpansion()
	}
	catalog, err := d.fakeModelDiscoverer.Discover(ctx, request)
	d.requestsMu.Lock()
	catalog.InputFingerprint = d.identity
	d.requestsMu.Unlock()
	catalog.AdditionalModelsAvailable = true
	if request.IncludeAdditional {
		if expansionErr != nil {
			return catalog, expansionErr
		}
		catalog.AdditionalModelsLoaded = true
		catalog.Models = append(catalog.Models, ports.AgentModelInfo{ID: "supplement", IsAdditional: true})
	}
	return catalog, err
}

func (d *expansionDiscoverer) CatalogIdentityFingerprint(context.Context, ports.AgentModelDiscoveryRequest) (string, bool) {
	d.requestsMu.Lock()
	defer d.requestsMu.Unlock()
	if d.identityObserved != nil {
		select {
		case d.identityObserved <- struct{}{}:
		default:
		}
	}
	return d.identity, d.identity != ""
}

func expansionService(d *expansionDiscoverer, cache *fakeModelCache) *Service {
	return newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, nil, d)
}

func TestModelExpansionPersistsIntentAndClearsChangedInputs(t *testing.T) {
	d := &expansionDiscoverer{fakeModelDiscoverer: successfulModelDiscoverer()}
	svc := expansionService(d, &fakeModelCache{})
	base, err := svc.Models(context.Background(), "claude-code", "", false)
	if err != nil || base.AdditionalModelsLoaded {
		t.Fatalf("base=%+v err=%v", base, err)
	}
	expanded, err := svc.ExpandModels(context.Background(), "claude-code", "")
	if err != nil || !expanded.AdditionalModelsLoaded || len(expanded.Models) != 2 {
		t.Fatalf("expanded=%+v err=%v", expanded, err)
	}
	refreshed, err := svc.Models(context.Background(), "claude-code", "", true)
	if err != nil || !refreshed.AdditionalModelsLoaded {
		t.Fatalf("refresh=%+v err=%v", refreshed, err)
	}
	d.version = "v2"
	reset, err := svc.Models(context.Background(), "claude-code", "", true)
	if err != nil || reset.AdditionalModelsLoaded || len(reset.Models) != 1 {
		t.Fatalf("reset=%+v err=%v", reset, err)
	}
	d.requestsMu.Lock()
	defer d.requestsMu.Unlock()
	want := []bool{false, true, true, false}
	if len(d.requests) != len(want) {
		t.Fatalf("requests=%v", d.requests)
	}
	for i, value := range want {
		if d.requests[i] != value {
			t.Fatalf("requests=%v want=%v", d.requests, want)
		}
	}
}

func TestFirstModelExpansionFailureLeavesBaseRecordUnchanged(t *testing.T) {
	cache := &fakeModelCache{}
	d := &expansionDiscoverer{fakeModelDiscoverer: successfulModelDiscoverer(), expansionErr: errors.New("native check unsupported")}
	svc := expansionService(d, cache)
	if _, err := svc.Models(context.Background(), "claude-code", "", false); err != nil {
		t.Fatal(err)
	}
	before, _, _ := cache.GetAgentModelCatalog(context.Background(), "claude-code", "")
	_, err := svc.ExpandModels(context.Background(), "claude-code", "")
	if err == nil {
		t.Fatal("want expansion failure")
	}
	after, _, _ := cache.GetAgentModelCatalog(context.Background(), "claude-code", "")
	if before != after {
		t.Fatalf("base record mutated before=%+v after=%+v", before, after)
	}
}

func TestExpandedModelFailureKeepsSameScopeLastGood(t *testing.T) {
	d := &expansionDiscoverer{fakeModelDiscoverer: successfulModelDiscoverer()}
	svc := expansionService(d, &fakeModelCache{})
	if _, err := svc.ExpandModels(context.Background(), "claude-code", ""); err != nil {
		t.Fatal(err)
	}
	d.requestsMu.Lock()
	d.expansionErr = errors.New("native check timeout")
	d.requestsMu.Unlock()
	got, err := svc.RevalidateModels(context.Background(), "claude-code", "")
	if err != nil || !got.AdditionalModelsLoaded || len(got.Models) != 2 || !got.Stale || got.RefreshState != "error" || got.RetryAt == nil {
		t.Fatalf("catalog=%+v err=%v", got, err)
	}
}

func TestModelExpansionWaitingForBaseRerunsDiscovery(t *testing.T) {
	d := &expansionDiscoverer{fakeModelDiscoverer: successfulModelDiscoverer(), baseStarted: make(chan struct{}), releaseBase: make(chan struct{})}
	svc := expansionService(d, &fakeModelCache{})
	baseDone := make(chan error, 1)
	go func() { _, err := svc.Models(context.Background(), "claude-code", "", false); baseDone <- err }()
	<-d.baseStarted
	expandedDone := make(chan ports.AgentModelCatalog, 1)
	errDone := make(chan error, 1)
	go func() {
		got, err := svc.ExpandModels(context.Background(), "claude-code", "")
		expandedDone <- got
		errDone <- err
	}()
	close(d.releaseBase)
	if err := <-baseDone; err != nil {
		t.Fatal(err)
	}
	got := <-expandedDone
	if err := <-errDone; err != nil || !got.AdditionalModelsLoaded {
		t.Fatalf("catalog=%+v err=%v", got, err)
	}
	d.requestsMu.Lock()
	defer d.requestsMu.Unlock()
	if len(d.requests) != 2 || d.requests[0] || !d.requests[1] {
		t.Fatalf("requests=%v", d.requests)
	}
}

func TestModelExpansionAccountChangesDuringDiscoveryReturnsBase(t *testing.T) {
	d := &expansionDiscoverer{fakeModelDiscoverer: successfulModelDiscoverer(), identity: "account-a"}
	svc := expansionService(d, &fakeModelCache{})
	if _, err := svc.Models(context.Background(), "claude-code", "", false); err != nil {
		t.Fatal(err)
	}
	d.beforeExpansion = func() { d.requestsMu.Lock(); d.identity = "account-b"; d.requestsMu.Unlock() }
	got, err := svc.ExpandModels(context.Background(), "claude-code", "")
	if err != nil || got.AdditionalModelsLoaded || got.InputFingerprint != "account-b" || len(got.Models) != 1 {
		t.Fatalf("catalog=%+v err=%v", got, err)
	}
	d.requestsMu.Lock()
	defer d.requestsMu.Unlock()
	if len(d.requests) != 3 || !d.requests[1] || d.requests[2] {
		t.Fatalf("requests=%v", d.requests)
	}
}

func TestQueuedModelExpansionAccountChangeDoesNotExpandNewAccount(t *testing.T) {
	d := &expansionDiscoverer{fakeModelDiscoverer: successfulModelDiscoverer(), identity: "account-a", baseBlockCall: 2}
	svc := expansionService(d, &fakeModelCache{})
	if _, err := svc.Models(context.Background(), "claude-code", "", false); err != nil {
		t.Fatal(err)
	}
	d.baseStarted = make(chan struct{})
	d.releaseBase = make(chan struct{})
	baseDone := make(chan error, 1)
	go func() { _, err := svc.Models(context.Background(), "claude-code", "", true); baseDone <- err }()
	<-d.baseStarted
	d.requestsMu.Lock()
	d.identityObserved = make(chan struct{}, 1)
	observed := d.identityObserved
	d.requestsMu.Unlock()
	expandedDone := make(chan ports.AgentModelCatalog, 1)
	errDone := make(chan error, 1)
	go func() {
		got, err := svc.ExpandModels(context.Background(), "claude-code", "")
		expandedDone <- got
		errDone <- err
	}()
	<-observed
	d.requestsMu.Lock()
	d.identity = "account-b"
	d.requestsMu.Unlock()
	close(d.releaseBase)
	if err := <-baseDone; err != nil {
		t.Fatal(err)
	}
	got := <-expandedDone
	if err := <-errDone; err != nil || got.AdditionalModelsLoaded || got.InputFingerprint != "account-b" {
		t.Fatalf("catalog=%+v err=%v", got, err)
	}
	d.requestsMu.Lock()
	defer d.requestsMu.Unlock()
	for _, expanded := range d.requests {
		if expanded {
			t.Fatalf("expanded across identity fence: %v", d.requests)
		}
	}
}

func TestColdModelExpansionAccountChangeReturnsOnlyNewBase(t *testing.T) {
	d := &expansionDiscoverer{fakeModelDiscoverer: successfulModelDiscoverer(), identity: "account-a"}
	d.beforeExpansion = func() { d.requestsMu.Lock(); d.identity = "account-b"; d.requestsMu.Unlock() }
	cache := &fakeModelCache{}
	svc := expansionService(d, cache)
	got, err := svc.ExpandModels(context.Background(), "claude-code", "")
	if err != nil || got.AdditionalModelsLoaded || got.InputFingerprint != "account-b" || len(got.Models) != 1 {
		t.Fatalf("catalog=%+v err=%v", got, err)
	}
	d.requestsMu.Lock()
	defer d.requestsMu.Unlock()
	if len(d.requests) != 2 || !d.requests[0] || d.requests[1] {
		t.Fatalf("requests=%v", d.requests)
	}
	if cache.puts != 1 {
		t.Fatalf("puts=%d want atomic base-only save", cache.puts)
	}
}

func TestModelExpansionWithoutDiscovererReturnsUnavailable(t *testing.T) {
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, &fakeModelCache{}, nil, nil)
	_, err := svc.ExpandModels(context.Background(), "claude-code", "")
	if err == nil || !strings.Contains(err.Error(), "Model discovery is unavailable") {
		t.Fatalf("err=%v, want discovery unavailable", err)
	}
}
