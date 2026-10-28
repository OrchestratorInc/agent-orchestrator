// Package sessionsearch owns the local full-text index over past AO sessions
// and the ranked query the desktop UI, the Cmd+K palette, and `ao search` share.
//
// Two invariants shape the package:
//
//   - It is READ-ONLY toward session data. It writes only its own index tables
//     (session_search_docs, session_search_fts, session_search_sources) and
//     never modifies a session row, a conversation row, or a provider
//     transcript. Native transcripts are opened read-only and read forward.
//   - It is incremental and bounded. Chat is re-indexed per conversation only
//     when its content fingerprint moved; native transcripts advance by byte
//     offset; backfill walks sessions in pages so the daemon's single writer
//     connection is never monopolized and memory stays flat on an 8 GB machine.
package sessionsearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Index bounds. They exist so a large transcript or a burst of activity cannot
// turn the indexer into a memory or writer problem on a small machine.
const (
	// maxDocBodyBytes caps one indexed document's text. A snippet needs far
	// less, and one 64 KiB command output would otherwise dominate a batch.
	maxDocBodyBytes = 8 << 10
	// maxTitleBytes caps a title document.
	maxTitleBytes = 512
	// maxIdentifierSplits bounds how many identifiers per document are expanded
	// into their parts, so a huge command output cannot triple the index.
	maxIdentifierSplits = 200
	// batchSize is how many documents one transaction writes.
	batchSize = 64
	// pageSize is how many sessions one backfill page reads before yielding.
	pageSize = 50
	// nativeReadChunk bounds one read from a transcript.
	nativeReadChunk = 256 << 10
)

// Store is the persistence surface the indexer and query share. The SQLite
// store satisfies it; tests use a fake.
type Store interface {
	SearchSessionSearchDocs(ctx context.Context, params domain.SessionSearchParams) ([]domain.SessionSearchHit, error)
	BrowseSessionSearchDocs(ctx context.Context, params domain.SessionSearchParams) ([]domain.SessionSearchHit, error)
	SessionSearchMatchesForSession(ctx context.Context, sessionID, matchQuery string) ([]domain.SessionSearchHit, error)
	SessionSearchMatchCounts(ctx context.Context, matchQuery string, sessionIDs []string) (map[string]int, error)
	CountSessionSearchDocsMatching(ctx context.Context, params domain.SessionSearchParams) (int64, error)
	CountSessionSearchSessionsBrowsing(ctx context.Context, params domain.SessionSearchParams) (int64, error)
	UpsertSessionSearchDocs(ctx context.Context, docs []domain.SessionSearchDoc) error
	PruneSessionSearchDocsBefore(ctx context.Context, sourceKey string, indexedAt time.Time) error
	DeleteSessionSearchDocsForSource(ctx context.Context, sourceKey string) error
	CountSessionSearchDocsForSource(ctx context.Context, sourceKey string) (int64, error)
	UpsertSessionSearchSource(ctx context.Context, source domain.SessionSearchSource) error
	GetSessionSearchSource(ctx context.Context, sourceKey string) (domain.SessionSearchSource, bool, error)
	CountSessionSearchSourcesByState(ctx context.Context) (map[domain.SessionSearchSourceState]int64, error)
	CountSessionSearchDocs(ctx context.Context) (int64, error)
	ListSessionSearchTargets(ctx context.Context) ([]domain.SessionSearchTarget, error)
	ListSessionSearchConversations(ctx context.Context) ([]domain.SessionSearchConversation, error)
	SessionSearchConversationStamps(ctx context.Context) (map[string]domain.SessionSearchConversationStamp, error)
	SessionSearchVisibleMessages(ctx context.Context, conversationID string) ([]domain.SessionSearchMessage, error)
	SessionSearchActivities(ctx context.Context, conversationID string) ([]domain.SessionSearchActivity, error)
	SessionSearchTouchedPaths(ctx context.Context, conversationID string) ([]string, error)
}

// Namer resolves display names for results. It is optional: an unresolved name
// leaves the field empty rather than failing the search.
type Namer interface {
	SessionSearchNames(ctx context.Context, sessionIDs, projectIDs []string) (map[string]string, map[string]string, error)
}

// StateNamer reports what clicking a result will do: wake a hibernated session,
// open a terminated one read-only, or focus a live one.
type StateNamer interface {
	SessionSearchStates(ctx context.Context, sessionIDs []string) (map[string]string, error)
}

// PRResolver maps a pasted pull-request URL to the session that owns it.
type PRResolver interface {
	SessionSearchSessionForPR(ctx context.Context, url string) (string, bool, error)
}

// TranscriptRoots are the provider-owned directories the indexer may read.
type TranscriptRoots struct {
	// ClaudeProjects is <claude config dir>/projects.
	ClaudeProjects string
	// CodexSessions is <codex home>/sessions.
	CodexSessions string
	// CodexArchived is <codex home>/archived_sessions.
	CodexArchived string
	// OpenCodeData is the XDG data home holding opencode/opencode.db.
	OpenCodeData string
	// OpenCodeV2Data is the isolated v2 data home.
	OpenCodeV2Data string
}

// Options configures a Service.
type Options struct {
	Store Store
	// Roots are the provider transcript roots. A zero value means native
	// transcripts are not indexed; Chat sessions still are.
	Roots TranscriptRoots
	// Namer resolves session and project display names for results.
	Namer Namer
	// PRs resolves a pasted PR URL to its session.
	PRs PRResolver
	// Machine is this daemon's host label, recorded on every document so the
	// machine: operator can filter.
	Machine string
	// Now overrides the clock in tests.
	Now func() time.Time
	// Logger receives indexer warnings. Nil discards them.
	Logger *slog.Logger
	// Ranking replaces the shipped ranking for requests that do not set their
	// own. It exists so a ranking change can be tried end to end against a
	// real archive (TestServeSessionSearchOverSnapshot); the daemon leaves it
	// zero.
	Ranking domain.SessionSearchRanking
}

