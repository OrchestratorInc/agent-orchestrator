package controllers_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
)

// commentWithFiles is a comment body linking each file, as the composer sends it.
func commentWithFiles(body string, files ...map[string]string) string {
	b, _ := json.Marshal(map[string]any{"prUrl": prURLForTests, "body": body, "attachments": files})
	return string(b)
}

func file(name, contentType, data string) map[string]string {
	return map[string]string{"name": name, "contentType": contentType, "data": data}
}

func TestPRsRoutes_PostCommentWithFiles_PassesTheDecodedFiles(t *testing.T) {
	svc := &fakePRService{commentResult: prsvc.CommentResult{ID: "IC_1", URL: "https://x/c"}}
	srv := newPRTestServer(t, svc)
	png := []byte("\x89PNG\r\n\x1a\nrest")
	resp, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/42/comments", commentWithFiles(
		"![shot](attachment:0) [log](attachment:1)",
		file("shot.png", "image/png", base64.StdEncoding.EncodeToString(png)),
		file("run.log", "", base64.StdEncoding.EncodeToString([]byte("ok"))),
	))
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, resp)
	}
	got := svc.commentReq
	if got.Body != "![shot](attachment:0) [log](attachment:1)" || len(got.Attachments) != 2 {
		t.Fatalf("request = %#v", got)
	}
	if a := got.Attachments[0]; a.Name != "shot.png" || a.ContentType != "image/png" || !bytes.Equal(a.Data, png) {
		t.Fatalf("first file = %#v", a)
	}
	if a := got.Attachments[1]; a.Name != "run.log" || string(a.Data) != "ok" {
		t.Fatalf("second file = %#v", a)
	}
}

func TestPRsRoutes_PostCommentWithFiles_Errors(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString([]byte("hello"))
	for _, tc := range []struct {
		name       string
		body       string
		svcErr     error
		wantStatus int
		wantCode   string
		reachesSvc bool
	}{
		{name: "blank name", body: commentWithFiles("[a](attachment:0)", file("  ", "text/plain", valid)), wantStatus: 400, wantCode: "INVALID_ATTACHMENT"},
		{name: "missing data", body: commentWithFiles("[a](attachment:0)", file("a.txt", "text/plain", "")), wantStatus: 400, wantCode: "INVALID_ATTACHMENT"},
		{name: "not base64", body: commentWithFiles("[a](attachment:0)", file("a.txt", "text/plain", "%%%not-base64%%%")), wantStatus: 400, wantCode: "INVALID_ATTACHMENT"},
		{name: "no write access", body: commentWithFiles("[a](attachment:0)", file("a.txt", "text/plain", valid)), svcErr: prsvc.ErrAttachmentsForbidden, wantStatus: 403, wantCode: "ATTACHMENTS_FORBIDDEN", reachesSvc: true},
		{name: "invalid per service", body: commentWithFiles("[a](attachment:0)", file("a.txt", "text/plain", valid)), svcErr: prsvc.ErrInvalidAttachment, wantStatus: 400, wantCode: "INVALID_ATTACHMENT", reachesSvc: true},
		{name: "too large per service", body: commentWithFiles("[a](attachment:0)", file("a.txt", "text/plain", valid)), svcErr: prsvc.ErrAttachmentTooLarge, wantStatus: 400, wantCode: "ATTACHMENT_TOO_LARGE", reachesSvc: true},
		{name: "type not allowed", body: commentWithFiles("[a](attachment:0)", file("a.svg", "image/svg+xml", valid)), svcErr: prsvc.ErrAttachmentTypeNotAllowed, wantStatus: 400, wantCode: "ATTACHMENT_TYPE_NOT_ALLOWED", reachesSvc: true},
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

// A 10 MiB file is ~14 MiB as base64: the route must accept it, refuse one
// byte more before decoding, and cap the raw body instead of reading it all.
func TestPRsRoutes_PostCommentWithFiles_SizeLimits(t *testing.T) {
	svc := &fakePRService{commentResult: prsvc.CommentResult{ID: "IC_1"}}
	srv := newPRTestServer(t, svc)

	atLimit := base64.StdEncoding.EncodeToString(make([]byte, prsvc.MaxAttachmentBytes))
	resp, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/comments", commentWithFiles("[big](attachment:0)", file("big.zip", "application/zip", atLimit)))
	if status != http.StatusOK || len(svc.commentReq.Attachments) != 1 || len(svc.commentReq.Attachments[0].Data) != prsvc.MaxAttachmentBytes {
		t.Fatalf("10 MiB file: status = %d body=%s", status, resp)
	}

	// The pre-decode check works on base64 length, so it refuses the first
	// size whose encoding is longer; the service checks the exact byte count.
	svc.commentCalls = 0
	overLimit := base64.StdEncoding.EncodeToString(make([]byte, prsvc.MaxAttachmentBytes+3))
	resp, status, _ = doRequest(t, srv, "POST", "/api/v1/prs/42/comments", commentWithFiles("[big](attachment:0)", file("big.zip", "application/zip", overLimit)))
	assertErrorCode(t, resp, status, http.StatusBadRequest, "ATTACHMENT_TOO_LARGE")

	hugeBody := `{"prUrl":"` + prURLForTests + `","body":"x","attachments":[{"name":"big.zip","data":"` + strings.Repeat("A", 40<<20) + `"}]}`
	resp, status, _ = doRequest(t, srv, "POST", "/api/v1/prs/42/comments", hugeBody)
	assertErrorCode(t, resp, status, http.StatusBadRequest, "ATTACHMENT_TOO_LARGE")
	if svc.commentCalls != 0 {
		t.Fatalf("oversized comments reached the service %d times", svc.commentCalls)
	}
}

func TestPRsRoutes_AttachmentAccess(t *testing.T) {
	svc := &fakePRService{accessResult: prsvc.AttachmentAccessResult{Location: "acme/widgets", Reason: "no_write_access"}}
	srv := newPRTestServer(t, svc)
	resp, status, headers := doRequest(t, srv, "GET", "/api/v1/prs/42/attachment-access?prUrl="+prURLForTests, "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, resp)
	}
	var out map[string]any
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatal(err)
	}
	if out["allowed"] != false || out["location"] != "acme/widgets" || out["reason"] != "no_write_access" {
		t.Fatalf("response = %v", out)
	}
	if len(svc.accessCalls) != 1 || svc.accessCalls[0] != "42 "+prURLForTests {
		t.Fatalf("calls = %q", svc.accessCalls)
	}

	resp, status, _ = doRequest(t, srv, "GET", "/api/v1/prs/42/attachment-access", "")
	assertErrorCode(t, resp, status, http.StatusBadRequest, "INVALID_PR")
	svc.accessErr = prsvc.ErrPRNotFound
	resp, status, _ = doRequest(t, srv, "GET", "/api/v1/prs/42/attachment-access?prUrl="+prURLForTests, "")
	assertErrorCode(t, resp, status, http.StatusNotFound, "PR_NOT_FOUND")
}

func TestPRsRoutes_NilService_AttachmentAccessReturns501(t *testing.T) {
	srv := newPRTestServer(t, nil)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/prs/1/attachment-access?prUrl=x", "")
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}
