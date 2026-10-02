package runner

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const modelRefreshBarrierMarker = "test model registry settled"

type modelRefreshBarrier struct {
	mu     sync.Mutex
	counts map[string]int
	done   bool
}

func runWithModelRefreshBarrier(t *testing.T) {
	t.Helper()
	barrier := &modelRefreshBarrier{counts: make(map[string]int)}
	cliproxy.SetGlobalModelRegistryHook(barrier)
	err := serve(t.Context(), os.Getenv("AO_ACCOUNTS_MANAGER_TEST_STATE"), func(manager *coreauth.Manager) {
		if os.Getenv("AO_ACCOUNTS_MANAGER_TEST_MODEL_BARRIER") == "missing-executor" {
			manager.UnregisterExecutor("codex")
			return
		}
		// The sentinel has no provider secret and no route; its refresh proves the
		// SDK's late startup pass ran even when it owns no managed credentials.
		sentinel := &coreauth.Auth{ID: "test-startup-model-sentinel", Provider: "codex", Attributes: map[string]string{coreauth.AttributeAuthKind: coreauth.AuthKindAPIKey}}
		if _, err := manager.Register(coreauth.WithSkipPersist(t.Context()), sentinel); err != nil {
			t.Fatal(err)
		}
		barrier.mu.Lock()
		for _, auth := range manager.List() {
			barrier.counts[auth.ID] = 0
		}
		barrier.mu.Unlock()
		cliproxy.GlobalModelRegistry().RegisterClient(sentinel.ID, sentinel.Provider, []*cliproxy.ModelInfo{{ID: "test-startup-model-sentinel", Object: "model"}})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (b *modelRefreshBarrier) OnModelsRegistered(_ context.Context, _, id string, _ []*cliproxy.ModelInfo) {
	b.observe(id)
}

func (b *modelRefreshBarrier) OnModelsUnregistered(_ context.Context, _, id string) {
	b.observe(id)
}

func (b *modelRefreshBarrier) observe(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, expected := b.counts[id]; !expected || b.done {
		return
	}
	b.counts[id]++
	for _, count := range b.counts {
		if count < 2 {
			return
		}
	}
	b.done = true
	fmt.Println(modelRefreshBarrierMarker)
}
