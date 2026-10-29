package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

// stubSessionSearch records the parsed query and returns a fixed page, so the
// controller test asserts the wire contract rather than index behavior.
type stubSessionSearch struct {
	parsed    domain.SessionSearchParsedQuery
	params    domain.SessionSearchParams
	calls     int
	matches   []domain.SessionSearchResult
	matchArgs [2]string
	response  domain.SessionSearchResponse
	coverage  domain.SessionSearchCoverage
	err       error
}

func (s *stubSessionSearch) Search(_ context.Context, parsed domain.SessionSearchParsedQuery, params domain.SessionSearchParams) (domain.SessionSearchResponse, error) {
	s.calls++
	s.parsed = parsed
	s.params = params
	return s.response, s.err
}

func (s *stubSessionSearch) MatchesForSession(_ context.Context, sessionID, matchQuery string) ([]domain.SessionSearchResult, error) {
	s.matchArgs = [2]string{sessionID, matchQuery}
	return s.matches, s.err
}

func (s *stubSessionSearch) Coverage(context.Context) (domain.SessionSearchCoverage, error) {
	return s.coverage, s.err
}

func newSessionSearchServer(t *testing.T, svc *stubSessionSearch) *httptest.Server {
	t.Helper()
	return newSessionSearchServerWith(t, svc)
}

