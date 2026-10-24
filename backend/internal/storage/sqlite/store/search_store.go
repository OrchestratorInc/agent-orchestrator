package store

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// searchConversationMessagesSQL ranks finished chat messages with FTS5 bm25
// (conversation_messages_fts), then keeps the ones the transcript shows: the
// SelectConversationMessages rule (active branch path; rolled-back, promoted
// and cancelled turns hidden). Snippets are built only for those, with matches
// wrapped in \x02 and \x03. Hand-written: sqlc cannot parse rowid or FTS5.
const searchConversationMessagesSQL = `
WITH RECURSIVE hits AS MATERIALIZED (
    SELECT rowid AS message_rowid, rank AS score
    FROM conversation_messages_fts
    WHERE conversation_messages_fts MATCH ?1
    ORDER BY rank
    LIMIT ?2
), active_path(conversation_id, branch_id, max_sequence) AS (
    SELECT conversations.id, conversations.active_branch_id, CAST(NULL AS INTEGER)
    FROM conversations
    WHERE conversations.id IN (
        SELECT message.conversation_id FROM hits
        JOIN conversation_messages AS message ON message.rowid = hits.message_rowid
    )
    UNION ALL
    SELECT path.conversation_id, branch.parent_branch_id,
           CASE
               WHEN path.max_sequence IS NULL THEN branch.fork_after_sequence
               WHEN branch.fork_after_sequence < path.max_sequence THEN branch.fork_after_sequence
               ELSE path.max_sequence
           END
    FROM active_path AS path
    JOIN conversation_branches AS branch ON branch.id = path.branch_id
    WHERE branch.parent_branch_id IS NOT NULL
)
SELECT message.id,
       (SELECT snippet(conversation_messages_fts, 0, char(2), char(3), '…', 24)
        FROM conversation_messages_fts
        WHERE conversation_messages_fts MATCH ?1 AND rowid = hits.message_rowid),
       COALESCE(conversation.session_id, conversation.current_session_id, review.session_id, ''),
       COALESCE(conversation.project_id, ''),
       COALESCE(conversation.review_id, ''),
       COALESCE(NULLIF(conversation.applied_title, ''), conversation.provider_title, '')
FROM hits
JOIN conversation_messages AS message ON message.rowid = hits.message_rowid
JOIN active_path AS path
  ON path.conversation_id = message.conversation_id AND path.branch_id = message.branch_id
JOIN conversations AS conversation ON conversation.id = message.conversation_id
LEFT JOIN review ON review.id = conversation.review_id
LEFT JOIN conversation_turns AS turn ON turn.id = message.turn_id
WHERE (path.max_sequence IS NULL OR message.sequence <= path.max_sequence)
  AND (message.turn_id IS NULL OR turn.id IS NULL OR (
      turn.rolled_back_at IS NULL AND turn.promoted_to_turn_id IS NULL AND turn.state <> 'cancelled'
  ))
ORDER BY hits.score`

const (
	chatSearchResults = 8
	// Matches read past the page: some fall on edited-away branches or repeat.
	chatSearchCandidates = 4 * chatSearchResults
)

// SearchConversationMessages returns visible chat messages matching query,
// best first. Every word must match (by word form; the last also by prefix,
// as it may be half typed); when none does, any word may, so one misspelled
// word does not hide the message.
func (s *Store) SearchConversationMessages(ctx context.Context, query string) ([]domain.ChatSearchHit, error) {
	terms := domain.SearchTerms(query)
	words := make([]string, 0, len(terms))
	for _, term := range terms {
		words = append(words, strings.FieldsFunc(term, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })...)
	}
	if len(words) == 0 {
		return nil, nil
	}
	terms = make([]string, len(words))
	for i, word := range words {
		terms[i] = `"` + word + `"`
	}
	terms[len(terms)-1] += "*"
	hits, err := s.searchConversationMessages(ctx, strings.Join(terms, " "))
	if err != nil || len(hits) > 0 || len(terms) == 1 {
		return hits, err
	}
	return s.searchConversationMessages(ctx, strings.Join(terms, " OR "))
}

func (s *Store) searchConversationMessages(ctx context.Context, match string) ([]domain.ChatSearchHit, error) {
	rows, err := s.readDB.QueryContext(ctx, searchConversationMessagesSQL, match, chatSearchCandidates)
	if err != nil {
		return nil, fmt.Errorf("search conversation messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var hits []domain.ChatSearchHit
	// A provider can record one reply twice in a turn; one row per passage.
	seen := map[[3]string]bool{}
	for rows.Next() && len(hits) < chatSearchResults {
		var hit domain.ChatSearchHit
		if err := rows.Scan(&hit.MessageID, &hit.Snippet, &hit.SessionID, &hit.ProjectID, &hit.ReviewID, &hit.Title); err != nil {
			return nil, fmt.Errorf("scan conversation message hit: %w", err)
		}
		key := [3]string{hit.SessionID, hit.ReviewID, hit.Snippet}
		if !seen[key] {
			seen[key] = true
			hits = append(hits, hit)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search conversation messages: %w", err)
	}
	return hits, nil
}
