package resources

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// 删除失败时 previousAt 的清零会使故障后的首样伪装成可用差分；删除零 ticks 判断则会接纳 Windows 的部分失败。
func TestSamplerRequiresConsecutiveValidReadings(t *testing.T) {
	now := time.Date(2026, time.November, 5, 8, 0, 0, 0, time.UTC)
	raw := procmem.System{TotalBytes: 16 << 30, AvailableBytes: 8 << 30, CPUTotalTicks: 100, CPUBusyTicks: 20}
	var readErr error
	s := &Sampler{clock: func() time.Time { return now }, read: func() (procmem.System, error) { return raw, readErr }}
	first, err := s.Sample(context.Background())
	if err != nil || first.ReasonCode != "resource_first_sample" || !first.PreviousAt.IsZero() {
		t.Fatalf("首样不能证明空闲: %+v, %v", first, err)
	}
	now = now.Add(5 * time.Second)
	raw.CPUTotalTicks, raw.CPUBusyTicks = 200, 50
	second, err := s.Sample(context.Background())
	if err != nil || second.ReasonCode != "" || second.PreviousAt != first.ObservedAt ||
		second.PreviousCPUTotalTicks != 100 || second.CPUBusyTicks-second.PreviousCPUBusyTicks != 30 {
		t.Fatalf("相邻读数未保留真实来源: %+v, %v", second, err)
	}
	now = now.Add(5 * time.Second)
	readErr = errors.New("sample unavailable")
	failed, err := s.Sample(context.Background())
	if !errors.Is(err, readErr) || failed.ReasonCode != "resource_sample_failed" {
		t.Fatalf("采样失败丢失: %+v, %v", failed, err)
	}
	readErr = nil
	now = now.Add(5 * time.Second)
	raw.CPUTotalTicks, raw.CPUBusyTicks = 400, 90
	restarted, err := s.Sample(context.Background())
	if err != nil || restarted.ReasonCode != "resource_first_sample" {
		t.Fatalf("失败后复用了旧基线: %+v, %v", restarted, err)
	}
	now = now.Add(5 * time.Second)
	raw.CPUTotalTicks, raw.CPUBusyTicks = 0, 0
	partial, err := s.Sample(context.Background())
	if err != nil || partial.ReasonCode != "resource_sample_invalid" {
		t.Fatalf("RAM成功掩盖CPU未知: %+v, %v", partial, err)
	}
	now = now.Add(5 * time.Second)
	raw.CPUTotalTicks, raw.CPUBusyTicks = 600, 120
	afterPartial, _ := s.Sample(context.Background())
	if afterPartial.ReasonCode != "resource_first_sample" {
		t.Fatalf("零CPU计数成为了可信基线: %+v", afterPartial)
	}
}

// 删除间隔或 tick 顺序分支会把休眠、时钟回退和计数重置伪装成即时资源证据。
func TestSamplerRejectsDiscontinuousEvidence(t *testing.T) {
	now := time.Date(2026, time.November, 5, 8, 0, 0, 0, time.UTC)
	raw := procmem.System{TotalBytes: 16 << 30, AvailableBytes: 8 << 30, CPUTotalTicks: 100, CPUBusyTicks: 20}
	s := &Sampler{clock: func() time.Time { return now }, read: func() (procmem.System, error) { return raw, nil }}
	_, _ = s.Sample(context.Background())
	now = now.Add(time.Minute)
	raw.CPUTotalTicks, raw.CPUBusyTicks = 200, 30
	gap, _ := s.Sample(context.Background())
	if gap.ReasonCode != "resource_sample_gap" {
		t.Fatalf("跨休眠间隔被接纳: %+v", gap)
	}
	now = now.Add(5 * time.Second)
	raw.CPUTotalTicks, raw.CPUBusyTicks = 300, 40
	current, _ := s.Sample(context.Background())
	if current.ReasonCode != "" || current.PreviousAt != gap.ObservedAt {
		t.Fatalf("没有从最新真实样本恢复: %+v", current)
	}
	now = now.Add(5 * time.Second)
	raw.CPUTotalTicks, raw.CPUBusyTicks = 250, 25
	reset, _ := s.Sample(context.Background())
	if reset.ReasonCode != "resource_ticks_invalid" {
		t.Fatalf("计数回退被接纳: %+v", reset)
	}
}
