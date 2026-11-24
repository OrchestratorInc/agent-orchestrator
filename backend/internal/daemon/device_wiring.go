package daemon

import (
	"context"
	"log/slog"

	localdevice "github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/device/local"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/devices"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

// deviceSessions is the slice of the session service the device service
// reads: workspace paths for platform detection and termination for freeing
// leases.
type deviceSessions interface {
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
}

// newDeviceService builds the simulator and emulator service with the local
// provider. Nothing installs or starts until the user opts in.
func newDeviceService(dataDir string, sessions deviceSessions, log *slog.Logger) *devices.Service {
	return devices.New(devices.Config{
		DataDir:  dataDir,
		Provider: localdevice.New(localdevice.Config{DataDir: dataDir, Log: log}),
		Sessions: func(ctx context.Context, id domain.SessionID) (devices.SessionInfo, error) {
			sess, err := sessions.Get(ctx, id)
			if err != nil {
				return devices.SessionInfo{}, err
			}
			return devices.SessionInfo{WorkspacePath: sess.Metadata.WorkspacePath, Terminated: sess.IsTerminated}, nil
		},
		Log: log,
	})
}
