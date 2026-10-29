package controllers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// SessionSearchService is the controller-facing session search contract.
type SessionSearchService interface {
	Search(ctx context.Context, parsed domain.SessionSearchParsedQuery, params domain.SessionSearchParams) (domain.SessionSearchResponse, error)
	MatchesForSession(ctx context.Context, sessionID, matchQuery string) ([]domain.SessionSearchResult, error)
	Coverage(ctx context.Context) (domain.SessionSearchCoverage, error)
}

// SessionSearchController serves the ranked search over past AO sessions.
//
// The index behind it is local and derived: this route reads it, never the
// provider transcripts themselves, and never writes session data.
type SessionSearchController struct {
	Svc SessionSearchService
}

// Register mounts the session search routes.
func (c *SessionSearchController) Register(r chi.Router) {
	r.Get("/search/sessions", c.search)
	r.Get("/search/index-status", c.indexStatus)
	r.Get("/sessions/{sessionId}/search/matches", c.matches)
}

func (c *SessionSearchController) search(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/search/sessions")
		return
	}
	query, params, err := parseSessionSearchQuery(r)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	result, err := c.Svc.Search(r.Context(), query, params)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, sessionSearchResponse(result))
}

// matches lists every match for one session so Enter/Shift+Enter can step
// through them after a result is opened. It is the in-session half of
// "open at the matching message".
func (c *SessionSearchController) matches(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/search/matches")
		return
	}
	sessionID := chi.URLParam(r, "sessionId")
	parsed := domain.ParseSessionSearchQuery(r.URL.Query().Get("q"))
	matchQuery := parsed.MatchQuery
	if matchQuery == "" && parsed.PRRef != nil {
		// A result opened from a pasted PR URL steps through where the PR is
		// mentioned, the same phrase the search ranked by.
		matchQuery = parsed.PRRef.Phrase()
	}
	if matchQuery == "" {
		envelope.WriteError(w, r, apierr.Invalid("SESSION_SEARCH_QUERY_REQUIRED",
			"Provide q with at least one word to find matches for", nil))
		return
	}
	results, err := c.Svc.MatchesForSession(r.Context(), sessionID, matchQuery)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionSearchMatchesResponse{Matches: sessionSearchResults(results)})
}

// indexStatus reports what the index covers, for `ao search --index-status` and
// for the palette's "still indexing" note.
func (c *SessionSearchController) indexStatus(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/search/index-status")
		return
	}
	coverage, err := c.Svc.Coverage(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionSearchIndexStatusResponse{Coverage: sessionSearchCoverage(coverage)})
}

// SessionSearchQuery is the wire shape of GET /api/v1/search/sessions.
//
// q is required. Every filter is optional and additive: an omitted filter does
// not narrow the search, and an empty result is a normal answer rather than an
// error. Operators typed into q (harness:, project:, before:, !term, …) are
// parsed by the same function the palette and CLI use.
type SessionSearchQuery struct {
	Query     string `query:"q" required:"true" description:"Words to search for, with optional operators such as harness:, project:, status:, before:, after:, path:, !exclude, \"exact phrase\", 'exact, ^prefix."`
	ProjectID string `query:"projectId,omitempty" description:"Restrict results to one project. Equivalent to project: in q."`
	Harness   string `query:"harness,omitempty" description:"Restrict results to one agent harness, such as claude-code or codex."`
	Machine   string `query:"machine,omitempty" description:"Restrict results to one host."`
	Path      string `query:"path,omitempty" description:"Restrict to sessions that touched a path containing this text."`
	Status    string `query:"status,omitempty" enum:"killed,live,hibernated" description:"Restrict to one session lifecycle state."`
	From      string `query:"from,omitempty" description:"Earliest result time, as an RFC 3339 date or date-time."`
	To        string `query:"to,omitempty" description:"Latest result time, as an RFC 3339 date or date-time."`
	Sort      string `query:"sort,omitempty" enum:"relevance,recent" description:"Order by relevance (while typing) or recency (while browsing). Defaults to relevance, or to recent for a filter-only query."`
	Group     string `query:"group,omitempty" enum:"session,message" description:"One result per session at its best match, with sessionMatches counting the rest (session, the default), or one result per matching message (message)."`
	Limit     int    `query:"limit,omitempty" minimum:"1" maximum:"100" description:"Page size. Defaults to 20."`
	Offset    int    `query:"offset,omitempty" minimum:"0" description:"Page offset."`
}

// SessionSearchResponse is the body of GET /api/v1/search/sessions (200).
type SessionSearchResponse struct {
	Query    string                `json:"query"`
	Results  []SessionSearchResult `json:"results"`
	Total    int                   `json:"total"`
	Limit    int                   `json:"limit"`
	Offset   int                   `json:"offset"`
	Sort     string                `json:"sort" enum:"relevance,recent"`
	Group    string                `json:"group" enum:"session,message"`
	Coverage SessionSearchCoverage `json:"coverage"`
}