// Service indexes session content and answers ranked queries over it.
type Service struct {
	store   Store
	roots   TranscriptRoots
	namer   Namer
	prs     PRResolver
	machine string
	now     func() time.Time
	log     *slog.Logger
	ranking domain.SessionSearchRanking

	mu           sync.Mutex
	state        string
	backfilledAt time.Time
}

// New constructs the session search service.
func New(opts Options) *Service {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{
		store: opts.Store, roots: opts.Roots, namer: opts.Namer, prs: opts.PRs,
		machine: opts.Machine, now: now, log: log, state: "ready", ranking: opts.Ranking,
	}
}

// Search runs one query, ranked by relevance or recency.
//
// It accepts either a pre-parsed query or raw text: the API and CLI parse with
// the same function, and passing the struct through keeps the operators intact.
func (s *Service) Search(ctx context.Context, parsed domain.SessionSearchParsedQuery, params domain.SessionSearchParams) (domain.SessionSearchResponse, error) {
	if s == nil || s.store == nil {
		return domain.SessionSearchResponse{}, apierr.Unavailable("SESSION_SEARCH_UNAVAILABLE",
			"Session search is not available on this daemon")
	}
	if err := parsed.Validate(); err != nil {
		return domain.SessionSearchResponse{}, apierr.Invalid("SESSION_SEARCH_INVALID_RANGE", err.Error(), nil)
	}
	params.ProjectID = firstNonEmpty(parsed.Project, params.ProjectID)
	params.Harness = firstNonEmpty(parsed.Harness, params.Harness)
	params.Machine = firstNonEmpty(parsed.Machine, params.Machine)
	params.Path = firstNonEmpty(parsed.Path, params.Path)
	if parsed.Status != domain.SessionSearchStatusAny {
		params.Status = parsed.Status
	}
	if parsed.From != nil {
		params.From = parsed.From
	}
	if parsed.To != nil {
		params.To = parsed.To
	}
	if params.Ranking.IsZero() {
		params.Ranking = s.ranking
	}
	// A typed sort: operator wins; otherwise the caller's sort stands, and
	// relevance is the default when neither chose.
	if parsed.Sort != "" {
		params.Sort = parsed.Sort
	}
	if params.Sort == "" {
		params.Sort = domain.SessionSearchSortRelevance
	}
	params.Limit = domain.NormalizeSessionSearchLimit(params.Limit)
	if params.Group != domain.SessionSearchGroupMessage {
		params.Group = domain.SessionSearchGroupSession
	}
	if params.Offset < 0 {
		return domain.SessionSearchResponse{}, apierr.Invalid("SESSION_SEARCH_INVALID_OFFSET",
			"Offset must not be negative", nil)
	}

	// A pasted PR URL lists the session that owns the PR first, then every
	// other session that mentions it (the one that reviewed it, the one that
	// tested it). A PR no AO session owns still finds the sessions that
	// discuss it. Its URL has no words of its own to rank by, so the PR's
	// phrase is the query unless words were typed beside it.
	prOwner := ""
	if parsed.PRRef != nil {
		if s.prs != nil {
			sessionID, ok, err := s.prs.SessionSearchSessionForPR(ctx, parsed.PRRef.URL)
			if err != nil {
				return domain.SessionSearchResponse{}, err
			}
			if ok {
				prOwner = sessionID
			}
		}
		if parsed.MatchQuery == "" && params.MatchQuery == "" {
			params.MatchQuery = parsed.PRRef.Phrase()
		}
	}

	params.ExcludeQuery = parsed.ExcludeQuery
	match := firstNonEmpty(parsed.MatchQuery, params.MatchQuery)
	var hits []domain.SessionSearchHit
	var err error
	switch {
	case match != "":
		params.MatchQuery = match
		if match == parsed.MatchQuery {
			params.AllTermsQuery = parsed.AllTermsQuery
			params.HighlightTerms = parsed.HighlightTerms
		}
		hits, err = s.store.SearchSessionSearchDocs(ctx, params)
	case hasSessionSearchFilter(params):
		// Filters but no words: browsing, so order by recency.
		params.Sort = domain.SessionSearchSortRecent
		hits, err = s.store.BrowseSessionSearchDocs(ctx, params)
	default:
		// Nothing to search and nothing to filter: an empty page, not an error.
		hits = nil
	}
	// A recency browse with no words is still a browse, so it lists sessions.
	if match == "" && len(hits) == 0 && params.Sort == domain.SessionSearchSortRecent {
		hits, err = s.store.BrowseSessionSearchDocs(ctx, params)
		if err != nil {
			return domain.SessionSearchResponse{}, err
		}
	}
	if err != nil {
		return domain.SessionSearchResponse{}, err
	}
	if match != "" && params.Group != domain.SessionSearchGroupMessage && len(hits) > 0 {
		if err := s.fillMatchCounts(ctx, hits, match); err != nil {
			return domain.SessionSearchResponse{}, err
		}
	}
	// The total comes from the matches themselves; a pinned row carries none.
	matched := hits
	pinned := false
	if prOwner != "" && params.Offset == 0 && params.Group != domain.SessionSearchGroupMessage {
		hits, pinned, err = s.pinSession(ctx, hits, prOwner, params)
		if err != nil {
			return domain.SessionSearchResponse{}, err
		}
	}
	results := s.toResults(ctx, hits)
	// The total is how many results matched (sessions when grouped, documents
	// when not), not how many fit on this page.
	// Without it a surface cannot say "12 shown · 47 matches" or offer a next
	// page, which is the difference between a launcher and a search.
	total := int64(len(results))
	if match != "" && len(matched) > 0 && !matched[0].MatchTotalCapped {
		// The ranking query counts its own candidates; a second count would
		// run the full-text match again.
		total = matched[0].MatchTotal
	} else if match != "" {
		// An empty page past the end, or a query broad enough to reach the
		// candidate cap: count exactly.
		if counted, countErr := s.store.CountSessionSearchDocsMatching(ctx, params); countErr == nil {
			total = counted
		} else {
			s.log.Warn("session search total unavailable", "error", countErr)
		}
	} else if counted, countErr := s.store.CountSessionSearchSessionsBrowsing(ctx, params); countErr == nil {
		total = counted
	} else {
		s.log.Warn("session search browse total unavailable", "error", countErr)
	}
	if pinned {
		// The owner was not among the matches; it is listed in addition to them.
		total++
	}
	return domain.SessionSearchResponse{
		Query:    parsed.Text,
		Results:  results,
		Total:    int(total),
		Limit:    params.Limit,
		Offset:   params.Offset,
		Sort:     params.Sort,
		Group:    params.Group,
		Coverage: s.coverageOrEmpty(ctx),
	}, nil
}

