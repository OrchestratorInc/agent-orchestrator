package controllers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
)

const prURLForTests = "https://github.com/acme/widgets/pull/42"

func TestPRsRoutes_Merge_PassesMethodAndRejectsUnknown(t *testing.T) {
	svc := &fakePRService{mergeResult: prsvc.MergeResult{PRNumber: 42, Method: "rebase", State: domain.PRStateMerged}}
	srv := newPRTestServer(t, svc)
	body := `{"prUrl":"` + prURLForTests + `","expectedHeadSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","method":"rebase"}`
	resp, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/merge", body)
	if status != http.StatusOK || svc.mergeRequest.Method != "rebase" {
		t.Fatalf("status=%d request=%#v body=%s", status, svc.mergeRequest, resp)
	}
	var out struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(resp, &out); err != nil || out.Method != "rebase" {
		t.Fatalf("method = %q, err %v", out.Method, err)
	}

	bad := `{"prUrl":"` + prURLForTests + `","expectedHeadSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","method":"octopus"}`
	resp, status, _ = doRequest(t, srv, "POST", "/api/v1/prs/42/merge", bad)
	assertErrorCode(t, resp, status, http.StatusBadRequest, "INVALID_ACTION")

	svc.mergeErr = prsvc.ErrUnsupported
	resp, status, _ = doRequest(t, srv, "POST", "/api/v1/prs/42/merge", body)
	assertErrorCode(t, resp, status, http.StatusNotImplemented, "SCM_UNSUPPORTED")
}

