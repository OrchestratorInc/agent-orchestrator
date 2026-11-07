package scheduler

import (
	"context"
	"errors"
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

// 删除采样后的时钟复核会把采样期间正常前进的合法读数误判为未来样本。
func TestRefreshResourcesAcceptsSampleCompletedAfterRefreshStarted(t *testing.T) {
	f := newFixture(t)
	var raw domain.ResourceSample
	var sampleErr error
	f.scheduler.deps.Resources = resourceSamplerFunc(func(context.Context) (domain.ResourceSample, error) {
		f.clock.Advance(time.Second)
		return raw, sampleErr
	})
	// 一条正式 Service 路径覆盖恢复滞回及时间/采样故障闭锁，不放宽既有阈值。
	cases := []struct {
		name   string
		shift  time.Duration
		reason string
		fail   bool
		want   int64
	}{
		{name: "first_sample", reason: "resource_first_sample"},
		{name: "healthy_1"}, {name: "healthy_2"}, {name: "healthy_3", want: 1},
		{name: "healthy_4", want: 2}, {name: "healthy_5", want: 3},
		{name: "future", shift: time.Second},
		{name: "stale", shift: -16 * time.Second},
		{name: "failed", fail: true},
		{name: "gap", reason: "resource_sample_gap"},
		{name: "incomplete", reason: "resource_sample_invalid"},
		{name: "recover_1"}, {name: "recover_2"}, {name: "recover_3", want: 1},
	}
	for _, tc := range cases {
		f.clock.Advance(SampleInterval)
		if _, err := f.scheduler.MaintainLeadership(t.Context()); err != nil {
			t.Fatal(err)
		}
		raw = healthyResource(f.clock.Now().Add(time.Second + tc.shift))
		raw.ReasonCode = tc.reason
		sampleErr = nil
		if tc.fail {
			sampleErr = errors.New("sample unavailable")
		}
		err := f.scheduler.RefreshResources(t.Context())
		if !errors.Is(err, sampleErr) {
			t.Fatalf("%s: error=%v", tc.name, err)
		}
		status, err := f.scheduler.Status(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: capacity=%+v resources=%+v", tc.name, status.Capacity, status.Resources)
		wantReason := "RESOURCE_READY"
		if tc.reason != "" || tc.shift != 0 || tc.fail {
			wantReason = "RESOURCE_UNKNOWN"
		}
		if status.Capacity.Limit != tc.want || status.Capacity.ReasonCode != wantReason {
			t.Fatalf("%s: capacity=%+v, want limit=%d reason=%s", tc.name, status.Capacity, tc.want, wantReason)
		}
		if !tc.fail && status.Resources != raw {
			t.Fatalf("%s: raw sample changed: %+v", tc.name, status.Resources)
		}
		if err := f.scheduler.RefreshResources(t.Context()); !errors.Is(err, nil) {
			t.Fatal(err)
		}
		repeated, err := f.scheduler.Status(t.Context())
		if err != nil || repeated.Capacity != status.Capacity {
			t.Fatalf("duplicate sample advanced recovery: %+v %v", repeated.Capacity, err)
		}
	}
}

// resourceSamplerFunc 只替换外部采样边界，让正式 Service 经历采样耗时。
type resourceSamplerFunc func(context.Context) (domain.ResourceSample, error)

// Sample 把带时间推进的样本交给正式 RefreshResources。
func (f resourceSamplerFunc) Sample(ctx context.Context) (domain.ResourceSample, error) {
	return f(ctx)
}

func healthyResource(now time.Time) domain.ResourceSample {
	return domain.ResourceSample{ObservedAt: now, PreviousAt: now.Add(-SampleInterval), TotalBytes: 16 << 30, AvailableBytes: 12 << 30,
		CPUTotalTicks: 200, PreviousCPUTotalTicks: 100, CPUBusyTicks: 30, PreviousCPUBusyTicks: 10}
}