// fillMatchCounts sets each row's "· N matches" to the number of places the
// session's find bar will step through for the same words.
func (s *Service) fillMatchCounts(ctx context.Context, hits []domain.SessionSearchHit, match string) error {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.SessionID)
	}
	counts, err := s.store.SessionSearchMatchCounts(ctx, match, ids)
	if err != nil {
		return err
	}
	for index := range hits {
		hits[index].SessionMatches = counts[hits[index].SessionID]
	}
	return nil
}

// hasSessionSearchFilter reports whether a query narrows by anything other
// than words, which is when a query without words is a browse.
func hasSessionSearchFilter(params domain.SessionSearchParams) bool {
	return params.SessionID != "" || params.ProjectID != "" || params.Harness != "" || params.Machine != "" ||
		params.Path != "" || params.ExcludeQuery != "" || params.Status != domain.SessionSearchStatusAny ||
		(params.From != nil && !params.From.IsZero()) || (params.To != nil && !params.To.IsZero())
}

// pinSession puts one session's row first: the owner of a pasted PR. When it
// already matched, its own row moves up; otherwise its browse row is added,
// and the page keeps its length. The second result reports an added row.
func (s *Service) pinSession(ctx context.Context, hits []domain.SessionSearchHit, sessionID string, params domain.SessionSearchParams) ([]domain.SessionSearchHit, bool, error) {
	for index, hit := range hits {
		if hit.SessionID == sessionID {
			pinned := append([]domain.SessionSearchHit{hit}, hits[:index]...)
			return append(pinned, hits[index+1:]...), false, nil
		}
	}
	owner := params
	owner.SessionID, owner.MatchQuery, owner.ExcludeQuery, owner.Offset, owner.Limit = sessionID, "", "", 0, 1
	rows, err := s.store.BrowseSessionSearchDocs(ctx, owner)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		// The owner falls outside the other filters (another project, ended
		// when live was asked for): the filters win.
		return hits, false, nil
	}
	hits = append(rows[:1], hits...)
	if len(hits) > params.Limit {
		hits = hits[:params.Limit]
	}
	return hits, true, nil
}

// MatchesForSession lists every indexed match for one session, ordered by
// position, so Enter/Shift+Enter can step through them after opening.
func (s *Service) MatchesForSession(ctx context.Context, sessionID, matchQuery string) ([]domain.SessionSearchResult, error) {
	if s == nil || s.store == nil {
		return nil, apierr.Unavailable("SESSION_SEARCH_UNAVAILABLE",
			"Session search is not available on this daemon")
	}
	hits, err := s.store.SessionSearchMatchesForSession(ctx, sessionID, matchQuery)
	if err != nil {
		return nil, err
	}
	// One stop per message: a message and the tool output shown with it are one
	// place to land, which is also how the row's match count counts them.
	seen := make(map[string]bool, len(hits))
	unique := hits[:0]
	for _, hit := range hits {
		if hit.MessageID != "" {
			if seen[hit.MessageID] {
				continue
			}
			seen[hit.MessageID] = true
		}
		unique = append(unique, hit)
	}
	return s.toResults(ctx, unique), nil
}

func (s *Service) toResults(ctx context.Context, hits []domain.SessionSearchHit) []domain.SessionSearchResult {
	results := make([]domain.SessionSearchResult, 0, len(hits))
	sessionIDs := make([]string, 0, len(hits))
	projectIDs := make([]string, 0, len(hits))
	for _, hit := range hits {
		snippet, highlights := SplitSnippet(hit.Snippet)
		results = append(results, domain.SessionSearchResult{
			SessionID:      hit.SessionID,
			ProjectID:      hit.ProjectID,
			Harness:        hit.Harness,
			Kind:           hit.Kind,
			MessageID:      hit.MessageID,
			Seq:            hit.Seq,
			Title:          hit.Title,
			Snippet:        snippet,
			Highlights:     highlights,
			Rank:           hit.Rank,
			CreatedAt:      hit.CreatedAt,
			Machine:        hit.Machine,
			ResumeCommand:  domain.SessionSearchResumeCommand(hit.SessionID),
			SessionMatches: hit.SessionMatches,
			ReviewID:       hit.ReviewID,
			NamedForQuery:  hit.NamedForQuery,
		})
		sessionIDs = append(sessionIDs, hit.SessionID)
		projectIDs = append(projectIDs, hit.ProjectID)
	}
	s.attachNames(ctx, results, sessionIDs, projectIDs)
	return results
}

// Coverage reports what the index currently sees.
func (s *Service) Coverage(ctx context.Context) (domain.SessionSearchCoverage, error) {
	if s == nil || s.store == nil {
		return domain.SessionSearchCoverage{State: "unavailable"}, nil
	}
	counts, err := s.store.CountSessionSearchSourcesByState(ctx)
	if err != nil {
		return domain.SessionSearchCoverage{}, err
	}
	docs, err := s.store.CountSessionSearchDocs(ctx)
	if err != nil {
		return domain.SessionSearchCoverage{}, err
	}
	coverage := domain.SessionSearchCoverage{
		State:              s.currentState(counts),
		IndexedSources:     counts[domain.SessionSearchSourceActive],
		PendingSources:     counts[domain.SessionSearchSourcePending],
		UnsupportedSources: counts[domain.SessionSearchSourceUnsupported],
		UnavailableSources: counts[domain.SessionSearchSourceUnavailable],
		IndexedDocs:        docs,
	}
	s.mu.Lock()
	coverage.BackfilledAt = s.backfilledAt
	s.mu.Unlock()
	if coverage.UnsupportedSources > 0 {
		coverage.UnindexedHarnesses = s.unsupportedHarnesses(ctx)
	}
	return coverage, nil
}

