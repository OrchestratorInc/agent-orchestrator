package daemon

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/storagemgr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// storageSessions adapts the session service to the storage service.
type storageSessions struct {
	svc *sessionsvc.Service
}

func (s storageSessions) ListAll(ctx context.Context) ([]domain.Session, error) {
	return s.svc.List(ctx, sessionsvc.ListFilter{})
}

func (s storageSessions) CleanupSessions(ctx context.Context, ids []domain.SessionID) ([]domain.SessionID, map[domain.SessionID]string, error) {
	out, err := s.svc.CleanupSessions(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	skipped := make(map[domain.SessionID]string, len(out.Skipped)+len(out.AlreadyGone))
	for _, skip := range out.Skipped {
		skipped[skip.SessionID] = skip.Reason
	}
	for _, id := range out.AlreadyGone {
		skipped[id] = "the worktree was already gone"
	}
	return out.Cleaned, skipped, nil
}

// refuseQuiet stands in when the session manager cannot guard an offload:
// every offload is skipped rather than run unguarded.
type refuseQuiet struct{}

func (refuseQuiet) WithQuietSession(context.Context, domain.SessionID, func(domain.SessionRecord) error) error {
	return sessionmanager.ErrSessionNotQuiet
}

// storageAutoCleanAllowed reads the AO_STORAGE_AUTOCLEAN kill switch: any of
// off/0/false/no keeps scheduled passes to logging what they would do.
func storageAutoCleanAllowed(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off", "0", "false", "no":
		return false
	default:
		return true
	}
}

// startStorage builds the storage service, lets the session manager restore
// and seed dependencies through it, and starts its schedule.
func startStorage(ctx context.Context, cfg config.Config, store *sqlite.Store, sessionSvc *sessionsvc.Service, mgr sessionLifecycle, log *slog.Logger) (*storagemgr.Service, <-chan struct{}) {
	var quiet storagemgr.QuietGuard = refuseQuiet{}
	if guard, ok := mgr.(storagemgr.QuietGuard); ok {
		quiet = guard
	}
	// Without the session manager's gate, orphan worktrees are left alone.
	var workspaces storagemgr.WorkspaceGuard
	if guard, ok := mgr.(storagemgr.WorkspaceGuard); ok {
		workspaces = guard
	}
	svc := storagemgr.New(storagemgr.Options{
		Layout:           storagemgr.Layout{StateDir: cfg.StateDir, DataDir: cfg.DataDir},
		Store:            store,
		Sessions:         storageSessions{svc: sessionSvc},
		Quiet:            quiet,
		Projects:         store,
		Logger:           log.With("component", "storage"),
		AutoCleanAllowed: storageAutoCleanAllowed(os.Getenv("AO_STORAGE_AUTOCLEAN")),
		Workspaces:       workspaces,
	})
	if hooked, ok := mgr.(interface {
		SetStorageHooks(hooks sessionmanager.StorageHooks)
	}); ok {
		hooked.SetStorageHooks(svc)
	}
	done := svc.Start(ctx)
	return svc, done
}
