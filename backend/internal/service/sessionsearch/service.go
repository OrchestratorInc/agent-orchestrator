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

const (
	maxDocBodyBytes        = 8 << 10
	maxMessageChunks       = 8
	chunkOverlapBytes      = 256
	chunkOverlapWordBytes  = 64
	maxTitleBytes          = 512
	maxIdentifierSplits    = 200
	batchSize              = 256
	orphanSessionsPerSweep = 8
	pageSize               = 50
	nativeReadChunk        = 256 << 10
)

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

type Namer interface {
	SessionSearchNames(ctx context.Context, sessionIDs, projectIDs []string) (map[string]string, map[string]string, error)
}

type StateNamer interface {
	SessionSearchStates(ctx context.Context, sessionIDs []string) (map[string]string, error)
}

type PRResolver interface {
	SessionSearchSessionForPR(ctx context.Context, url string) (string, bool, error)
}

type TranscriptRoots struct {
	ClaudeProjects string
	CodexSessions  string
	CodexArchived  string
	OpenCodeData   string
	OpenCodeV2Data string
	AOStateDir     string
}

type Options struct {
	Store  Store
	Roots  TranscriptRoots
	Namer  Namer
	PRs    PRResolver
	Now    func() time.Time
	Logger *slog.Logger
}

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
	rebuilding   bool
	reindex      chan struct{}
}

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
	default:
	}
	return nil
}

func (s *Service) ReindexRequests() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.reindex
}

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
		params.Sort = domain.SessionSearchSortRecent
		hits, err = s.store.BrowseSessionSearchDocs(ctx, params)
	default:
		hits = nil
	}
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
	matched := hits
	pinned := false
	if prOwner != "" && params.Offset == 0 && params.Group != domain.SessionSearchGroupMessage {
		hits, pinned, err = s.pinSession(ctx, hits, prOwner, params)
		if err != nil {
			return domain.SessionSearchResponse{}, err
		}
	}
	results := s.toResults(ctx, hits)
	total := int64(len(results))
	if match != "" && len(matched) > 0 && !matched[0].MatchTotalCapped {
		total = matched[0].MatchTotal
	} else if match != "" {
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

func hasSessionSearchFilter(params domain.SessionSearchParams) bool {
	return params.SessionID != "" || params.ProjectID != "" || params.Harness != "" ||
		params.Path != "" || params.ExcludeQuery != "" || params.Status != domain.SessionSearchStatusAny ||
		(params.From != nil && !params.From.IsZero()) || (params.To != nil && !params.To.IsZero())
}

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
		return "backfilling"
	}
	return "ready"
}

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
			units += utf16.RuneLen(character)
		}
	}
	return text.String(), highlights
}

const (
	sentinelStart = '\x02'
	sentinelEnd   = '\x03'
)

func ChatSourceKey(sessionID string) string { return "chat:" + sessionID }

func ReviewSourceKey(reviewID string) string { return "review:" + reviewID }

func sessionMetadata(branch string, project domain.ProjectID) string {
	parts := make([]string, 0, 2)
	for _, value := range []string{strings.TrimSpace(branch), strings.TrimSpace(string(project))} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n")
}

func conversationSourceKey(conversation domain.SessionSearchConversation) string {
	if conversation.ReviewID != "" {
		return ReviewSourceKey(conversation.ReviewID)
	}
	return ChatSourceKey(string(conversation.SessionID))
}

func NativeSourceKey(harness domain.AgentHarness, sessionID string) string {
	return "native:" + string(harness) + ":" + sessionID
}

func (s *Service) IndexChat(ctx context.Context, conversation domain.SessionSearchConversation) (int, error) {
	return s.indexChatWithStamp(ctx, conversation, nil)
}

