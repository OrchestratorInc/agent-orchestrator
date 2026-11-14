package store

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// SetPRContent records a title, description, or both that the provider just
// confirmed, so the view shows the edit before the next observation, which
// rewrites them from the provider. A nil field is left as it is. The
// description is cut to the stored cap like an observed one, and is skipped
// when the PR has no stored details yet: it then waits for that observation
// instead of fabricating a details row.
func (s *Store) SetPRContent(ctx context.Context, prURL string, title, description *string, changedAt time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "set pr content", func(q *gen.Queries) error {
		if title != nil {
			rows, err := q.SetPRTitle(ctx, gen.SetPRTitleParams{Title: *title, UpdatedAt: changedAt, URL: prURL})
			if err != nil {
				return fmt.Errorf("set pr title %s: %w", prURL, err)
			}
			if rows != 1 {
				return fmt.Errorf("set pr title %s: updated %d rows", prURL, rows)
			}
		}
		if description != nil {
			body, truncated := *description, false
			if len(body) > domain.MaxPullRequestDescriptionBytes {
				cut := domain.MaxPullRequestDescriptionBytes
				for cut > 0 && !utf8.RuneStart(body[cut]) {
					cut--
				}
				body, truncated = body[:cut], true
			}
			if _, err := q.SetPRDetailsDescription(ctx, gen.SetPRDetailsDescriptionParams{Description: body, DescriptionTruncated: boolInt(truncated), PRURL: prURL}); err != nil {
				return fmt.Errorf("set pr description %s: %w", prURL, err)
			}
		}
		return nil
	})
}
