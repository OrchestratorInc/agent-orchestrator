package store

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// searchConversationMessagesSQL orders finished chat messages matching ?1
// (conversation_messages_fts) by relevance (FTS5 bm25, with messages holding
// the query as typed, phrase ?3, first) or, when a query is too common to rank
// cheaply, newest first. It keeps the ones the transcript shows:
// the SelectConversationMessages rule (active branch path; rolled-back,
// promoted and cancelled turns hidden), and only the conversation currently
// selected for a session/reviewer can produce a navigable hit. Snippets have
// matches wrapped in \x02 and \x03. Visibility is checked before the candidate
// limit; each match walks only its own conversation lineage. Hand-written:
// sqlc cannot parse rowid or FTS5.
const searchConversationMessagesSQL = `
WITH hits AS MATERIALIZED (
    SELECT conversation_messages_fts.rowid AS message_rowid, %[1]s AS score
    FROM conversation_messages_fts
    JOIN conversation_messages AS candidate ON candidate.rowid = conversation_messages_fts.rowid
    JOIN conversations AS owner ON owner.id = candidate.conversation_id
    LEFT JOIN conversation_turns AS candidate_turn ON candidate_turn.id = candidate.turn_id
    LEFT JOIN review AS owner_review ON owner_review.id = owner.current_review_id
    JOIN sessions AS owner_session ON owner_session.id = COALESCE(owner.current_session_id, owner_review.session_id)
    WHERE conversation_messages_fts MATCH ?1
      AND ((owner.current_review_id IS NOT NULL AND owner_review.interface_mode = 'chat' AND owner_review.is_archived = 0)
        OR (owner.current_review_id IS NULL AND owner_session.session_mode = 'chat' AND owner.id = (
            SELECT current.id FROM conversations AS current
            WHERE current.current_session_id = owner_session.id
            ORDER BY current.scope = 'project' DESC, current.created_at DESC, current.id DESC
            LIMIT 1
        )))
      AND (candidate.turn_id IS NULL OR candidate_turn.id IS NULL OR (
          candidate_turn.rolled_back_at IS NULL AND candidate_turn.promoted_to_turn_id IS NULL AND candidate_turn.state <> 'cancelled'
      ))
      AND EXISTS (
          WITH RECURSIVE candidate_path(branch_id, max_sequence) AS (
              SELECT owner.active_branch_id, CAST(NULL AS INTEGER)
              UNION ALL
              SELECT branch.parent_branch_id,
                     CASE
                         WHEN path.max_sequence IS NULL THEN branch.fork_after_sequence
                         WHEN branch.fork_after_sequence < path.max_sequence THEN branch.fork_after_sequence
                         ELSE path.max_sequence
                     END
              FROM candidate_path AS path
              JOIN conversation_branches AS branch ON branch.id = path.branch_id
              WHERE branch.parent_branch_id IS NOT NULL
          )
          SELECT 1 FROM candidate_path
          WHERE branch_id = candidate.branch_id
            AND (max_sequence IS NULL OR candidate.sequence <= max_sequence)
      )
    ORDER BY %[2]s
    LIMIT ?2
)
SELECT message.id,
       (SELECT snippet(conversation_messages_fts, 0, char(2), char(3), '…', 24)
        FROM conversation_messages_fts
        WHERE conversation_messages_fts MATCH ?1 AND rowid = hits.message_rowid),
       COALESCE(conversation.current_session_id, review.session_id, ''),
       COALESCE(session.project_id, ''),
       COALESCE(conversation.current_review_id, ''),
       COALESCE(NULLIF(conversation.applied_title, ''), conversation.provider_title, '')
FROM hits
JOIN conversation_messages AS message ON message.rowid = hits.message_rowid
JOIN conversations AS conversation ON conversation.id = message.conversation_id
LEFT JOIN review ON review.id = conversation.current_review_id
JOIN sessions AS session ON session.id = COALESCE(conversation.current_session_id, review.session_id)
ORDER BY hits.score`

var (
	searchMessagesByRelevanceSQL = fmt.Sprintf(searchConversationMessagesSQL,
		"conversation_messages_fts.rank - 1e6 * (conversation_messages_fts.rowid IN (SELECT rowid FROM conversation_messages_fts WHERE conversation_messages_fts MATCH ?3))", "score")
	// FTS5 walks rowids in reverse and stops at the limit; bm25 would score
	// every match first.
	searchMessagesByRecencySQL = fmt.Sprintf(searchConversationMessagesSQL, "-conversation_messages_fts.rowid", "conversation_messages_fts.rowid DESC")
)

const (
	// Past this many matches a query is too common to rank by relevance.
	chatSearchRankLimit = 2000
	chatSearchResults   = 8
	// Matches read past the page: providers may repeat the same passage.
	chatSearchCandidates = 4 * chatSearchResults
)

// SearchConversationMessages returns visible chat messages matching query,
// best first. Every word must match (by word form; the last also by prefix,
// as it may be half typed); when none does, any word may, so one misspelled
// word does not hide the message.
func (s *Store) SearchConversationMessages(ctx context.Context, query string) ([]domain.ChatSearchHit, error) {
	words := ftsWords(strings.Join(domain.SearchTerms(query), " "))
	if len(words) == 0 {
		return nil, nil
	}
	terms := make([]string, len(words))
	for i, word := range words {
		terms[i] = `"` + word + `"`
	}
	terms[len(terms)-1] += "*"
	// Every word, filler included, in the order typed.
	phrase := `"` + strings.Join(ftsWords(query), " ") + `"*`
	hits, err := s.searchConversationMessages(ctx, strings.Join(terms, " "), phrase)
	if err != nil || len(hits) > 0 || len(terms) == 1 {
		return hits, err
	}
	return s.searchConversationMessages(ctx, strings.Join(terms, " OR "), phrase)
}

// ftsWords keeps the letters and digits of text as words, so punctuation and
// FTS5 operators are never parsed as query syntax.
func ftsWords(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

func (s *Store) searchConversationMessages(ctx context.Context, match, phrase string) ([]domain.ChatSearchHit, error) {
	var matches int
	if err := s.readDB.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM conversation_messages_fts WHERE conversation_messages_fts MATCH ? LIMIT ?)`, match, chatSearchRankLimit).Scan(&matches); err != nil {
		return nil, fmt.Errorf("count conversation message matches: %w", err)
	}
	query, args := searchMessagesByRelevanceSQL, []any{match, chatSearchCandidates, phrase}
	if matches == chatSearchRankLimit {
		query, args = searchMessagesByRecencySQL, args[:2]
	}
	rows, err := s.readDB.QueryContext(ctx, query, args...)
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
