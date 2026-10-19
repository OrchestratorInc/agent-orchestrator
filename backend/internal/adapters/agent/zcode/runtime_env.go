package zcode

import (
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentRuntimeLaunchEnv = (*Plugin)(nil)

// AugmentRuntimeLaunchEnv pins the credential store the launched zcode process
// uses, so the auth probe (AuthStatus), the login plan, and every session
// launch resolve the same store. Resolution mirrors the probe: a value already
// in the session env map wins (project-level override), then an operator-set
// ZCODE_DATA_BASE_DIR in the daemon's own environment, then the adapter-owned
// location under the manager's data dir.
func (p *Plugin) AugmentRuntimeLaunchEnv(env map[string]string, dataDir string, _ domain.SessionID, _ string) {
	if env == nil {
		return
	}
	if _, ok := env[zcodeDataBaseDirEnv]; ok {
		return
	}
	base := strings.TrimSpace(os.Getenv(zcodeDataBaseDirEnv))
	if base == "" {
		base = zcodeHarnessStoreBase(dataDir)
	}
	if base != "" {
		env[zcodeDataBaseDirEnv] = base
	}
}

// CredentialStoreBase returns the ZCODE_DATA_BASE_DIR value the adapter pins
// for a given AO data dir: <aoDataDir>/harnesses/zcode. Exported for the
// agentauth login plan, which must launch `zcode login zai` against the same
// store the probe reads and the sessions launch with.
func CredentialStoreBase(aoDataDir string) string {
	return zcodeHarnessStoreBase(aoDataDir)
}
