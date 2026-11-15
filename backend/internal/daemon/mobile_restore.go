package daemon

import (
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

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
