package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type persistentChatSessionStore interface {
	ListAllSessions(context.Context) ([]domain.SessionRecord, error)
	ListRecoverableChatReviews(context.Context) ([]domain.Review, error)
}

// reconcilePersistentChatHosts removes hosts only when durable state proves
// there is no live Chat session to adopt. An unreadable session set is not
// evidence that any host is orphaned.
func reconcilePersistentChatHosts(ctx context.Context, dataDir string, store persistentChatSessionStore, appRunIDs ...string) error {
	records, err := store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("list sessions for persistent chat hosts: %w", err)
	}
	reviews, err := store.ListRecoverableChatReviews(ctx)
	if err != nil {
		return fmt.Errorf("list reviews for persistent chat hosts: %w", err)
	}
	keep := persistentChatHostKeepSet(records, reviews)
	if len(appRunIDs) > 0 && appRunIDs[0] != "" {
		keepLaunchSideHosts(dataDir, appRunIDs[0], keep)
	}
	return persistenthost.Reconcile(ctx, dataDir, keep)
}

func persistentChatHostKeepSet(records []domain.SessionRecord, reviews []domain.Review) map[string]struct{} {
	keep := make(map[string]struct{})
	for _, rec := range records {
		if rec.IsTerminated || rec.HibernatedAt != nil || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
			continue
		}
		keep[string(rec.ID)] = struct{}{}
	}
	for _, review := range reviews {
		keep["review-"+review.ID] = struct{}{}
	}
	return keep
}

// Launch identity in the host name preserves side providers until Electron
// restores its RAM snapshot. A fresh desktop launch has a different identity,
// so the existing authenticated reconciliation retires its predecessor's hosts.
func keepLaunchSideHosts(dataDir, runID string, keep map[string]struct{}) {
	entries, err := os.ReadDir(filepath.Join(dataDir, "chat-hosts"))
	if err != nil {
		return
	}
	prefix := "btw-" + runID + "-"
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			keep[entry.Name()] = struct{}{}
		}
	}
}
