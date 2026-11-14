package sessionmanager

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type workspaceRestoreContext struct {
	nativeID, configDir string
}

func restoredWorkspaceContext(rec domain.SessionRecord, fresh bool) workspaceRestoreContext {
	if fresh || rec.Metadata.AgentSessionID == "" {
		return workspaceRestoreContext{}
	}
	context := workspaceRestoreContext{nativeID: rec.Metadata.AgentSessionID}
	if source := rec.Metadata.ImportSource; source != nil {
		if ref := importedNativeRef(source); context.nativeID == ref.NativeSessionID {
			context.configDir = ref.ConfigDir
		}
	}
	return context
}

func (r workspaceRestoreContext) apply(cfg *ports.WorkspaceHookConfig) {
	cfg.NativeSessionID, cfg.ImportConfigDir = r.nativeID, r.configDir
}