func (s *Service) coverageOrEmpty(ctx context.Context) domain.SessionSearchCoverage {
	coverage, err := s.Coverage(ctx)
	if err != nil {
		// Coverage is a note, never the reason a search fails.
		s.log.Warn("session search coverage unavailable", "error", err)
		return domain.SessionSearchCoverage{}
	}
	return coverage
}

func (s *Service) currentState(counts map[domain.SessionSearchSourceState]int64) string {
	s.mu.Lock()
	state := s.state
	firstPass := s.backfilledAt.IsZero()
	s.mu.Unlock()
	switch {
	case state == "unavailable":
		return state
	case counts[domain.SessionSearchSourcePending] > 0:
		return "backfilling"
	case firstPass && (state == "backfilling" || counts[domain.SessionSearchSourceActive] == 0):
		// Until this process's first full pass finishes, an empty index, or
		// one that pass is still filling, is not complete. Reporting "ready"
		// there made a fresh daemon answer "no results" with no "still
		// indexing" note for its first half minute.
		return "backfilling"
	}
	return "ready"
}

// unsupportedHarnesses names harnesses whose transcripts the index cannot read,
// so the UI can say what it does not cover instead of silently returning fewer
// results. It reads the recorded sources rather than a hardcoded list, so the
// note cannot drift from what was actually attempted.
func (s *Service) unsupportedHarnesses(ctx context.Context) []string {
	counts, err := s.store.CountSessionSearchSourcesByState(ctx)
	if err != nil || counts[domain.SessionSearchSourceUnsupported] == 0 {
		return nil
	}
	targets, err := s.store.ListSessionSearchTargets(ctx)
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var harnesses []string
	for _, target := range targets {
		if target.Harness == "" {
			continue
		}
		source, ok, readErr := s.store.GetSessionSearchSource(ctx, NativeSourceKey(target.Harness, string(target.ID)))
		if readErr != nil || !ok || source.State != domain.SessionSearchSourceUnsupported {
			continue
		}
		if _, duplicate := seen[string(target.Harness)]; duplicate {
			continue
		}
		seen[string(target.Harness)] = struct{}{}
		harnesses = append(harnesses, string(target.Harness))
	}
	sort.Strings(harnesses)
	return harnesses
}

func (s *Service) attachNames(ctx context.Context, results []domain.SessionSearchResult, sessionIDs, projectIDs []string) {
	if s.namer == nil || len(results) == 0 {
		return
	}
	sessions, projects, err := s.namer.SessionSearchNames(ctx, sessionIDs, projectIDs)
	if err != nil {
		s.log.Warn("session search names unavailable", "error", err)
		return
	}
	for index := range results {
		results[index].SessionName = sessions[results[index].SessionID]
		results[index].ProjectName = projects[results[index].ProjectID]
	}
	states, ok := s.namer.(StateNamer)
	if !ok {
		return
	}
	bySession, err := states.SessionSearchStates(ctx, sessionIDs)
	if err != nil {
		s.log.Warn("session search states unavailable", "error", err)
		return
	}
	for index := range results {
		results[index].SessionState = bySession[results[index].SessionID]
	}
}

// SplitSnippet converts the indexer's sentinel-marked snippet into plain text
// plus byte ranges. Nothing from the index is ever rendered as markup.
func SplitSnippet(snippet string) (string, []domain.SessionSearchHighlight) {
	if !strings.ContainsRune(snippet, sentinelStart) {
		return snippet, nil
	}
	var text strings.Builder
	var highlights []domain.SessionSearchHighlight
	open, units := -1, 0
	for _, character := range snippet {
		switch character {
		case sentinelStart:
			open = units
		case sentinelEnd:
			if open >= 0 {
				highlights = append(highlights, domain.SessionSearchHighlight{Start: open, End: units})
				open = -1
			}
		default:
			text.WriteRune(character)
			// Offsets count UTF-16 code units, which is how the renderer
			// indexes the string (see domain.SessionSearchHighlight).
			units += utf16.RuneLen(character)
		}
	}
	return text.String(), highlights
}

const (
	sentinelStart = '\x02'
	sentinelEnd   = '\x03'
)

// ChatSourceKey is the source key for a session's own AO conversation.
func ChatSourceKey(sessionID string) string { return "chat:" + sessionID }

// ReviewSourceKey is the source key for a reviewer chat. The store reads the
// review id back out of it, so the key is the only place it is recorded.
func ReviewSourceKey(reviewID string) string { return "review:" + reviewID }

// sessionMetadata is the session metadata a title document makes findable:
// its branch and its project. Branch names are identifiers ("fix/flaky-login"),
// so searchableText splits them into their words.
func sessionMetadata(branch string, project domain.ProjectID) string {
	parts := make([]string, 0, 2)
	for _, value := range []string{strings.TrimSpace(branch), strings.TrimSpace(string(project))} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n")
}

// conversationSourceKey is the source a conversation indexes into.
func conversationSourceKey(conversation domain.SessionSearchConversation) string {
	if conversation.ReviewID != "" {
		return ReviewSourceKey(conversation.ReviewID)
	}
	return ChatSourceKey(string(conversation.SessionID))
}

// NativeSourceKey is the source key for one session's native transcript. The
// session id is part of the key (not the file path) so a transcript that is
// moved or rewritten keeps one cursor row.
func NativeSourceKey(harness domain.AgentHarness, sessionID string) string {
	return "native:" + string(harness) + ":" + sessionID
}

// IndexChat indexes one Chat conversation: its title, every visible message,
// and every activity (commands with output, tool calls, file changes, plans,
// reasoning). It rewrites the conversation when its fingerprint moved and prunes
// everything the current view no longer shows, which is what makes a rollback,
// an edit, or a deleted message disappear from results.
func (s *Service) IndexChat(ctx context.Context, conversation domain.SessionSearchConversation) (int, error) {
	return s.indexChatWithStamp(ctx, conversation, nil)
}

