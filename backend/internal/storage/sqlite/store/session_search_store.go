package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/searchtext"
)

// This file is the persistence half of session search. It writes only the three
// session_search_* tables, which this feature owns; the session data it reads
// (sessions, conversations, activities) is never written here. Reads go through
// the reader pool, writes through the single writer connection.

// sessionSearchFilterSQL is the WHERE tail shared by the ranked search and its
// count, so the two can never disagree about what matched. It expects the
// documents table aliased as d; sessionSearchFilterArgs supplies its arguments.
const sessionSearchFilterSQL = `
  -- A deleted session's documents are removed after it, in batches; until
  -- then they are not results.
  AND d.session_id NOT IN (SELECT session_id FROM session_search_deleted)
  AND (? = '' OR d.project_id = ?)
  AND (? = '' OR d.harness = ?)
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
// session, path:, !term) listed every session instead.
const browseSessionFilterSQL = `
WHERE (? = '' OR s.project_id = ?)
  AND (? = '' OR s.harness = ?)
  AND (? = '' OR s.id = ?)
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
		pathMatch, pathMatch,
		params.ExcludeQuery, params.ExcludeQuery,
		from, from, to, to,
		status, status, status, status,
	}
}

// searchSessionSearchDocsSQL ranks candidates and returns one page.
//
// Scoring is bm25 throughout. FTS5's bm25 weights the title column above paths
// above body, and the document's kind multiplies the score
// (domain.SessionSearchKindWeights). FTS5 scores are negative, better is more
// negative, so a weight above 1 promotes and ascending order is best-first.
//
// Order, most significant first:
//
//  1. name: grouped by session, sessions whose name holds every typed word
//     come first (sessionsNamedFor). bm25 cannot see a name match: a session
//     with nothing indexed but its name, or one mentioned hundreds of times
//     elsewhere, would otherwise lose to volume.
//  2. exact: candidates that hold every ranked word as typed come before
//     those that match only a widened form (the stem's prefix, or a longer
//     word), which bm25 cannot tell apart. For a grouped session, any one
//     exact document lifts the session and is the row it is shown as.
//  3. rank: the weighted bm25 score (for a grouped session, its best document
//     plus a quarter of the rest; domain.SessionSearchTailWeight).
//  4. recency, then id, so equal scores keep a stable order while typing.
//
// When grouped, each session contributes its single best document, so one
// long session cannot fill a page. Snippets are cut from the page's rows by
// domain.SessionSearchSnippet, and the total is COUNT(*) OVER () on the
// collapsed rows before LIMIT, so neither needs a second full-text pass.
const searchSessionSearchDocsSQL = `
WITH candidates AS (
    -- Stage one: FTS5 alone ranks the matches by raw bm25 and keeps the best
    -- ones (sessionSearchCandidateCap; -1 keeps all). No document row is read
    -- per match, so a word that matches thousands of documents costs one pass
    -- over the posting lists instead of thousands of row lookups.
    --
    -- The documents of deleted sessions are left out here, before the cap,
    -- not only by the shared filter below: they wait for the sweep, and enough
    -- of them (one long deleted session) would take every place the cap has
    -- and leave a match that is a result outside it. Their ids are listed once
    -- per query, not per match. CROSS JOIN fixes the order: from the deleted
    -- sessions, usually none, to their documents by index. Left to choose,
    -- SQLite scanned every document's index entry on every search.
    SELECT rowid AS id, bm25(session_search_fts, 8.0, 1.0, 4.0) AS raw
    FROM session_search_fts
    WHERE session_search_fts MATCH ?
      AND rowid NOT IN (
          SELECT gone.id FROM session_search_deleted removed
          CROSS JOIN session_search_docs gone ON gone.session_id = removed.session_id
      )
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
           -- Sessions named for the query (a JSON array; '' when none).
           CASE WHEN ? <> '' AND d.session_id IN (SELECT value FROM json_each(?)) THEN 0 ELSE 1 END AS name_tier,
           -- Holds every ranked word as typed ('' when nothing was widened).
           CASE WHEN ? = '' OR d.id IN (
               SELECT rowid FROM session_search_fts WHERE session_search_fts MATCH ?
           ) THEN 0 ELSE 1 END AS exact_tier
    FROM candidates c
    JOIN session_search_docs d ON d.id = c.id
    WHERE 1 = 1` + sessionSearchFilterSQL + `
      -- Paging past the candidate cap leaves out what the capped part returned.
      AND (? = '' OR d.session_id NOT IN (SELECT value FROM json_each(?)))
      AND (? = '' OR d.id NOT IN (SELECT value FROM json_each(?)))
),
-- The row's match count ("· 7 matches") is not computed here: these are the
-- capped, filtered candidates, and the count must equal find in chat's stops.
-- SessionSearchMatchCounts counts the page's sessions exactly.
ranked AS (
    SELECT m.*,
           -- The row a grouped session is shown as is its best-matching message:
           -- a document that opens somewhere (a message, or tool output anchored
           -- to its turn's message) before one that does not, and the title last,
           -- so the snippet shows what was said and Enter lands on it. The
           -- session's place in the list is decided by its score, not by this row.
           -- The session's own transcript comes before its reviewer chat, so
           -- the row opens where its match count and find in chat are. Sorted
           -- newest first, the row is the session's newest such match, so
           -- the times a person reads down the list are in order.
           ROW_NUMBER() OVER (
               PARTITION BY CASE WHEN ? = 1 THEN m.session_id ELSE CAST(m.id AS TEXT) END
               ORDER BY m.message_id = '' ASC, m.kind = 'title' ASC, m.source_key LIKE 'review:%' ASC,
                        CASE WHEN ? = 1 THEN m.created_at END DESC,
                        m.exact_tier, m.rank, m.created_at DESC, m.id
           ) AS position_in_session,
           0 AS session_matches,
           MIN(m.rank) OVER (PARTITION BY m.session_id) AS session_best_rank,
           MIN(m.exact_tier) OVER (PARTITION BY m.session_id) AS session_exact_tier,
           ROW_NUMBER() OVER (PARTITION BY m.session_id ORDER BY m.rank, m.id) AS rank_in_session,
           COUNT(*) OVER () AS candidate_count
    FROM matched m
),
tailed AS (
    -- A session's tail: its next-best matches after the best one, at most
    -- domain.SessionSearchTailDepth of them. An unbounded tail lets a long
    -- session win on volume alone: the orchestrator, which relays every
    -- worker's messages, outranked the session named exactly what was typed.
    SELECT r.*,
           SUM(CASE WHEN r.rank_in_session > 1 AND r.rank_in_session <= ? + 1 THEN r.rank ELSE 0 END)
               OVER (PARTITION BY r.session_id) AS session_tail
    FROM ranked r
),
scored AS (
    -- One blended score per session: its best document, title or content (the
    -- title column weighs 8x in bm25, so a name match leads), plus a fraction
    -- of its tail (domain.SessionSearchTailWeight).
    -- Ungrouped rows score, and tier, as themselves.
    SELECT r.*,
           CASE WHEN ? = 1 THEN r.session_best_rank + ? * r.session_tail ELSE r.rank END AS score,
           CASE WHEN ? = 1 THEN r.session_exact_tier ELSE r.exact_tier END AS order_exact_tier
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
    ORDER BY recency DESC, r.name_tier ASC, r.order_exact_tier ASC, r.score ASC, r.created_at DESC, r.id
    LIMIT ? OFFSET ?
)
-- A reviewer chat stays searchable after its review is archived (which is
-- what ending a session does) or switched to the terminal, because "any word
-- from any past session" includes it. It is only openable while the review is
-- live in Chat, which is when the app shows that transcript; otherwise the
-- result opens the session itself rather than a reviewer chat that cannot
-- render, so it carries neither the review id nor a message to scroll to.
SELECT p.id, d.session_id, d.project_id, d.harness, d.kind,
       CASE WHEN d.source_key LIKE 'review:%' AND rv.id IS NULL THEN '' ELSE d.message_id END,
       d.seq,
       COALESCE(t.title, ''), d.created_at, p.score, p.session_matches,
       CASE WHEN ? = 1 THEN NULL ELSE t.body END,
       p.match_total, p.candidate_count,
       COALESCE(rv.id, '')
FROM page p
JOIN session_search_docs d ON d.id = p.id
LEFT JOIN session_search_text t ON t.id = p.id
LEFT JOIN review rv ON d.source_key = 'review:' || rv.id AND rv.is_archived = 0 AND rv.interface_mode = 'chat'
ORDER BY p.recency DESC, p.name_tier ASC, p.order_exact_tier ASC, p.score ASC, p.created_at DESC, p.id;`

