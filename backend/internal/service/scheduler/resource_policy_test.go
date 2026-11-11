package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// 删除任一限制开关的事务判断会拒绝关闭后的启动；删除并发检查则无法在重新开启时拦截新增。
func TestResourcePolicyChangesApplyToSessionAdmission(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	var sampleErr error
	busy, availablePercent := uint64(90), uint64(10)
	f.scheduler.deps.Resources = resourceSamplerFunc(func(context.Context) (domain.ResourceSample, error) {
		r := healthyResource(f.clock.Now())
		r.CPUBusyTicks = r.PreviousCPUBusyTicks + busy
		r.AvailableBytes = r.TotalBytes * availablePercent / 100
		return r, sampleErr
	})
	cfg := ports.SpawnConfig{ProjectID: "project", Harness: domain.HarnessCodex, RequestedMode: domain.SessionModeChat}
	reserve := func(id string) error {
		evidence, err := f.scheduler.PrepareSessionAdmission(ctx, cfg)
		if err != nil {
			return err
		}
		_, _, err = f.store.ReserveSessionAdmission(ctx, domain.SessionAdmissionInput{
			ProjectID: cfg.ProjectID, Harness: cfg.Harness, Mode: cfg.RequestedMode,
			ClientRequestID: id, ClientRequestHash: strings.Repeat("a", 64), Evidence: evidence,
		})
		return err
	}
	if err := reserve("first"); !errors.Is(err, domain.ErrSchedulerCapacity) {
		t.Fatalf("enabled resource limits: %v", err)
	}
	operator := principal.Caller{Kind: principal.KindOperator, OperatorTokenFingerprint: "test-operator"}
	save := func(p domain.SchedulerPolicy, key string) domain.SchedulerPolicy {
		t.Helper()
		updated, applied, err := f.scheduler.PutPolicy(ctx, p, p.Revision, f.change(key), operator)
		if err != nil || !applied {
			t.Fatalf("save %s: applied=%v err=%v", key, applied, err)
		}
		return updated
	}
	p := domain.DefaultSchedulerPolicy("")
	// 使用持久策略的 JSON 入口，旧版本忽略开关时应在真实准入上失败。
	if err := json.Unmarshal([]byte(`{"ResourceLimitsDisabled":true}`), &p); err != nil {
		t.Fatal(err)
	}
	p = save(p, "disable-resources")
	if err := reserve("first"); err != nil {
		t.Fatalf("disabled resource limits must apply without restart: %v", err)
	}
	sampleErr = errors.New("resource sampler unavailable")
	f.clock.Advance(SampleInterval)
	if err := reserve("second"); err != nil {
		t.Fatalf("disabled resource limits must not require a sample: %v", err)
	}
	status, err := f.scheduler.Status(ctx)
	if err != nil || status.Capacity.Limit != 3 || status.Capacity.ReasonCode != "RESOURCE_LIMITS_DISABLED" {
		t.Fatalf("disabled status: %+v err=%v", status.Capacity, err)
	}
	if err := reserve("third"); !errors.Is(err, domain.ErrSchedulerCapacity) {
		t.Fatalf("adapter limit must remain enforced: %v", err)
	}
	var adapter domain.SchedulerPolicy
	for _, configured := range status.Policies {
		if configured.ScopeID == string(cfg.Harness) {
			adapter = configured
		}
	}
	adapter.MaxConcurrent = 3
	save(adapter, "raise-adapter")
	if err := reserve("third"); err != nil {
		t.Fatalf("new adapter limit must apply immediately: %v", err)
	}
	if err := reserve("fourth"); !errors.Is(err, domain.ErrSchedulerCapacity) {
		t.Fatalf("global limit must remain enforced: %v", err)
	}
	p.MaxConcurrent = 2
	p = save(p, "lower-global")
	status, err = f.scheduler.Status(ctx)
	if err != nil || status.Capacity.Limit != 2 || status.Occupied != 3 {
		t.Fatalf("lowering limit must preserve existing admissions: %+v err=%v", status, err)
	}
	if err := json.Unmarshal([]byte(`{"ConcurrencyLimitsDisabled":true}`), &p); err != nil {
		t.Fatal(err)
	}
	p = save(p, "disable-concurrency")
	for count := 4; count <= 11; count++ {
		if err := reserve("unlimited-" + strconv.Itoa(count)); err != nil {
			t.Fatalf("disabled concurrency must admit session %d beyond both caps without restart: %v", count, err)
		}
	}
	if err := json.Unmarshal([]byte(`{"ConcurrencyLimitsDisabled":false}`), &p); err != nil {
		t.Fatal(err)
	}
	p = save(p, "enable-concurrency")
	if err := reserve("twelfth"); !errors.Is(err, domain.ErrSchedulerCapacity) {
		t.Fatalf("re-enabled concurrency must reject new admissions: %v", err)
	}
	status, err = f.scheduler.Status(ctx)
	if err != nil || status.Occupied != 11 {
		t.Fatalf("re-enabling concurrency must preserve all existing admissions: occupied=%d err=%v", status.Occupied, err)
	}
	if err := json.Unmarshal([]byte(`{"ConcurrencyLimitsDisabled":true,"ResourceLimitsDisabled":false}`), &p); err != nil {
		t.Fatal(err)
	}
	save(p, "enable-resources")
	if err := reserve("fourth"); !errors.Is(err, domain.ErrSchedulerCapacity) {
		t.Fatalf("re-enabled resource limits must apply immediately: %v", err)
	}
	status, err = f.scheduler.Status(ctx)
	if err != nil || status.Capacity.Limit != 0 || status.Capacity.ReasonCode != "RESOURCE_UNKNOWN" {
		t.Fatalf("re-enabled status: %+v err=%v", status.Capacity, err)
	}
	sampleErr, busy, availablePercent = nil, 20, 75
	// 真实新样本恢复资源容量后，不限数量仍允许超过保存的上限；软线降额则继续保护主机。
	for sample := 0; sample < 4; sample++ {
		f.clock.Advance(SampleInterval)
		if _, err := f.scheduler.MaintainLeadership(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.scheduler.RefreshResources(ctx); err != nil {
			t.Fatal(err)
		}
		if sample == 2 {
			if err := reserve("recovering"); !errors.Is(err, domain.ErrSchedulerCapacity) {
				t.Fatalf("disabled concurrency must preserve recovery capacity: %v", err)
			}
		}
	}
	if err := reserve("twelfth"); err != nil {
		t.Fatalf("healthy resources must permit unlimited concurrency: %v", err)
	}
	busy = 75
	f.clock.Advance(SampleInterval)
	if err := reserve("thirteenth"); !errors.Is(err, domain.ErrSchedulerCapacity) {
		t.Fatalf("disabled concurrency must preserve resource soft-limit admission: %v", err)
	}
	status, err = f.scheduler.Status(ctx)
	if err != nil || status.Capacity.Limit != 1 || status.Capacity.ReasonCode != "RESOURCE_SOFT_LIMIT" {
		t.Fatalf("soft limit must retain positive reduced capacity: %+v err=%v", status.Capacity, err)
	}
}
