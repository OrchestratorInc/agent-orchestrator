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
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

type fakeTransferSessionService struct {
	*fakeSessionService
	archivePath     string
	parts           map[int]string
	receivedProject domain.ProjectID
	received        domain.SessionTransfer
	preparedDest    domain.SessionTransferDestination
	stopTurn        bool
	cancelled       string
}

func (f *fakeTransferSessionService) PrepareTransfer(_ context.Context, _ domain.SessionID, transferID, _ string, dest domain.SessionTransferDestination, stopTurn bool) (domain.SessionTransfer, error) {
	f.preparedDest, f.stopTurn = dest, stopTurn
	return domain.SessionTransfer{TransferID: transferID}, nil
}

func (f *fakeTransferSessionService) CancelTransfer(_ context.Context, id domain.SessionID, transferID string) error {
	f.cancelled = string(id) + "/" + transferID
	return nil
}

func (f *fakeTransferSessionService) TransferArchivePath(id domain.SessionID, _ string) (string, error) {
	if id != "ao-1" {
		return "", apierr.NotFound("TRANSFER_ARCHIVE_INVALID", "no prepared transfer")
	}
	return f.archivePath, nil
}

func (f *fakeTransferSessionService) StoreTransferArchivePart(_ string, part int, r io.Reader) error {
	b, err := io.ReadAll(r)
	f.parts[part] = string(b)
	return err
}

func (f *fakeTransferSessionService) ReceiveTransfer(_ context.Context, projectID domain.ProjectID, t domain.SessionTransfer) (domain.Session, bool, []string, error) {
	f.receivedProject, f.received = projectID, t
	return f.sessions["ao-1"], true, nil, nil
}

func (f *fakeTransferSessionService) SessionMovedTo(_ context.Context, id domain.SessionID) (domain.SessionMovedTo, bool, error) {
	if id == "ao-3" {
		return domain.SessionMovedTo{TransferID: "tr87654321", HostID: "host-b", Label: "MacBook B"}, true, nil
	}
	if id != "ao-1" {
		return domain.SessionMovedTo{}, false, nil
	}
	return domain.SessionMovedTo{TransferID: "tr12345678", HostID: "host-b", Label: "MacBook B", SessionID: "web-9"}, true, nil
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

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/transfer/prepare", `{"transferId":"tr12345678","destination":{"hostId":"host-b","label":"MacBook B"},"stopTurn":true}`)
	if status != http.StatusOK || svc.preparedDest.HostID != "host-b" || svc.preparedDest.Label != "MacBook B" || !svc.stopTurn {
		t.Fatalf("prepare = %d %s, destination %+v", status, body, svc.preparedDest)
	}

	body, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/ao-1/transfer/tr12345678/native", "")
	if status != http.StatusOK || string(body) != "archive-bytes" {
		t.Fatalf("archive = %d %q", status, body)
	}
	body, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/ao-2/transfer/tr12345678/native", "")
	assertErrorCode(t, body, status, http.StatusNotFound, "TRANSFER_ARCHIVE_INVALID")

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
	if received.Session.ID != "ao-1" || received.Warnings == nil || received.HostLabel == "" || !received.Resumed || svc.receivedProject != "web" || svc.received.Branch != "feat/x" {
		t.Fatalf("receive response = %+v, service got %s %+v", received, svc.receivedProject, svc.received)
	}

	var state controllers.SessionTransferStateResponse
	body, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/ao-1/transfer", "")
	mustJSON(t, body, &state)
	if status != http.StatusOK || state.MovedTo == nil || state.MovedTo.SessionID != "web-9" {
		t.Fatalf("transfer state = %d %s", status, body)
	}
	var pending controllers.SessionTransferStateResponse
	body, _, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/ao-3/transfer", "")
	if mustJSON(t, body, &pending); pending.MovedTo != nil || pending.Pending == nil || pending.Pending.Label != "MacBook B" {
		t.Fatalf("pending transfer state = %s, want pending only", body)
	}
	var unmoved controllers.SessionTransferStateResponse
	body, _, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/ao-2/transfer", "")
	if mustJSON(t, body, &unmoved); unmoved.MovedTo != nil {
		t.Fatalf("unmoved transfer state = %s, want no movedTo", body)
	}
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/transfer/commit",
		`{"transferId":"tr12345678","destination":{"hostId":"host-b","label":"MacBook B","sessionId":"web-9"}}`)
	var committed controllers.CommitTransferResponse
	defer func() {
		body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/transfer/cancel", `{"transferId":"tr12345678"}`)
		if status != http.StatusOK || svc.cancelled != "ao-1/tr12345678" {
			t.Fatalf("cancel = %d %s, cancelled %q", status, body, svc.cancelled)
		}
	}()
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
