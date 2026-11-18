// Package sessionsearch owns the local full-text index over past AO sessions
// and the ranked query the desktop UI, the Cmd+K palette, and `ao search` share.
//
//   - It is READ-ONLY toward session data. It writes only its own index tables
//     and never modifies a session row, a conversation row, or a provider
//     transcript. Native transcripts are opened read-only and read forward.
//   - It is incremental and bounded. Chat is re-indexed per conversation only
//     when its content fingerprint moved; native transcripts advance by byte
//     offset; backfill walks sessions in pages so the daemon's single writer
//     connection is never monopolized and memory stays flat.
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
	// maxMessageChunks bounds how many documents one long message becomes. A
	// message past maxDocBodyBytes is split rather than cut, so a long pasted
	// prompt stays searchable to its end; the bound keeps one pasted log from
	// turning into hundreds of documents.
	maxMessageChunks = 8
	// chunkOverlapBytes is how much of a chunk's end the next chunk repeats,
	// on top of its own maxDocBodyBytes. Each chunk is its own document and a
	// quoted phrase must sit inside one, so without the repeat a phrase across
	// a cut is in neither. A phrase is found when the part of it before the
	// cut fits in the overlap.
	chunkOverlapBytes = 256
	// chunkOverlapWordBytes is how far back the overlap may reach to start at
	// the beginning of a word instead of inside one.
	chunkOverlapWordBytes = 64
	// maxTitleBytes caps a title document.
	maxTitleBytes = 512
	// maxIdentifierSplits bounds how many identifiers per document are expanded
	// into their parts, so a huge command output cannot triple the index.
	maxIdentifierSplits = 200
	// batchSize is how many documents one transaction writes. The index has
	// its own database file, so its transactions hold only that file's write
	// lock: nothing else in the daemon waits for them.
	batchSize = 256
	// orphanSessionsPerSweep bounds how many deleted sessions one sweep cleans.
	orphanSessionsPerSweep = 8
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
	SessionSearchMatchCounts(ctx context.Context, matchQuery string, sessionIDs []string) (map[string]int, error)
	CountSessionSearchDocsMatching(ctx context.Context, params domain.SessionSearchParams) (int64, error)
	CountSessionSearchSessionsBrowsing(ctx context.Context, params domain.SessionSearchParams) (int64, error)
	UpsertSessionSearchDocs(ctx context.Context, docs []domain.SessionSearchDoc) error
	DeleteSessionSearchDocsForSource(ctx context.Context, sourceKey string) error
	SessionSearchSourceDocs(ctx context.Context, sourceKey string) ([]domain.SessionSearchDocRef, error)
	SessionSearchDocFingerprint(ctx context.Context, sourceKey, docKey string, kind domain.SessionSearchKind) (int64, bool, error)
	SessionSearchUnsupportedHarnesses(ctx context.Context) ([]string, error)
	DeleteSessionSearchDocsByID(ctx context.Context, ids []int64) error
	SessionSearchOrphanSessions(ctx context.Context, limit int) ([]string, error)
	MarkSessionSearchSessionsMissing(ctx context.Context) (int, error)
	DeleteSessionSearchSession(ctx context.Context, sessionID string) error
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
	ResetSessionSearchIndex(ctx context.Context) error
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
	// AOStateDir is AO's own state directory. An AO-managed provider account
	// keeps its config, and so its transcripts, under it (agent-accounts/,
	// harnesses/codex/accounts/), outside the provider's default root. A
	// transcript path a session recorded is read only inside a provider root
	// or this directory.
	AOStateDir string
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
	// Now overrides the clock in tests.
	Now func() time.Time
	// Logger receives indexer warnings. Nil discards them.
	Logger *slog.Logger
}

// Service indexes session content and answers ranked queries over it.
type Service struct {
	store Store
	roots TranscriptRoots
	namer Namer
	prs   PRResolver
	now   func() time.Time
	log   *slog.Logger

	mu           sync.Mutex
	state        string
	backfilledAt time.Time
	// rebuilding is set from a reindex request until its backfill ends, so
	// coverage says "backfilling" while the old index is being replaced.
	rebuilding bool
	// reindex carries a pending reindex request to the index loop, which runs
	// every pass; one slot, so repeated requests collapse into one rebuild.
	reindex chan struct{}
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
		now: now, log: log, state: "ready", reindex: make(chan struct{}, 1),
	}
}

