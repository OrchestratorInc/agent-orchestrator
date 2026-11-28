package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/prpreview"
)

type fakePRPreview struct {
	preview prpreview.Preview
	err     error
}

func (f fakePRPreview) Preview(_ context.Context, rawURL string) (prpreview.Preview, error) {
	if _, _, err := prpreview.ParseURL(rawURL); err != nil {
		return prpreview.Preview{}, err
	}
	return f.preview, f.err
}

func getPRPreview(t *testing.T, svc controllers.PRPreviewService, rawURL string) *http.Response {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	daemon := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{PRPreview: svc}, httpd.ControlDeps{}))
	t.Cleanup(daemon.Close)
	res, err := daemon.Client().Get(daemon.URL + "/api/v1/pr-preview?url=" + url.QueryEscape(rawURL))
	if err != nil {
		t.Fatalf("GET pr-preview: %v", err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func TestPRPreviewStatuses(t *testing.T) {
	const pr = "https://github.com/acme/api/pull/12"
	cases := []struct {
		name string
		svc  controllers.PRPreviewService
		url  string
		want int
	}{
		{"no provider", nil, pr, http.StatusNotImplemented},
		{"missing url", fakePRPreview{}, "", http.StatusBadRequest},
		{"not a PR url", fakePRPreview{}, "https://github.com/acme/api/issues/12", http.StatusBadRequest},
		{"not found", fakePRPreview{err: apierr.NotFound("PR_NOT_FOUND", "not found")}, pr, http.StatusNotFound},
		{"read failed", fakePRPreview{err: prpreview.ErrFetchFailed}, pr, http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if res := getPRPreview(t, tc.svc, tc.url); res.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", res.StatusCode, tc.want)
			}
		})
	}
}

func TestPRPreviewBody(t *testing.T) {
	merged := time.Date(2026, 11, 20, 10, 0, 0, 0, time.FixedZone("IST", 19800))
	res := getPRPreview(t, fakePRPreview{preview: prpreview.Preview{
		URL: "https://github.com/acme/api/pull/12", Repo: "acme/api", Number: 12, Title: "Fix it",
		State: "merged", Checks: "passing", Additions: 4, ChangedFiles: 1, StateChangedAt: merged,
	}}, "https://github.com/acme/api/pull/12")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	got := decodeBody(t, res)
	if got["state"] != "merged" || got["checks"] != "passing" || got["number"] != float64(12) || got["stateChangedAt"] != "2026-11-20T04:30:00Z" {
		t.Fatalf("body = %v", got)
	}
	if _, ok := got["author"]; ok {
		t.Fatalf("empty author should be omitted: %v", got)
	}
}
