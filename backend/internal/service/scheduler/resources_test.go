package scheduler

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// 删除 UNKNOWN 清零、CPU/RAM 硬线或 observedAt 去重，会使本测试恢复时机和容量断言变红。
func TestResourceWindowFailsClosedAndRecoversOnlyOnNewSamples(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	p := domain.DefaultSchedulerPolicy("")
	w := resourceWindow{}
	r := healthyResource(now)
	if got := w.evaluate(r, now, p); got.Limit != 3 {
		t.Fatalf("healthy = %+v", got)
	}
	r.CPUBusyTicks = r.PreviousCPUBusyTicks + 85
	if got := w.evaluate(r, now, p); got.Limit != 0 || got.ReasonCode != "RESOURCE_HARD_LIMIT" {
		t.Fatalf("85%% = %+v", got)
	}
	for i := 1; i <= 5; i++ {
		now = now.Add(SampleInterval)
		r = healthyResource(now)
		want := int64(max(0, i-2))
		if got := w.evaluate(r, now, p); got.Limit != want {
			t.Fatalf("fresh sample %d = %+v, want %d", i, got, want)
		}
		for duplicate := 0; duplicate < 4; duplicate++ {
			if got := w.evaluate(r, now, p); got.Limit != want {
				t.Fatalf("repeated sample expanded capacity: %+v", got)
			}
		}
	}
	invalid := []domain.ResourceSample{healthyResource(now), healthyResource(now), healthyResource(now), healthyResource(now), healthyResource(now), healthyResource(now)}
	invalid[0].ReasonCode = "FIRST_SAMPLE"
	invalid[1].ObservedAt = now.Add(time.Second)
	invalid[2].PreviousAt = now.Add(-16 * time.Second)
	invalid[3].CPUTotalTicks = invalid[3].PreviousCPUTotalTicks
	invalid[4].CPUBusyTicks = invalid[4].PreviousCPUBusyTicks + 101
	invalid[5].AvailableBytes = invalid[5].TotalBytes + 1
	for i, sample := range invalid {
		if got := w.evaluate(sample, now, p); got.Limit != 0 || got.ReasonCode != "RESOURCE_UNKNOWN" {
			t.Fatalf("invalid sample %d = %+v", i, got)
		}
	}
	if got := w.evaluate(healthyResource(now), now.Add(16*time.Second), p); got.Limit != 0 {
		t.Fatalf("stale = %+v", got)
	}
}

// 删除软线降速、内存字节下限或恢复严格不等式，会突破相应边界。
func TestResourceSoftLimitAndMemoryBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	p := domain.DefaultSchedulerPolicy("")
	r := healthyResource(now)
	w := resourceWindow{}
	r.CPUBusyTicks = r.PreviousCPUBusyTicks + 75
	if got := w.evaluate(r, now, p); got.Limit != 2 || got.MaxNewClaims != 1 {
		t.Fatalf("75%% = %+v", got)
	}
	r = healthyResource(now.Add(SampleInterval))
	r.TotalBytes, r.AvailableBytes = 8<<30, (2<<30)-1
	if got := w.evaluate(r, r.ObservedAt, p); got.Limit != 0 {
		t.Fatalf("RAM byte floor = %+v", got)
	}
	for i := 1; i <= 3; i++ {
		r = healthyResource(r.ObservedAt.Add(SampleInterval))
		r.CPUBusyTicks = r.PreviousCPUBusyTicks + 65
		if got := w.evaluate(r, r.ObservedAt, p); got.Limit != 0 {
			t.Fatalf("65%% must not recover = %+v", got)
		}
	}
	r = healthyResource(r.ObservedAt.Add(SampleInterval))
	r.AvailableBytes = 3 << 30
	if got := w.evaluate(r, r.ObservedAt, p); got.ReasonCode != "RESOURCE_SOFT_LIMIT" {
		t.Fatalf("RAM below 25%% = %+v", got)
	}
	r = healthyResource(r.ObservedAt.Add(SampleInterval))
	r.AvailableBytes = 2 << 30
	if got := w.evaluate(r, r.ObservedAt, p); got.ReasonCode != "RESOURCE_HARD_LIMIT" {
		t.Fatalf("RAM below 15%% = %+v", got)
	}
}

func healthyResource(now time.Time) domain.ResourceSample {
	return domain.ResourceSample{ObservedAt: now, PreviousAt: now.Add(-SampleInterval), TotalBytes: 16 << 30, AvailableBytes: 12 << 30,
		CPUTotalTicks: 200, PreviousCPUTotalTicks: 100, CPUBusyTicks: 30, PreviousCPUBusyTicks: 10}
}
