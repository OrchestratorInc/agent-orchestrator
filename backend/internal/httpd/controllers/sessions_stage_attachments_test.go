package controllers_test

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

func stagingServer(t *testing.T, svc *fakeSessionService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil,
		httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func pngBody(count int) string {
	data := base64.StdEncoding.EncodeToString([]byte("pngbytes"))
	entries := make([]string, 0, count)
	for range count {
		entries = append(entries, `{"mimeType":"image/png","data":"`+data+`"}`)
	}
	return `{"attachments":[` + strings.Join(entries, ",") + `]}`
}

func TestStageAttachmentsReturnsTheWorktreePaths(t *testing.T) {
	svc := newFakeSessionService()
	svc.stagedPaths = []string{".ao/attachments/attachment-ab12cd34ef.png"}
	srv := stagingServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost,
		"/api/v1/sessions/ao-1/attachments", pngBody(1))
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", status, body)
	}
	if !containsAll(body, `"sessionId":"ao-1"`, `".ao/attachments/attachment-ab12cd34ef.png"`) {
		t.Fatalf("body = %s", body)
	}
	if len(svc.staged) != 1 {
		t.Fatalf("service received %d attachments, want 1", len(svc.staged))
	}
	if svc.staged[0].Ext != ".png" || string(svc.staged[0].Data) != "pngbytes" {
		t.Errorf("decoded attachment = %q %q", svc.staged[0].Ext, svc.staged[0].Data)
	}
}

func TestStageAttachmentsRejectsWhatSpawnRejects(t *testing.T) {
	srv := stagingServer(t, newFakeSessionService())

	for name, payload := range map[string]string{
		"svg":            `{"attachments":[{"mimeType":"image/svg+xml","data":"PHN2Zy8+"}]}`,
		"bad base64":     `{"attachments":[{"mimeType":"image/png","data":"!!!"}]}`,
		"empty payload":  `{"attachments":[{"mimeType":"image/png","data":""}]}`,
		"too many":       pngBody(9),
		"no attachments": `{"attachments":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			body, status, _ := doRequest(t, srv, http.MethodPost,
				"/api/v1/sessions/ao-1/attachments", payload)
			if status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (%s)", status, body)
			}
		})
	}
}

func TestStageAttachmentsAcceptsAllNonBlockedTypes(t *testing.T) {
	svc := newFakeSessionService()
	srv := stagingServer(t, svc)

	pdfBody := `{"attachments":[{"mimeType":"application/pdf","data":"eA=="}]}`
	body, status, _ := doRequest(t, srv, http.MethodPost,
		"/api/v1/sessions/ao-1/attachments", pdfBody)
	if status != http.StatusCreated {
		t.Errorf("status = %d, want 201 (%s)", status, body)
	}
	if len(svc.staged) != 1 {
		t.Fatalf("service received %d attachments, want 1", len(svc.staged))
	}
	if svc.staged[0].Ext != ".pdf" {
		t.Errorf("decoded attachment ext = %q, want .pdf", svc.staged[0].Ext)
	}
}

func TestStageAttachmentsReportsAnUnknownSession(t *testing.T) {
	srv := stagingServer(t, newFakeSessionService())

	body, status, _ := doRequest(t, srv, http.MethodPost,
		"/api/v1/sessions/nope/attachments", pngBody(1))
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", status, body)
	}
}
