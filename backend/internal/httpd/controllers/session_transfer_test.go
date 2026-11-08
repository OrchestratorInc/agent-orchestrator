package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

type fakeTransferSessionService struct {
	*fakeSessionService
	archivePath     string
	parts           map[int]string
	receivedProject domain.ProjectID
	received        domain.SessionTransfer
}

func (f *fakeTransferSessionService) PrepareTransfer(_ context.Context, _ domain.SessionID, transferID, _ string) (domain.SessionTransfer, error) {
	return domain.SessionTransfer{TransferID: transferID}, nil
}

func (f *fakeTransferSessionService) TransferArchivePath(string) (string, error) {
	return f.archivePath, nil
}

func (f *fakeTransferSessionService) StoreTransferArchivePart(_ string, part int, r io.Reader) error {
	b, err := io.ReadAll(r)
	f.parts[part] = string(b)
	return err
}

func (f *fakeTransferSessionService) ReceiveTransfer(_ context.Context, projectID domain.ProjectID, t domain.SessionTransfer) (domain.Session, []string, error) {
	f.receivedProject, f.received = projectID, t
	return f.sessions["ao-1"], nil, nil
}

func (f *fakeTransferSessionService) CommitTransfer(_ context.Context, id domain.SessionID, transferID string, dest domain.SessionTransferDestination) (domain.SessionMovedTo, error) {
	return domain.SessionMovedTo{TransferID: transferID, HostID: dest.HostID, Label: dest.Label, SessionID: dest.SessionID}, nil
}

func TestSessionsAPI_Transfer(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "native.tgz")
	if err := os.WriteFile(archive, []byte("archive-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := &fakeTransferSessionService{fakeSessionService: newFakeSessionService(), archivePath: archive, parts: map[int]string{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/ao-1/transfer/tr12345678/native", "")
	if status != http.StatusOK || string(body) != "archive-bytes" {
		t.Fatalf("archive = %d %q", status, body)
	}

	if _, status, _ = doRequest(t, srv, http.MethodPut, "/api/v1/sessions/transfer/tr12345678/native/1", "part-one"); status != http.StatusNoContent {
		t.Fatalf("put part = %d", status)
	}
	if svc.parts[1] != "part-one" {
		t.Fatalf("stored parts = %v", svc.parts)
	}
	body, status, _ = doRequest(t, srv, http.MethodPut, "/api/v1/sessions/transfer/tr12345678/native/-1", "x")
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_PART")

	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/sessions/transfer/receive", `{"projectId":"web","transfer":{"transferId":"tr12345678"}}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "TRANSFER_INCOMPLETE")

	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/sessions/transfer/receive",
		`{"projectId":"web","transfer":{"transferId":"tr12345678","branch":"feat/x","headSha":"abc"}}`)
	if status != http.StatusOK {
		t.Fatalf("receive = %d %s", status, body)
	}
	var received controllers.ReceiveTransferResponse
	mustJSON(t, body, &received)
	if received.Session.ID != "ao-1" || received.Warnings == nil || received.HostLabel == "" || svc.receivedProject != "web" || svc.received.Branch != "feat/x" {
		t.Fatalf("receive response = %+v, service got %s %+v", received, svc.receivedProject, svc.received)
	}

	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/transfer/commit",
		`{"transferId":"tr12345678","destination":{"hostId":"host-b","label":"MacBook B","sessionId":"web-9"}}`)
	var committed controllers.CommitTransferResponse
	mustJSON(t, body, &committed)
	if status != http.StatusOK || committed.MovedTo.HostID != "host-b" || committed.MovedTo.SessionID != "web-9" {
		t.Fatalf("commit = %d %+v", status, committed)
	}
}

func TestSessionsAPI_TransferNotImplementedWithoutSupport(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Sessions: newFakeSessionService()}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/transfer/prepare", `{"transferId":"tr12345678"}`)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}
