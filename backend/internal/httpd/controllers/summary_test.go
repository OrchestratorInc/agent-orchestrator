package controllers_test

import (
	"context"
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
)

type fakeCardSummaries struct {
	id         domain.SessionID
	summary    string
	done       bool
	calls      int
	found      bool
	retryAfter time.Duration
}

func (f *fakeCardSummaries) Update(_ context.Context, id domain.SessionID, summary string, done bool) (bool, time.Duration, error) {
	f.calls++
	f.id, f.summary, f.done = id, summary, done
	return f.found, f.retryAfter, nil
}

func TestSummaryAPI_StoresNormalizedTextAndEnforcesTheLimit(t *testing.T) {
	store := &fakeCardSummaries{found: true}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{CardSummaries: store}, httpd.ControlDeps{}))
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

	store.retryAfter = 90*time.Second + time.Millisecond
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/summary", `{"sessionId":"ao-7","summary":"Adding tests"}`)
	if status != http.StatusOK || !strings.Contains(string(body), `"updated":false`) || !strings.Contains(string(body), `"retryAfterSeconds":91`) {
		t.Fatalf("held-back summary: status=%d body=%s", status, body)
	}

	store.retryAfter = 0
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/summary", `{"sessionId":"ao-7","summary":"Added tests","done":true}`)
	if status != http.StatusOK || !store.done || !strings.Contains(string(body), `"updated":true`) {
		t.Fatalf("final summary: status=%d done=%v body=%s", status, store.done, body)
	}

	store.found = false
	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/summary", `{"sessionId":"missing","summary":"Fixing a bug"}`); status != http.StatusNotFound {
		t.Fatalf("unknown session: status=%d, want %d", status, http.StatusNotFound)
	}
}