// indexChatWithStamp is IndexChat with an optional pre-read aggregate. The sweep
// passes one so an unchanged conversation is skipped without loading its
// timeline; the backfill passes nil because it must read everything anyway.
func (s *Service) indexChatWithStamp(ctx context.Context, conversation domain.SessionSearchConversation, stamp *domain.SessionSearchConversationStamp) (int, error) {
	if s == nil || s.store == nil {
		return 0, errors.New("session search: store is required")
	}
	sourceKey := conversationSourceKey(conversation)
	previous, hadPrevious, err := s.store.GetSessionSearchSource(ctx, sourceKey)
	if err != nil {
		return 0, err
	}
	// The stamp is the cheap skip: if the aggregate is unchanged and the last
	// pass succeeded, there is nothing to read.
	if stamp != nil && hadPrevious && previous.State == domain.SessionSearchSourceActive &&
		previous.Stamp == stampFingerprint(*stamp) {
		return 0, nil
	}
	messages, err := s.store.SessionSearchVisibleMessages(ctx, conversation.ID)
	if err != nil {
		return 0, fmt.Errorf("read conversation messages: %w", err)
	}
	activities, err := s.store.SessionSearchActivities(ctx, conversation.ID)
	if err != nil {
		return 0, fmt.Errorf("read conversation activities: %w", err)
	}
	paths, err := s.store.SessionSearchTouchedPaths(ctx, conversation.ID)
	if err != nil {
		return 0, fmt.Errorf("read conversation paths: %w", err)
	}
	fingerprint := chatFingerprint(conversation, messages, activities, paths)
	if hadPrevious && previous.State == domain.SessionSearchSourceActive && previous.Fingerprint == fingerprint {
		// Nothing the index can see changed. This is the incremental path: the
		// reads above are bounded by one conversation, and no writes happen.
		return 0, nil
	}
	now := s.passTime(previous, hadPrevious)
	docs := make([]domain.SessionSearchDoc, 0, len(messages)+len(activities)+1)
	var highest int64
	// The title a person searches for is the name they see in the sidebar; the
	// provider's thread title is the fallback when the session was never named.
	title := truncateBytes(domain.SessionSearchTitle(conversation.DisplayName, conversation.ProviderTitle), maxTitleBytes)
	metadata := sessionMetadata(conversation.Branch, conversation.ProjectID)
	// A reviewer chat shares its session's name; the session's own transcript
	// already holds the title document, and a second one would double-count it.
	if (title != "" || metadata != "") && conversation.ReviewID == "" {
		docs = append(docs, domain.SessionSearchDoc{
			SourceKey: sourceKey,
			DocKey:    "title",
			SessionID: string(conversation.SessionID),
			ProjectID: string(conversation.ProjectID),
			Harness:   string(conversation.Harness),
			Machine:   s.machine,
			Kind:      domain.SessionSearchKindTitle,
			Title:     title,
			// The title column carries the name at bm25 weight 8; the body
			// repeats it and adds the session's metadata at weight 1, so one
			// blended score ranks a name match above a branch match above
			// neither.
			Body: searchableText(strings.TrimSpace(
				domain.SessionSearchTitle(conversation.DisplayName, conversation.ProviderTitle) + "\n" + metadata)),
			// A name is as old as its session. Dating it by this pass made every
			// session whose name matched look newest under sort:recent and pass
			// any after: filter.
			CreatedAt: firstNonZeroTime(conversation.CreatedAt, now),
			IndexedAt: now,
		})
	}
	for _, message := range messages {
		if message.Role != domain.MessageRoleUser && message.Role != domain.MessageRoleAssistant {
			continue
		}
		body := searchableText(message.Text)
		if strings.TrimSpace(body) == "" {
			continue
		}
		kind := domain.SessionSearchKindAssistant
		if message.Role == domain.MessageRoleUser {
			kind = domain.SessionSearchKindUser
		}
		docs = append(docs, domain.SessionSearchDoc{
			SourceKey: sourceKey,
			DocKey:    message.ID,
			SessionID: string(conversation.SessionID),
			ProjectID: string(conversation.ProjectID),
			Harness:   string(conversation.Harness),
			Machine:   s.machine,
			Kind:      kind,
			MessageID: message.ID,
			Seq:       message.Sequence,
			Body:      body,
			CreatedAt: message.CreatedAt,
			IndexedAt: now,
		})
		highest = max(highest, message.Sequence)
	}
	for _, activity := range activities {
		doc, ok := s.activityDoc(conversation, activity, now)
		if !ok {
			continue
		}
		docs = append(docs, doc)
		highest = max(highest, activity.Sequence)
	}
	// Touched paths are indexed by each file_change activity above, so one
	// session has one path document per edit rather than a single undifferentiated
	// list. `paths` is still part of the fingerprint: a path that disappeared from
	// the activity list must trigger a rewrite.
	_ = paths
	if err := s.writeDocs(ctx, docs); err != nil {
		return 0, err
	}
	if err := s.store.PruneSessionSearchDocsBefore(ctx, sourceKey, now); err != nil {
		return 0, err
	}
	count, err := s.store.CountSessionSearchDocsForSource(ctx, sourceKey)
	if err != nil {
		return 0, err
	}
	source := domain.SessionSearchSource{
		SourceKey:   sourceKey,
		Kind:        domain.SessionSearchSourceChat,
		SessionID:   string(conversation.SessionID),
		ProjectID:   string(conversation.ProjectID),
		Harness:     string(conversation.Harness),
		CursorValue: highest,
		Fingerprint: fingerprint,
		DocCount:    count,
		// Record the stamp this pass compared, never a locally-derived one: the
		// sweep compares against the aggregate query's exact shape, and a stamp
		// computed from only the rows in hand can never match it. A backfill has
		// no stamp to record, so it stores none and the next sweep reads once,
		// records the real one, and skips from then on.
		Stamp:     stampValue(stamp),
		State:     domain.SessionSearchSourceActive,
		UpdatedAt: now,
	}
	if err := s.store.UpsertSessionSearchSource(ctx, source); err != nil {
		return 0, err
	}
	return len(docs), nil
}