// newSessionSearchServerWith takes the interface directly so a test can mount an
// untyped nil service and exercise the 501 branch.
func newSessionSearchServerWith(t *testing.T, svc controllers.SessionSearchService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		SessionSearch: svc,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func getSearch(t *testing.T, srv *httptest.Server, query url.Values) (*http.Response, []byte) {
	t.Helper()
	res, err := http.Get(srv.URL + "/api/v1/search/sessions?" + query.Encode())
	if err != nil {
		t.Fatalf("GET search: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read search body: %v", err)
	}
	return res, body
}

func TestSearchSessionsReturnsRankedResults(t *testing.T) {
	created := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	svc := &stubSessionSearch{response: domain.SessionSearchResponse{
		Results: []domain.SessionSearchResult{{
			SessionID:     "proj-1",
			ProjectID:     "proj",
			Harness:       "claude-code",
			Kind:          domain.SessionSearchKindCommand,
			Seq:           7,
			Snippet:       "undefined symbol zzNeedle",
			Highlights:    []domain.SessionSearchHighlight{{Start: 18, End: 26}},
			Rank:          -0.5,
			CreatedAt:     created,
			SessionName:   "Fix the login test",
			ProjectName:   "proj",
			Machine:       "mac",
			SessionState:  "terminated",
			ResumeCommand: "ao session resume-agent proj-1",
		}},
		Total:  1,
		Limit:  20,
		Offset: 0,
		Sort:   domain.SessionSearchSortRelevance,
		Coverage: domain.SessionSearchCoverage{
			State: "ready", IndexedDocs: 12, IndexedSources: 3,
			UnsupportedSources: 1, UnindexedHarnesses: []string{"aider"},
		},
	}}
	srv := newSessionSearchServer(t, svc)

	res, body := getSearch(t, srv, url.Values{"q": {"zzNeedle"}})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
	var payload struct {
		Sort    string `json:"sort"`
		Results []struct {
			SessionID     string `json:"sessionId"`
			Kind          string `json:"kind"`
			Snippet       string `json:"snippet"`
			ResumeCommand string `json:"resumeCommand"`
			Highlights    []struct {
				Start int `json:"start"`
				End   int `json:"end"`
			} `json:"highlights"`
		} `json:"results"`
		Coverage struct {
			State              string   `json:"state"`
			IndexedDocs        int64    `json:"indexedDocs"`
			UnindexedHarnesses []string `json:"unindexedHarnesses"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response: %v (%s)", err, body)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(payload.Results))
	}
	result := payload.Results[0]
	if result.SessionID != "proj-1" || result.Kind != "command" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Highlights) != 1 || result.Highlights[0].Start != 18 {
		t.Fatalf("highlights = %+v, want byte offsets", result.Highlights)
	}
	if result.ResumeCommand != "ao session resume-agent proj-1" {
		t.Fatalf("resume command = %q", result.ResumeCommand)
	}
	if payload.Sort != "relevance" {
		t.Fatalf("sort = %q", payload.Sort)
	}
	if payload.Coverage.State != "ready" || payload.Coverage.IndexedDocs != 12 {
		t.Fatalf("coverage = %+v", payload.Coverage)
	}
	// The controller hands the service a parsed query, not raw text.
	// "zzNeedle" matches as a prefix of its stem, which still covers the token.
	if svc.parsed.Text != "zzNeedle" || svc.parsed.MatchQuery != `"zzneedl"*` {
		t.Fatalf("parsed = %+v", svc.parsed)
	}
}

func TestSearchSessionsParsesOperatorsAndFilters(t *testing.T) {
	svc := &stubSessionSearch{response: domain.SessionSearchResponse{Results: []domain.SessionSearchResult{}}}
	srv := newSessionSearchServer(t, svc)

	res, body := getSearch(t, srv, url.Values{
		"q":      {`backoff harness:codex project:api !upload "resets its" 'exact ^pre`},
		"status": {"killed"},
		"path":   {"src/upload"},
		"from":   {"2026-01-02"},
		"to":     {"2026-06-02T10:00:00Z"},
		"sort":   {"recent"},
		"limit":  {"5"},
		"offset": {"10"},
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
	parsed := svc.parsed
	if parsed.Harness != "codex" || parsed.Project != "api" {
		t.Fatalf("operators = %+v", parsed)
	}
	if parsed.ExcludeQuery != `"upload"` {
		t.Fatalf("exclude = %q", parsed.ExcludeQuery)
	}
	if len(parsed.Terms) != 4 {
		t.Fatalf("terms = %v, want backoff, the phrase, the exact term, the prefix", parsed.Terms)
	}
	if svc.params.Status != domain.SessionSearchStatusKilled || svc.params.Path != "src/upload" {
		t.Fatalf("filters = %+v", svc.params)
	}
	if svc.params.Limit != 5 || svc.params.Offset != 10 || svc.params.Sort != domain.SessionSearchSortRecent {
		t.Fatalf("paging/sort = %+v", svc.params)
	}
	if svc.params.From == nil || svc.params.From.Format(time.DateOnly) != "2026-01-02" {
		t.Fatalf("from = %v", svc.params.From)
	}
	if svc.params.To == nil || !svc.params.To.Equal(time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("to = %v", svc.params.To)
	}
}

// A filter-only query is a browse, not an error: opening search with a project
// filter and no words must show that project's recent sessions.
func TestSearchSessionsAllowsFilterOnlyBrowse(t *testing.T) {
	svc := &stubSessionSearch{response: domain.SessionSearchResponse{Results: []domain.SessionSearchResult{}}}
	srv := newSessionSearchServer(t, svc)
	res, body := getSearch(t, srv, url.Values{"projectId": {"proj"}})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", res.StatusCode, body)
	}
	if svc.calls != 1 || svc.params.ProjectID != "proj" {
		t.Fatalf("params = %+v calls = %d", svc.params, svc.calls)
	}
}

func TestSearchSessionsRejectsEmptyQuery(t *testing.T) {
	svc := &stubSessionSearch{}
	srv := newSessionSearchServer(t, svc)
	res, body := getSearch(t, srv, url.Values{})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", res.StatusCode, body)
	}
	if svc.calls != 0 {
		t.Fatalf("service called %d times for a missing query", svc.calls)
	}
	var apiError struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(body, &apiError); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if apiError.Code != "SESSION_SEARCH_QUERY_REQUIRED" || apiError.Error != "bad_request" {
		t.Fatalf("error = %+v", apiError)
	}
}

func TestSearchSessionsRejectsInvertedDateRange(t *testing.T) {
	svc := &stubSessionSearch{}
	srv := newSessionSearchServer(t, svc)
	res, body := getSearch(t, srv, url.Values{
		"q":    {"reaper"},
		"from": {"2026-06-02"},
		"to":   {"2026-01-02"},
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", res.StatusCode, body)
	}
	if svc.calls != 0 {
		t.Fatalf("service called %d times for an inverted range", svc.calls)
	}
}

func TestSearchSessionsRejectsUnknownSort(t *testing.T) {
	svc := &stubSessionSearch{}
	srv := newSessionSearchServer(t, svc)
	res, body := getSearch(t, srv, url.Values{"q": {"reaper"}, "sort": {"alphabetical"}})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", res.StatusCode, body)
	}
}

func TestSearchSessionsAnswersNotImplementedWithoutService(t *testing.T) {
	srv := newSessionSearchServerWith(t, nil)
	res, body := getSearch(t, srv, url.Values{"q": {"reaper"}})
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (body %s)", res.StatusCode, body)
	}
}

