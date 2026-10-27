package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// This file is the persistence half of session search. It writes only the three
// session_search_* tables, which this feature owns; the session data it reads
// (sessions, conversations, activities) is never written here. Reads go through
// the reader pool, writes through the single writer connection.

// sessionSearchSnippetStart and End bracket a matched term inside a snippet.
const (
	sessionSearchSnippetStart = domain.SessionSearchSnippetStart
	sessionSearchSnippetEnd   = domain.SessionSearchSnippetEnd
)

// sessionSearchFilterSQL is the WHERE tail shared by the ranked search and its
// count, so the two can never disagree about what matched. It expects the
// documents table aliased as d; sessionSearchFilterArgs supplies its arguments.
const sessionSearchFilterSQL = `
  AND (? = '' OR d.project_id = ?)
  AND (? = '' OR d.harness = ?)
  AND (? = '' OR d.machine = ?)
  AND (? = '' OR d.session_id = ?)
  -- path: and !term filter SESSIONS, not documents. A person asking for
  -- "sessions that touched src/upload" means the session, not the one row that
  -- happens to carry the path; and "!upload" means "not this session", not
  -- "not this document" (which would leave the session's other rows visible and
  -- read as if the exclusion were ignored).
  AND (? = '' OR d.session_id IN (
      SELECT p.session_id FROM session_search_docs p
      WHERE p.id IN (SELECT rowid FROM session_search_fts WHERE session_search_fts MATCH ?)
  ))
  AND (? = '' OR d.session_id NOT IN (
      SELECT x.session_id FROM session_search_docs x
      WHERE x.id IN (SELECT rowid FROM session_search_fts WHERE session_search_fts MATCH ?)
  ))
  AND (? = '' OR (
      ? = 'killed' AND d.session_id IN (SELECT id FROM sessions WHERE is_terminated = 1)
      OR ? = 'hibernated' AND d.session_id IN (SELECT id FROM sessions WHERE hibernated_at IS NOT NULL AND is_terminated = 0)
      OR ? = 'live' AND d.session_id IN (SELECT id FROM sessions WHERE is_terminated = 0 AND hibernated_at IS NULL)
  ))
  AND (? = 0 OR d.created_at >= ?)
  AND (? = 0 OR d.created_at <= ?)`

// sessionSearchFilterArgs binds sessionSearchFilterSQL.
func sessionSearchFilterArgs(params domain.SessionSearchParams) []any {
	from, to := int64(0), int64(0)
	if params.From != nil && !params.From.IsZero() {
		from = params.From.UnixMilli()
	}
	if params.To != nil && !params.To.IsZero() {
		to = params.To.UnixMilli()
	}
	pathMatch := domain.SessionSearchPathMatch(params.Path)
	status := string(params.Status)
	return []any{
		params.ProjectID, params.ProjectID,
		params.Harness, params.Harness,
		params.Machine, params.Machine,
		params.SessionID, params.SessionID,
		pathMatch, pathMatch,
		params.ExcludeQuery, params.ExcludeQuery,
		status, status, status, status,
		from, from, to, to,
	}
}

// browseSessionFilterSQL is the WHERE clause of a browse (filters, no words)
// and of its count. It applies every filter the ranked search applies, against
// the sessions table aliased as s: a browse that dropped one (a pasted PR's
// session, path:, machine:, !term) listed every session instead.
const browseSessionFilterSQL = `
WHERE (? = '' OR s.project_id = ?)
  AND (? = '' OR s.harness = ?)
  AND (? = '' OR s.id = ?)
  AND (? = '' OR s.id IN (SELECT m.session_id FROM session_search_docs m WHERE m.machine = ?))
  AND (? = '' OR s.id IN (
      SELECT p.session_id FROM session_search_docs p
      WHERE p.id IN (SELECT rowid FROM session_search_fts WHERE session_search_fts MATCH ?)
  ))
  AND (? = '' OR s.id NOT IN (
      SELECT x.session_id FROM session_search_docs x
      WHERE x.id IN (SELECT rowid FROM session_search_fts WHERE session_search_fts MATCH ?)
  ))
  AND (? = 0 OR ` + sessionCreatedMillisSQL + ` >= ?)
  AND (? = 0 OR ` + sessionCreatedMillisSQL + ` <= ?)
  AND (? = '' OR (
      ? = 'killed' AND s.is_terminated = 1
      OR ? = 'hibernated' AND s.is_terminated = 0 AND s.hibernated_at IS NOT NULL
      OR ? = 'live' AND s.is_terminated = 0 AND s.hibernated_at IS NULL
  ))`

// browseSessionFilterArgs binds browseSessionFilterSQL.
func browseSessionFilterArgs(params domain.SessionSearchParams) []any {
	from, to := int64(0), int64(0)
	if params.From != nil && !params.From.IsZero() {
		from = params.From.UnixMilli()
	}
	if params.To != nil && !params.To.IsZero() {
		to = params.To.UnixMilli()
	}
	pathMatch := domain.SessionSearchPathMatch(params.Path)
	status := string(params.Status)
	return []any{
		params.ProjectID, params.ProjectID,
		params.Harness, params.Harness,
		params.SessionID, params.SessionID,
		params.Machine, params.Machine,
		pathMatch, pathMatch,
		params.ExcludeQuery, params.ExcludeQuery,
		from, from, to, to,
		status, status, status, status,
	}
}

