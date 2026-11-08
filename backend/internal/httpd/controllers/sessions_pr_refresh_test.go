package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakePRRefresher struct{ got []domain.SessionID }

func (f *fakePRRefresher) RequestSessionRefresh(id domain.SessionID) { f.got = append(f.got, id) }

type stubActivity struct{}

func (stubActivity) ApplyActivitySignal(context.Context, domain.SessionID, ports.ActivitySignal) error {
	return nil
}

func TestSessionsAPI_ActivityRequestsPRRefreshOnlyWhenFlagged(t *testing.T) {
	refresh := &fakePRRefresher{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Activity: stubActivity{}, PRRefresh: refresh}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	for _, body := range []string{`{"state":"active"}`, `{"state":"active","prMayHaveChanged":true}`} {
		if got, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/activity", body); status != http.StatusOK {
			t.Fatalf("activity = %d; body=%s", status, got)
		}
	}
	if len(refresh.got) != 1 || refresh.got[0] != "ao-1" {
		t.Fatalf("refreshes = %v, want one for ao-1", refresh.got)
	}
}
