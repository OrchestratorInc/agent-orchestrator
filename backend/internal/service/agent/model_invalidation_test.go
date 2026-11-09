package agent

import (
	"testing"
	"time"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestInvalidationDuringDiscoveryRunsSuccessor 防止安装与认证同时失效后，旧探测占用合并槽而永久留下 queued。
func TestInvalidationDuringDiscoveryRunsSuccessor(t *testing.T) {
	cache := sqlitetest.MustOpen(t)
	discoverer := &blockingSubsequentModelDiscoverer{
		fakeModelDiscoverer: successfulModelDiscoverer(),
		started:             make(chan struct{}), release: make(chan struct{}),
	}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, discoverer)
	catalog, err := svc.Models(t.Context(), "codex", "", true)
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan struct{})
	go func() { _, _ = svc.Models(t.Context(), "codex", "", true); close(first) }()
	select {
	case <-discoverer.started:
	case <-time.After(2 * time.Second):
		t.Fatal("discovery did not start")
	}
	svc.requeueCatalog("", catalog)
	second := make(chan struct{})
	go func() { _, _ = svc.revalidateModels(t.Context(), "codex", ""); close(second) }()
	close(discoverer.release)
	for _, done := range []chan struct{}{first, second} {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("invalidated discovery did not finish")
		}
	}
	if discoverer.discoverCalls.Load() != 3 {
		t.Fatalf("discovery calls = %d, want initial, old and successor", discoverer.discoverCalls.Load())
	}
	record, ok, err := cache.GetAgentModelCatalog(t.Context(), "codex", "")
	if err != nil || !ok || record.RefreshState != "idle" {
		t.Fatalf("catalog stuck after invalidation: %+v, %v", record, err)
	}
}