// RequestReindex asks the index loop to empty the index and rebuild it from
// scratch (`ao search --reindex`). It returns at once; coverage reports
// "backfilling" until the rebuild's backfill ends. Session data is untouched.
func (s *Service) RequestReindex() error {
	if s == nil || s.store == nil {
		return apierr.Unavailable("SESSION_SEARCH_UNAVAILABLE",
			"Session search is not available on this daemon")
	}
	s.mu.Lock()
	s.rebuilding = true
	s.mu.Unlock()
	select {
	case s.reindex <- struct{}{}:
	default: // a rebuild is already pending
	}
	return nil
}

// ReindexRequests delivers reindex requests to the index loop.
func (s *Service) ReindexRequests() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.reindex
}

// Reindex empties the index and backfills it from scratch. Only the index
// loop calls it, so it never overlaps another pass. A backfill that fails
// leaves the index to the loop's retry, as at startup.
func (s *Service) Reindex(ctx context.Context) (int, error) {
	if s == nil || s.store == nil {
		return 0, errors.New("session search: store is required")
	}
	defer func() {
		s.mu.Lock()
		s.rebuilding = false
		s.mu.Unlock()
	}()
	if err := s.store.ResetSessionSearchIndex(ctx); err != nil {
		return 0, fmt.Errorf("reset session search index: %w", err)
	}
	s.mu.Lock()
	s.backfilledAt = time.Time{}
	s.mu.Unlock()
	return s.IndexAll(ctx)
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
			params.ExactQuery = parsed.ExactQuery
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
// session's find in chat will step through for the same words.
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
	return params.SessionID != "" || params.ProjectID != "" || params.Harness != "" ||
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
			SessionMatches: hit.SessionMatches,
			ReviewID:       hit.ReviewID,
		})
		sessionIDs = append(sessionIDs, hit.SessionID)
		projectIDs = append(projectIDs, hit.ProjectID)
	}
	s.attachNames(ctx, results, sessionIDs, projectIDs)
	for index := range results {
		results[index].ResumeCommand = domain.SessionSearchResumeCommand(results[index].SessionID, results[index].SessionState)
	}
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
	rebuilding := s.rebuilding
	s.mu.Unlock()
	switch {
	case rebuilding:
		return "backfilling"
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
// note cannot drift from what was actually attempted, in one grouped query:
// coverage is part of every search response.
func (s *Service) unsupportedHarnesses(ctx context.Context) []string {
	harnesses, err := s.store.SessionSearchUnsupportedHarnesses(ctx)
	if err != nil {
		s.log.Warn("session search unsupported harnesses unavailable", "error", err)
		return nil
	}
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
		// Nothing the index can see changed (a status or revision moved). Record
		// the new stamp, or every later sweep would re-read the conversation.
		if current := stampValue(stamp); current != "" && current != previous.Stamp {
			previous.Stamp = current
			previous.UpdatedAt = s.now()
			if err := s.store.UpsertSessionSearchSource(ctx, previous); err != nil {
				return 0, err
			}
		}
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
		kind := domain.SessionSearchKindAssistant
		if message.Role == domain.MessageRoleUser {
			kind = domain.SessionSearchKindUser
		}
		// A long message is several documents with one message id, so it is
		// still one place to open and one match in the row's count.
		for index, chunk := range textChunks(message.Text, maxDocBodyBytes, chunkOverlapBytes, maxMessageChunks) {
			body := domain.SessionSearchSplitIdentifiers(chunk, maxIdentifierSplits)
			if strings.TrimSpace(body) == "" {
				continue
			}
			docs = append(docs, domain.SessionSearchDoc{
				SourceKey: sourceKey,
				DocKey:    chunkDocKey(message.ID, index),
				SessionID: string(conversation.SessionID),
				ProjectID: string(conversation.ProjectID),
				Harness:   string(conversation.Harness),
				Kind:      kind,
				MessageID: message.ID,
				Seq:       message.Sequence,
				Body:      body,
				CreatedAt: message.CreatedAt,
				IndexedAt: now,
			})
		}
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
	written, err := s.writeChanged(ctx, sourceKey, docs, true)
	if err != nil {
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
	return written, nil
}

// Budgets for the short text of an activity that is not a tool call (a plan,
// an approval, an error, an AO action).
const (
	activitySummaryBytes = 1 << 10
	activityDetailBytes  = 2 << 10
)

// activityDoc turns one timeline activity into a searchable document.
//
// A command, an MCP tool or a file change is a tool row (coverage.go): the
// tool's name and its one remembered argument, then the start of its output.
// A file change keeps its paths, which path: and file-name searches use, but
// not its diff. A reasoning activity is not indexed. Anything else (a plan, an
// approval, an error, an AO action) is short text: its summary and the text
// values of its detail.
func (s *Service) activityDoc(conversation domain.SessionSearchConversation, activity domain.SessionSearchActivity, now time.Time) (domain.SessionSearchDoc, bool) {
	kind := domain.SessionSearchKindTool
	paths := ""
	var body string
	switch activity.Kind {
	case domain.ActivityKindReasoning:
		return domain.SessionSearchDoc{}, false
	case domain.ActivityKindCommand, domain.ActivityKindMCPTool, domain.ActivityKindFileChange:
		tool := activityTool(activity.Detail)
		argument := tool.argument
		output := activity.Output
		if strings.TrimSpace(output) == "" {
			output = tool.output
		}
		switch activity.Kind {
		case domain.ActivityKindCommand:
			kind = domain.SessionSearchKindCommand
		case domain.ActivityKindFileChange:
			kind = domain.SessionSearchKindPath
			changed := pathsFromActivityDetail(activity.Detail)
			// A file change is exactly the row that carries paths, so its own
			// paths are what path: should match.
			paths = searchableText(strings.Join(changed, "\n"))
			if argument == "" {
				argument = strings.Join(changed, "\n")
			}
		}
		name := tool.name
		switch {
		case name == "" && argument == "":
			// An activity with no recorded input (an older row): its summary
			// is the command or the call.
			argument = activity.Summary
		case name == "" && activity.Kind != domain.ActivityKindCommand:
			// A tool without a recorded name is named by its summary. A
			// command's summary is the command itself, already the argument.
			name = activity.Summary
		}
		body = toolText(name, argument, output)
	default:
		if activity.Kind == domain.ActivityKindPlan {
			kind = domain.SessionSearchKindAssistant
		}
		body = joinSections(
			truncateBytes(activity.Summary, activitySummaryBytes),
			truncateBytes(rawJSONText(json.RawMessage(activity.Detail)), activityDetailBytes),
			toolOutputText(activity.Output),
		)
	}
	if strings.TrimSpace(body) == "" {
		return domain.SessionSearchDoc{}, false
	}
	// Tool and command rows carry no paths. path: filters whole sessions, and
	// paths on a tool row would give it the paths column's bm25 weight for any
	// word in any file the session touched, letting a tool call outrank the
	// agent's reply about that word. Paths live only on file changes.
	return domain.SessionSearchDoc{
		SourceKey: conversationSourceKey(conversation),
		DocKey:    "activity:" + activity.ID,
		SessionID: string(conversation.SessionID),
		ProjectID: string(conversation.ProjectID),
		Harness:   string(conversation.Harness),
		Kind:      kind,
		MessageID: activity.AnchorMessageID,
		Seq:       activity.Sequence,
		Paths:     paths,
		// The body is already within its caps, so this only appends the parts
		// of split identifiers.
		Body:      domain.SessionSearchSplitIdentifiers(body, maxIdentifierSplits),
		CreatedAt: activity.CreatedAt,
		IndexedAt: now,
	}, true
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
	witness := sha256.Sum256([]byte(stamp.Witness))
	return fmt.Sprintf("v%d:%d:%d:%d:%d:%d:%s", indexFormatVersion,
		stamp.MessageCount, stamp.MessageSeqMax, stamp.ActivityCount, stamp.ActivitySeqMax, stamp.UpdatedAt,
		hex.EncodeToString(witness[:8]))
}

// indexFormatVersion is the shape of the documents a Chat source produces. It
// is part of both the stamp and the fingerprint, so bumping it makes the next
// sweep re-index every conversation once, even though none of them changed.
// Without it, a change to how documents are built would only reach sessions
// that happen to receive a new message. Bump it when those documents change.
const indexFormatVersion = 6

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
	// Every field that reaches a document is hashed whole, length-prefixed so
	// two fields cannot run together. Hashing lengths instead kept an
	// equal-length edit ("red key" to "new key") from ever being re-indexed.
	digest := sha256.New()
	field := func(value string) { _, _ = fmt.Fprintf(digest, "%d:%s", len(value), value) }
	_, _ = fmt.Fprintf(digest, "v%d:%d:", indexFormatVersion, conversation.LatestSequence)
	for _, value := range []string{
		conversation.ProviderTitle, conversation.DisplayName, conversation.Branch, conversation.ActiveBranchID,
		string(conversation.ProjectID), string(conversation.Harness), conversation.ReviewID,
	} {
		field(value)
	}
	for _, message := range messages {
		_, _ = fmt.Fprintf(digest, "m%d:", message.Sequence)
		field(message.ID)
		field(string(message.Role))
		field(message.Text)
	}
	for _, activity := range activities {
		_, _ = fmt.Fprintf(digest, "a%d:", activity.Sequence)
		for _, value := range []string{
			activity.ID, string(activity.Kind), string(activity.Status), activity.Summary,
			activity.Detail, activity.Output, activity.Streamed, activity.AnchorMessageID,
		} {
			field(value)
		}
	}
	for _, path := range paths {
		field(path)
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

// sessionTitleDoc is a session's title document: its name, or the prompt it was
// spawned with when it has none, so every session is findable even when its
// transcript holds nothing AO can read. ok is false when there is neither.
func (s *Service) sessionTitleDoc(sourceKey string, target domain.SessionSearchTarget) (domain.SessionSearchDoc, bool) {
	title := strings.TrimSpace(target.DisplayName)
	if title == "" {
		title = strings.TrimSpace(firstNonEmpty(target.LatestPrompt, target.Prompt))
	}
	if title == "" {
		return domain.SessionSearchDoc{}, false
	}
	return domain.SessionSearchDoc{
		SourceKey: sourceKey,
		DocKey:    "title",
		SessionID: string(target.ID),
		ProjectID: string(target.ProjectID),
		Harness:   string(target.Harness),
		Kind:      domain.SessionSearchKindTitle,
		Title:     truncateBytes(title, maxTitleBytes),
		Body: searchableText(strings.TrimSpace(firstNonEmpty(target.LatestPrompt, target.Prompt) + "\n" +
			sessionMetadata(target.Branch, target.ProjectID))),
		CreatedAt: target.CreatedAt,
		IndexedAt: s.now(),
	}, true
}

// writeTitleDoc writes a session's title document when it changed (a rename,
// say). An unchanged title costs one indexed read and no write, so a source
// with nothing else to index is not rewritten on every sweep. It reports
// whether it wrote.
func (s *Service) writeTitleDoc(ctx context.Context, sourceKey string, target domain.SessionSearchTarget) bool {
	doc, ok := s.sessionTitleDoc(sourceKey, target)
	if !ok {
		return false
	}
	stored, indexed, err := s.store.SessionSearchDocFingerprint(ctx, sourceKey, doc.DocKey, doc.Kind)
	if err != nil {
		s.log.Warn("session search title read failed", "source", sourceKey, "error", err)
	} else if indexed && stored == doc.Fingerprint() {
		return false
	}
	if err := s.writeDocs(ctx, []domain.SessionSearchDoc{doc}); err != nil {
		s.log.Warn("session search title index failed", "source", sourceKey, "error", err)
		return false
	}
	return true
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

// writeChanged writes the documents of a source that are new or changed, and,
// for a source rebuilt whole each pass, deletes the stored ones it no longer
// produces. A long active session therefore writes its new message, not its
// thousands of others.
func (s *Service) writeChanged(ctx context.Context, sourceKey string, docs []domain.SessionSearchDoc, deleteMissing bool) (int, error) {
	refs, err := s.store.SessionSearchSourceDocs(ctx, sourceKey)
	if err != nil {
		return 0, err
	}
	stored := make(map[string]domain.SessionSearchDocRef, len(refs))
	for _, ref := range refs {
		stored[ref.DocKey+"\x00"+string(ref.Kind)] = ref
	}
	changed := make([]domain.SessionSearchDoc, 0)
	kept := make(map[int64]bool, len(docs))
	for _, doc := range docs {
		ref, ok := stored[doc.DocKey+"\x00"+string(doc.Kind)]
		if ok {
			kept[ref.ID] = true
			if ref.Fingerprint == doc.Fingerprint() {
				continue
			}
		}
		changed = append(changed, doc)
	}
	if err := s.writeDocs(ctx, changed); err != nil {
		return 0, err
	}
	if deleteMissing {
		var removed []int64
		for _, ref := range refs {
			if !kept[ref.ID] {
				removed = append(removed, ref.ID)
			}
		}
		if err := s.store.DeleteSessionSearchDocsByID(ctx, removed); err != nil {
			return len(changed), err
		}
	}
	return len(changed), nil
}

// removeDeletedSessions removes the documents of sessions that were deleted.
// The index lives in its own database file, which the session delete cannot
// touch: the delete records the id, search ignores the session at once, and
// this removes its documents.
func (s *Service) removeDeletedSessions(ctx context.Context) {
	orphans, err := s.store.SessionSearchOrphanSessions(ctx, orphanSessionsPerSweep)
	if err != nil {
		s.log.Warn("session search orphan scan failed", "error", err)
		return
	}
	for _, sessionID := range orphans {
		if err := s.store.DeleteSessionSearchSession(ctx, sessionID); err != nil {
			s.log.Warn("session search orphan cleanup failed", "session", sessionID, "error", err)
			return
		}
	}
}

// dropSessionsMissingFromTheDatabase hides, and queues for removal, the
// sessions the index holds that the app database no longer has. The index is a
// separate file, so it can be newer than its database: after a backup of ao.db
// is restored, it still holds the sessions created since. A backfill starts
// with this because a restore happens while the daemon is stopped.
func (s *Service) dropSessionsMissingFromTheDatabase(ctx context.Context) {
	missing, err := s.store.MarkSessionSearchSessionsMissing(ctx)
	if err != nil {
		s.log.Warn("session search could not check its sessions against the database", "error", err)
		return
	}
	if missing > 0 {
		s.log.Info("session search dropped sessions the database no longer has", "sessions", missing)
	}
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
	s.dropSessionsMissingFromTheDatabase(ctx)
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
		}
		s.forEachNativePlan(ctx, targets[start:end], func(plan nativePlan) bool {
			if s.applyNativePlan(ctx, plan) {
				written++
			}
			return ctx.Err() == nil
		})
		if err := ctx.Err(); err != nil {
			return written, err
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
	s.removeDeletedSessions(ctx)
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
	s.forEachNativePlan(ctx, targets, func(plan nativePlan) bool {
		if s.applyNativePlan(ctx, plan) {
			written++
		}
		return ctx.Err() == nil
	})
	return written, ctx.Err()
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

// textChunks splits a text into at most maxChunks pieces of at most limit
// bytes, breaking at a line or space near the limit when there is one. Each
// piece after the first is returned with the last overlap bytes of the one
// before it in front (chunkOverlapStart), so a phrase across a break is whole
// in the later piece. The breaks themselves do not depend on the overlap.
func textChunks(value string, limit, overlap, maxChunks int) []string {
	if len(value) <= limit {
		return []string{value}
	}
	chunks := make([]string, 0, min(maxChunks, len(value)/limit+1))
	start := 0
	for start < len(value) && len(chunks) < maxChunks {
		piece := value[start:]
		if len(piece) > limit {
			piece = truncateBytes(piece, limit)
			// Prefer a break in the last eighth of the piece, so a word is not cut.
			if cut := strings.LastIndexAny(piece, "\n "); cut > limit-limit/8 {
				piece = piece[:cut+1]
			}
		}
		if piece == "" {
			break
		}
		end := start + len(piece)
		chunks = append(chunks, value[chunkOverlapStart(value, start, overlap):end])
		start = end
	}
	return chunks
}

// chunkOverlapStart returns where the chunk whose own text starts at at
// begins once it carries the overlap: overlap bytes earlier, moved back to
// the start of the word there so the overlap holds whole words. A word longer
// than chunkOverlapWordBytes (a hash, a URL) is entered at a character
// boundary instead.
func chunkOverlapStart(value string, at, overlap int) int {
	if overlap <= 0 || at <= 0 {
		return at
	}
	start := at - overlap
	if start <= 0 {
		return 0
	}
	floor := max(start-chunkOverlapWordBytes, 0)
	if cut := strings.LastIndexAny(value[floor:start], "\n "); cut >= 0 {
		return floor + cut + 1
	}
	for start < at && !utf8.RuneStart(value[start]) {
		start++
	}
	return start
}

// chunkDocKey names a message's chunk: the first keeps the message's own key,
// later ones add "#n". Match counts strip the suffix, so a long message is one
// match however many chunks hold the word.
func chunkDocKey(key string, index int) string {
	if index == 0 {
		return key
	}
	return fmt.Sprintf("%s#%d", key, index+1)
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
