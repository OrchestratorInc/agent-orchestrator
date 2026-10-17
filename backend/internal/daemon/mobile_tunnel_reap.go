package daemon

import (
	"log/slog"
	"sync"
)

// A failed startup reap must block both connector-resolution paths. Retry on
// demand, then stop reaping once a replacement connector may have been created.
func mobileTunnelReapGuard(reap func() error, log *slog.Logger) func() bool {
	var mu sync.Mutex
	complete := false
	return func() bool {
		mu.Lock()
		defer mu.Unlock()
		if complete {
			return true
		}
		if err := reap(); err != nil {
			log.Warn("mobile tunnel replacement blocked by unresolved cleanup", "error", err)
			return false
		}
		complete = true
		return true
	}
}