// searchSessionSearchDocsSQL ranks candidates and returns one page.
//
// Scoring is bm25 throughout. FTS5's bm25 (k1 = 1.2, b = 0.75, the Robertson
// defaults Lucene also ships) weights the title column above paths above body,
// and the document's kind multiplies the score (a BM25F-style field boost; see
// domain.DefaultSessionSearchKindWeights). FTS5 scores are negative, better is
// more negative, so a weight above 1 promotes and ascending order is best-first.
//
// Order, most significant first:
//
//  1. name: grouped by session, sessions whose name holds every typed word
//     come first (sessionsNamedFor; off with Ranking.NameTierOff). A name
//     match is the strongest evidence of the session meant, and bm25 cannot
//     see it: a session with nothing indexed but its name, or one mentioned
//     hundreds of times elsewhere, would otherwise lose to volume.
//  2. rank: the weighted bm25 score (for a grouped session, its best document
//     plus a quarter of the rest; domain.DefaultSessionSearchTailWeight).
//  3. recency, then id, so equal scores keep a stable order while typing.
//
// coverage_tier and kind_tier are always 0 except in the benchmark's ablation
// variants (all-terms-first and the old hard kind tier), which is why they are
// still in the ORDER BY: the benchmark measures the alternatives through this
// exact query rather than a copy.
//
// Collapsing: when grouped, each session contributes its single best document
// (ROW_NUMBER over the session partition, using the same order), and carries
// how many documents in it matched. Elasticsearch calls this field collapsing;
// without it one long session can fill a whole page, and the palette shows only
// eight rows. The match count lets the row say "+N more in this session".
//
// Snippets are not computed by FTS5. snippet() re-runs the match per call, and
// for a prefix term that rebuilds a merged posting list each time; the page's
// rows return their text instead and domain.SessionSearchSnippet cuts it. The
// total is COUNT(*) OVER () on the collapsed rows, before LIMIT, so the page
// does not need a second full-text pass to report "12 of 47".
const searchSessionSearchDocsSQL = `
WITH candidates AS (
    -- Stage one: FTS5 alone ranks the matches by raw bm25 and keeps the best
    -- ones (sessionSearchCandidateCap; -1 keeps all). No other table is read
    -- here, so a word that matches thousands of documents costs one pass over
    -- the posting lists instead of thousands of row lookups.
    SELECT rowid AS id, bm25(session_search_fts, 8.0, 1.0, 4.0) AS raw
    FROM session_search_fts
    WHERE session_search_fts MATCH ?
    ORDER BY raw
    LIMIT ?
),
matched AS (
    SELECT d.id, d.session_id, d.created_at, d.kind, d.message_id, d.source_key,
           c.raw * CASE d.kind
               WHEN 'title' THEN ?
               WHEN 'user' THEN ?
               WHEN 'path' THEN ?
               WHEN 'assistant' THEN ?
               WHEN 'tool' THEN ?
               ELSE ?
           END AS rank,
           CASE WHEN ? = '' OR d.id IN (
               SELECT rowid FROM session_search_fts WHERE session_search_fts MATCH ?
           ) THEN 0 ELSE 1 END AS coverage_tier,
           -- The pre-bm25 kind tier survives only as a benchmark ablation.
           CASE WHEN ? = 0 THEN 0 ELSE CASE d.kind
               WHEN 'title' THEN 0 WHEN 'user' THEN 1 WHEN 'path' THEN 2 WHEN 'assistant' THEN 3 ELSE 4
           END END AS kind_tier,
           -- Sessions named for the query (a JSON array; '' when none).
           CASE WHEN ? <> '' AND d.session_id IN (SELECT value FROM json_each(?)) THEN 0 ELSE 1 END AS name_tier
    FROM candidates c
    JOIN session_search_docs d ON d.id = c.id
    WHERE 1 = 1` + sessionSearchFilterSQL + `
),
-- The row's match count ("· 7 matches") is not computed here: these are the
-- capped, filtered candidates, and the count must equal the find bar's stops.
-- SessionSearchMatchCounts counts the page's sessions exactly.
ranked AS (
    SELECT m.*,
           -- The row a grouped session is shown as is its best-matching message:
           -- a document that opens somewhere (a message, or tool output anchored
           -- to its turn's message) before one that does not, and the title last,
           -- so the snippet shows what was said and Enter lands on it. The
           -- session's place in the list is decided by its score, not by this row.
           -- The session's own transcript comes before its reviewer chat, so
           -- the row opens where its match count and find bar are. Sorted
           -- newest first, the row is the session's newest such match, so
           -- the times a person reads down the list are in order.
           ROW_NUMBER() OVER (
               PARTITION BY CASE WHEN ? = 1 THEN m.session_id ELSE CAST(m.id AS TEXT) END
               ORDER BY m.message_id = '' ASC, m.kind = 'title' ASC, m.source_key LIKE 'review:%' ASC,
                        CASE WHEN ? = 1 THEN m.created_at END DESC,
                        m.coverage_tier, m.kind_tier, m.rank, m.created_at DESC, m.id
           ) AS position_in_session,
           0 AS session_matches,
           MIN(m.rank) OVER (PARTITION BY m.session_id) AS session_best_rank,
           MIN(m.coverage_tier) OVER (PARTITION BY m.session_id) AS session_coverage_tier,
           MIN(m.kind_tier) OVER (PARTITION BY m.session_id) AS session_kind_tier,
           ROW_NUMBER() OVER (PARTITION BY m.session_id ORDER BY m.rank, m.id) AS rank_in_session,
           COUNT(*) OVER () AS candidate_count
    FROM matched m
),
tailed AS (
    -- A session's tail: its next-best matches after the best one, at most
    -- domain.SessionSearchRanking.TailDepth() of them (negative: all). An
    -- unbounded tail let a long session win on volume alone: the orchestrator,
    -- which relays every worker's messages, outranked the session named
    -- exactly what was typed.
    SELECT r.*,
           SUM(CASE WHEN r.rank_in_session > 1 AND (? < 0 OR r.rank_in_session <= ? + 1) THEN r.rank ELSE 0 END)
               OVER (PARTITION BY r.session_id) AS session_tail
    FROM ranked r
),
scored AS (
    -- One blended score per session: its best document, title or content (the
    -- title column weighs 8x in bm25, so a name match leads), plus a fraction
    -- of its tail (domain.SessionSearchRanking.SessionTailWeight).
    -- Ungrouped rows score, and tier, as themselves.
    SELECT r.*,
           CASE WHEN ? = 1 THEN r.session_best_rank + ? * r.session_tail ELSE r.rank END AS score,
           CASE WHEN ? = 1 THEN r.session_coverage_tier ELSE r.coverage_tier END AS order_coverage_tier,
           CASE WHEN ? = 1 THEN r.session_kind_tier ELSE r.kind_tier END AS order_kind_tier
    FROM tailed r
    WHERE r.position_in_session = 1
),
page AS (
    -- The page is cut before any text is read: the window total is computed
    -- over the collapsed rows (before LIMIT), and only the rows that survive the
    -- LIMIT are joined to their text below.
    SELECT r.*, COUNT(*) OVER () AS match_total,
           CASE WHEN ? = 1 THEN r.created_at END AS recency
    FROM scored r
    ORDER BY recency DESC, r.order_coverage_tier ASC, r.order_kind_tier ASC, r.name_tier ASC, r.score ASC, r.created_at DESC, r.id
    LIMIT ? OFFSET ?
)
-- A reviewer chat stays searchable after its review is archived (which is
-- what ending a session does) or switched to the terminal, because "any word
-- from any past session" includes it. It is only openable while the review is
-- live in Chat, which is when the app shows that transcript; otherwise the
-- result opens the session itself rather than a reviewer chat that cannot
-- render, so it carries neither the review id nor a message to scroll to.
SELECT p.id, d.session_id, d.project_id, d.harness, d.machine, d.kind,
       CASE WHEN d.source_key LIKE 'review:%' AND rv.id IS NULL THEN '' ELSE d.message_id END,
       d.seq,
       COALESCE(t.title, ''), d.created_at, p.score, p.session_matches,
       CASE WHEN ? = 1 THEN '' ELSE COALESCE(t.body, '') END,
       p.match_total, p.candidate_count,
       COALESCE(rv.id, '')
FROM page p
JOIN session_search_docs d ON d.id = p.id
LEFT JOIN session_search_text t ON t.id = p.id
LEFT JOIN review rv ON d.source_key = 'review:' || rv.id AND rv.is_archived = 0 AND rv.interface_mode = 'chat'
ORDER BY p.recency DESC, p.order_coverage_tier ASC, p.order_kind_tier ASC, p.name_tier ASC, p.score ASC, p.created_at DESC, p.id;`

