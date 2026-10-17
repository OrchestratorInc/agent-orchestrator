package sessionmanager

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The handle remains unpublished and ordinary input remains fenced throughout
// this proof. A failed witness destroys only this replacement runtime.
func (m *Manager) verifyNativeRestore(ctx context.Context, verifier ports.NativeRestoreVerifier, cfg ports.RestoreConfig, generation string, handle ports.RuntimeHandle) error {
	reader, ok := m.runtime.(ports.StyledTerminalOutputReader)
	if !ok {
		return errors.New("native restore requires a current rendered terminal")
	}
	proofCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		output, err := reader.GetStyledOutput(proofCtx, handle, 32)
		if err != nil {
			return err
		}
		ready, err := verifier.VerifyNativeRestore(proofCtx, cfg, generation, output)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if err := sleepContext(proofCtx, 250*time.Millisecond); err != nil {
			return err
		}
	}
}
