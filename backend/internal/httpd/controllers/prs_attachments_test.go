package controllers_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
)

func attachmentBody(name, contentType, data string) string {
	b, _ := json.Marshal(map[string]string{"prUrl": prURLForTests, "name": name, "contentType": contentType, "data": data})
	return string(b)
}

func TestPRsRoutes_UploadAttachment_200(t *testing.T) {
	svc := &fakePRService{attachResult: prsvc.AttachmentResult{
		URL:      "https://github.com/acme/widgets/blob/ao-attachments/pr-42/x-shot.png?raw=true",
		Name:     "shot.png",
		Markdown: "![shot.png](https://github.com/acme/widgets/blob/ao-attachments/pr-42/x-shot.png?raw=true)",
	}}
	srv := newPRTestServer(t, svc)
	raw := []byte("\x89PNG\r\n\x1a\nrest")
	resp, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/42/attachments",
		attachmentBody("shot.png", "image/png", base64.StdEncoding.EncodeToString(raw)))
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, resp)
	}
	var out struct {
		OK       bool   `json:"ok"`
		URL      string `json:"url"`
		Name     string `json:"name"`
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.URL != svc.attachResult.URL || out.Name != "shot.png" || out.Markdown != svc.attachResult.Markdown {
		t.Fatalf("response = %+v", out)
	}
	got := svc.attachReq
	if got.PRID != "42" || got.PRURL != prURLForTests || got.Name != "shot.png" || got.ContentType != "image/png" || !bytes.Equal(got.Data, raw) {
		t.Fatalf("request = %#v", got)
	}
}

func TestPRsRoutes_UploadAttachment_Errors(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString([]byte("hello"))
	for _, tc := range []struct {
		name       string
		path       string
		body       string
		svcErr     error
		wantStatus int
		wantCode   string
		reachesSvc bool
	}{
		{name: "bad id", path: "/api/v1/prs/0/attachments", body: attachmentBody("a.txt", "text/plain", valid), wantStatus: 400, wantCode: "INVALID_PR"},
		{name: "bad json", body: `{`, wantStatus: 400, wantCode: "INVALID_JSON"},
		{name: "missing url", body: `{"name":"a.txt","data":"` + valid + `"}`, wantStatus: 400, wantCode: "INVALID_PR"},
		{name: "blank name", body: attachmentBody("  ", "text/plain", valid), wantStatus: 400, wantCode: "INVALID_ATTACHMENT"},
		{name: "missing data", body: attachmentBody("a.txt", "text/plain", ""), wantStatus: 400, wantCode: "INVALID_ATTACHMENT"},
		{name: "not base64", body: attachmentBody("a.txt", "text/plain", "%%%not-base64%%%"), wantStatus: 400, wantCode: "INVALID_ATTACHMENT"},
		{name: "invalid per service", body: attachmentBody("a.txt", "text/plain", valid), svcErr: prsvc.ErrInvalidAttachment, wantStatus: 400, wantCode: "INVALID_ATTACHMENT", reachesSvc: true},
		{name: "too large per service", body: attachmentBody("a.txt", "text/plain", valid), svcErr: prsvc.ErrAttachmentTooLarge, wantStatus: 400, wantCode: "ATTACHMENT_TOO_LARGE", reachesSvc: true},
		{name: "type not allowed", body: attachmentBody("a.svg", "image/svg+xml", valid), svcErr: prsvc.ErrAttachmentTypeNotAllowed, wantStatus: 400, wantCode: "ATTACHMENT_TYPE_NOT_ALLOWED", reachesSvc: true},
		{name: "unknown pr", body: attachmentBody("a.txt", "text/plain", valid), svcErr: prsvc.ErrPRNotFound, wantStatus: 404, wantCode: "PR_NOT_FOUND", reachesSvc: true},
		{name: "provider refused", body: attachmentBody("a.txt", "text/plain", valid), svcErr: prsvc.ErrPRRejected, wantStatus: 422, wantCode: "PR_ACTION_REJECTED", reachesSvc: true},
		{name: "unsupported", body: attachmentBody("a.txt", "text/plain", valid), svcErr: prsvc.ErrUnsupported, wantStatus: 501, wantCode: "SCM_UNSUPPORTED", reachesSvc: true},
		{name: "unexpected", body: attachmentBody("a.txt", "text/plain", valid), svcErr: errors.New("boom"), wantStatus: 500, wantCode: "PR_OPERATION_FAILED", reachesSvc: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.path
			if path == "" {
				path = "/api/v1/prs/42/attachments"
			}
			svc := &fakePRService{attachErr: tc.svcErr}
			srv := newPRTestServer(t, svc)
			resp, status, _ := doRequest(t, srv, "POST", path, tc.body)
			assertErrorCode(t, resp, status, tc.wantStatus, tc.wantCode)
			if (svc.attachCalls > 0) != tc.reachesSvc {
				t.Fatalf("service calls = %d, reachesSvc = %v", svc.attachCalls, tc.reachesSvc)
			}
		})
	}
}

// A 10 MiB file is ~14 MiB as base64: the route must accept it, refuse one
// byte more before decoding, and cap the raw body instead of reading it all.
func TestPRsRoutes_UploadAttachment_SizeLimits(t *testing.T) {
	svc := &fakePRService{attachResult: prsvc.AttachmentResult{URL: "u", Name: "big.zip", Markdown: "[big.zip](u)"}}
	srv := newPRTestServer(t, svc)

	atLimit := base64.StdEncoding.EncodeToString(make([]byte, prsvc.MaxAttachmentBytes))
	resp, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/attachments", attachmentBody("big.zip", "application/zip", atLimit))
	if status != http.StatusOK || len(svc.attachReq.Data) != prsvc.MaxAttachmentBytes {
		t.Fatalf("10 MiB upload: status = %d decoded = %d body=%s", status, len(svc.attachReq.Data), resp)
	}

	// The pre-decode check works on base64 length, so it refuses the first
	// size whose encoding is longer; the service checks the exact byte count.
	svc.attachCalls = 0
	overLimit := base64.StdEncoding.EncodeToString(make([]byte, prsvc.MaxAttachmentBytes+3))
	resp, status, _ = doRequest(t, srv, "POST", "/api/v1/prs/42/attachments", attachmentBody("big.zip", "application/zip", overLimit))
	assertErrorCode(t, resp, status, http.StatusBadRequest, "ATTACHMENT_TOO_LARGE")

	hugeBody := `{"prUrl":"` + prURLForTests + `","name":"big.zip","data":"` + strings.Repeat("A", 16<<20) + `"}`
	resp, status, _ = doRequest(t, srv, "POST", "/api/v1/prs/42/attachments", hugeBody)
	assertErrorCode(t, resp, status, http.StatusBadRequest, "ATTACHMENT_TOO_LARGE")
	if svc.attachCalls != 0 {
		t.Fatalf("oversized uploads reached the service %d times", svc.attachCalls)
	}
}

func TestPRsRoutes_NilService_AttachmentsReturn501(t *testing.T) {
	srv := newPRTestServer(t, nil)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/prs/1/attachments", "")
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}