// SessionSearchMatchesQuery is the query for the in-session match list.
type SessionSearchMatchesQuery struct {
	Query string `query:"q" required:"true" description:"The same words or operators used for the search that opened the session."`
}

// SessionSearchMatchesResponse is the body of the in-session match list (200).
type SessionSearchMatchesResponse struct {
	Matches []SessionSearchResult `json:"matches"`
}

// SessionSearchIndexStatusResponse is the body of GET /api/v1/search/index-status.
type SessionSearchIndexStatusResponse struct {
	Coverage SessionSearchCoverage `json:"coverage"`
}

// SessionSearchResult is one ranked hit. Snippet is plain text; Highlights are
// byte offsets into it, so the renderer never parses markup out of the index.
type SessionSearchResult struct {
	SessionID     string                   `json:"sessionId"`
	ProjectID     string                   `json:"projectId"`
	Harness       string                   `json:"harness"`
	Kind          string                   `json:"kind" enum:"title,user,assistant,tool,command,path"`
	MessageID     string                   `json:"messageId,omitempty"`
	Seq           int64                    `json:"seq"`
	Title         string                   `json:"title,omitempty"`
	Snippet       string                   `json:"snippet"`
	Highlights    []SessionSearchHighlight `json:"highlights,omitempty"`
	Rank          float64                  `json:"rank"`
	CreatedAt     time.Time                `json:"createdAt"`
	SessionName   string                   `json:"sessionName,omitempty"`
	ProjectName   string                   `json:"projectName,omitempty"`
	Machine       string                   `json:"machine,omitempty"`
	SessionState  string                   `json:"sessionState,omitempty"`
	ResumeCommand string                   `json:"resumeCommand,omitempty"`
	// SessionMatches counts the session's matching messages when results are
	// grouped by session, so a row can say "+N more in this session".
	SessionMatches int `json:"sessionMatches,omitempty"`
	// ReviewID is set when the match is in one of the session's reviewer chats;
	// opening the result opens that reviewer chat at the message.
	ReviewID string `json:"reviewId,omitempty"`
}

// SessionSearchHighlight is one matched span inside a result's snippet.
type SessionSearchHighlight struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// SessionSearchCoverage says what the index can see, so a caller can tell an
// empty answer from an incomplete index.
type SessionSearchCoverage struct {
	State              string    `json:"state" enum:"ready,backfilling,unavailable"`
	IndexedSources     int64     `json:"indexedSources"`
	PendingSources     int64     `json:"pendingSources"`
	UnsupportedSources int64     `json:"unsupportedSources"`
	UnavailableSources int64     `json:"unavailableSources"`
	IndexedDocs        int64     `json:"indexedDocs"`
	UnindexedHarnesses []string  `json:"unindexedHarnesses,omitempty"`
	BackfilledAt       time.Time `json:"backfilledAt,omitempty"`
}