func TestSearchMatchesListsEveryMatchForASession(t *testing.T) {
	svc := &stubSessionSearch{matches: []domain.SessionSearchResult{
		{SessionID: "proj-1", Kind: domain.SessionSearchKindUser, MessageID: "msg-1", Seq: 1, Snippet: "retry"},
		{SessionID: "proj-1", Kind: domain.SessionSearchKindAssistant, MessageID: "msg-2", Seq: 2, Snippet: "retry"},
	}}
	srv := newSessionSearchServer(t, svc)
	res, err := http.Get(srv.URL + "/api/v1/sessions/proj-1/search/matches?q=retry")
	if err != nil {
		t.Fatalf("GET matches: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
	var payload struct {
		Matches []struct {
			MessageID string `json:"messageId"`
			Seq       int64  `json:"seq"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode matches: %v", err)
	}
	if len(payload.Matches) != 2 {
		t.Fatalf("matches = %d, want 2", len(payload.Matches))
	}
	if svc.matchArgs[0] != "proj-1" || svc.matchArgs[1] != `"retr"*` {
		t.Fatalf("match args = %v", svc.matchArgs)
	}
}

// A result opened from a pasted PR URL prefills the find bar with that URL,
// which has no words; the matches are where the PR is mentioned.
func TestSearchMatchesForAPastedPRURLUseThePRPhrase(t *testing.T) {
	svc := &stubSessionSearch{}
	srv := newSessionSearchServer(t, svc)
	res, err := http.Get(srv.URL + "/api/v1/sessions/proj-1/search/matches?q=" +
		url.QueryEscape("https://github.com/acme/widgets/pull/6553"))
	if err != nil {
		t.Fatalf("GET matches: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
	if svc.matchArgs[1] != `"pull 6553"` {
		t.Fatalf("match query = %q, want the PR phrase", svc.matchArgs[1])
	}
}

func TestSearchIndexStatusReportsCoverage(t *testing.T) {
	svc := &stubSessionSearch{coverage: domain.SessionSearchCoverage{
		State: "backfilling", PendingSources: 4, IndexedDocs: 900,
		UnsupportedSources: 2, UnindexedHarnesses: []string{"aider", "cursor"},
	}}
	srv := newSessionSearchServer(t, svc)
	res, err := http.Get(srv.URL + "/api/v1/search/index-status")
	if err != nil {
		t.Fatalf("GET index status: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
	var payload struct {
		Coverage struct {
			State              string   `json:"state"`
			PendingSources     int64    `json:"pendingSources"`
			IndexedDocs        int64    `json:"indexedDocs"`
			UnindexedHarnesses []string `json:"unindexedHarnesses"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode index status: %v", err)
	}
	if payload.Coverage.State != "backfilling" || payload.Coverage.PendingSources != 4 {
		t.Fatalf("coverage = %+v", payload.Coverage)
	}
	if len(payload.Coverage.UnindexedHarnesses) != 2 {
		t.Fatalf("unindexed harnesses = %v", payload.Coverage.UnindexedHarnesses)
	}
}

// group selects one row per session (the default) or one per message, and an
// unknown value is rejected rather than silently treated as the default.
func TestSearchSessionsGroupsBySessionByDefault(t *testing.T) {
	svc := &stubSessionSearch{response: domain.SessionSearchResponse{
		Group: domain.SessionSearchGroupSession,
		Results: []domain.SessionSearchResult{{
			SessionID: "proj-1", Kind: domain.SessionSearchKindUser, Snippet: "flaky", SessionMatches: 4,
			ReviewID: "review-1",
		}},
	}}
	srv := newSessionSearchServer(t, svc)
	res, body := getSearch(t, srv, url.Values{"q": {"flaky"}})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
	if svc.params.Group != domain.SessionSearchGroupSession {
		t.Fatalf("group = %q, want session by default", svc.params.Group)
	}
	var decoded struct {
		Group   string `json:"group"`
		Results []struct {
			SessionMatches int    `json:"sessionMatches"`
			ReviewID       string `json:"reviewId"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// reviewId routes a reviewer-chat match to that reviewer chat.
	if decoded.Group != "session" || len(decoded.Results) != 1 || decoded.Results[0].SessionMatches != 4 ||
		decoded.Results[0].ReviewID != "review-1" {
		t.Fatalf("response = %s", body)
	}

	if res, body := getSearch(t, srv, url.Values{"q": {"flaky"}, "group": {"message"}}); res.StatusCode != http.StatusOK ||
		svc.params.Group != domain.SessionSearchGroupMessage {
		t.Fatalf("group=message: status %d, params %+v, body %s", res.StatusCode, svc.params, body)
	}
	calls := svc.calls
	if res, body := getSearch(t, srv, url.Values{"q": {"flaky"}, "group": {"project"}}); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("group=project: status = %d, want 400 (body %s)", res.StatusCode, body)
	}
	if svc.calls != calls {
		t.Fatal("service called for an invalid group")
	}
}
