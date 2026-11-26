package daemon

import (
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

// restoreMobileOnBoot re-arms the Connect Mobile LAN listener across daemon
// restarts. If the persisted state says the bridge was enabled, it delegates to
// BridgeService.RestoreOnBoot, which reuses the existing password (no rotation
// — the paired phone keeps working), derives the auth hash in memory, restarts
// the listener on its last bound port, and — critically — re-applies the
// secure-pairing `tailscale serve` proxy against whatever port Start actually
// returned, not the persisted port. That distinction matters: Start falls back
// to an ephemeral port when the persisted one is taken, and a stale `serve`
// config would otherwise keep proxying the tailnet at a dead (or, worse,
// someone else's) port. Restoration errors are warnings and do not stop daemon
// startup. Connect Mobile is best-effort, not load-bearing. Wake ownership is
// restored independently, including when the listener was disabled.
func restoreMobileOnBoot(path string, bs *controllers.BridgeService) error {
	state, err := mobilebridge.Load(path)
	if err != nil {
		return fmt.Errorf("load mobile bridge state: %w", err)
	}
	if !state.Enabled && !state.KeepAwake {
		return nil
	}
	if err := bs.RestoreOnBoot(state); err != nil {
		return fmt.Errorf("restore mobile and awake settings: %w", err)
	}
	return nil
}
