package daemon

import (
	"context"
	"fmt"

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
func reconcilePersistentChatHosts(ctx context.Context, dataDir string, store persistentChatSessionStore) error {
	records, err := store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("list sessions for persistent chat hosts: %w", err)
	}
	reviews, err := store.ListRecoverableChatReviews(ctx)
	if err != nil {
		return fmt.Errorf("list reviews for persistent chat hosts: %w", err)
	}
	return persistenthost.Reconcile(ctx, dataDir, persistentChatHostKeepSet(records, reviews))
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
