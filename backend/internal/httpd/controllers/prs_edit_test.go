package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
)

func (f *fakePRService) EditContent(_ context.Context, prID, prURL string) (prsvc.EditContentResult, error) {
	f.editContentCalls = append(f.editContentCalls, prID+" "+prURL)
	return f.editContent, f.editErr
}

func (f *fakePRService) Edit(_ context.Context, request prsvc.EditRequest) (prsvc.EditResult, error) {
	f.editCalls++
	f.editRequest = request
	return f.editResult, f.editErr
}

func TestPRsRoutes_EditContent(t *testing.T) {
	svc := &fakePRService{editContent: prsvc.EditContentResult{Title: "Fix it", Body: "Why", CanEdit: true}}
	srv := newPRTestServer(t, svc)
	resp, status, headers := doRequest(t, srv, "GET", "/api/v1/prs/42/edit?prUrl="+prURLForTests, "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, resp)
	}
	var out map[string]any
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatal(err)
	}
	if out["title"] != "Fix it" || out["body"] != "Why" || out["canEdit"] != true {
		t.Fatalf("response = %v", out)
	}
	if _, has := out["reason"]; has {
		t.Fatalf("an editable PR carries a reason: %v", out)
	}
	if len(svc.editContentCalls) != 1 || svc.editContentCalls[0] != "42 "+prURLForTests {
		t.Fatalf("calls = %q", svc.editContentCalls)
	}

	svc.editContent = prsvc.EditContentResult{Title: "Fix it", Reason: prsvc.EditReasonNoPermission}
	resp, _, _ = doRequest(t, srv, "GET", "/api/v1/prs/42/edit?prUrl="+prURLForTests, "")
	out = map[string]any{}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatal(err)
	}
	if out["canEdit"] != false || out["reason"] != "no_permission" || out["body"] != "" {
		t.Fatalf("response = %v", out)
	}

	resp, status, _ = doRequest(t, srv, "GET", "/api/v1/prs/42/edit", "")
	assertErrorCode(t, resp, status, http.StatusBadRequest, "INVALID_PR")
	resp, status, _ = doRequest(t, srv, "GET", "/api/v1/prs/abc/edit?prUrl="+prURLForTests, "")
	assertErrorCode(t, resp, status, http.StatusBadRequest, "INVALID_PR")
	svc.editErr = prsvc.ErrPRNotFound
	resp, status, _ = doRequest(t, srv, "GET", "/api/v1/prs/42/edit?prUrl="+prURLForTests, "")
	assertErrorCode(t, resp, status, http.StatusNotFound, "PR_NOT_FOUND")
}

func TestPRsRoutes_Edit_200(t *testing.T) {
	updatedAt := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	svc := &fakePRService{editResult: prsvc.EditResult{PRNumber: 42, Title: "New title", Body: "Body", UpdatedAt: updatedAt}}
	srv := newPRTestServer(t, svc)
	resp, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/42/edit", `{"prUrl":"`+prURLForTests+`","title":"New title","baseTitle":"Old title"}`)
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, resp)
	}
	var out struct {
		OK        bool      `json:"ok"`
		PRNumber  int       `json:"prNumber"`
		Title     string    `json:"title"`
		Body      string    `json:"body"`
		UpdatedAt time.Time `json:"updatedAt"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.PRNumber != 42 || out.Title != "New title" || out.Body != "Body" || !out.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("response = %+v", out)
	}
	got := svc.editRequest
	if got.PRID != "42" || got.PRURL != prURLForTests || got.Title == nil || *got.Title != "New title" || *got.BaseTitle != "Old title" || got.Body != nil || got.BaseBody != nil {
		t.Fatalf("request = %#v", got)
	}

	// An empty body clears the description; it is not "no body".
	_, status, _ = doRequest(t, srv, "POST", "/api/v1/prs/42/edit", `{"prUrl":"`+prURLForTests+`","body":"","baseBody":"Old"}`)
	if status != http.StatusOK || svc.editRequest.Body == nil || *svc.editRequest.Body != "" || svc.editRequest.Title != nil {
		t.Fatalf("status = %d request = %#v", status, svc.editRequest)
	}
}

func TestPRsRoutes_Edit_RejectsBadRequestsBeforeTheService(t *testing.T) {
	svc := &fakePRService{}
	srv := newPRTestServer(t, svc)
	for _, tc := range []struct {
		name, path, body, code string
	}{
		{"bad id", "/api/v1/prs/0/edit", `{"prUrl":"x","title":"T","baseTitle":"O"}`, "INVALID_PR"},
		{"bad json", "/api/v1/prs/42/edit", `{`, "INVALID_JSON"},
		{"no url", "/api/v1/prs/42/edit", `{"title":"T","baseTitle":"O"}`, "INVALID_PR"},
		{"no field", "/api/v1/prs/42/edit", `{"prUrl":"x"}`, "INVALID_EDIT"},
		{"title without base", "/api/v1/prs/42/edit", `{"prUrl":"x","title":"T"}`, "INVALID_EDIT"},
		{"body without base", "/api/v1/prs/42/edit", `{"prUrl":"x","body":"B","baseTitle":"O"}`, "INVALID_EDIT"},
	} {
		resp, status, _ := doRequest(t, srv, "POST", tc.path, tc.body)
		assertErrorCode(t, resp, status, http.StatusBadRequest, tc.code)
	}
	if svc.editCalls != 0 {
		t.Fatalf("bad requests reached the service %d times", svc.editCalls)
	}
}

func TestPRsRoutes_Edit_MapsServiceErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{prsvc.ErrPREditConflict, http.StatusConflict, "PR_EDIT_CONFLICT"},
		{prsvc.ErrPREditForbidden, http.StatusForbidden, "PR_EDIT_FORBIDDEN"},
		{prsvc.ErrInvalidEdit, http.StatusBadRequest, "INVALID_EDIT"},
		{prsvc.ErrInvalidTransition, http.StatusUnprocessableEntity, "INVALID_STATE_TRANSITION"},
		{prsvc.ErrPRRejected, http.StatusUnprocessableEntity, "PR_ACTION_REJECTED"},
		{prsvc.ErrUnsupported, http.StatusNotImplemented, "SCM_UNSUPPORTED"},
		{prsvc.ErrPRNotFound, http.StatusNotFound, "PR_NOT_FOUND"},
	} {
		srv := newPRTestServer(t, &fakePRService{editErr: tc.err})
		resp, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/edit", `{"prUrl":"`+prURLForTests+`","title":"T","baseTitle":"O"}`)
		assertErrorCode(t, resp, status, tc.status, tc.code)
	}
}

func TestPRsRoutes_NilService_EditReturns501(t *testing.T) {
	srv := newPRTestServer(t, nil)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/prs/1/edit?prUrl=x", "")
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/prs/1/edit", `{"prUrl":"x","title":"T","baseTitle":"O"}`)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}
