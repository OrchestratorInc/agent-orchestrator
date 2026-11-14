package lifecycle

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

func (m *Manager) ReconcileSessionOutputType(ctx context.Context, id domain.SessionID) error {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	artifactDir := rec.Metadata.ArtifactDir
	backfilled := false
	if artifactDir == "" {
		if dir := sessionartifacts.Dir(m.dataDir, id); dir != "" {
			artifactDir = dir
			backfilled = true
		}
	}
	prs, err := m.store.ListPRsBySession(ctx, id)
	if err != nil {
		return err
	}
	artifacts, err := sessionartifacts.List(ctx, artifactDir)
	if err != nil {
		return err
	}
	next := sessionartifacts.DeriveOutputType(len(prs), len(artifacts))
	if next == rec.OutputType && !backfilled {
		return nil
	}
	_, err = m.store.UpdateSessionArtifactOutput(ctx, id, artifactDir, next)
	return err
}
