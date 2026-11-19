package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
)

type fakePRService struct {
	mergeResult   prsvc.MergeResult
	mergeErr      error
	mergeRequest  prsvc.MergeRequest
	resolveResult prsvc.ResolveResult
	resolveErr    error
	stateResult   prsvc.StateResult
	stateErr      error
	stateRequest  prsvc.StateRequest
	stateCalls    int
	commentResult prsvc.CommentResult
	commentErr    error
	commentReq    prsvc.CommentRequest
	commentCalls  int
	accessResult  prsvc.AttachmentAccessResult
	accessErr     error
	accessCalls   []string
	stackResult   prsvc.StackResult
	stackMerge    prsvc.StackMergeResult
	stackMergeReq prsvc.StackMergeRequest
	stackErr      error
	stackCalls    []string
	detailsErr    error
	detailsCalls  []string
}

func (f *fakePRService) AttachmentAccess(_ context.Context, prID, prURL string) (prsvc.AttachmentAccessResult, error) {
	f.accessCalls = append(f.accessCalls, prID+" "+prURL)
	return f.accessResult, f.accessErr
}

func (f *fakePRService) Stack(_ context.Context, prID, prURL string) (prsvc.StackResult, error) {
	f.stackCalls = append(f.stackCalls, prID+" "+prURL)
	return f.stackResult, f.stackErr
}

func (f *fakePRService) LoadDetails(_ context.Context, prID, prURL string) error {
	f.detailsCalls = append(f.detailsCalls, prID+" "+prURL)
	return f.detailsErr
}

func (f *fakePRService) AddToStack(_ context.Context, prID, prURL string) (prsvc.StackResult, error) {
	f.stackCalls = append(f.stackCalls, "add "+prID+" "+prURL)
	return f.stackResult, f.stackErr
}

func (f *fakePRService) MergeStack(_ context.Context, request prsvc.StackMergeRequest) (prsvc.StackMergeResult, error) {
	f.stackMergeReq = request
	return f.stackMerge, f.stackErr
}

func (f *fakePRService) ChangeState(_ context.Context, request prsvc.StateRequest) (prsvc.StateResult, error) {
	f.stateCalls++
	f.stateRequest = request
	return f.stateResult, f.stateErr
}

func (f *fakePRService) PostComment(_ context.Context, request prsvc.CommentRequest) (prsvc.CommentResult, error) {
	f.commentCalls++
	f.commentReq = request
	return f.commentResult, f.commentErr
}

func (f *fakePRService) Merge(_ context.Context, request prsvc.MergeRequest) (prsvc.MergeResult, error) {
	f.mergeRequest = request
	return f.mergeResult, f.mergeErr
}

func (f *fakePRService) ResolveComments(_ context.Context, _ string, _ []string) (prsvc.ResolveResult, error) {
	return f.resolveResult, f.resolveErr
}

func newPRTestServer(t *testing.T, svc prsvc.ActionManager) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{PRs: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

const validMergeBody = `{"prUrl":"https://github.com/acme/widgets/pull/42","expectedHeadSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`

// ---- Nil service → 503 SCM_NOT_CONFIGURED ----

func TestPRsRoutes_NilService_MergeReturns501(t *testing.T) {
	srv := newPRTestServer(t, nil)
	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestPRsRoutes_NilService_ResolveCommentsReturns501(t *testing.T) {
	srv := newPRTestServer(t, nil)
	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/resolve-comments", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

// ---- Merge: 200 ----

func TestPRsRoutes_Merge_200(t *testing.T) {
	stateChangedAt := time.Date(2026, 10, 19, 11, 6, 22, 0, time.UTC)
	svc := &fakePRService{mergeResult: prsvc.MergeResult{
		PRNumber: 42, Method: "squash", State: domain.PRStateMerged,
		MergeCommitSHA: "merge-sha", StateChangedAt: stateChangedAt,
	}}
	srv := newPRTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/merge", validMergeBody)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		OK             bool           `json:"ok"`
		PRNumber       int            `json:"prNumber"`
		Method         string         `json:"method"`
		State          domain.PRState `json:"state"`
		StateChangedAt time.Time      `json:"stateChangedAt"`
	}
	mustJSON(t, body, &resp)
	if !resp.OK || resp.PRNumber != 42 || resp.Method != "squash" || resp.State != domain.PRStateMerged || !resp.StateChangedAt.Equal(stateChangedAt) {
		t.Errorf("unexpected merge response: %+v", resp)
	}
	if svc.mergeRequest.PRID != "42" || svc.mergeRequest.PRURL != "https://github.com/acme/widgets/pull/42" || svc.mergeRequest.ExpectedHeadSHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("merge request = %#v", svc.mergeRequest)
	}
	// The CLI's merge doesn't ask to treat no checks as passing; the PR view's does.
	if svc.mergeRequest.AllowNoChecks {
		t.Fatal("a merge that doesn't ask must not allow no checks")
	}
	withNoChecks := strings.Replace(validMergeBody, "{", `{"allowNoChecks":true,`, 1)
	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/merge", withNoChecks); status != http.StatusOK || !svc.mergeRequest.AllowNoChecks {
		t.Fatalf("status = %d, request = %#v", status, svc.mergeRequest)
	}
}