// Per-section budgets for one activity document. They sum to the document
// budget, so the assembled body never has to be truncated from the front —
// which would discard exactly the end of a failing command's output that the
// document exists to keep.
const (
	activitySummaryBytes  = 1 << 10
	activityDetailBytes   = 2 << 10
	activityOutputBytes   = 4 << 10
	activityStreamedBytes = 1 << 10
	// activityDiffBytes bounds the diff text indexed for one file change. A large
	// patch is mostly context lines; the changed lines carry the signal.
	activityDiffBytes = 6 << 10
)

// activityDoc turns one timeline activity into a searchable document.
//
// The kind decides the tier: a shell command and its output rank with tool
// output, while a plan or reasoning summary is agent-authored prose. The body
// concatenates what a reader would see: the summary, the file paths or tool
// arguments from the detail payload, the command output, and the streamed text.
// Output is tail-truncated because the useful part of a failing command is at
// its end.
func (s *Service) activityDoc(conversation domain.SessionSearchConversation, activity domain.SessionSearchActivity, now time.Time) (domain.SessionSearchDoc, bool) {
	body := strings.Builder{}
	appendSection := func(text string) {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return
		}
		if body.Len() > 0 {
			body.WriteString("\n\n")
		}
		body.WriteString(trimmed)
	}
	appendSection(truncateBytes(activity.Summary, activitySummaryBytes))
	if activity.Kind == domain.ActivityKindFileChange {
		// A file change's detail is provider JSON. Indexing it verbatim makes
		// snippets unreadable, so the paths and the diff text are extracted and
		// indexed instead: the added and removed lines are the reason someone
		// searches for a function or a string they remember changing.
		appendSection(strings.Join(pathsFromActivityDetail(activity.Detail), "\n"))
		appendSection(diffText(activity.Detail, activityDiffBytes))
	} else {
		// Other details are provider JSON too (a tool's arguments, an MCP
		// result). Its text values are what was typed or returned; indexed raw,
		// the keys spent the budget and snippets showed escapes such as
		// "\u003c" and a literal "\n".
		appendSection(truncateBytes(rawJSONText(json.RawMessage(activity.Detail)), activityDetailBytes))
	}
	appendSection(tailText(activity.Output, activityOutputBytes))
	appendSection(tailText(activity.Streamed, activityStreamedBytes))
	if strings.TrimSpace(body.String()) == "" {
		return domain.SessionSearchDoc{}, false
	}
	kind := domain.SessionSearchKindTool
	paths := ""
	switch activity.Kind {
	case domain.ActivityKindCommand:
		kind = domain.SessionSearchKindCommand
	case domain.ActivityKindReasoning, domain.ActivityKindPlan:
		kind = domain.SessionSearchKindAssistant
	case domain.ActivityKindFileChange:
		kind = domain.SessionSearchKindPath
		// A file change is exactly the row that carries paths, so its own paths
		// are what path: should match.
		paths = searchableText(strings.Join(pathsFromActivityDetail(activity.Detail), "\n"))
	}
	// Tool and command rows used to copy the session's touched paths into their
	// own paths column. path: filters whole sessions, so that copy never helped
	// a filter, but it gave every tool row the paths column's bm25 weight for
	// any word in any file the session touched, which let a tool call outrank
	// the agent's reply about that word. Paths now live only on file changes.
	return domain.SessionSearchDoc{
		SourceKey: conversationSourceKey(conversation),
		DocKey:    "activity:" + activity.ID,
		SessionID: string(conversation.SessionID),
		ProjectID: string(conversation.ProjectID),
		Harness:   string(conversation.Harness),
		Machine:   s.machine,
		Kind:      kind,
		MessageID: activity.AnchorMessageID,
		Seq:       activity.Sequence,
		Paths:     paths,
		// The sections are already within the document budget, so this only
		// appends identifier splits; it never truncates the tail away.
		Body:      domain.SessionSearchSplitIdentifiers(body.String(), maxIdentifierSplits),
		CreatedAt: activity.CreatedAt,
		IndexedAt: now,
	}, true
}

// diffText extracts the changed lines from a file_change activity's detail.
//
// The detail is provider JSON with a per-file "patch" holding unified diff text.
// Only added and removed lines are kept: context lines are the surrounding code
// a person already knows, while the changed lines are what they remember
// writing. Dropping the +/- markers keeps the indexed words clean, so a search
// for a function name does not depend on guessing the marker.
func diffText(detail string, limit int) string {
	var builder strings.Builder
	rest := detail
	for {
		index := strings.Index(rest, `"patch"`)
		if index < 0 {
			break
		}
		rest = rest[index+len(`"patch"`):]
		colon := strings.IndexByte(rest, ':')
		if colon < 0 {
			break
		}
		rest = strings.TrimLeft(rest[colon+1:], " \t")
		if rest == "" || rest[0] != '"' {
			continue
		}
		patch, remainder, ok := readJSONString(rest[1:])
		if !ok {
			break
		}
		rest = remainder
		for _, line := range strings.Split(patch, "\n") {
			if line == "" {
				continue
			}
			switch line[0] {
			case '+', '-':
				// Skip the file headers ("+++ b/x", "--- a/x"); they duplicate the
				// path already indexed above.
				if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
					continue
				}
				builder.WriteString(strings.TrimSpace(line[1:]))
				builder.WriteByte('\n')
			}
			if builder.Len() >= limit {
				return truncateBytes(builder.String(), limit)
			}
		}
	}
	return truncateBytes(builder.String(), limit)
}

