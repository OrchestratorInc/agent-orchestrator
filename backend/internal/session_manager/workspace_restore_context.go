package sessionmanager

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type workspaceRestoreContext struct {
	nativeID, configDir string
	mode                ports.WorkspaceRestoreMode
}

func restoredWorkspaceContext(rec domain.SessionRecord, fresh bool) workspaceRestoreContext {
	if fresh || rec.Metadata.AgentSessionID == "" {
		return workspaceRestoreContext{}
	}
	context := workspaceRestoreContext{nativeID: rec.Metadata.AgentSessionID, mode: ports.WorkspaceRestoreNative}
	if source := rec.Metadata.ImportSource; source != nil {
		ref := importedNativeRef(source)
		if context.nativeID == ref.NativeSessionID {
			context.configDir = ref.ConfigDir
			if rec.NeedsImportResume() {
				context.mode = ports.WorkspaceRestoreImported
			}
		}
	}
	return context
}

func (r workspaceRestoreContext) apply(cfg *ports.WorkspaceHookConfig) {
	cfg.NativeSessionID, cfg.RestoreMode, cfg.ImportConfigDir = r.nativeID, r.mode, r.configDir
}