// browseSessionSearchDocsSQL lists one row per session for the recency view.
//
// It reads the session table rather than the index: a session with no provider
// title (or one whose transcript AO cannot parse) still has a name, and the
// browse view is about finding sessions, not documents. Documents that exist
// for the session supply a preview snippet when there is one.
const browseSessionSearchDocsSQL = `
SELECT s.id, COALESCE(s.project_id, ''), s.harness,
       'title', '', 0,
       COALESCE(NULLIF(s.display_name, ''), s.id),
       ` + sessionCreatedMillisSQL + `,
       0.0 AS rank,
       (SELECT dt.body FROM session_search_docs d
        JOIN session_search_text dt ON dt.id = d.id
        WHERE d.session_id = s.id AND length(dt.body) > 1
        ORDER BY d.seq DESC LIMIT 1) AS snippet
FROM sessions s` + browseSessionFilterSQL + `
ORDER BY s.created_at DESC, s.id
LIMIT ? OFFSET ?;`

// sessionSearchCandidateCap is how many raw-bm25 candidates the ranking stage
// considers. Below it, results are exact. Above it, the page is built from the
// best 2,000 matches by bm25, which the kind weights (0.8x to 1.25x) cannot
// reorder past: a document outside the top 2,000 would need a raw score within
// a factor of 1.56 of the page's, and the page is 25 rows. With the cap, a
// word that matches thousands of documents costs the full-text pass alone
// instead of a row lookup per match.
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
	return params.ProjectID != "" || params.Harness != "" || params.SessionID != "" ||
		params.Path != "" || params.ExcludeQuery != "" || params.Status != domain.SessionSearchStatusAny ||
		(params.From != nil && !params.From.IsZero()) || (params.To != nil && !params.To.IsZero())
}

