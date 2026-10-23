package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	searchsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/search"
)

type fakeSearch struct {
	got  searchsvc.Request
	resp searchsvc.Response
	err  error
}

func (f *fakeSearch) Search(_ context.Context, req searchsvc.Request) (searchsvc.Response, error) {
	f.got = req
	return f.resp, f.err
}

func newSearchRig(t *testing.T, svc SearchService) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	(&SearchController{Svc: svc}).Register(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func getSearch(t *testing.T, srv *httptest.Server, params url.Values) (int, SearchResponse, envelope.APIError) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/search?" + params.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body SearchResponse
	var apiErr envelope.APIError
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
	} else if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body, apiErr
}

func TestSearchControllerPassesRequestAndShapesResponse(t *testing.T) {
	svc := &fakeSearch{resp: searchsvc.Response{
		FileNames: []searchsvc.FileNameHit{{Path: "src/palette.ts", Score: 9, ExactName: true}},
		FileContent: []searchsvc.ContentHit{{Path: "src/palette.ts", Score: 5, Decisive: true,
			Snippet: searchsvc.Snippet{Text: "close the palette", Line: 12, Highlights: [][2]int{{10, 17}}}}},
		Messages: []searchsvc.MessageHit{{MessageID: "m1", SessionID: "s1", ConversationTitle: "Palette", Score: 4, OlderVersion: true,
			// "é" is two bytes but one UTF-16 unit; "😀" is four bytes and two units.
			Snippet: searchsvc.Snippet{Text: "é😀 palette", Highlights: [][2]int{{7, 14}}}}},
		Files: searchsvc.IndexReady,
	}}
	srv := newSearchRig(t, svc)
	code, body, _ := getSearch(t, srv, url.Values{"q": {"palette"}, "sessionId": {" s1 "}, "kinds": {"fileNames,messages"}})
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if svc.got.Query != "palette" || svc.got.SessionID != "s1" || !svc.got.Kinds.FileNames || svc.got.Kinds.FileContent || !svc.got.Kinds.Messages {
		t.Fatalf("request = %+v", svc.got)
	}
	if len(body.FileNames) != 1 || !body.FileNames[0].ExactName || body.Files != "ready" {
		t.Fatalf("body = %+v", body)
	}
	if c := body.FileContent[0]; c.Snippet.Line != 12 || !c.Decisive || c.Snippet.Highlights[0] != (SearchHighlight{Start: 10, End: 17}) {
		t.Fatalf("content hit = %+v", c)
	}
	m := body.Messages[0]
	if m.MessageID != "m1" || m.SessionID != "s1" || !m.OlderVersion {
		t.Fatalf("message hit = %+v", m)
	}
	if h := m.Snippet.Highlights[0]; h != (SearchHighlight{Start: 4, End: 11}) {
		t.Fatalf("UTF-16 highlight = %+v, want {4 11}", h)
	}
}

func TestSearchControllerDefaultsToAllKinds(t *testing.T) {
	svc := &fakeSearch{}
	code, body, _ := getSearch(t, newSearchRig(t, svc), url.Values{"q": {"x"}})
	if code != http.StatusOK || !svc.got.Kinds.FileNames || !svc.got.Kinds.FileContent || !svc.got.Kinds.Messages {
		t.Fatalf("status = %d, request = %+v", code, svc.got)
	}
	// Empty groups are arrays, never null.
	if body.FileNames == nil || body.FileContent == nil || body.Messages == nil {
		t.Fatalf("nil groups in %+v", body)
	}
}

func TestSearchControllerErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		svc    SearchService
		params url.Values
		status int
		code   string
	}{
		{"bad kinds", &fakeSearch{}, url.Values{"q": {"x"}, "kinds": {"files"}}, http.StatusBadRequest, "INVALID_SEARCH_KINDS"},
		{"service error", &fakeSearch{err: apierr.Invalid("SEARCH_QUERY_REQUIRED", "query is required", nil)}, url.Values{"q": {""}}, http.StatusBadRequest, "SEARCH_QUERY_REQUIRED"},
		{"no service", nil, url.Values{"q": {"x"}}, http.StatusNotImplemented, ""},
	} {
		status, _, apiErr := getSearch(t, newSearchRig(t, tc.svc), tc.params)
		if status != tc.status || (tc.code != "" && apiErr.Code != tc.code) {
			t.Errorf("%s: status=%d code=%q, want %d %s", tc.name, status, apiErr.Code, tc.status, tc.code)
		}
	}
}