func (s *Service) indexChatWithStamp(ctx context.Context, conversation domain.SessionSearchConversation, stamp *domain.SessionSearchConversationStamp) (int, error) {
	if s == nil || s.store == nil {
		return 0, errors.New("session search: store is required")
	}
	sourceKey := conversationSourceKey(conversation)
	previous, hadPrevious, err := s.store.GetSessionSearchSource(ctx, sourceKey)
	if err != nil {
		return 0, err
	}
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
	title := truncateBytes(domain.SessionSearchTitle(conversation.DisplayName, conversation.ProviderTitle), maxTitleBytes)
	metadata := sessionMetadata(conversation.Branch, conversation.ProjectID)
	if (title != "" || metadata != "") && conversation.ReviewID == "" {
		docs = append(docs, domain.SessionSearchDoc{
			SourceKey: sourceKey,
			DocKey:    "title",
			SessionID: string(conversation.SessionID),
			ProjectID: string(conversation.ProjectID),
			Harness:   string(conversation.Harness),
			Kind:      domain.SessionSearchKindTitle,
			Title:     title,
			Body: searchableText(strings.TrimSpace(
				domain.SessionSearchTitle(conversation.DisplayName, conversation.ProviderTitle) + "\n" + metadata)),
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
		Stamp:       stampValue(stamp),
		State:       domain.SessionSearchSourceActive,
		UpdatedAt:   now,
	}
	if err := s.store.UpsertSessionSearchSource(ctx, source); err != nil {
		return 0, err
	}
	return written, nil
}

const (
	activitySummaryBytes = 1 << 10
	activityDetailBytes  = 2 << 10
)

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
			paths = searchableText(strings.Join(changed, "\n"))
			if argument == "" {
				argument = strings.Join(changed, "\n")
			}
		}
		name := tool.name
		switch {
		case name == "" && argument == "":
			argument = activity.Summary
		case name == "" && activity.Kind != domain.ActivityKindCommand:
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
		Body:      domain.SessionSearchSplitIdentifiers(body, maxIdentifierSplits),
		CreatedAt: activity.CreatedAt,
		IndexedAt: now,
	}, true
}

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

func stampFingerprint(stamp domain.SessionSearchConversationStamp) string {
	witness := sha256.Sum256([]byte(stamp.Witness))
	return fmt.Sprintf("v%d:%d:%d:%d:%d:%d:%s", indexFormatVersion,
		stamp.MessageCount, stamp.MessageSeqMax, stamp.ActivityCount, stamp.ActivitySeqMax, stamp.UpdatedAt,
		hex.EncodeToString(witness[:8]))
}

const indexFormatVersion = 6

func stampValue(stamp *domain.SessionSearchConversationStamp) string {
	if stamp == nil {
		return ""
	}
	return stampFingerprint(*stamp)
}

func chatFingerprint(
	conversation domain.SessionSearchConversation,
	messages []domain.SessionSearchMessage,
	activities []domain.SessionSearchActivity,
	paths []string,
) string {
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

func (s *Service) writeDocs(ctx context.Context, docs []domain.SessionSearchDoc) error {
	for start := 0; start < len(docs); start += batchSize {
		end := min(start+batchSize, len(docs))
		if err := s.store.UpsertSessionSearchDocs(ctx, docs[start:end]); err != nil {
			return err
		}
	}
	return nil
}

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
	bySession := make(map[string][]domain.SessionSearchConversation, len(conversations))
	for _, conversation := range conversations {
		key := string(conversation.SessionID)
		bySession[key] = append(bySession[key], conversation)
	}
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

func (s *Service) Sweep(ctx context.Context) (int, error) {
	if s == nil || s.store == nil {
		return 0, errors.New("session search: store is required")
	}
	s.removeDeletedSessions(ctx)
	conversations, err := s.store.ListSessionSearchConversations(ctx)
	if err != nil {
		return 0, err
	}
	stamps, err := s.store.SessionSearchConversationStamps(ctx)
	if err != nil {
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

func searchableText(value string) string {
	trimmed := truncateBytes(value, maxDocBodyBytes)
	if trimmed == "" {
		return ""
	}
	return domain.SessionSearchSplitIdentifiers(trimmed, maxIdentifierSplits)
}

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

func chunkDocKey(key string, index int) string {
	if index == 0 {
		return key
	}
	return fmt.Sprintf("%s#%d", key, index+1)
}

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
