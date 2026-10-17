package tasknaming

import (
	"context"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
)

// RunProviderHost handles the internal chat-host subprocess re-exec used by the
// shared Chat drivers. Binaries using RunBackground must dispatch this command
// before their normal startup so the same persistent provider host can run.
func RunProviderHost(ctx context.Context, args []string) error {
	cfg, err := persistenthost.ParseArgs(args)
	if err != nil {
		return err
	}
	cfg.Env = os.Environ()
	return persistenthost.Run(ctx, cfg)
}