// browseSessionSearchDocsSQL lists one row per session for the recency view.
//
// It reads the session table rather than the index: a session with no provider
// title (or one whose transcript AO cannot parse) still has a name, and the
// browse view is about finding sessions, not documents. Documents that exist
// for the session supply a preview snippet when there is one.
const browseSessionSearchDocsSQL = `
SELECT s.id, COALESCE(s.project_id, ''), s.harness, '',
       'title', '', 0,
       COALESCE(NULLIF(s.display_name, ''), s.id),
       ` + sessionCreatedMillisSQL + `,
       0.0 AS rank,
       COALESCE((SELECT dt.body FROM session_search_docs d
                 JOIN session_search_text dt ON dt.id = d.id
                 WHERE d.session_id = s.id AND dt.body <> ''
                 ORDER BY d.seq DESC LIMIT 1), '') AS snippet
FROM sessions s` + browseSessionFilterSQL + `
ORDER BY s.created_at DESC, s.id
LIMIT ? OFFSET ?;`

// sessionSearchCandidateCap is how many raw-bm25 candidates the ranking stage
// considers. Below it, results are exact. Above it, the page is built from the
// best 2,000 matches by bm25, which the kind weights (0.8x to 1.25x) cannot
// reorder past: a document outside the top 2,000 would need a raw score within
// a factor of 1.56 of the page's, and the page is 25 rows. Meilisearch bounds
// its candidate set the same way (maxTotalHits, 1,000 by default). On a
// 22,000-document archive, a word in 6,700 documents fell from about 300 ms to
// the cost of the FTS pass alone.
const sessionSearchCandidateCap = 2000

// sessionCreatedMillisSQL is a session's creation time in Unix milliseconds.
//
// The store writes times in Go's default text form ("2026-10-20 18:53:47.757699
// +0000 UTC"), which strftime cannot parse whole: it returns NULL, so every
// date filter and recency value built on it silently read as zero. The first
// 19 characters are the UTC date and time strftime understands.
const sessionCreatedMillisSQL = "CAST(COALESCE(strftime('%s', substr(s.created_at, 1, 19)), '0') AS INTEGER) * 1000"

// latestChangeMillisSQL is the same conversion for a conversation stamp's
// newest message or activity update.
const latestChangeMillisSQL = "CAST(COALESCE(strftime('%s', substr(MAX(t.at), 1, 19)), '0') AS INTEGER) * 1000"

// sessionSearchFiltered reports whether a query filters documents after the
// full-text match, which is when the candidate cap must not apply.
func sessionSearchFiltered(params domain.SessionSearchParams) bool {
	return params.ProjectID != "" || params.Harness != "" || params.Machine != "" || params.SessionID != "" ||
		params.Path != "" || params.ExcludeQuery != "" || params.Status != domain.SessionSearchStatusAny ||
		(params.From != nil && !params.From.IsZero()) || (params.To != nil && !params.To.IsZero())
}

