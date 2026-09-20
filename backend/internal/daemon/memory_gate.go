package daemon

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// hostMemory is the slice of the memory reader the gate needs.
type hostMemory interface {
	SystemMemory(ctx context.Context) (domain.SystemMemory, error)
}

// lowMemoryGate refuses agent-requested spawns while the host has less free
// RAM than the user's reserve. It is a warning line, not a cap: nothing
// running is touched, and a host whose memory cannot be read is never gated.
func lowMemoryGate(settings autoPauseSettings, memory hostMemory, log *slog.Logger) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		sys, err := memory.SystemMemory(ctx)
		if err != nil || sys.TotalBytes == 0 {
			return nil
		}
		snapshot, err := settings.Get(ctx)
		if err != nil {
			log.Warn("memory gate: read settings", "err", err)
			return nil
		}
		reserve, _ := snapshot.ResolveMemoryReserve()
		if sys.AvailableBytes >= reserve {
			return nil
		}
		return apierr.Conflict("LOW_MEMORY",
			fmt.Sprintf("Host has %s free, below the %s reserve; pause or stop a session before starting another", formatBytes(sys.AvailableBytes), formatBytes(reserve)),
			map[string]any{"availableBytes": sys.AvailableBytes, "reserveBytes": reserve})
	}
}

func formatBytes(b uint64) string {
	const gib, mib = 1 << 30, 1 << 20
	if b >= gib {
		return fmt.Sprintf("%.1f GB", float64(b)/gib)
	}
	return fmt.Sprintf("%d MB", b/mib)
}
