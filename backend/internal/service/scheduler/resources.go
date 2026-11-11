package scheduler

import (
	"context"
	"math/bits"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Capacity 是非秘密的容量结论；Limit 约束总占位，MaxNewClaims 约束每轮新增速度。
type Capacity struct {
	Limit        int64
	MaxNewClaims int64
	ReasonCode   string
}

// resourceWindow 保存短期滞回；重启遇到未知样本必须重新积累恢复证据。
type resourceWindow struct {
	initialized bool
	observedAt  time.Time
	limit       int64
	healthy     int
}

// RefreshResources 每五秒至多采一次；失败立即替换旧样本，不能沿用以前的空闲证据。
func (s *Service) RefreshResources(ctx context.Context) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	now := s.deps.Clock().UTC()
	if !s.sampledAt.IsZero() && !now.Before(s.sampledAt) && now.Sub(s.sampledAt) < SampleInterval {
		return nil
	}
	s.sampledAt = now
	policy := domain.DefaultSchedulerPolicy("")
	if s.deps.Store == nil {
		return domain.ErrAdmissionRequired
	}
	policies, err := s.deps.Store.ListSchedulerPolicies(ctx)
	if err != nil {
		s.sample = domain.ResourceSample{ReasonCode: "RESOURCE_POLICY_UNAVAILABLE"}
		s.capacity = s.window.evaluate(s.sample, now, policy)
		return err
	}
	for _, p := range policies {
		if err := p.Validate(); err != nil {
			s.sample = domain.ResourceSample{ReasonCode: "RESOURCE_POLICY_INVALID"}
			s.capacity = s.window.evaluate(s.sample, now, policy)
			return err
		}
		if p.Scope == "global" {
			policy = p
		}
	}
	if s.deps.Resources == nil {
		s.sample = domain.ResourceSample{ReasonCode: "RESOURCE_UNAVAILABLE"}
		s.capacity = s.window.evaluate(s.sample, now, policy)
		if policy.ResourceLimitsDisabled {
			return nil
		}
		return domain.ErrSchedulerCapacity
	}
	sample, err := s.deps.Resources.Sample(ctx)
	// 采样器读取系统计数后才记录 ObservedAt，不能用采样前的时钟判断未来样本。
	now = s.deps.Clock().UTC()
	if err != nil {
		sample = domain.ResourceSample{ObservedAt: now, ReasonCode: "RESOURCE_SAMPLE_FAILED"}
	}
	s.sample = sample
	s.capacity = s.window.evaluate(s.sample, now, policy)
	if policy.ResourceLimitsDisabled {
		return nil
	}
	return err
}

// evaluate 从原始 ticks 判定资源；同一时间戳绝不重复累加恢复样本。
func (w *resourceWindow) evaluate(r domain.ResourceSample, now time.Time, p domain.SchedulerPolicy) Capacity {
	if p.ResourceLimitsDisabled {
		if p.ConcurrencyLimitsDisabled {
			return Capacity{Limit: domain.SchedulerUnlimitedCap, MaxNewClaims: ClaimLimit, ReasonCode: "RESOURCE_LIMITS_DISABLED"}
		}
		return Capacity{Limit: min(p.MaxConcurrent, domain.SchedulerHardCap), MaxNewClaims: ClaimLimit, ReasonCode: "RESOURCE_LIMITS_DISABLED"}
	}
	if !w.initialized {
		w.initialized = true
		w.limit = p.MaxConcurrent
	}
	w.limit = min(w.limit, p.MaxConcurrent, domain.SchedulerHardCap)
	valid := r.ReasonCode == "" && !r.ObservedAt.IsZero() && !r.PreviousAt.IsZero() && !r.ObservedAt.After(now) && now.Sub(r.ObservedAt) <= 15*time.Second &&
		r.ObservedAt.After(r.PreviousAt) && r.ObservedAt.Sub(r.PreviousAt) <= 15*time.Second && r.TotalBytes > 0 && r.AvailableBytes <= r.TotalBytes &&
		r.CPUTotalTicks > r.PreviousCPUTotalTicks && r.CPUBusyTicks >= r.PreviousCPUBusyTicks && !r.ObservedAt.Before(w.observedAt)
	total, busy := r.CPUTotalTicks-r.PreviousCPUTotalTicks, r.CPUBusyTicks-r.PreviousCPUBusyTicks
	if !valid || busy > total {
		w.limit, w.healthy = 0, 0
		return Capacity{ReasonCode: "RESOURCE_UNKNOWN"}
	}
	newSample := r.ObservedAt.After(w.observedAt)
	w.observedAt = r.ObservedAt
	if percentCompare(busy, total, p.CPUHardPercent) >= 0 || percentCompare(r.AvailableBytes, r.TotalBytes, p.RAMHardPercent) < 0 || r.AvailableBytes < uint64(p.RAMFloorBytes) { // #nosec G115 -- 生产调用只传默认或通过 SchedulerPolicy.Validate 的策略，RAMFloorBytes 非负且不超过 int64。
		w.limit, w.healthy = 0, 0
		return Capacity{ReasonCode: "RESOURCE_HARD_LIMIT"}
	}
	if percentCompare(busy, total, p.CPUSoftPercent) >= 0 || percentCompare(r.AvailableBytes, r.TotalBytes, p.RAMSoftPercent) < 0 {
		w.healthy = 0
		if newSample {
			w.limit = max(0, w.limit-1)
		}
		return Capacity{Limit: w.limit, MaxNewClaims: 1, ReasonCode: "RESOURCE_SOFT_LIMIT"}
	}
	if newSample {
		if percentCompare(busy, total, p.CPURecoverPercent) < 0 && percentCompare(r.AvailableBytes, r.TotalBytes, p.RAMSoftPercent) > 0 {
			w.healthy = min(3, w.healthy+1)
			if w.healthy == 3 {
				w.limit = min(p.MaxConcurrent, domain.SchedulerHardCap, w.limit+1)
			}
		} else {
			w.healthy = 0
		}
	}
	if p.ConcurrencyLimitsDisabled && w.healthy == 3 && w.limit >= p.MaxConcurrent {
		return Capacity{Limit: domain.SchedulerUnlimitedCap, MaxNewClaims: ClaimLimit, ReasonCode: "RESOURCE_READY"}
	}
	return Capacity{Limit: w.limit, MaxNewClaims: ClaimLimit, ReasonCode: "RESOURCE_READY"}
}

// percentCompare 用完整 128 位乘积比较比例，避免大 tick 或字节数溢出与浮点边界误差。
func percentCompare(value, total uint64, percent int64) int {
	lh, ll := bits.Mul64(value, 100)
	rh, rl := bits.Mul64(total, uint64(percent)) // #nosec G115 -- 调用者只传已校验策略的百分比，范围为0到100，不可能转换溢出。
	if lh < rh || lh == rh && ll < rl {
		return -1
	}
	if lh > rh || lh == rh && ll > rl {
		return 1
	}
	return 0
}