func TestPRsRoutes_ChangeState_200(t *testing.T) {
	changedAt := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	svc := &fakePRService{stateResult: prsvc.StateResult{PRNumber: 42, State: domain.PRStateClosed, StateChangedAt: changedAt}}
	srv := newPRTestServer(t, svc)
	resp, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/42/state", `{"prUrl":"`+prURLForTests+`","action":"close"}`)
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, resp)
	}
	var out struct {
		OK             bool           `json:"ok"`
		PRNumber       int            `json:"prNumber"`
		State          domain.PRState `json:"state"`
		StateChangedAt time.Time      `json:"stateChangedAt"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.PRNumber != 42 || out.State != domain.PRStateClosed || !out.StateChangedAt.Equal(changedAt) {
		t.Fatalf("response = %+v", out)
	}
	if svc.stateRequest != (prsvc.StateRequest{PRID: "42", PRURL: prURLForTests, Action: "close"}) {
		t.Fatalf("request = %#v", svc.stateRequest)
	}
}

func TestPRsRoutes_ChangeState_Errors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		path       string
		body       string
		svcErr     error
		wantStatus int
		wantCode   string
		reachesSvc bool
	}{
		{name: "bad id", path: "/api/v1/prs/abc/state", body: `{"prUrl":"u","action":"close"}`, wantStatus: 400, wantCode: "INVALID_PR"},
		{name: "bad json", path: "/api/v1/prs/42/state", body: `{`, wantStatus: 400, wantCode: "INVALID_JSON"},
		{name: "missing url", path: "/api/v1/prs/42/state", body: `{"action":"close"}`, wantStatus: 400, wantCode: "INVALID_PR"},
		{name: "bad action", path: "/api/v1/prs/42/state", body: `{"prUrl":"u","action":"merge"}`, wantStatus: 400, wantCode: "INVALID_ACTION"},
		{name: "unknown pr", path: "/api/v1/prs/42/state", body: `{"prUrl":"u","action":"close"}`, svcErr: prsvc.ErrPRNotFound, wantStatus: 404, wantCode: "PR_NOT_FOUND", reachesSvc: true},
		{name: "invalid transition", path: "/api/v1/prs/42/state", body: `{"prUrl":"u","action":"reopen"}`, svcErr: prsvc.ErrInvalidTransition, wantStatus: 422, wantCode: "INVALID_STATE_TRANSITION", reachesSvc: true},
		{name: "provider refused", path: "/api/v1/prs/42/state", body: `{"prUrl":"u","action":"reopen"}`, svcErr: prsvc.ErrPRRejected, wantStatus: 422, wantCode: "PR_ACTION_REJECTED", reachesSvc: true},
		{name: "unsupported", path: "/api/v1/prs/42/state", body: `{"prUrl":"u","action":"draft"}`, svcErr: prsvc.ErrUnsupported, wantStatus: 501, wantCode: "SCM_UNSUPPORTED", reachesSvc: true},
		{name: "unexpected", path: "/api/v1/prs/42/state", body: `{"prUrl":"u","action":"close"}`, svcErr: errors.New("boom"), wantStatus: 500, wantCode: "PR_OPERATION_FAILED", reachesSvc: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakePRService{stateErr: tc.svcErr}
			srv := newPRTestServer(t, svc)
			resp, status, _ := doRequest(t, srv, "POST", tc.path, tc.body)
			assertErrorCode(t, resp, status, tc.wantStatus, tc.wantCode)
			if (svc.stateCalls > 0) != tc.reachesSvc {
				t.Fatalf("service calls = %d, reachesSvc = %v", svc.stateCalls, tc.reachesSvc)
			}
		})
	}
}

func TestPRsRoutes_PostComment_200(t *testing.T) {
	createdAt := time.Date(2026, 10, 31, 9, 30, 0, 0, time.UTC)
	svc := &fakePRService{commentResult: prsvc.CommentResult{ID: "PRRC_1", URL: "https://x/r1", CreatedAt: createdAt}}
	srv := newPRTestServer(t, svc)
	resp, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/comments", `{"prUrl":"`+prURLForTests+`","body":"Fixed","threadId":"PRRT_1"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, resp)
	}
	var out struct {
		OK        bool      `json:"ok"`
		ID        string    `json:"id"`
		URL       string    `json:"url"`
		CreatedAt time.Time `json:"createdAt"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.ID != "PRRC_1" || out.URL != "https://x/r1" || !out.CreatedAt.Equal(createdAt) {
		t.Fatalf("response = %+v", out)
	}
	if svc.commentReq != (prsvc.CommentRequest{PRID: "42", PRURL: prURLForTests, Body: "Fixed", ThreadID: "PRRT_1"}) {
		t.Fatalf("request = %#v", svc.commentReq)
	}
}

func TestPRsRoutes_PostComment_Errors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		svcErr     error
		wantStatus int
		wantCode   string
		reachesSvc bool
	}{
		{name: "blank body", body: `{"prUrl":"u","body":"  \n "}`, wantStatus: 400, wantCode: "INVALID_COMMENT"},
		{name: "missing body", body: `{"prUrl":"u"}`, wantStatus: 400, wantCode: "INVALID_COMMENT"},
		{name: "missing url", body: `{"body":"hi"}`, wantStatus: 400, wantCode: "INVALID_PR"},
		{name: "oversized", body: `{"prUrl":"u","body":"hi"}`, svcErr: prsvc.ErrInvalidComment, wantStatus: 400, wantCode: "INVALID_COMMENT", reachesSvc: true},
		{name: "unknown pr", body: `{"prUrl":"u","body":"hi"}`, svcErr: prsvc.ErrPRNotFound, wantStatus: 404, wantCode: "PR_NOT_FOUND", reachesSvc: true},
		{name: "foreign thread", body: `{"prUrl":"u","body":"hi","threadId":"x"}`, svcErr: prsvc.ErrThreadNotFound, wantStatus: 422, wantCode: "REVIEW_THREAD_NOT_FOUND", reachesSvc: true},
		{name: "provider refused", body: `{"prUrl":"u","body":"hi"}`, svcErr: prsvc.ErrPRRejected, wantStatus: 422, wantCode: "PR_ACTION_REJECTED", reachesSvc: true},
		{name: "unsupported", body: `{"prUrl":"u","body":"hi"}`, svcErr: prsvc.ErrUnsupported, wantStatus: 501, wantCode: "SCM_UNSUPPORTED", reachesSvc: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakePRService{commentErr: tc.svcErr}
			srv := newPRTestServer(t, svc)
			resp, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/comments", tc.body)
			assertErrorCode(t, resp, status, tc.wantStatus, tc.wantCode)
			if (svc.commentCalls > 0) != tc.reachesSvc {
				t.Fatalf("service calls = %d, reachesSvc = %v", svc.commentCalls, tc.reachesSvc)
			}
		})
	}
}

func TestPRsRoutes_NilService_StateAndCommentsReturn501(t *testing.T) {
	srv := newPRTestServer(t, nil)
	for _, path := range []string{"/api/v1/prs/1/state", "/api/v1/prs/1/comments"} {
		body, status, _ := doRequest(t, srv, "POST", path, "")
		assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
	}
}
