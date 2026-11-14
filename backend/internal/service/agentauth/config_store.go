package agentauth

import (
	"context"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func atomicConfigWrite(ctx context.Context, dir, path string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The credential store directory could not be prepared.")
	}
	tmp, err := os.CreateTemp(dir, ".ao-auth-*")
	if err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The credential file could not be prepared.")
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The credential file could not be secured.")
	}
	_, writeErr := tmp.Write(data)
	if writeErr == nil {
		writeErr = tmp.Sync()
	}
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The credential file could not be written.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The credential file could not be replaced.")
	}
	return nil
}