// SearchSessionSearchDocs ranks candidates and returns one page.
//
// Stage one keeps the best sessionSearchCandidateCap documents by raw bm25,
// which keeps a broad query fast. The cap is only safe when nothing after it
// can discard a candidate, so a filtered query and a recency sort run
// uncapped, and a deleted session's documents are left out inside stage one.
//
// The total counts every match, so every page it advertises must be fetchable
// and each result must appear exactly once. Every page therefore comes from
// one ranked list: the capped candidates in their final order, then everything
// the cap left out in the same order, excluding what the capped part returned.
// Re-ranking a late page without the cap would not do: the cap selects by raw
// bm25 and the final order also applies kind weights and tiers, so the two
// orders disagree. Pages inside the cap, nearly all of them, cost one pass.
func (s *Store) SearchSessionSearchDocs(ctx context.Context, params domain.SessionSearchParams) ([]domain.SessionSearchHit, error) {
	capped := sessionSearchCandidateCap
	if params.Sort == domain.SessionSearchSortRecent || sessionSearchFiltered(params) {
		capped = -1
	}
	hits, capReached, err := s.searchSessionSearchDocs(ctx, params, sessionSearchPass{capped: capped})
	if err != nil || capped < 0 {
		return hits, err
	}
	limit := domain.NormalizeSessionSearchLimit(params.Limit)
	if len(hits) == limit || (len(hits) > 0 && !capReached) || (len(hits) == 0 && params.Offset <= 0) {
		return hits, nil
	}
	// The page runs past the end of the capped part, or past the end of
	// everything. Listing the capped part tells which, and what to exclude.
	listing := params
	listing.Offset, listing.SkipSnippets = 0, true
	capPart, truncated, err := s.searchSessionSearchDocs(ctx, listing, sessionSearchPass{capped: capped, limit: capped})
	if err != nil || !truncated {
		return hits, err
	}
	pass := sessionSearchPass{capped: -1, limit: limit - len(hits)}
	if params.Group != domain.SessionSearchGroupMessage {
		ids := make([]string, 0, len(capPart))
		for _, hit := range capPart {
			ids = append(ids, hit.SessionID)
		}
		pass.excludeSessions = jsonArray(ids)
	} else {
		ids := make([]int64, 0, len(capPart))
		for _, hit := range capPart {
			ids = append(ids, hit.DocID)
		}
		encoded, _ := json.Marshal(ids)
		pass.excludeDocs = string(encoded)
	}
	rest := params
	rest.Offset = max(0, params.Offset-len(capPart))
	more, _, err := s.searchSessionSearchDocs(ctx, rest, pass)
	if err != nil {
		return nil, err
	}
	hits = append(hits, more...)
	for index := range hits {
		// The rows reach past the cap: the total must be counted exactly.
		hits[index].MatchTotalCapped = true
	}
	return hits, nil
}

// sessionSearchPass shapes one ranked query: how many stage-one candidates to
// keep (-1: all), a row limit overriding the request's (0: the request's), and
// sessions or documents (JSON arrays) to leave out.
type sessionSearchPass struct {
	capped          int
	limit           int
	excludeSessions string
	excludeDocs     string
}