// parseSessionSearchQuery splits the request into the parsed query (operators
// from q) and the explicit filters. Explicit filters win only when the operator
// was not typed, which is what lets a palette user override a saved filter.
func parseSessionSearchQuery(r *http.Request) (domain.SessionSearchParsedQuery, domain.SessionSearchParams, error) {
	values := r.URL.Query()
	raw := strings.TrimSpace(values.Get("q"))
	parsed := domain.ParseSessionSearchQuery(raw)
	params := domain.SessionSearchParams{
		ProjectID: strings.TrimSpace(values.Get("projectId")),
		Harness:   strings.TrimSpace(values.Get("harness")),
		Machine:   strings.TrimSpace(values.Get("machine")),
		Path:      strings.TrimSpace(values.Get("path")),
		Status:    domain.SessionSearchStatusFilter(strings.TrimSpace(values.Get("status"))),
		Limit:     domain.NormalizeSessionSearchLimit(atoiOrZero(values.Get("limit"))),
		Offset:    max(atoiOrZero(values.Get("offset")), 0),
	}
	// A query needs words or a filter. The one exception is an explicit recency
	// browse, which is what a palette shows before anything is typed.
	explicitRecents := strings.EqualFold(strings.TrimSpace(values.Get("sort")), string(domain.SessionSearchSortRecent))
	if raw == "" && params.ProjectID == "" && params.Harness == "" && params.Machine == "" &&
		params.Path == "" && params.Status == "" && !explicitRecents {
		return domain.SessionSearchParsedQuery{}, domain.SessionSearchParams{},
			apierr.Invalid("SESSION_SEARCH_QUERY_REQUIRED",
				"Provide q with at least one word, a filter, or sort=recent to browse", nil)
	}
	if err := parsed.Validate(); err != nil {
		return domain.SessionSearchParsedQuery{}, domain.SessionSearchParams{},
			apierr.Invalid("SESSION_SEARCH_INVALID_RANGE", err.Error(), nil)
	}
	from, err := parseSearchBoundary(values.Get("from"))
	if err != nil {
		return domain.SessionSearchParsedQuery{}, domain.SessionSearchParams{},
			apierr.Invalid("SESSION_SEARCH_INVALID_FROM", "from must be an RFC 3339 date or date-time", nil)
	}
	to, err := parseSearchBoundary(values.Get("to"))
	if err != nil {
		return domain.SessionSearchParsedQuery{}, domain.SessionSearchParams{},
			apierr.Invalid("SESSION_SEARCH_INVALID_TO", "to must be an RFC 3339 date or date-time", nil)
	}
	if from != nil && to != nil && from.After(*to) {
		return domain.SessionSearchParsedQuery{}, domain.SessionSearchParams{},
			apierr.Invalid("SESSION_SEARCH_INVALID_RANGE", "from must not be after to", nil)
	}
	params.From = from
	params.To = to
	switch strings.ToLower(strings.TrimSpace(values.Get("sort"))) {
	case string(domain.SessionSearchSortRecent):
		params.Sort = domain.SessionSearchSortRecent
	case "", string(domain.SessionSearchSortRelevance):
		params.Sort = domain.SessionSearchSortRelevance
	default:
		return domain.SessionSearchParsedQuery{}, domain.SessionSearchParams{},
			apierr.Invalid("SESSION_SEARCH_INVALID_SORT", "sort must be relevance or recent", nil)
	}
	switch strings.ToLower(strings.TrimSpace(values.Get("group"))) {
	case "", string(domain.SessionSearchGroupSession):
		params.Group = domain.SessionSearchGroupSession
	case string(domain.SessionSearchGroupMessage):
		params.Group = domain.SessionSearchGroupMessage
	default:
		return domain.SessionSearchParsedQuery{}, domain.SessionSearchParams{},
			apierr.Invalid("SESSION_SEARCH_INVALID_GROUP", "group must be session or message", nil)
	}
	return parsed, params, nil
}

// parseSearchBoundary accepts a date (midnight UTC) or a full RFC 3339
// timestamp, which is what a date picker and a script respectively send.
func parseSearchBoundary(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		value := parsed.UTC()
		return &value, nil
	}
	parsed, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return nil, err
	}
	value := parsed.UTC()
	return &value, nil
}

func atoiOrZero(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return value
}

func sessionSearchResponse(result domain.SessionSearchResponse) SessionSearchResponse {
	return SessionSearchResponse{
		Query:    result.Query,
		Results:  sessionSearchResults(result.Results),
		Total:    result.Total,
		Limit:    result.Limit,
		Offset:   result.Offset,
		Sort:     string(result.Sort),
		Group:    string(result.Group),
		Coverage: sessionSearchCoverage(result.Coverage),
	}
}

func sessionSearchResults(results []domain.SessionSearchResult) []SessionSearchResult {
	out := make([]SessionSearchResult, 0, len(results))
	for _, item := range results {
		highlights := make([]SessionSearchHighlight, 0, len(item.Highlights))
		for _, span := range item.Highlights {
			highlights = append(highlights, SessionSearchHighlight{Start: span.Start, End: span.End})
		}
		out = append(out, SessionSearchResult{
			SessionID:      item.SessionID,
			ProjectID:      item.ProjectID,
			Harness:        item.Harness,
			Kind:           string(item.Kind),
			MessageID:      item.MessageID,
			Seq:            item.Seq,
			Title:          item.Title,
			Snippet:        item.Snippet,
			Highlights:     highlights,
			Rank:           item.Rank,
			CreatedAt:      item.CreatedAt,
			SessionName:    item.SessionName,
			ProjectName:    item.ProjectName,
			Machine:        item.Machine,
			SessionState:   item.SessionState,
			ResumeCommand:  item.ResumeCommand,
			SessionMatches: item.SessionMatches,
			ReviewID:       item.ReviewID,
		})
	}
	return out
}

func sessionSearchCoverage(coverage domain.SessionSearchCoverage) SessionSearchCoverage {
	return SessionSearchCoverage{
		State:              coverage.State,
		IndexedSources:     coverage.IndexedSources,
		PendingSources:     coverage.PendingSources,
		UnsupportedSources: coverage.UnsupportedSources,
		UnavailableSources: coverage.UnavailableSources,
		IndexedDocs:        coverage.IndexedDocs,
		UnindexedHarnesses: coverage.UnindexedHarnesses,
		BackfilledAt:       coverage.BackfilledAt,
	}
}
