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
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	instructionssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/instructions"
)

type fakeInstructionsService struct {
	gotProject domain.ProjectID
	gotSave    instructionssvc.SaveInput
	view       instructionssvc.View
	err        error
}

func (f *fakeInstructionsService) Get(_ context.Context, projectID domain.ProjectID) (instructionssvc.View, error) {
	f.gotProject = projectID
	return f.view, f.err
}

func (f *fakeInstructionsService) Save(_ context.Context, projectID domain.ProjectID, input instructionssvc.SaveInput) (instructionssvc.View, error) {
	f.gotProject, f.gotSave = projectID, input
	return f.view, f.err
}

func newInstructionsServer(t *testing.T, svc *fakeInstructionsService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Instructions: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func doInstructions(t *testing.T, method, url, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res, out
}

// Both scopes share one handler, so the project route must pass its project
// and the user route must pass none; a dream's revision and session must reach
// the service intact.
func TestInstructionsAPI_RoutesEachScopeAndPassesTheSave(t *testing.T) {
	updated := time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
	svc := &fakeInstructionsService{view: instructionssvc.View{
		ProjectID: "notes", Body: "Run python3 -m unittest.", Revision: 7, UpdatedAt: &updated,
		UpdatedBy: &instructionssvc.Author{SessionID: "dream-1", Title: "Dreaming: Fix note ordering", Dreamed: true},
	}}
	srv := newInstructionsServer(t, svc)

	res, body := doInstructions(t, http.MethodGet, srv.URL+"/api/v1/instructions", "")
	if res.StatusCode != http.StatusOK || svc.gotProject != "" {
		t.Fatalf("GET user instructions status=%d project=%q", res.StatusCode, svc.gotProject)
	}
	got := body["instructions"].(map[string]any)
	if got["revision"].(float64) != 7 || got["updatedBy"].(map[string]any)["dreamed"] != true {
		t.Fatalf("instructions = %#v", got)
	}
	if lines, ok := got["protectedLines"].([]any); !ok || len(lines) != 0 {
		t.Fatalf("protectedLines = %#v, want an empty list", got["protectedLines"])
	}

	res, _ = doInstructions(t, http.MethodPut, srv.URL+"/api/v1/projects/notes/instructions", `{"body":"Run python3 -m unittest.","revision":6,"sessionId":"dream-1"}`)
	if res.StatusCode != http.StatusOK || svc.gotProject != "notes" || svc.gotSave.Revision == nil || *svc.gotSave.Revision != 6 || svc.gotSave.SessionID != "dream-1" {
		t.Fatalf("PUT project instructions status=%d project=%q save=%#v", res.StatusCode, svc.gotProject, svc.gotSave)
	}
	res, _ = doInstructions(t, http.MethodPut, srv.URL+"/api/v1/instructions", `{"text":"misspelled field"}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", res.StatusCode)
	}
}

func TestInstructionsAPI_ReportsAStaleRevisionAsAConflict(t *testing.T) {
	svc := &fakeInstructionsService{err: apierr.Conflict("INSTRUCTIONS_CHANGED", "The instructions changed after you read them.", nil)}
	res, body := doInstructions(t, http.MethodPut, newInstructionsServer(t, svc).URL+"/api/v1/instructions", `{"body":"x","revision":1}`)
	if res.StatusCode != http.StatusConflict || body["code"] != "INSTRUCTIONS_CHANGED" {
		t.Fatalf("stale save status=%d body=%#v", res.StatusCode, body)
	}
}