// SearchSessionSearchDocs runs one ranked query against the index.
func (s *Store) SearchSessionSearchDocs(ctx context.Context, params domain.SessionSearchParams) ([]domain.SessionSearchHit, error) {
	limit := domain.NormalizeSessionSearchLimit(params.Limit)
	offset := max(params.Offset, 0)
	ranking := params.Ranking
	recent, grouped, tierFirst := 0, 0, 0
	if params.Sort == domain.SessionSearchSortRecent {
		recent = 1
	}
	if params.Group != domain.SessionSearchGroupMessage {
		grouped = 1
	}
	if ranking.KindTierFirst {
		tierFirst = 1
	}
	allTerms := ""
	if ranking.AllTermsTier {
		allTerms = params.AllTermsQuery
	}
	// The candidate cap is only safe when nothing after stage one can discard
	// a candidate: a filter could reject all 2,000 and hide matches that
	// exist, and a recency sort wants the newest, not the most relevant.
	capped := sessionSearchCandidateCap
	if params.Sort == domain.SessionSearchSortRecent || sessionSearchFiltered(params) {
		capped = -1
	}
	// The name tier orders sessions by relevance; a recency sort or a list of
	// single messages has no use for it.
	named := ""
	if grouped == 1 && recent == 0 && !ranking.NameTierOff {
		var err error
		if named, err = s.sessionsNamedFor(ctx, params.HighlightTerms); err != nil {
			return nil, err
		}
	}
	args := make([]any, 0, 40)
	args = append(args, params.MatchQuery, capped,
		ranking.Weight(domain.SessionSearchKindTitle),
		ranking.Weight(domain.SessionSearchKindUser),
		ranking.Weight(domain.SessionSearchKindPath),
		ranking.Weight(domain.SessionSearchKindAssistant),
		ranking.Weight(domain.SessionSearchKindTool),
		ranking.Weight(domain.SessionSearchKindCommand),
		allTerms, allTerms,
		tierFirst,
		named, named,
	)
	args = append(args, sessionSearchFilterArgs(params)...)
	skipSnippets := 0
	if params.SkipSnippets {
		skipSnippets = 1
	}
	depth := ranking.TailDepth()
	args = append(args, grouped, recent, depth, depth, grouped, ranking.TailWeight(), grouped, grouped, recent, limit, offset, skipSnippets)
	rows, err := s.readDB.QueryContext(ctx, searchSessionSearchDocsSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("search session index: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hits := make([]domain.SessionSearchHit, 0, limit)
	for rows.Next() {
		var hit domain.SessionSearchHit
		var docID, createdAt, candidates int64
		var kind, body string
		if err := rows.Scan(
			&docID, &hit.SessionID, &hit.ProjectID, &hit.Harness, &hit.Machine, &kind, &hit.MessageID, &hit.Seq,
			&hit.Title, &createdAt, &hit.Rank, &hit.SessionMatches, &body, &hit.MatchTotal, &candidates,
			&hit.ReviewID,
		); err != nil {
			return nil, fmt.Errorf("scan session search hit: %w", err)
		}
		hit.Kind = domain.SessionSearchKind(kind)
		hit.CreatedAt = time.UnixMilli(createdAt).UTC()
		hit.MatchTotalCapped = capped > 0 && candidates >= int64(capped)
		if !params.SkipSnippets {
			text := body
			if hit.Kind == domain.SessionSearchKindTitle && hit.Title != "" {
				text = hit.Title
			}
			hit.Snippet = domain.SessionSearchSnippet(text, params.HighlightTerms)
		}
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session search hits: %w", err)
	}
	return hits, nil
}

// CountSessionSearchDocsMatching returns how many results a query has, ignoring
// limit and offset: matching sessions when grouped, matching documents when not.
//
// A page length is not a total: Orca shows "12 shown · 47 recent" and offers
// "Show more", and neither is possible if the daemon reports the page size as
// the count. The count runs the same WHERE clause as the search with the LIMIT
// removed, so the two can never disagree about what matched.
func (s *Store) CountSessionSearchDocsMatching(ctx context.Context, params domain.SessionSearchParams) (int64, error) {
	counted := "COUNT(DISTINCT d.session_id)"
	if params.Group == domain.SessionSearchGroupMessage {
		counted = "COUNT(*)"
	}
	args := append([]any{params.MatchQuery}, sessionSearchFilterArgs(params)...)
	var count int64
	err := s.readDB.QueryRowContext(ctx, `
SELECT `+counted+`
FROM session_search_fts
JOIN session_search_docs d ON d.id = session_search_fts.rowid
WHERE session_search_fts MATCH ?`+sessionSearchFilterSQL, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count session search matches: %w", err)
	}
	return count, nil
}

// CountSessionSearchSessionsBrowsing returns how many sessions a filter-only
// browse would list.
func (s *Store) CountSessionSearchSessionsBrowsing(ctx context.Context, params domain.SessionSearchParams) (int64, error) {
	var count int64
	err := s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions s`+browseSessionFilterSQL,
		browseSessionFilterArgs(params)...,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count session search browse rows: %w", err)
	}
	return count, nil
}

// BrowseSessionSearchDocs lists documents without a text query, for the
// recency view: opening search with only filters should show recent sessions
// rather than nothing.
func (s *Store) BrowseSessionSearchDocs(ctx context.Context, params domain.SessionSearchParams) ([]domain.SessionSearchHit, error) {
	limit := domain.NormalizeSessionSearchLimit(params.Limit)
	offset := max(params.Offset, 0)
	args := append(browseSessionFilterArgs(params), limit, offset)
	rows, err := s.readDB.QueryContext(ctx, browseSessionSearchDocsSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("browse session index: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hits := make([]domain.SessionSearchHit, 0, limit)
	for rows.Next() {
		var hit domain.SessionSearchHit
		var createdAt int64
		var kind string
		if err := rows.Scan(
			&hit.SessionID, &hit.ProjectID, &hit.Harness, &hit.Machine, &kind, &hit.MessageID, &hit.Seq,
			&hit.Title, &createdAt, &hit.Rank, &hit.Snippet,
		); err != nil {
			return nil, fmt.Errorf("scan session search browse row: %w", err)
		}
		hit.Kind = domain.SessionSearchKind(kind)
		hit.CreatedAt = time.UnixMilli(createdAt).UTC()
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session search browse rows: %w", err)
	}
	return hits, nil
}

// SessionSearchMatchesForSession lists every indexed match for one session and
// one expression, ordered by position. Enter/Shift+Enter stepping uses it.
func (s *Store) SessionSearchMatchesForSession(ctx context.Context, sessionID, matchQuery string) ([]domain.SessionSearchHit, error) {
	if sessionID == "" || matchQuery == "" {
		return nil, nil
	}
	rows, err := s.readDB.QueryContext(ctx, `
SELECT d.session_id, d.project_id, d.harness, d.machine, d.kind, d.message_id, d.seq,
       session_search_fts.title, d.created_at,
       bm25(session_search_fts, 8.0, 1.0, 4.0) AS rank,
       snippet(session_search_fts, -1, ?, ?, '…', 12) AS snippet
FROM session_search_fts
JOIN session_search_docs d ON d.id = session_search_fts.rowid
WHERE session_search_fts MATCH ?
  AND d.session_id = ?
  -- Stepping walks the session's own transcript; a reviewer chat's messages
  -- are not in it and could not be scrolled to. The title is the session's
  -- name, not a place in the transcript.
  AND d.source_key NOT LIKE 'review:%'
  AND d.kind <> 'title'
ORDER BY d.seq, d.id`,
		sessionSearchSnippetStart, sessionSearchSnippetEnd,
		matchQuery, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list session search matches: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var hits []domain.SessionSearchHit
	for rows.Next() {
		var hit domain.SessionSearchHit
		var createdAt int64
		var kind string
		if err := rows.Scan(
			&hit.SessionID, &hit.ProjectID, &hit.Harness, &hit.Machine, &kind, &hit.MessageID, &hit.Seq,
			&hit.Title, &createdAt, &hit.Rank, &hit.Snippet,
		); err != nil {
			return nil, fmt.Errorf("scan session search match: %w", err)
		}
		hit.Kind = domain.SessionSearchKind(kind)
		hit.CreatedAt = time.UnixMilli(createdAt).UTC()
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session search matches: %w", err)
	}
	return hits, nil
}

// SessionSearchMatchCounts counts, for each session, the places its find bar
// steps through for a query: matches in the session's own transcript (not its
// reviewer chat, not its title), with a message and the tool output shown
// with it counted once. It is the "· 7 matches" on a result row, so the two
// cannot disagree; the ranking query cannot supply it because it only sees the
// best 2,000 candidates and the query's filters.
func (s *Store) SessionSearchMatchCounts(ctx context.Context, matchQuery string, sessionIDs []string) (map[string]int, error) {
	counts := make(map[string]int, len(sessionIDs))
	if matchQuery == "" || len(sessionIDs) == 0 {
		return counts, nil
	}
	ids, err := json.Marshal(sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("encode session ids: %w", err)
	}
	rows, err := s.readDB.QueryContext(ctx, `
SELECT d.session_id, COUNT(DISTINCT COALESCE(NULLIF(d.message_id, ''), 'doc:' || d.id))
FROM session_search_docs d
WHERE d.session_id IN (SELECT value FROM json_each(?))
  AND d.kind <> 'title'
  AND d.source_key NOT LIKE 'review:%'
  AND d.id IN (SELECT rowid FROM session_search_fts WHERE session_search_fts MATCH ?)
GROUP BY d.session_id`, string(ids), matchQuery)
	if err != nil {
		return nil, fmt.Errorf("count session search matches: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, fmt.Errorf("scan session search match count: %w", err)
		}
		counts[id] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session search match counts: %w", err)
	}
	return counts, nil
}

// CountSessionSearchDocs reports how many documents the index holds.
func (s *Store) CountSessionSearchDocs(ctx context.Context) (int64, error) {
	var count int64
	if err := s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_search_docs`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count session search docs: %w", err)
	}
	return count, nil
}

const upsertSessionSearchDocSQL = `
INSERT INTO session_search_docs (
    source_key, doc_key, session_id, project_id, harness, machine, kind, message_id, seq,
    created_at, indexed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source_key, doc_key, kind) DO UPDATE SET
    session_id = excluded.session_id,
    project_id = excluded.project_id,
    harness = excluded.harness,
    machine = excluded.machine,
    message_id = excluded.message_id,
    seq = excluded.seq,
    created_at = excluded.created_at,
    indexed_at = excluded.indexed_at
RETURNING id;`

// upsertSessionSearchTextSQL writes a document's text only when it changed.
// Every pass rewrites the documents of a source it re-reads, and most of them
// are unchanged; without the guard each one would cost an FTS delete and
// re-insert (the update trigger), which is the bulk of an incremental pass.
const upsertSessionSearchTextSQL = `
INSERT INTO session_search_text (id, title, body, paths) VALUES (?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    title = excluded.title,
    body = excluded.body,
    paths = excluded.paths
WHERE title IS NOT excluded.title OR body IS NOT excluded.body OR paths IS NOT excluded.paths;`

// UpsertSessionSearchDocs writes one batch in a single transaction, so the FTS
// mirror and the projection advance together and a batch costs one commit.
func (s *Store) UpsertSessionSearchDocs(ctx context.Context, docs []domain.SessionSearchDoc) error {
	if len(docs) == 0 {
		return nil
	}
	return s.inTxDB(ctx, "upsert session search docs", func(_ *gen.Queries, tx gen.DBTX) error {
		executor := tx
		if executor == nil {
			executor = s.writeDB
		}
		docStmt, err := executor.PrepareContext(ctx, upsertSessionSearchDocSQL)
		if err != nil {
			return fmt.Errorf("prepare session search upsert: %w", err)
		}
		defer func() { _ = docStmt.Close() }()
		textStmt, err := executor.PrepareContext(ctx, upsertSessionSearchTextSQL)
		if err != nil {
			return fmt.Errorf("prepare session search text upsert: %w", err)
		}
		defer func() { _ = textStmt.Close() }()
		for _, doc := range docs {
			var id int64
			if err := docStmt.QueryRowContext(ctx,
				doc.SourceKey, doc.DocKey, doc.SessionID, doc.ProjectID, doc.Harness,
				doc.Machine, string(doc.Kind), doc.MessageID, doc.Seq,
				doc.CreatedAt.UnixMilli(), doc.IndexedAt.UnixMilli(),
			).Scan(&id); err != nil {
				return fmt.Errorf("upsert session search doc %s/%s: %w", doc.SourceKey, doc.DocKey, err)
			}
			if _, err := textStmt.ExecContext(ctx, id, doc.Title, doc.Body, doc.Paths); err != nil {
				return fmt.Errorf("upsert session search text %s/%s: %w", doc.SourceKey, doc.DocKey, err)
			}
		}
		return nil
	})
}

// PruneSessionSearchDocsBefore removes documents for one source that were not
// rewritten by the current pass. A pass stamps every document it writes with its
// own timestamp, so "older than this pass" is exactly "no longer visible". It is
// what makes an edit, a rollback, or a deleted message disappear from results
// without needing a diff of every document key.
func (s *Store) PruneSessionSearchDocsBefore(ctx context.Context, sourceKey string, indexedAt time.Time) error {
	if _, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM session_search_docs WHERE source_key = ? AND indexed_at < ?`, sourceKey, indexedAt.UnixMilli(),
	); err != nil {
		return fmt.Errorf("prune session search docs: %w", err)
	}
	return nil
}

// DeleteSessionSearchDocsForSource clears one source before a full re-index.
func (s *Store) DeleteSessionSearchDocsForSource(ctx context.Context, sourceKey string) error {
	if _, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM session_search_docs WHERE source_key = ?`, sourceKey,
	); err != nil {
		return fmt.Errorf("delete session search source docs: %w", err)
	}
	return nil
}