// readJSONString decodes one JSON string body (the opening quote already
// consumed) and returns it plus the remainder after the closing quote.
func readJSONString(raw string) (string, string, bool) {
	var value strings.Builder
	escaped := false
	for index := 0; index < len(raw); index++ {
		character := raw[index]
		if escaped {
			switch character {
			case 'n':
				value.WriteByte('\n')
			case 't':
				value.WriteByte('\t')
			case 'r':
				value.WriteByte('\r')
			case 'u':
				// A unicode escape is four hex digits; keep them literal rather
				// than decoding, because the index only needs the words and a
				// mis-decoded escape is worse than a literal one.
				if index+4 < len(raw) {
					value.WriteString(raw[index : index+4])
					index += 3
				}
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
			return value.String(), raw[index+1:], true
		}
		value.WriteByte(character)
	}
	return "", "", false
}

// pathsFromActivityDetail extracts the file paths from a file_change activity's
// provider payload. It is shared by the indexer and the store's path reader.
func pathsFromActivityDetail(detail string) []string {
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

// stampFingerprint renders a conversation stamp as the string stored on its
// source row. The sweep compares strings, so a stamp that differs in any part
// triggers a real read.
func stampFingerprint(stamp domain.SessionSearchConversationStamp) string {
	return fmt.Sprintf("v%d:%d:%d:%d:%d:%d", indexFormatVersion,
		stamp.MessageCount, stamp.MessageSeqMax, stamp.ActivityCount, stamp.ActivitySeqMax, stamp.UpdatedAt)
}

// indexFormatVersion is the shape of the documents a Chat source produces. It
// is part of both the stamp and the fingerprint, so bumping it makes the next
// sweep re-index every conversation once, even though none of them changed.
// Without it, a change to how documents are built would only reach sessions
// that happen to receive a new message.
//
//	1: the original format.
//	2: tool and command rows no longer copy the session's paths.
//	3: activities open at their turn's message; titles carry branch and project.
//	4: titles are dated by their session, not the pass; tool details are
//	   indexed as their text values rather than raw JSON.
const indexFormatVersion = 4

// stampValue renders the stamp a pass compared, or the empty string when the
// pass had none (a backfill, which reads everything anyway).
func stampValue(stamp *domain.SessionSearchConversationStamp) string {
	if stamp == nil {
		return ""
	}
	return stampFingerprint(*stamp)
}

// chatFingerprint identifies the exact visible content of a conversation: its
// title, every visible message id and length, every activity id and revision
// witness, and its touched paths. A rollback, an edit, or a deleted message
// changes it even though the sequence head may not move, which keeps the
// incremental skip honest.
func chatFingerprint(
	conversation domain.SessionSearchConversation,
	messages []domain.SessionSearchMessage,
	activities []domain.SessionSearchActivity,
	paths []string,
) string {
	digest := sha256.New()
	_, _ = fmt.Fprintf(digest, "v%d\x00%s\x00%d\x00%s\x00", indexFormatVersion,
		conversation.ProviderTitle, conversation.LatestSequence, conversation.ActiveBranchID)
	for _, message := range messages {
		_, _ = fmt.Fprintf(digest, "%s:%d:%d\x00", message.ID, message.Sequence, len(message.Text))
	}
	for _, activity := range activities {
		_, _ = fmt.Fprintf(digest, "%s:%d:%d:%d:%d:%d\x00",
			activity.ID, activity.Sequence, len(activity.Summary), len(activity.Detail),
			len(activity.Output), len(activity.Streamed))
	}
	for _, path := range paths {
		_, _ = fmt.Fprintf(digest, "%s\x00", path)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// passTime returns a timestamp strictly after the source's previous pass, so
// "prune anything older than this pass" cannot keep a stale document that was
// written in the same clock tick. Timestamps are stored in milliseconds, so the
// step is a millisecond — a microsecond would round away and leave the
// comparison equal.
func (s *Service) passTime(previous domain.SessionSearchSource, hadPrevious bool) time.Time {
	now := s.now().Truncate(time.Millisecond)
	if hadPrevious {
		previousPass := previous.UpdatedAt.Truncate(time.Millisecond)
		if !now.After(previousPass) {
			return previousPass.Add(time.Millisecond)
		}
	}
	return now
}

// writeTitleDoc indexes a session's own name and first prompt. It is the one
// document every session can have, whatever its harness or state, so it is
// shared by the Chat, native, and unsupported paths.
func (s *Service) writeTitleDoc(ctx context.Context, sourceKey string, target domain.SessionSearchTarget) {
	title := strings.TrimSpace(target.DisplayName)
	if title == "" {
		// An unnamed session is still findable by the prompt it was spawned with.
		title = strings.TrimSpace(firstNonEmpty(target.LatestPrompt, target.Prompt))
	}
	if title == "" {
		return
	}
	doc := domain.SessionSearchDoc{
		SourceKey: sourceKey,
		DocKey:    "title",
		SessionID: string(target.ID),
		ProjectID: string(target.ProjectID),
		Harness:   string(target.Harness),
		Machine:   s.machine,
		Kind:      domain.SessionSearchKindTitle,
		Title:     truncateBytes(title, maxTitleBytes),
		Body: searchableText(strings.TrimSpace(firstNonEmpty(target.LatestPrompt, target.Prompt) + "\n" +
			sessionMetadata(target.Branch, target.ProjectID))),
		CreatedAt: target.CreatedAt,
		IndexedAt: s.now(),
	}
	if err := s.writeDocs(ctx, []domain.SessionSearchDoc{doc}); err != nil {
		s.log.Warn("session search title index failed", "source", sourceKey, "error", err)
	}
}

// writeDocs batches documents so one pass is a handful of transactions.
func (s *Service) writeDocs(ctx context.Context, docs []domain.SessionSearchDoc) error {
	for start := 0; start < len(docs); start += batchSize {
		end := min(start+batchSize, len(docs))
		if err := s.store.UpsertSessionSearchDocs(ctx, docs[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// IndexAll backfills every session, in pages, yielding between them. It returns
// how many documents were written. A failure on one source is recorded on that
// source and does not stop the rest: an unreadable transcript must not hide the
// conversations that are readable.
//
// A backfill that fails, other than by shutting down, leaves the state
// "unavailable" rather than "backfilling": nothing is filling the index, so
// "still indexing" would promise results that never come. The daemon retries it.
func (s *Service) IndexAll(ctx context.Context) (int, error) {
	if s == nil || s.store == nil {
		return 0, errors.New("session search: store is required")
	}
	written, err := s.indexAll(ctx)
	if err != nil && ctx.Err() == nil {
		s.mu.Lock()
		s.state = "unavailable"
		s.mu.Unlock()
	}
	return written, err
}

// Backfilled reports whether a full backfill has completed in this process.
func (s *Service) Backfilled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.backfilledAt.IsZero()
}

func (s *Service) indexAll(ctx context.Context) (int, error) {
	s.mu.Lock()
	s.state = "backfilling"
	s.mu.Unlock()
	conversations, err := s.store.ListSessionSearchConversations(ctx)
	if err != nil {
		return 0, err
	}
	targets, err := s.store.ListSessionSearchTargets(ctx)
	if err != nil {
		return 0, err
	}
	// A session can hold its own transcript and reviewer chats; each is its own
	// source.
	bySession := make(map[string][]domain.SessionSearchConversation, len(conversations))
	for _, conversation := range conversations {
		key := string(conversation.SessionID)
		bySession[key] = append(bySession[key], conversation)
	}
	// The backfill reads every conversation anyway, so it takes the stamps too
	// and records them. Without this, the first sweep after a restart would
	// re-read the whole archive just to learn what the backfill already knew.
	stamps, stampErr := s.store.SessionSearchConversationStamps(ctx)
	if stampErr != nil {
		s.log.Warn("session search stamps unavailable during backfill", "error", stampErr)
		stamps = nil
	}
	written := 0
	for start := 0; start < len(targets); start += pageSize {
		end := min(start+pageSize, len(targets))
		for _, target := range targets[start:end] {
			if err := ctx.Err(); err != nil {
				return written, err
			}
			for _, conversation := range bySession[string(target.ID)] {
				var stamp *domain.SessionSearchConversationStamp
				if stamps != nil {
					if found, has := stamps[conversation.ID]; has {
						stamp = &found
					}
				}
				count, indexErr := s.indexChatWithStamp(ctx, conversation, stamp)
				if indexErr != nil {
					s.recordSourceError(ctx, conversationSourceKey(conversation), domain.SessionSearchSourceChat, target, indexErr)
					continue
				}
				written += count
			}
			if s.indexNativeSession(ctx, target) {
				written++
			}
		}
		// Yield so a backfill cannot hold the writer connection or a CPU while
		// other sessions are working.
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	s.mu.Lock()
	s.state = "ready"
	s.backfilledAt = s.now()
	s.mu.Unlock()
	return written, nil
}

// Sweep indexes what changed since the last pass: conversations whose content
// fingerprint moved and transcripts whose size or mtime moved. It is cheap
// enough to run on a timer because unchanged sources cost one read of their own
// rows and one comparison.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	if s == nil || s.store == nil {
		return 0, errors.New("session search: store is required")
	}
	conversations, err := s.store.ListSessionSearchConversations(ctx)
	if err != nil {
		return 0, err
	}
	// One grouped query for the whole archive, then a per-conversation skip that
	// never loads message or activity text. This is what keeps a 30-second sweep
	// cheap as the archive grows.
	stamps, err := s.store.SessionSearchConversationStamps(ctx)
	if err != nil {
		// The stamp is an optimization; without it, fall back to reading each
		// conversation rather than failing the sweep.
		s.log.Warn("session search stamps unavailable; sweeping without the skip", "error", err)
		stamps = nil
	}
	written := 0
	for _, conversation := range conversations {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		var stamp *domain.SessionSearchConversationStamp
		if stamps != nil {
			if found, ok := stamps[conversation.ID]; ok {
				stamp = &found
			}
		}
		count, indexErr := s.indexChatWithStamp(ctx, conversation, stamp)
		if indexErr != nil {
			s.log.Warn("session search chat index failed", "conversation", conversation.ID, "error", indexErr)
			continue
		}
		written += count
	}
	targets, err := s.store.ListSessionSearchTargets(ctx)
	if err != nil {
		return written, err
	}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		if s.indexNativeSession(ctx, target) {
			written++
		}
	}
	return written, nil
}

// recordSourceError marks a source unavailable without deleting what it already
// contributed, so a temporarily missing file does not erase history.
func (s *Service) recordSourceError(ctx context.Context, sourceKey string, kind domain.SessionSearchSourceKind, target domain.SessionSearchTarget, cause error) {
	previous, had, err := s.store.GetSessionSearchSource(ctx, sourceKey)
	if err != nil {
		s.log.Warn("session search source read failed", "source", sourceKey, "error", err)
		return
	}
	source := domain.SessionSearchSource{
		SourceKey: sourceKey,
		Kind:      kind,
		SessionID: string(target.ID),
		ProjectID: string(target.ProjectID),
		Harness:   string(target.Harness),
		State:     domain.SessionSearchSourceUnavailable,
		UpdatedAt: s.now(),
	}
	if had {
		source = previous
		source.State = domain.SessionSearchSourceUnavailable
		source.UpdatedAt = s.now()
	}
	source.LastErrorCode = errorCode(cause)
	if err := s.store.UpsertSessionSearchSource(ctx, source); err != nil {
		s.log.Warn("session search source update failed", "source", sourceKey, "error", err)
	}
	s.log.Warn("session search source unavailable", "source", sourceKey, "error", cause)
}

// errorCode reduces an error to a stable, non-sensitive code for the source
// row: the index records why a source is stale, never a filesystem path or a
// provider message.
func errorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "cancelled"
	case errors.Is(err, io.EOF):
		return "truncated"
	case errors.Is(err, errUnsupportedHarness):
		return "unsupported_harness"
	default:
		return "read_failed"
	}
}

// searchableText prepares text for indexing: it truncates to the document
// budget and expands identifiers and paths into their parts, so "handleSubmit"
// is findable as both the whole identifier and "submit".
func searchableText(value string) string {
	trimmed := truncateBytes(value, maxDocBodyBytes)
	if trimmed == "" {
		return ""
	}
	return domain.SessionSearchSplitIdentifiers(trimmed, maxIdentifierSplits)
}

// tailText keeps the END of a long text. Command output is read to diagnose a
// failure, and the failure is at the end.
func tailText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	start := len(value) - limit
	for start < len(value) && !utf8.ValidString(value[start:]) {
		start++
	}
	return value[start:]
}

// truncateBytes shortens a string to at most limit bytes without splitting a
// UTF-8 sequence, which keeps the FTS index valid text.
func truncateBytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	trimmed := value[:limit]
	for trimmed != "" && !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
