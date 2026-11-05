// Package resources 把既有系统采集器的原始读数转成调度准入证据。
package resources

import (
	"context"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// Sampler 串行保存相邻两次原始采样；失败或长间隔会中断 CPU 差分基线。
// 不使用显示层缓存时间、Load1 或进程 CPU 回退推断整机空闲。
type Sampler struct {
	mu         sync.Mutex
	read       func() (procmem.System, error)
	clock      func() time.Time
	previous   procmem.System
	previousAt time.Time
}

var _ ports.ResourceSampler = (*Sampler)(nil)

// New 复用各平台已有的 procmem 实现，Windows 无需额外权限或依赖。
func New() *Sampler {
	return &Sampler{read: procmem.ReadSystem, clock: time.Now}
}

// Sample 返回真实采样时刻和前后 CPU ticks，未知证据不会被补成零负载。
func (s *Sampler) Sample(ctx context.Context) (domain.ResourceSample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.ResourceSample{ReasonCode: "resource_sample_cancelled"}, err
	}
	raw, err := s.read()
	now := s.clock().UTC()
	sample := domain.ResourceSample{
		ObservedAt: now, PreviousAt: s.previousAt,
		TotalBytes: raw.TotalBytes, AvailableBytes: raw.AvailableBytes,
		CPUTotalTicks: raw.CPUTotalTicks, CPUBusyTicks: raw.CPUBusyTicks,
		PreviousCPUTotalTicks: s.previous.CPUTotalTicks, PreviousCPUBusyTicks: s.previous.CPUBusyTicks,
	}
	if err != nil {
		s.previousAt = time.Time{}
		s.previous = procmem.System{}
		sample.ReasonCode = "resource_sample_failed"
		return sample, err
	}
	// Windows CPU 调用失败时 ReadSystem 仍可能只返回有效 RAM，零 ticks 不能成为下次基线。
	if raw.CPUTotalTicks == 0 || raw.CPUBusyTicks > raw.CPUTotalTicks ||
		raw.TotalBytes == 0 || raw.AvailableBytes > raw.TotalBytes {
		s.previousAt = time.Time{}
		s.previous = procmem.System{}
		sample.ReasonCode = "resource_sample_invalid"
		return sample, nil
	}
	s.previous, s.previousAt = raw, now
	switch {
	case sample.PreviousAt.IsZero():
		sample.ReasonCode = "resource_first_sample"
	case !now.After(sample.PreviousAt) || now.Sub(sample.PreviousAt) > 15*time.Second:
		sample.ReasonCode = "resource_sample_gap"
	case raw.CPUTotalTicks <= sample.PreviousCPUTotalTicks || raw.CPUBusyTicks < sample.PreviousCPUBusyTicks ||
		raw.CPUBusyTicks-sample.PreviousCPUBusyTicks > raw.CPUTotalTicks-sample.PreviousCPUTotalTicks:
		sample.ReasonCode = "resource_ticks_invalid"
	}
	return sample, nil
}
