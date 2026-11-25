package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// leasingSessionService adds the optional draft-lease capability to the fake.
type leasingSessionService struct {
	*fakeSessionService
	draftStages int
	release     []string
	renew       []string
	missing     []string
}

func (f *leasingSessionService) StageDraftAttachments(ctx context.Context, id domain.SessionID, attachments []ports.SpawnAttachment) ([]string, error) {
	f.draftStages++
	return f.StageAttachments(ctx, id, attachments)
}

func (f *leasingSessionService) UpdateAttachmentLeases(_ context.Context, id domain.SessionID, release, renew []string) ([]string, error) {
	if _, ok := f.sessions[id]; !ok {
		return nil, ports.ErrSessionNotFound
	}
	f.release, f.renew = release, renew
	return f.missing, nil
}

func leasingServer(t *testing.T, svc controllers.SessionService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil,
		httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// Only a composer's draft staging is leased; every other caller of the same
// route keeps files until the session is deleted, exactly as before.
func TestStageAttachmentsLeasesOnlyDraftFiles(t *testing.T) {
	svc := &leasingSessionService{fakeSessionService: newFakeSessionService()}
	svc.stagedPaths = []string{".ao/attachments/attachment-ab12cd34ef.png"}
	srv := leasingServer(t, svc)

	if body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/attachments", pngBody(1)); status != http.StatusCreated {
		t.Fatalf("plain stage status = %d (%s)", status, body)
	}
	if svc.draftStages != 0 {
		t.Fatalf("plain stage was leased")
	}
	draft := strings.Replace(pngBody(1), `{"attachments":`, `{"draft":true,"attachments":`, 1)
	if body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/attachments", draft); status != http.StatusCreated {
		t.Fatalf("draft stage status = %d (%s)", status, body)
	}
	if svc.draftStages != 1 {
		t.Fatalf("draft stages = %d, want 1", svc.draftStages)
	}
}

func TestUpdateAttachmentLeases(t *testing.T) {
	svc := &leasingSessionService{fakeSessionService: newFakeSessionService()}
	svc.missing = []string{".ao/attachments/attachment-gone.png"}
	srv := leasingServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/attachments/leases",
		`{"release":[".ao/attachments/attachment-a.png"],"renew":[".ao/attachments/attachment-gone.png"]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d (%s)", status, body)
	}
	if !strings.Contains(string(body), `"missing":[".ao/attachments/attachment-gone.png"]`) {
		t.Fatalf("body = %s", body)
	}
	if !reflect.DeepEqual(svc.release, []string{".ao/attachments/attachment-a.png"}) ||
		!reflect.DeepEqual(svc.renew, []string{".ao/attachments/attachment-gone.png"}) {
		t.Fatalf("service got release=%v renew=%v", svc.release, svc.renew)
	}

	svc.missing = nil
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/attachments/leases", `{}`)
	if status != http.StatusOK || !strings.Contains(string(body), `"missing":[]`) {
		t.Fatalf("empty update = %d %s, want an empty missing list", status, body)
	}

	if body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/nope/attachments/leases", `{}`); status != http.StatusNotFound {
		t.Fatalf("unknown session status = %d (%s)", status, body)
	}
	if body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/attachments/leases", `{`); status != http.StatusBadRequest {
		t.Fatalf("bad JSON status = %d (%s)", status, body)
	}
	many := `{"release":[` + strings.TrimSuffix(strings.Repeat(`"x",`, 257), ",") + `]}`
	if body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/attachments/leases", many); status != http.StatusBadRequest {
		t.Fatalf("too many paths status = %d (%s)", status, body)
	}
}

// A service without lease support answers 501 rather than pretending a
// release happened.
func TestUpdateAttachmentLeasesUnsupported(t *testing.T) {
	srv := leasingServer(t, newFakeSessionService())
	if body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/attachments/leases", `{}`); status != http.StatusNotImplemented {
		t.Fatalf("status = %d (%s), want 501", status, body)
	}
}
