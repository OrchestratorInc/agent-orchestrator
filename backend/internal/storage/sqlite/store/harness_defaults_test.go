package store_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestHarnessDefaultsPersistIndependentlyAndReset(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	initial, err := s.GetAppSettings(ctx)
	if err != nil || len(initial.HarnessDefaults) != 0 {
		t.Fatalf("initial defaults = %+v, %v", initial, err)
	}
	var wg sync.WaitGroup
	for _, agent := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SetHarnessDefault(ctx, agent, domain.HarnessDefault{DisabledModels: []string{string(agent) + "-model"}}, time.Now()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	saved, err := s.GetAppSettings(ctx)
	if err != nil || len(saved.HarnessDefaults) != 2 {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	if !slices.Equal(saved.HarnessDefaults["codex"].DisabledModels, []string{"codex-model"}) || !slices.Equal(saved.HarnessDefaults["claude-code"].DisabledModels, []string{"claude-code-model"}) {
		t.Fatalf("defaults lost values: %+v", saved.HarnessDefaults)
	}
	if err := s.SetHarnessDefault(ctx, domain.HarnessCodex, domain.HarnessDefault{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	reset, err := s.GetAppSettings(ctx)
	if err != nil || len(reset.HarnessDefaults) != 1 || reset.HarnessDefaults["claude-code"].IsZero() {
		t.Fatalf("reset changed another harness: %+v, %v", reset, err)
	}
	if reset.DefaultSessionMode != initial.DefaultSessionMode || reset.VersionRefresh != initial.VersionRefresh {
		t.Fatal("harness defaults changed unrelated settings")
	}
	// 只有 CLI 字段时仍必须保留 JSON 条目，不能被空模型筛选误判为恢复默认。
	cli := domain.HarnessDefault{ExecutableDirectory: "/desktop", ExecutablePath: "/desktop/bin/dsh"}
	if err := s.SetHarnessDefault(ctx, domain.HarnessDeepSeek, cli, time.Now()); err != nil {
		t.Fatal(err)
	}
	persisted, err := s.GetAppSettings(ctx)
	if err != nil || persisted.HarnessDefaults[string(domain.HarnessDeepSeek)].ExecutablePath != cli.ExecutablePath ||
		persisted.HarnessDefaults[string(domain.HarnessDeepSeek)].ExecutableDirectory != cli.ExecutableDirectory {
		t.Fatalf("CLI-only preference lost: %+v, %v", persisted.HarnessDefaults, err)
	}
}
