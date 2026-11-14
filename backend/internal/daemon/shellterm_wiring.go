package daemon

import (
	"context"
	"log/slog"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	shelltermsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func startShellTerminals(
	ctx context.Context,
	cfg config.Config,
	runtime shelltermsvc.ShellRuntime,
	store *sqlite.Store,
	projects projectsvc.Manager,
	sessions *sessionsvc.Service,
	inputForgetter shelltermsvc.TerminalInputForgetter,
	log *slog.Logger,
) *shelltermsvc.Service {
	svc := shelltermsvc.NewService(
		runtime,
		store,
		&projectRootLocator{projects: projects},
		&sessionWorkspaceLocator{sessions: sessions},
		cfg.DataDir,
		cfg.AppRunID,
		log,
	)
	svc.SetTerminalInputForgetter(inputForgetter)
	if _, err := svc.ReapShellTerminalsFromPreviousAppRuns(ctx); err != nil {
		log.Warn("reaping shell terminals from previous app runs failed", "err", err)
	}
	return svc
}

type projectRootLocator struct {
	projects projectsvc.Manager
}

func (l *projectRootLocator) ProjectRoot(ctx context.Context, id domain.ProjectID) (string, error) {
	if l.projects == nil {
		return "", nil
	}
	res, err := l.projects.Get(ctx, id)
	if err != nil {
		return "", err
	}
	switch {
	case res.Project != nil:
		return res.Project.Path, nil
	case res.Degraded != nil:
		return res.Degraded.Path, nil
	default:
		return "", nil
	}
}

func (l *projectRootLocator) ProjectEnv(ctx context.Context, id domain.ProjectID) (map[string]string, error) {
	if l.projects == nil {
		return nil, nil
	}
	res, err := l.projects.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if res.Project == nil || res.Project.Config == nil {
		return nil, nil
	}
	return res.Project.Config.Env, nil
}

type sessionGetter interface {
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
}

type sessionWorkspaceLocator struct {
	sessions sessionGetter
}

func (l *sessionWorkspaceLocator) SessionWorkspace(ctx context.Context, id domain.SessionID) (string, domain.ProjectID, error) {
	if l.sessions == nil {
		return "", "", nil
	}
	sess, err := l.sessions.Get(ctx, id)
	if err != nil {
		return "", "", err
	}
	path := sess.Metadata.WorkspacePath
	if path != "" {
		if _, statErr := os.Stat(path); statErr != nil {
			path = ""
		}
	}
	return path, sess.ProjectID, nil
}

func (l *sessionWorkspaceLocator) CueCommandSessionTarget(ctx context.Context, id domain.SessionID) (shelltermsvc.CueCommandSessionTarget, error) {
	if l.sessions == nil {
		return shelltermsvc.CueCommandSessionTarget{}, apierr.Internal("SHELL_TERMINAL_NO_SESSION_LOOKUP", "Session lookup is unavailable")
	}
	sess, err := l.sessions.Get(ctx, id)
	if err != nil {
		return shelltermsvc.CueCommandSessionTarget{}, err
	}
	return shelltermsvc.CueCommandSessionTarget{
		ProjectID:     sess.ProjectID,
		WorkspacePath: sess.Metadata.WorkspacePath,
		Activity:      sess.Activity.State,
		IsTerminated:  sess.IsTerminated,
	}, nil
}