// CountSessionSearchDocsForSource reports how many documents a source holds.
func (s *Store) CountSessionSearchDocsForSource(ctx context.Context, sourceKey string) (int64, error) {
	var count int64
	if err := s.readDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_search_docs WHERE source_key = ?`, sourceKey,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count session search docs: %w", err)
	}
	return count, nil
}

const upsertSessionSearchSourceSQL = `
INSERT INTO session_search_sources (
    source_key, kind, session_id, project_id, harness, path, cursor_value,
    file_size, file_mtime, fingerprint, stamp, doc_count, state, last_error_code, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source_key) DO UPDATE SET
    kind = excluded.kind,
    session_id = excluded.session_id,
    project_id = excluded.project_id,
    harness = excluded.harness,
    path = excluded.path,
    cursor_value = excluded.cursor_value,
    file_size = excluded.file_size,
    file_mtime = excluded.file_mtime,
    fingerprint = excluded.fingerprint,
    stamp = excluded.stamp,
    doc_count = excluded.doc_count,
    state = excluded.state,
    last_error_code = excluded.last_error_code,
    updated_at = excluded.updated_at;`

// UpsertSessionSearchSource records a source's cursor and health.
func (s *Store) UpsertSessionSearchSource(ctx context.Context, source domain.SessionSearchSource) error {
	if _, err := s.writeDB.ExecContext(ctx, upsertSessionSearchSourceSQL,
		source.SourceKey, string(source.Kind), source.SessionID, source.ProjectID, source.Harness,
		source.Path, source.CursorValue, source.FileSize, source.FileMtime, source.Fingerprint, source.Stamp, source.DocCount,
		string(source.State), source.LastErrorCode, source.UpdatedAt.UnixMilli(),
	); err != nil {
		return fmt.Errorf("upsert session search source %s: %w", source.SourceKey, err)
	}
	return nil
}

// GetSessionSearchSource reads one source's cursor; ok is false before the
// indexer has seen it.
func (s *Store) GetSessionSearchSource(ctx context.Context, sourceKey string) (domain.SessionSearchSource, bool, error) {
	var source domain.SessionSearchSource
	var kind, state string
	var updatedAt int64
	err := s.readDB.QueryRowContext(ctx, `
SELECT source_key, kind, session_id, project_id, harness, path, cursor_value,
       file_size, file_mtime, fingerprint, stamp, doc_count, state, last_error_code, updated_at
FROM session_search_sources WHERE source_key = ?`, sourceKey,
	).Scan(
		&source.SourceKey, &kind, &source.SessionID, &source.ProjectID, &source.Harness,
		&source.Path, &source.CursorValue, &source.FileSize, &source.FileMtime, &source.Fingerprint, &source.Stamp, &source.DocCount,
		&state, &source.LastErrorCode, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionSearchSource{}, false, nil
	}
	if err != nil {
		return domain.SessionSearchSource{}, false, fmt.Errorf("get session search source: %w", err)
	}
	source.Kind = domain.SessionSearchSourceKind(kind)
	source.State = domain.SessionSearchSourceState(state)
	source.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return source, true, nil
}

// CountSessionSearchSourcesByState feeds the coverage note, so a search can say
// which sources it cannot read instead of silently returning fewer results.
func (s *Store) CountSessionSearchSourcesByState(ctx context.Context) (map[domain.SessionSearchSourceState]int64, error) {
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT state, COUNT(*) FROM session_search_sources GROUP BY state`)
	if err != nil {
		return nil, fmt.Errorf("count session search sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	counts := map[domain.SessionSearchSourceState]int64{}
	for rows.Next() {
		var state string
		var count int64
		if err := rows.Scan(&state, &count); err != nil {
			return nil, fmt.Errorf("scan session search source state: %w", err)
		}
		counts[domain.SessionSearchSourceState(state)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session search source states: %w", err)
	}
	return counts, nil
}

// ListSessionSearchTargets returns every session the indexer covers. It reads
// only durable session facts and includes terminated sessions on purpose: they
// are the most valuable search targets. A standalone session has no project
// (project_id is NULL), and one NULL scanned into a string fails the whole
// query, so every project read here and in the search queries is coalesced.
func (s *Store) ListSessionSearchTargets(ctx context.Context) ([]domain.SessionSearchTarget, error) {
	rows, err := s.readDB.QueryContext(ctx, `
SELECT id, COALESCE(project_id, ''), harness, kind, display_name, prompt, latest_user_prompt,
       workspace_path, branch, session_mode, is_terminated, hibernated_at IS NOT NULL, created_at
FROM sessions
ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list session search targets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var targets []domain.SessionSearchTarget
	for rows.Next() {
		var target domain.SessionSearchTarget
		var harness, mode string
		var createdAt time.Time
		if err := rows.Scan(
			&target.ID, &target.ProjectID, &harness, &target.Kind, &target.DisplayName,
			&target.Prompt, &target.LatestPrompt, &target.WorkspacePath, &target.Branch, &mode,
			&target.IsTerminated, &target.Hibernated, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan session search target: %w", err)
		}
		target.Harness = domain.AgentHarness(harness)
		target.SessionMode = domain.SessionMode(mode)
		target.CreatedAt = createdAt
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session search targets: %w", err)
	}
	return targets, nil
}

// ListSessionSearchConversations returns the Chat conversations to index.
func (s *Store) ListSessionSearchConversations(ctx context.Context) ([]domain.SessionSearchConversation, error) {
	rows, err := s.readDB.QueryContext(ctx, `
SELECT c.id, COALESCE(c.current_session_id, r.session_id), COALESCE(s.project_id, c.project_id, ''),
       c.provider_title, c.active_branch_id, c.latest_sequence,
       COALESCE(s.display_name, ''), COALESCE(s.harness, ''), COALESCE(c.current_review_id, ''),
       COALESCE(s.branch, ''), s.created_at, c.created_at
FROM conversations c
LEFT JOIN review r ON r.id = c.current_review_id
LEFT JOIN sessions s ON s.id = COALESCE(c.current_session_id, r.session_id)
WHERE
    -- A session's own transcript: the same conversation the palette's chat
    -- search treats as the session's (project scope first, then newest), so a
    -- session with two conversations cannot index both into one source.
    (c.current_session_id IS NOT NULL AND c.id = (
        SELECT current.id FROM conversations current
        WHERE current.current_session_id = c.current_session_id
        ORDER BY current.scope = 'project' DESC, current.created_at DESC, current.id DESC
        LIMIT 1
    ))
    -- A reviewer chat belongs to the session it reviews. Whether it is shown
    -- (archived, or switched to the terminal interface) is decided at query
    -- time, so a review that changes later never needs re-indexing.
    OR (c.current_review_id IS NOT NULL AND r.session_id IS NOT NULL)
ORDER BY c.created_at, c.id`)
	if err != nil {
		return nil, fmt.Errorf("list session search conversations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var conversations []domain.SessionSearchConversation
	for rows.Next() {
		var conversation domain.SessionSearchConversation
		var harness string
		// Two columns, not COALESCE: the driver only scans a column declared
		// as a timestamp into time.Time, and COALESCE loses the declaration.
		var sessionCreated, conversationCreated sql.NullTime
		if err := rows.Scan(
			&conversation.ID, &conversation.SessionID, &conversation.ProjectID,
			&conversation.ProviderTitle, &conversation.ActiveBranchID, &conversation.LatestSequence,
			&conversation.DisplayName, &harness, &conversation.ReviewID, &conversation.Branch,
			&sessionCreated, &conversationCreated,
		); err != nil {
			return nil, fmt.Errorf("scan session search conversation: %w", err)
		}
		conversation.Harness = domain.AgentHarness(harness)
		conversation.CreatedAt = conversationCreated.Time
		if sessionCreated.Valid {
			conversation.CreatedAt = sessionCreated.Time
		}
		conversations = append(conversations, conversation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session search conversations: %w", err)
	}
	return conversations, nil
}

// SessionSearchVisibleMessages returns the messages a reader can actually see:
// the active branch path, minus discarded turns. It reuses
// SelectConversationMessages rather than reading conversation_messages raw, so a
// rolled-back or cancelled turn can never become searchable.
func (s *Store) SessionSearchVisibleMessages(ctx context.Context, conversationID string) ([]domain.SessionSearchMessage, error) {
	messages, err := s.ConversationMessages(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	visible := make([]domain.SessionSearchMessage, 0, len(messages))
	for _, message := range messages {
		visible = append(visible, domain.SessionSearchMessage{
			ID:        message.ID,
			Sequence:  message.Sequence,
			Role:      message.Role,
			Text:      message.Text,
			CreatedAt: message.CreatedAt,
		})
	}
	return visible, nil
}

// SessionSearchActivities returns the searchable slice of a conversation's
// non-prose timeline: commands with their output, tool calls, file changes,
// plans, and reasoning summaries. This is the text every competitor skips.
func (s *Store) SessionSearchActivities(ctx context.Context, conversationID string) ([]domain.SessionSearchActivity, error) {
	rows, err := s.readDB.QueryContext(ctx, `
SELECT a.id, a.sequence, a.kind, a.status, a.summary, a.detail_json, a.command_output, a.streamed_text,
       a.created_at, a.updated_at,
       COALESCE(
           (SELECT m.id FROM conversation_messages m
            WHERE m.conversation_id = a.conversation_id AND m.turn_id = a.turn_id AND m.sequence <= a.sequence
            ORDER BY m.sequence DESC LIMIT 1),
           (SELECT m.id FROM conversation_messages m
            WHERE m.conversation_id = a.conversation_id AND m.turn_id = a.turn_id
            ORDER BY m.sequence LIMIT 1),
           '')
FROM conversation_activities a
WHERE a.conversation_id = ?
ORDER BY a.sequence`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list session search activities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var activities []domain.SessionSearchActivity
	for rows.Next() {
		var activity domain.SessionSearchActivity
		var kind, status string
		if err := rows.Scan(
			&activity.ID, &activity.Sequence, &kind, &status, &activity.Summary,
			&activity.Detail, &activity.Output, &activity.Streamed,
			&activity.CreatedAt, &activity.UpdatedAt, &activity.AnchorMessageID,
		); err != nil {
			return nil, fmt.Errorf("scan session search activity: %w", err)
		}
		activity.Kind = domain.ActivityKind(kind)
		activity.Status = domain.ActivityStatus(status)
		activities = append(activities, activity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session search activities: %w", err)
	}
	return activities, nil
}

// SessionSearchTouchedPaths returns the file paths a conversation's turns
// touched. detail_json holds {"files":[{"path":…}]} for a file_change activity.
func (s *Store) SessionSearchTouchedPaths(ctx context.Context, conversationID string) ([]string, error) {
	rows, err := s.readDB.QueryContext(ctx, `
SELECT detail_json FROM conversation_activities
WHERE conversation_id = ? AND kind = 'file_change'
ORDER BY sequence`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list session search touched paths: %w", err)
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]struct{}{}
	var paths []string
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			return nil, fmt.Errorf("scan session search activity detail: %w", err)
		}
		for _, path := range pathsFromFileChangeDetail(detail) {
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session search activities: %w", err)
	}
	return paths, nil
}

// pathsFromFileChangeDetail extracts paths without decoding unrelated provider
// payload: the detail is provider-shaped and only the path list is contractual.
func pathsFromFileChangeDetail(detail string) []string {
	const key = `"path"`
	var paths []string
	rest := detail
	for {
		index := strings.Index(rest, key)
		if index < 0 {
			return paths
		}
		rest = rest[index+len(key):]
		colon := strings.IndexByte(rest, ':')
		if colon < 0 {
			return paths
		}
		rest = strings.TrimLeft(rest[colon+1:], " \t")
		if rest == "" || rest[0] != '"' {
			continue
		}
		rest = rest[1:]
		var value strings.Builder
		escaped := false
		closed := false
		for i := 0; i < len(rest); i++ {
			character := rest[i]
			if escaped {
				switch character {
				case 'n':
					value.WriteByte('\n')
				case 't':
					value.WriteByte('\t')
				default:
					value.WriteByte(character)
				}
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				rest = rest[i+1:]
				closed = true
				break
			}
			value.WriteByte(character)
		}
		if !closed {
			return paths
		}
		if path := strings.TrimSpace(value.String()); path != "" {
			paths = append(paths, path)
		}
	}
}

// SessionSearchConversationStamps returns one cheap aggregate per conversation:
// row counts, the highest sequence in each timeline table, and the newest row
// time.
//
// The sweep uses this to decide whether a conversation needs reading at all. It
// is a single grouped query over two indexed tables rather than a per-conversation
// read of every message and activity, which is what keeps an idle sweep cheap as
// the archive grows.
func (s *Store) SessionSearchConversationStamps(ctx context.Context) (map[string]domain.SessionSearchConversationStamp, error) {
	stamps := map[string]domain.SessionSearchConversationStamp{}
	rows, err := s.readDB.QueryContext(ctx, `
SELECT c.id,
       (SELECT COUNT(*) FROM conversation_messages m WHERE m.conversation_id = c.id),
       COALESCE((SELECT MAX(m.sequence) FROM conversation_messages m WHERE m.conversation_id = c.id), 0),
       (SELECT COUNT(*) FROM conversation_activities a WHERE a.conversation_id = c.id),
       COALESCE((SELECT MAX(a.sequence) FROM conversation_activities a WHERE a.conversation_id = c.id), 0),
       COALESCE((
           SELECT `+latestChangeMillisSQL+`
           FROM (
               SELECT MAX(m.updated_at) AS at FROM conversation_messages m WHERE m.conversation_id = c.id
               UNION ALL
               SELECT MAX(a.updated_at) AS at FROM conversation_activities a WHERE a.conversation_id = c.id
           ) AS t
       ), 0)
FROM conversations c
WHERE c.current_session_id IS NOT NULL OR c.current_review_id IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("read session search conversation stamps: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var stamp domain.SessionSearchConversationStamp
		if err := rows.Scan(
			&stamp.ConversationID, &stamp.MessageCount, &stamp.MessageSeqMax,
			&stamp.ActivityCount, &stamp.ActivitySeqMax, &stamp.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan session search conversation stamp: %w", err)
		}
		stamps[stamp.ConversationID] = stamp
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session search conversation stamps: %w", err)
	}
	return stamps, nil
}
