package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

type fakeSummaryStore struct {
	id      domain.SessionID
	summary string
	calls   int
	found   bool
}

func (f *fakeSummaryStore) UpdateSessionCardSummary(_ context.Context, id domain.SessionID, summary string) (bool, error) {
	f.calls++
	f.id, f.summary = id, summary
	return f.found, nil
}

func TestSummaryAPI_StoresNormalizedTextAndEnforcesTheLimit(t *testing.T) {
	store := &fakeSummaryStore{found: true}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{SummaryStore: store}, httpd.ControlDeps{}))
	defer srv.Close()

	_, status, _ := doRequest(t, srv, "POST", "/api/v1/summary", `{"sessionId":"ao-7","summary":"  Fixing   the\nlogin redirect "}`)
	if status != http.StatusOK || store.id != "ao-7" || store.summary != "Fixing the login redirect" {
		t.Fatalf("status=%d stored=%q for %q", status, store.summary, store.id)
	}

	store.calls = 0
	tooLong := strings.Repeat("修", domain.MaxCardSummaryRunes+1)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/summary", `{"sessionId":"ao-7","summary":"`+tooLong+`"}`)
	if status != http.StatusBadRequest || store.calls != 0 {
		t.Fatalf("over-limit summary: status=%d stored=%d body=%s", status, store.calls, body)
	}

	atLimit := strings.Repeat("修", domain.MaxCardSummaryRunes)
	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/summary", `{"sessionId":"ao-7","summary":"`+atLimit+`"}`); status != http.StatusOK || store.summary != atLimit {
		t.Fatalf("summary at the limit rejected: status=%d", status)
	}

	store.found = false
	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/summary", `{"sessionId":"missing","summary":"Fixing a bug"}`); status != http.StatusNotFound {
		t.Fatalf("unknown session: status=%d, want %d", status, http.StatusNotFound)
	}
}
