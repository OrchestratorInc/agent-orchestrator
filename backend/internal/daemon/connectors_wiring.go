package daemon

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/connectors"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type connectorStore interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
}

type connectorSessions interface {
	SetSessionConnectors(servers func(context.Context, sessionmanager.SessionConnectorsInput) []ports.ChatMCPServerConfig)
}

// wireConnectors gives new sessions their connectors, and gives the connector
// service the sessions and projects it checks calls and imports against.
func wireConnectors(svc *connectors.Service, store connectorStore, sessions connectorSessions) {
	svc.SetSessionLookup(func(ctx context.Context, sessionID string) (string, bool) {
		rec, ok, err := store.GetSession(ctx, domain.SessionID(sessionID))
		return string(rec.ProjectID), err == nil && ok && !rec.IsTerminated
	})
	svc.SetImportProjects(func(ctx context.Context) []connectors.ImportProject {
		projects, err := store.ListProjects(ctx)
		if err != nil {
			return nil
		}
		out := make([]connectors.ImportProject, 0, len(projects))
		for _, project := range projects {
			if project.ArchivedAt.IsZero() {
				out = append(out, connectors.ImportProject{ID: project.ID, Path: project.Path})
			}
		}
		return out
	})
	sessions.SetSessionConnectors(func(ctx context.Context, in sessionmanager.SessionConnectorsInput) []ports.ChatMCPServerConfig {
		return svc.ForSession(ctx, connectors.SessionInput{
			SessionID: in.SessionID, ProjectID: string(in.ProjectID),
			Native: connectors.NativeNames(in.Harness, in.Workspace, in.Env),
		})
	})
}
