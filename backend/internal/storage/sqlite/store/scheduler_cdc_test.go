package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// 删除策略 INSERT 触发器会让首次 Adapter 限额变更无法通过 CDC 被观察。
func TestM3NewAdapterPolicyPublishesCDC(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	p := domain.DefaultSchedulerPolicy(domain.HarnessCodex)
	p.MaxConcurrent = 1
	_, wrote, err := s.PutSchedulerPolicy(ctx, p, 0, domain.PolicyChange{IdempotencyKey: "new-adapter", RequestFingerprint: strings.Repeat("c", 64), ActorRef: "test-operator", Reason: "ADAPTER_LIMIT_CHANGED", Now: workflowStoreTime()})
	if err != nil || !wrote {
		t.Fatalf("新增策略失败: wrote=%v err=%v", wrote, err)
	}
	events, err := s.EventsAfter(ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if string(event.Type) != "scheduler_updated" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["kind"] == "policy" && payload["id"] == p.ID && payload["revision"] == float64(1) {
			return
		}
	}
	t.Fatal("Adapter 策略已新增，但缺失对应的 scheduler_updated 事件")
}
