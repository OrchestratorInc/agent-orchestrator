package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

// stubSessionSearch records the parsed query and returns a fixed page, so the
// controller test asserts the wire contract rather than index behavior.
type stubSessionSearch struct {
	parsed   domain.SessionSearchParsedQuery
	params   domain.SessionSearchParams
	calls    int
	response domain.SessionSearchResponse
	coverage domain.SessionSearchCoverage
	err      error
	// reindexes counts reindex requests; reindexErr is what they return.
	reindexes  int
	reindexErr error
}

func (s *stubSessionSearch) Search(_ context.Context, parsed domain.SessionSearchParsedQuery, params domain.SessionSearchParams) (domain.SessionSearchResponse, error) {
	s.calls++
	s.parsed = parsed
	s.params = params
	return s.response, s.err
}

func (s *stubSessionSearch) Coverage(context.Context) (domain.SessionSearchCoverage, error) {
	return s.coverage, s.err
}

func (s *stubSessionSearch) RequestReindex() error {
	s.reindexes++
	return s.reindexErr
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
			Highlights:    []domain.SessionSearchHighlight{{Start: 17, End: 25}},
			Rank:          -0.5,
			CreatedAt:     created,
			SessionName:   "Fix the login test",
			ProjectName:   "proj",
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
	if len(result.Highlights) != 1 || result.Snippet[result.Highlights[0].Start:result.Highlights[0].End] != "zzNeedle" {
		t.Fatalf("highlights = %+v, want the span of zzNeedle in the snippet", result.Highlights)
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
	// A date range alone is a filter too: browsing a week needs no word.
	for _, dates := range []url.Values{{"from": {"2026-08-01"}}, {"to": {"2026-08-31"}}} {
		if res, body := getSearch(t, srv, dates); res.StatusCode != http.StatusOK {
			t.Fatalf("%v: status = %d, want 200 (body %s)", dates, res.StatusCode, body)
		}
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
	// No full pass has finished in this process: the time is left out rather
	// than sent as year 1, which `ao search --index-status` would print.
	if strings.Contains(string(body), "backfilledAt") {
		t.Fatalf("zero backfilledAt was sent: %s", body)
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

func postReindex(t *testing.T, srv *httptest.Server) (*http.Response, []byte) {
	t.Helper()
	res, err := http.Post(srv.URL+"/api/v1/search/reindex", "application/json", nil)
	if err != nil {
		t.Fatalf("POST reindex: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read reindex body: %v", err)
	}
	return res, body
}

// A reindex is accepted at once and answers with the coverage that follows it,
// so `ao search --reindex` can say the index is rebuilding.
func TestReindexAcceptsAndReportsCoverage(t *testing.T) {
	svc := &stubSessionSearch{coverage: domain.SessionSearchCoverage{State: "backfilling"}}
	srv := newSessionSearchServer(t, svc)
	res, body := postReindex(t, srv)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
	if svc.reindexes != 1 {
		t.Fatalf("reindex requests = %d, want 1", svc.reindexes)
	}
	var payload struct {
		Coverage struct {
			State string `json:"state"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Coverage.State != "backfilling" {
		t.Fatalf("coverage = %+v, err %v", payload.Coverage, err)
	}
}

// With search off the daemon says so instead of pretending to rebuild.
func TestReindexWhileSearchIsOffIsUnavailable(t *testing.T) {
	svc := &stubSessionSearch{reindexErr: apierr.Unavailable("SESSION_SEARCH_UNAVAILABLE", "off")}
	srv := newSessionSearchServer(t, svc)
	res, body := postReindex(t, srv)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
}
