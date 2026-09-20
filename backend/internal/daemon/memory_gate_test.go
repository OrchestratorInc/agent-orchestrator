package daemon

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	settingssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/settings"
)

type fakeHostMemory struct {
	sys domain.SystemMemory
	err error
}

func (f fakeHostMemory) SystemMemory(context.Context) (domain.SystemMemory, error) {
	return f.sys, f.err
}

type fakeReserveSettings struct{ bytes int64 }

func (f fakeReserveSettings) Get(context.Context) (settingssvc.Snapshot, error) {
	return settingssvc.Snapshot{MemoryReserveBytes: f.bytes}, nil
}

func TestLowMemoryGateRefusesBelowReserveOnly(t *testing.T) {
	const gib = 1 << 30
	gate := lowMemoryGate(fakeReserveSettings{}, fakeHostMemory{sys: domain.SystemMemory{TotalBytes: 16 * gib, AvailableBytes: 1 * gib}}, slog.Default())
	err := gate(context.Background())
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "LOW_MEMORY" {
		t.Fatalf("err = %v, want LOW_MEMORY", err)
	}
	gate = lowMemoryGate(fakeReserveSettings{}, fakeHostMemory{sys: domain.SystemMemory{TotalBytes: 16 * gib, AvailableBytes: 3 * gib}}, slog.Default())
	if err := gate(context.Background()); err != nil {
		t.Fatalf("3 GiB free above the 2 GiB default should pass: %v", err)
	}
	gate = lowMemoryGate(fakeReserveSettings{bytes: 4 * gib}, fakeHostMemory{sys: domain.SystemMemory{TotalBytes: 16 * gib, AvailableBytes: 3 * gib}}, slog.Default())
	if err := gate(context.Background()); err == nil {
		t.Fatal("3 GiB free below an explicit 4 GiB reserve should refuse")
	}
}

func TestLowMemoryGateNeverBlocksWhenHostCannotBeRead(t *testing.T) {
	gate := lowMemoryGate(fakeReserveSettings{}, fakeHostMemory{err: errors.New("unsupported")}, slog.Default())
	if err := gate(context.Background()); err != nil {
		t.Fatalf("unreadable host must not gate: %v", err)
	}
}