// ---- Merge: 404 ----

func TestPRsRoutes_Merge_404(t *testing.T) {
	svc := &fakePRService{mergeErr: prsvc.ErrPRNotFound}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/99/merge", validMergeBody)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotFound, "PR_NOT_FOUND")
}

// ---- Merge: 409 ----

func TestPRsRoutes_Merge_409(t *testing.T) {
	svc := &fakePRService{mergeErr: prsvc.ErrPRNotMergeable}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", validMergeBody)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusConflict, "PR_NOT_MERGEABLE")
}

// ---- Merge: 422 ----

func TestPRsRoutes_Merge_422(t *testing.T) {
	svc := &fakePRService{mergeErr: prsvc.ErrPRPreconditions}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", validMergeBody)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "PR_PRECONDITIONS_UNMET")
}

// ---- ResolveComments: 200 ----

func TestPRsRoutes_ResolveComments_200(t *testing.T) {
	svc := &fakePRService{resolveResult: prsvc.ResolveResult{Resolved: 3}}
	srv := newPRTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/resolve-comments", `{"commentIds":["T_1","T_2","T_3"]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		OK       bool `json:"ok"`
		Resolved int  `json:"resolved"`
	}
	mustJSON(t, body, &resp)
	if !resp.OK || resp.Resolved != 3 {
		t.Errorf("resp = %+v, want {ok:true resolved:3}", resp)
	}
}

func TestPRsRoutes_ResolveComments_200_NoBody(t *testing.T) {
	svc := &fakePRService{resolveResult: prsvc.ResolveResult{Resolved: 2}}
	srv := newPRTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/resolve-comments", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
}

// ---- ResolveComments: 404 ----

func TestPRsRoutes_ResolveComments_404(t *testing.T) {
	svc := &fakePRService{resolveErr: prsvc.ErrPRNotFound}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/99/resolve-comments", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotFound, "PR_NOT_FOUND")
}

// ---- ResolveComments: 422 ----

func TestPRsRoutes_ResolveComments_422(t *testing.T) {
	svc := &fakePRService{resolveErr: prsvc.ErrNothingToResolve}
	srv := newPRTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/resolve-comments", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "NOTHING_TO_RESOLVE")
}

func TestPRsRoutes_ResolveComments_InvalidPRID(t *testing.T) {
	svc := &fakePRService{resolveResult: prsvc.ResolveResult{Resolved: 0}}
	srv := newPRTestServer(t, svc)

	for _, id := range []string{"0", "abc", "-1"} {
		body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/"+id+"/resolve-comments", "")
		assertJSON(t, headers)
		assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_PR")
	}
}