// searchSessionSearchDocs runs one ranked query against the index and reports
// whether stage one's candidate cap was reached.
func (s *Store) searchSessionSearchDocs(ctx context.Context, params domain.SessionSearchParams, pass sessionSearchPass) ([]domain.SessionSearchHit, bool, error) {
	capped := pass.capped
	limit := domain.NormalizeSessionSearchLimit(params.Limit)
	if pass.limit > 0 {
		limit = pass.limit
	}
	offset := max(params.Offset, 0)
	recent, grouped := 0, 0
	if params.Sort == domain.SessionSearchSortRecent {
		recent = 1
	}
	if params.Group != domain.SessionSearchGroupMessage {
		grouped = 1
	}
	// A recency sort orders by time first, so the exact tier would cost a
	// full-text pass that changes nothing.
	exact := ""
	if recent == 0 {
		exact = params.ExactQuery
	}
	// The name tier orders sessions by relevance; a recency sort or a list of
	// single messages has no use for it.
	named := ""
	if grouped == 1 && recent == 0 {
		var err error
		if named, err = s.sessionsNamedFor(ctx, params.HighlightTerms); err != nil {
			return nil, false, err
		}
	}
	weights := domain.SessionSearchKindWeights
	args := make([]any, 0, 40)
	args = append(args, params.MatchQuery, capped,
		weights[domain.SessionSearchKindTitle],
		weights[domain.SessionSearchKindUser],
		weights[domain.SessionSearchKindPath],
		weights[domain.SessionSearchKindAssistant],
		weights[domain.SessionSearchKindTool],
		weights[domain.SessionSearchKindCommand],
		named, named,
		exact, exact,
	)
	args = append(args, sessionSearchFilterArgs(params)...)
	args = append(args, pass.excludeSessions, pass.excludeSessions, pass.excludeDocs, pass.excludeDocs)
	skipSnippets := 0
	if params.SkipSnippets {
		skipSnippets = 1
	}
	args = append(args, grouped, recent, domain.SessionSearchTailDepth, grouped, domain.SessionSearchTailWeight, grouped, recent, limit, offset, skipSnippets)
	rows, err := s.searchReader().QueryContext(ctx, searchSessionSearchDocsSQL, args...)
	if err != nil {
		return nil, false, fmt.Errorf("search session index: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hits := make([]domain.SessionSearchHit, 0, limit)
	capReached := false
	for rows.Next() {
		var hit domain.SessionSearchHit
		var docID, createdAt, candidates int64
		var kind string
		var body []byte
		if err := rows.Scan(
			&docID, &hit.SessionID, &hit.ProjectID, &hit.Harness, &kind, &hit.MessageID, &hit.Seq,
			&hit.Title, &createdAt, &hit.Rank, &hit.SessionMatches, &body, &hit.MatchTotal, &candidates,
			&hit.ReviewID,
		); err != nil {
			return nil, false, fmt.Errorf("scan session search hit: %w", err)
		}
		hit.Kind = domain.SessionSearchKind(kind)
		hit.DocID = docID
		hit.CreatedAt = time.UnixMilli(createdAt).UTC()
		hit.MatchTotalCapped = capped > 0 && candidates >= int64(capped)
		capReached = capReached || hit.MatchTotalCapped
		if !params.SkipSnippets {
			text := hit.Title
			if hit.Kind != domain.SessionSearchKindTitle || hit.Title == "" {
				text = searchtext.Decode(body)
			}
			hit.Snippet = domain.SessionSearchSnippet(text, params.HighlightTerms)
		}
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate session search hits: %w", err)
	}
	return hits, capReached, nil
}

// CountSessionSearchDocsMatching returns how many results a query has, ignoring
// limit and offset: matching sessions when grouped, matching documents when not.
//
// A page length is not a total: paging (ao search --offset) needs the real
// count. It runs the same WHERE clause as the search with the LIMIT removed,
// so the two can never disagree about what matched.
func (s *Store) CountSessionSearchDocsMatching(ctx context.Context, params domain.SessionSearchParams) (int64, error) {
	counted := "COUNT(DISTINCT d.session_id)"
	if params.Group == domain.SessionSearchGroupMessage {
		counted = "COUNT(*)"
	}
	args := append([]any{params.MatchQuery}, sessionSearchFilterArgs(params)...)
	var count int64
	err := s.searchReader().QueryRowContext(ctx, `
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
	err := s.searchReader().QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions s`+browseSessionFilterSQL,
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
	rows, err := s.searchReader().QueryContext(ctx, browseSessionSearchDocsSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("browse session index: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hits := make([]domain.SessionSearchHit, 0, limit)
	for rows.Next() {
		var hit domain.SessionSearchHit
		var createdAt int64
		var kind string
		var preview []byte
		if err := rows.Scan(
			&hit.SessionID, &hit.ProjectID, &hit.Harness, &kind, &hit.MessageID, &hit.Seq,
			&hit.Title, &createdAt, &hit.Rank, &preview,
		); err != nil {
			return nil, fmt.Errorf("scan session search browse row: %w", err)
		}
		hit.Snippet = searchtext.Decode(preview)
		hit.Kind = domain.SessionSearchKind(kind)
		hit.CreatedAt = time.UnixMilli(createdAt).UTC()
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session search browse rows: %w", err)
	}
	return hits, nil
}

// SessionSearchMatchCounts counts, for each session, the places find in chat
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
	rows, err := s.searchReader().QueryContext(ctx, `
SELECT d.session_id, COUNT(DISTINCT COALESCE(NULLIF(d.message_id, ''),
       -- A long message's chunks ("key#2", "key#3") are one match.
       d.source_key || '|' || d.kind || '|' ||
       CASE WHEN instr(d.doc_key, '#') > 0 THEN substr(d.doc_key, 1, instr(d.doc_key, '#') - 1) ELSE d.doc_key END))
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
	if err := s.searchReader().QueryRowContext(ctx, `SELECT COUNT(*) FROM session_search_docs`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count session search docs: %w", err)
	}
	return count, nil
}

const upsertSessionSearchDocSQL = `
INSERT INTO session_search_docs (
    source_key, doc_key, session_id, project_id, harness, kind, message_id, seq,
    created_at, indexed_at, fingerprint
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source_key, doc_key, kind) DO UPDATE SET
    session_id = excluded.session_id,
    project_id = excluded.project_id,
    harness = excluded.harness,
    message_id = excluded.message_id,
    seq = excluded.seq,
    created_at = excluded.created_at,
    indexed_at = excluded.indexed_at,
    fingerprint = excluded.fingerprint
RETURNING id;`

// UpsertSessionSearchDocs writes one batch in a single transaction, so the
// index and the projection advance together and a batch costs one commit.
//
// The index is contentless, so a changed document's old index row is removed
// with FTS5's 'delete' command and the text it was indexed with, read before
// that text is overwritten. Text and index rows are then written one statement
// each: FTS5 flushes its pending terms whenever another statement opens a
// savepoint, which would make each document its own segment to merge.
// Documents whose hash is unchanged keep their index rows.
func (s *Store) UpsertSessionSearchDocs(ctx context.Context, docs []domain.SessionSearchDoc) error {
	if len(docs) == 0 {
		return nil
	}
	// Everything that is not SQL happens before the transaction: normalising,
	// hashing, compressing and encoding a batch's text is CPU time the index's
	// writer would otherwise hold its lock for.
	prepared, err := prepareSessionSearchDocs(docs)
	if err != nil {
		return err
	}
	return s.inSearchTx(ctx, "upsert session search docs", func(executor *sql.Tx) error {
		docStmt, err := executor.PrepareContext(ctx, upsertSessionSearchDocSQL)
		if err != nil {
			return fmt.Errorf("prepare session search upsert: %w", err)
		}
		defer func() { _ = docStmt.Close() }()
		ids := make([]int64, len(docs))
		for index, doc := range docs {
			if err := docStmt.QueryRowContext(ctx,
				doc.SourceKey, doc.DocKey, doc.SessionID, doc.ProjectID, doc.Harness,
				string(doc.Kind), doc.MessageID, doc.Seq,
				doc.CreatedAt.UnixMilli(), doc.IndexedAt.UnixMilli(), prepared[index].fingerprint,
			).Scan(&ids[index]); err != nil {
				return fmt.Errorf("upsert session search doc %s/%s: %w", doc.SourceKey, doc.DocKey, err)
			}
		}
		stored, err := sessionSearchStoredHashes(ctx, executor, ids)
		if err != nil {
			return err
		}
		// The last write of a document in the batch wins, as it did when each
		// was written in turn.
		latest := make(map[int64]int, len(docs))
		for index := range docs {
			latest[ids[index]] = index
		}
		var textRows, indexRows jsonRows
		replaced := make([]int64, 0, len(docs))
		for index := range docs {
			id := ids[index]
			if latest[id] != index {
				continue
			}
			doc := prepared[index]
			previous, exists := stored[id]
			if exists && previous == doc.hash {
				continue
			}
			if exists {
				replaced = append(replaced, id)
			}
			textRows.add(id, doc.textRow)
			indexRows.add(id, doc.indexRow)
		}
		if textRows.empty() {
			return nil
		}
		if len(replaced) > 0 {
			replacedIDs, err := json.Marshal(replaced)
			if err != nil {
				return fmt.Errorf("encode replaced session search rows: %w", err)
			}
			if _, err := executor.ExecContext(ctx, `
INSERT INTO session_search_fts (session_search_fts, rowid, title, body, paths)
SELECT 'delete', t.id, t.title, ao_search_body(t.body), t.paths
FROM session_search_text t
WHERE t.id IN (SELECT value FROM json_each(?))`, string(replacedIDs)); err != nil {
				return fmt.Errorf("remove replaced session search index rows: %w", err)
			}
		}
		if _, err := executor.ExecContext(ctx, `
INSERT INTO session_search_text (id, title, paths, body, hash)
SELECT json_extract(value, '$[0]'), json_extract(value, '$[1]'), json_extract(value, '$[2]'),
       unhex(json_extract(value, '$[3]')), json_extract(value, '$[4]')
FROM json_each(?) WHERE true
ON CONFLICT (id) DO UPDATE SET
    title = excluded.title,
    paths = excluded.paths,
    body = excluded.body,
    hash = excluded.hash`, textRows.String()); err != nil {
			return fmt.Errorf("store session search text: %w", err)
		}
		if _, err := executor.ExecContext(ctx, `
INSERT INTO session_search_fts (rowid, title, body, paths)
SELECT json_extract(value, '$[0]'), json_extract(value, '$[1]'), json_extract(value, '$[2]'), json_extract(value, '$[3]')
FROM json_each(?)`, indexRows.String()); err != nil {
			return fmt.Errorf("index session search docs: %w", err)
		}
		return nil
	})
}

// inSearchTx runs fn in one transaction on the search database's writer.
func (s *Store) inSearchTx(ctx context.Context, what string, fn func(*sql.Tx) error) error {
	tx, err := s.searchWriter().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s: %w", what, err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("%s: %w", what, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", what, err)
	}
	return nil
}

// preparedSessionSearchDoc is a document's text ready to write: hashed,
// compressed and encoded as the tail of its JSON row, after the row id.
type preparedSessionSearchDoc struct {
	fingerprint int64
	hash        int64
	textRow     []byte // ,title,paths,hex(compressed body),hash]
	indexRow    []byte // ,title,body,paths]
}

func prepareSessionSearchDocs(docs []domain.SessionSearchDoc) ([]preparedSessionSearchDoc, error) {
	prepared := make([]preparedSessionSearchDoc, len(docs))
	for index, doc := range docs {
		title, body, paths := searchtext.Normalize(doc.Title), searchtext.Normalize(doc.Body), searchtext.Normalize(doc.Paths)
		hash := searchtext.Hash(title, body, paths)
		textRow, err := json.Marshal([4]any{title, paths, hex.EncodeToString(searchtext.Encode(body)), hash})
		if err != nil {
			return nil, fmt.Errorf("encode session search text row: %w", err)
		}
		indexRow, err := json.Marshal([3]any{title, body, paths})
		if err != nil {
			return nil, fmt.Errorf("encode session search index row: %w", err)
		}
		prepared[index] = preparedSessionSearchDoc{
			fingerprint: doc.Fingerprint(),
			hash:        hash,
			textRow:     jsonArrayTail(textRow),
			indexRow:    jsonArrayTail(indexRow),
		}
	}
	return prepared, nil
}

// jsonArrayTail turns an encoded array ["a",...] into ,"a",...] so a row id
// can be put in front of it.
func jsonArrayTail(encoded []byte) []byte {
	encoded[0] = ','
	return encoded
}

// jsonRows builds a JSON array of [id, ...] rows from pre-encoded row tails.
type jsonRows struct{ b []byte }

func (r *jsonRows) add(id int64, tail []byte) {
	if r.b == nil {
		r.b = append(r.b, '[')
	} else {
		r.b = append(r.b, ',')
	}
	r.b = append(r.b, '[')
	r.b = strconv.AppendInt(r.b, id, 10)
	r.b = append(r.b, tail...)
}

func (r *jsonRows) empty() bool { return r.b == nil }

func (r *jsonRows) String() string { return string(r.b) + "]" }

// sessionSearchStoredHashes returns the stored text hash of each document that
// has text, keyed by document id.
func sessionSearchStoredHashes(ctx context.Context, executor gen.DBTX, ids []int64) (map[int64]int64, error) {
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("encode session search ids: %w", err)
	}
	rows, err := executor.QueryContext(ctx,
		`SELECT id, hash FROM session_search_text WHERE id IN (SELECT value FROM json_each(?))`, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("read session search text hashes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hashes := make(map[int64]int64, len(ids))
	for rows.Next() {
		var id, hash int64
		if err := rows.Scan(&id, &hash); err != nil {
			return nil, fmt.Errorf("scan session search text hash: %w", err)
		}
		hashes[id] = hash
	}
	return hashes, rows.Err()
}

// sessionSearchDeleteBatch bounds one delete transaction on the search
// database. Removing a document also removes its index row, which re-reads and
// decompresses its text, so a large delete is many transactions rather than one
// that keeps search readers' snapshots and the -wal file growing.
const sessionSearchDeleteBatch = 256

// SessionSearchSourceDocs lists a source's stored documents (id, key and
// fingerprint), so a pass can write only what changed and delete only what
// disappeared. It reads; it takes no write lock.
func (s *Store) SessionSearchSourceDocs(ctx context.Context, sourceKey string) ([]domain.SessionSearchDocRef, error) {
	rows, err := s.searchReader().QueryContext(ctx,
		`SELECT id, doc_key, kind, fingerprint FROM session_search_docs WHERE source_key = ?`, sourceKey)
	if err != nil {
		return nil, fmt.Errorf("list session search source docs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var refs []domain.SessionSearchDocRef
	for rows.Next() {
		var ref domain.SessionSearchDocRef
		var kind string
		if err := rows.Scan(&ref.ID, &ref.DocKey, &kind, &ref.Fingerprint); err != nil {
			return nil, fmt.Errorf("scan session search source doc: %w", err)
		}
		ref.Kind = domain.SessionSearchKind(kind)
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// SessionSearchUnsupportedHarnesses lists, sorted, the harnesses of sources the
// index recorded as unsupported.
func (s *Store) SessionSearchUnsupportedHarnesses(ctx context.Context) ([]string, error) {
	rows, err := s.searchReader().QueryContext(ctx,
		`SELECT DISTINCT harness FROM session_search_sources
WHERE state = ? AND harness <> ''
  AND session_id NOT IN (SELECT session_id FROM session_search_deleted)
ORDER BY harness`,
		string(domain.SessionSearchSourceUnsupported))
	if err != nil {
		return nil, fmt.Errorf("list unsupported session search harnesses: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var harnesses []string
	for rows.Next() {
		var harness string
		if err := rows.Scan(&harness); err != nil {
			return nil, fmt.Errorf("scan unsupported session search harness: %w", err)
		}
		harnesses = append(harnesses, harness)
	}
	return harnesses, rows.Err()
}

// SessionSearchDocFingerprint reads one document's stored fingerprint by its
// unique source, key and kind. indexed is false when there is no such document.
func (s *Store) SessionSearchDocFingerprint(ctx context.Context, sourceKey, docKey string, kind domain.SessionSearchKind) (fingerprint int64, indexed bool, err error) {
	err = s.searchReader().QueryRowContext(ctx,
		`SELECT fingerprint FROM session_search_docs WHERE source_key = ? AND doc_key = ? AND kind = ?`,
		sourceKey, docKey, string(kind)).Scan(&fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read session search doc fingerprint: %w", err)
	}
	return fingerprint, true, nil
}

// DeleteSessionSearchDocsByID removes documents by id, sessionSearchDeleteBatch
// per transaction.
func (s *Store) DeleteSessionSearchDocsByID(ctx context.Context, ids []int64) error {
	for start := 0; start < len(ids); start += sessionSearchDeleteBatch {
		encoded, err := json.Marshal(ids[start:min(start+sessionSearchDeleteBatch, len(ids))])
		if err != nil {
			return fmt.Errorf("encode session search ids: %w", err)
		}
		if _, err := s.searchWriter().ExecContext(ctx,
			`DELETE FROM session_search_docs WHERE id IN (SELECT value FROM json_each(?))`, string(encoded),
		); err != nil {
			return fmt.Errorf("delete session search docs: %w", err)
		}
	}
	return nil
}

// deleteSessionSearchDocsWhere removes the documents matching one column, a
// batch per transaction, until none are left.
func (s *Store) deleteSessionSearchDocsWhere(ctx context.Context, column, value string) error {
	query := `DELETE FROM session_search_docs WHERE id IN (
    SELECT id FROM session_search_docs WHERE ` + column + ` = ? LIMIT ?)`
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := s.searchWriter().ExecContext(ctx, query, value, sessionSearchDeleteBatch)
		if err != nil {
			return fmt.Errorf("delete session search docs by %s: %w", column, err)
		}
		if removed, err := result.RowsAffected(); err != nil || removed < sessionSearchDeleteBatch {
			return err
		}
	}
}

// DeleteSessionSearchDocsForSource clears one source before a full re-index,
// in short transactions.
func (s *Store) DeleteSessionSearchDocsForSource(ctx context.Context, sourceKey string) error {
	return s.deleteSessionSearchDocsWhere(ctx, "source_key", sourceKey)
}

// SessionSearchOrphanSessions lists up to limit deleted sessions whose search
// documents are not removed yet. Deleting a session only records its id (a
// long session has thousands of documents, too many for that transaction);
// the indexer removes them afterwards, and search ignores them meanwhile.
func (s *Store) SessionSearchOrphanSessions(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.searchReader().QueryContext(ctx, `SELECT session_id FROM session_search_deleted LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list session search orphans: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan session search orphan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// sessionSearchMissingBatch is how many session ids one write records as
// deleted. Each write is a few primary-key lookups, well inside the app
// database's lock budget.
const sessionSearchMissingBatch = 256

// MarkSessionSearchSessionsMissing records as deleted every session the index
// holds that the app database does not have, and returns how many it recorded.
//
// A session delete records itself (the sessions_search_ad trigger). This is
// for an index that outlived its database's history: a restored backup of
// ao.db keeps the owner token, so it passes the owner check (search_db.go)
// while the index still holds sessions created after the backup. Recorded
// here, search ignores them at once and the sweep removes their documents.
func (s *Store) MarkSessionSearchSessionsMissing(ctx context.Context) (int, error) {
	rows, err := s.searchReader().QueryContext(ctx, `
SELECT held.session_id FROM (
    SELECT session_id FROM session_search_sources
    UNION
    SELECT session_id FROM session_search_docs
) held
WHERE held.session_id != ''
  AND NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id = held.session_id)
  AND held.session_id NOT IN (SELECT session_id FROM session_search_deleted)`)
	if err != nil {
		return 0, fmt.Errorf("list indexed sessions missing from the database: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan indexed session: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate indexed sessions: %w", err)
	}
	recorded := 0
	for start := 0; start < len(ids); start += sessionSearchMissingBatch {
		batch := ids[start:min(start+sessionSearchMissingBatch, len(ids))]
		// Checked again where it is written: the read above and this write are
		// two connections, and a session created in between is not missing.
		result, err := s.writeDB.ExecContext(ctx, `
INSERT OR IGNORE INTO session_search_deleted (session_id)
SELECT value FROM json_each(?)
WHERE NOT EXISTS (SELECT 1 FROM sessions WHERE id = value)`, jsonArray(batch))
		if err != nil {
			return recorded, fmt.Errorf("record sessions missing from the database: %w", err)
		}
		if count, err := result.RowsAffected(); err == nil {
			recorded += int(count)
		}
	}
	return recorded, nil
}

// DeleteSessionSearchSession removes a deleted session's documents, in short
// transactions, then its sources.
func (s *Store) DeleteSessionSearchSession(ctx context.Context, sessionID string) error {
	if err := s.deleteSessionSearchDocsWhere(ctx, "session_id", sessionID); err != nil {
		return err
	}
	if _, err := s.searchWriter().ExecContext(ctx,
		`DELETE FROM session_search_sources WHERE session_id = ?`, sessionID,
	); err != nil {
		return fmt.Errorf("delete session search sources: %w", err)
	}
	if _, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM session_search_deleted WHERE session_id = ?`, sessionID,
	); err != nil {
		return fmt.Errorf("clear deleted session %s: %w", sessionID, err)
	}
	return nil
}

// CountSessionSearchDocsForSource reports how many documents a source holds.
func (s *Store) CountSessionSearchDocsForSource(ctx context.Context, sourceKey string) (int64, error) {
	var count int64
	if err := s.searchReader().QueryRowContext(ctx,
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
	if _, err := s.searchWriter().ExecContext(ctx, upsertSessionSearchSourceSQL,
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
	err := s.searchReader().QueryRowContext(ctx, `
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
	rows, err := s.searchReader().QueryContext(ctx,
		`SELECT state, COUNT(*) FROM session_search_sources
WHERE session_id NOT IN (SELECT session_id FROM session_search_deleted) GROUP BY state`)
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
	rows, err := s.searchReader().QueryContext(ctx, `
SELECT id, COALESCE(project_id, ''), harness, kind, display_name, prompt, latest_user_prompt,
       workspace_path, branch, session_mode, is_terminated, hibernated_at IS NOT NULL, created_at,
       agent_session_id, native_transcript_path
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
			&target.NativeSessionID, &target.NativeTranscriptPath,
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
	rows, err := s.searchReader().QueryContext(ctx, `
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
	activities, messages, err := s.sessionSearchVisibleTimeline(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	// Each activity opens at its turn's message: the last one at or before it,
	// else the turn's first. Only visible messages count, so a result never
	// points at a message the transcript cannot display.
	byTurn := map[string][]domain.ConversationMessage{}
	for _, message := range messages {
		if message.TurnID != "" {
			byTurn[message.TurnID] = append(byTurn[message.TurnID], message)
		}
	}
	out := make([]domain.SessionSearchActivity, 0, len(activities))
	for _, activity := range activities {
		anchor := ""
		if turn := byTurn[activity.TurnID]; len(turn) > 0 {
			anchor = turn[0].ID
			for _, message := range turn {
				if message.Sequence <= activity.Sequence {
					anchor = message.ID
				}
			}
		}
		out = append(out, domain.SessionSearchActivity{
			ID: activity.ID, Sequence: activity.Sequence, Kind: activity.Kind, Status: activity.Status,
			Summary: activity.Summary, Detail: string(activity.Detail), Output: activity.CommandOutput, Streamed: activity.StreamedText,
			CreatedAt: activity.CreatedAt, UpdatedAt: activity.UpdatedAt, AnchorMessageID: anchor,
		})
	}
	return out, nil
}

// sessionSearchVisibleTimeline reads a conversation's activities and messages
// through the conversation's own readers, so search indexes exactly what the
// transcript shows: the active branch's path, without rolled-back or cancelled
// turns. Reading the raw tables instead kept discarded command output, undone
// file changes and other branches searchable after a full reindex.
func (s *Store) sessionSearchVisibleTimeline(ctx context.Context, conversationID string) ([]domain.ConversationActivity, []domain.ConversationMessage, error) {
	activityRows, err := s.qr.SelectConversationActivities(ctx, conversationID)
	if err != nil {
		return nil, nil, fmt.Errorf("list session search activities: %w", err)
	}
	activities := make([]domain.ConversationActivity, 0, len(activityRows))
	for _, row := range activityRows {
		activities = append(activities, activityToDomain(row))
	}
	messages, err := s.ConversationMessages(ctx, conversationID)
	if err != nil {
		return nil, nil, fmt.Errorf("list session search messages: %w", err)
	}
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].Sequence < messages[j].Sequence })
	return activities, messages, nil
}

// SessionSearchTouchedPaths returns the file paths a conversation's visible
// turns touched: an undone edit is not a path the session changed. detail_json
// holds {"files":[{"path":…}]} for a file_change activity.
func (s *Store) SessionSearchTouchedPaths(ctx context.Context, conversationID string) ([]string, error) {
	activityRows, err := s.qr.SelectConversationActivities(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list session search touched paths: %w", err)
	}
	seen := map[string]struct{}{}
	var paths []string
	for _, row := range activityRows {
		activity := activityToDomain(row)
		if activity.Kind != domain.ActivityKindFileChange {
			continue
		}
		for _, path := range pathsFromFileChangeDetail(string(activity.Detail)) {
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
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
	rows, err := s.searchReader().QueryContext(ctx, `
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
       ), 0),
       COALESCE(c.active_branch_id, '') || char(31) || COALESCE(c.provider_title, '') || char(31) ||
       COALESCE(s.display_name, '') || char(31) || COALESCE(s.branch, '') || char(31) ||
       (SELECT COUNT(*) || ':' || COALESCE(MAX(t.rolled_back_at), '') || ':' || COALESCE(MAX(t.completed_at), '')
        FROM conversation_turns t
        WHERE t.conversation_id = c.id AND (t.rolled_back_at IS NOT NULL OR t.state = 'cancelled')) || char(31) ||
       -- Every write to a row bumps its revision, and the newest update time is
       -- kept at full precision: the millisecond column above rounds to whole
       -- seconds, so an edit in the same second as the last pass went unseen.
       (SELECT COALESCE(SUM(m.revision), 0) || ':' || COALESCE(MAX(m.updated_at), '')
        FROM conversation_messages m WHERE m.conversation_id = c.id) || char(31) ||
       (SELECT COALESCE(SUM(a.revision), 0) || ':' || COALESCE(MAX(a.updated_at), '')
        FROM conversation_activities a WHERE a.conversation_id = c.id)
FROM conversations c
LEFT JOIN review r ON r.id = c.current_review_id
LEFT JOIN sessions s ON s.id = COALESCE(c.current_session_id, r.session_id)
WHERE c.current_session_id IS NOT NULL OR c.current_review_id IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("read session search conversation stamps: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var stamp domain.SessionSearchConversationStamp
		if err := rows.Scan(
			&stamp.ConversationID, &stamp.MessageCount, &stamp.MessageSeqMax,
			&stamp.ActivityCount, &stamp.ActivitySeqMax, &stamp.UpdatedAt, &stamp.Witness,
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
