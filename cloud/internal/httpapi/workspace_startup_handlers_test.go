package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// workspaceStartupStore records the workspace startup errors a worker reports.
type workspaceStartupStore struct {
	Store
	mu                    sync.Mutex
	code, message, detail string
	recorded, cleared     int
}

func (s *workspaceStartupStore) RecordWorkspaceStartupError(_ context.Context, _, _, _ string, _ int64, code, message, detail string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.code, s.message, s.detail = code, message, detail
	s.recorded++
	return nil
}

func (s *workspaceStartupStore) ClearWorkspaceStartupError(context.Context, string, string, string, int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleared++
	return nil
}

// A worker that could not prepare the workspace records the session's startup
// error (redacted), a ready report clears it, and anything else is refused.
func TestWorkerEventRecordsAndClearsWorkspaceStartupError(t *testing.T) {
	t.Parallel()
	store := &workspaceStartupStore{}
	srv := testServer(store)
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.workerEvent(rec, workerRequest(t, http.MethodPost, "/worker/events", body, "worker:event"))
		return rec
	}

	rec := post(`{"type":"workspace.startup","payload":{"status":"failed","code":"project_setup_failed",` +
		`"message":"Setup step 1 failed: exit status 3. Fix the project's Setup script (Project settings > Scripts), then reopen this session to retry.",` +
		`"detail":"starting stack\nGITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789"}}`)
	if rec.Code >= 300 {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if store.recorded != 1 || store.code != domain.WorkspaceStartupErrorProjectSetup ||
		!strings.HasPrefix(store.message, "Setup step 1 failed: exit status 3.") ||
		!strings.Contains(store.detail, "starting stack") || strings.Contains(store.detail, "abcdefghijklmnop") {
		t.Fatalf("recorded = %+v", store)
	}

	// A colour code between a secret's name and its value is stripped
	// before the redaction, so the pair is still recognized.
	rec = post(`{"type":"workspace.startup","payload":{"status":"failed","code":"project_setup_failed",` +
		`"message":"Setup step 1 failed: exit status 1.",` +
		`"detail":"\u001b[1mNPM_AUTH\u001b[0m=\u001b[32mLeakAnsiDetail\u001b[0m\nSIGNING_KEY\u001b[0m: LeakAnsiTwo"}}`)
	if rec.Code >= 300 || store.recorded != 2 || strings.Contains(store.detail, "LeakAnsi") ||
		strings.Contains(store.detail, "\x1b") || !strings.Contains(store.detail, "NPM_AUTH=[REDACTED]") {
		t.Fatalf("ANSI detail: status = %d, recorded = %+v", rec.Code, store)
	}

	if rec := post(`{"type":"workspace.startup","payload":{"status":"ready"}}`); rec.Code >= 300 || store.cleared != 1 {
		t.Fatalf("ready: status = %d, cleared = %d; body = %s", rec.Code, store.cleared, rec.Body.String())
	}

	for name, payload := range map[string]string{
		"unknown status":     `{"status":"done"}`,
		"control plane code": `{"status":"failed","code":"worker_never_started","message":"x"}`,
		"missing message":    `{"status":"failed","code":"repository_checkout_failed"}`,
		"not an object":      `[]`,
	} {
		if rec := post(`{"type":"workspace.startup","payload":` + payload + `}`); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d; body = %s", name, rec.Code, rec.Body.String())
		}
	}
	if store.recorded != 2 || store.cleared != 1 {
		t.Fatalf("refused payloads reached the store: %+v", store)
	}
}