func TestPRStack_ReturnsLayersBottomFirst(t *testing.T) {
	svc := &fakePRService{stackResult: prsvc.StackResult{Stack: &prsvc.Stack{
		Number: 7, URL: "https://github.com/a/b/stack/7", BaseBranch: "main", Open: true,
		Layers: []prsvc.StackLayer{{Number: 1, Title: "Base", State: domain.PRStateMerged}, {Number: 2, Title: "Top", State: domain.PRStateOpen, CI: "unknown", NoChecks: true, SessionID: "s-2"}},
	}}}
	srv := newPRTestServer(t, svc)
	resp, err := http.Get(srv.URL + "/api/v1/prs/2/stack?prUrl=https%3A%2F%2Fgithub.com%2Fa%2Fb%2Fpull%2F2")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out struct {
		Stack struct {
			Number int
			Layers []struct {
				Number    int
				State     string
				NoChecks  bool
				SessionID string
			}
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Stack.Number != 7 || len(out.Stack.Layers) != 2 || out.Stack.Layers[0].State != "merged" || out.Stack.Layers[1].SessionID != "s-2" || out.Stack.Layers[0].NoChecks || !out.Stack.Layers[1].NoChecks {
		t.Fatalf("stack = %#v", out.Stack)
	}
	if len(svc.stackCalls) != 1 || svc.stackCalls[0] != "2 https://github.com/a/b/pull/2" {
		t.Fatalf("calls = %v", svc.stackCalls)
	}
}

func TestPRDetails_LoadsThroughTheService(t *testing.T) {
	svc := &fakePRService{}
	srv := newPRTestServer(t, svc)
	resp, err := http.Post(srv.URL+"/api/v1/prs/7/details", "application/json", strings.NewReader(`{"prUrl":"https://github.com/a/b/pull/7"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if len(svc.detailsCalls) != 1 || svc.detailsCalls[0] != "7 https://github.com/a/b/pull/7" {
		t.Fatalf("calls = %v", svc.detailsCalls)
	}

	svc.detailsErr = prsvc.ErrPRNotFound
	resp, err = http.Post(srv.URL+"/api/v1/prs/7/details", "application/json", strings.NewReader(`{"prUrl":"https://github.com/a/b/pull/7"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown PR status = %d", resp.StatusCode)
	}

	resp, err = http.Post(srv.URL+"/api/v1/prs/7/details", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing URL status = %d", resp.StatusCode)
	}
}

func TestPRStack_RequiresURL(t *testing.T) {
	srv := newPRTestServer(t, &fakePRService{})
	resp, err := http.Get(srv.URL + "/api/v1/prs/2/stack")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestPRStackMerge_PassesHeadsAndValidates(t *testing.T) {
	svc := &fakePRService{stackMerge: prsvc.StackMergeResult{PRNumber: 2, Numbers: []int{1, 2}, Method: "squash", Status: "merged"}}
	srv := newPRTestServer(t, svc)
	post := func(body string) int {
		t.Helper()
		resp, err := http.Post(srv.URL+"/api/v1/prs/2/stack/merge", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}
	if got := post(`{"prUrl":"https://github.com/a/b/pull/2","expectedHeads":[{"number":1,"headSha":"a"},{"number":2,"headSha":"b"}]}`); got != http.StatusOK {
		t.Fatalf("status = %d", got)
	}
	if len(svc.stackMergeReq.ExpectedHeads) != 2 || svc.stackMergeReq.ExpectedHeads[1].HeadSHA != "b" || svc.stackMergeReq.AllowNoChecks {
		t.Fatalf("request = %#v", svc.stackMergeReq)
	}
	if got := post(`{"prUrl":"https://github.com/a/b/pull/2","expectedHeads":[{"number":1,"headSha":"a"}],"allowNoChecks":true}`); got != http.StatusOK || !svc.stackMergeReq.AllowNoChecks {
		t.Fatalf("status = %d, request = %#v", got, svc.stackMergeReq)
	}
	if got := post(`{"prUrl":"https://github.com/a/b/pull/2","expectedHeads":[]}`); got != http.StatusBadRequest {
		t.Fatalf("no heads status = %d", got)
	}
	if got := post(`{"prUrl":"https://github.com/a/b/pull/2","method":"octopus","expectedHeads":[{"number":2,"headSha":"b"}]}`); got != http.StatusBadRequest {
		t.Fatalf("bad method status = %d", got)
	}
}
