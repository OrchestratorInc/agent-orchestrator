package sessionmanager

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// spawnBranch 为独立编排会话使用会话专属分支，主编排器仍复用项目分支。
func spawnBranch(rec domain.SessionRecord, prefix string, projectKind domain.ProjectKind, dataDir string) string {
	kind := rec.Kind
	if rec.IndependentOrchestrator {
		kind = domain.KindWorker
	}
	return DefaultSpawnBranch(rec.ID, kind, prefix, projectKind, dataDir)
}
