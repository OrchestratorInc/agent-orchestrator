package httpd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/litmus"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type testingTimeoutProbe struct {
	controllers.TestingService
	budget time.Duration
}

func (p *testingTimeoutProbe) RefuseTester(string, string) error { return nil }
func (p *testingTimeoutProbe) Start(ctx context.Context, _ litmus.StartRequest) (litmus.Started, error) {
	ctx, cancel := ports.ExtendSpawnRequestBudget(ctx, time.Minute)
	defer cancel()
	p.budget = remainingRequestBudget(ctx)
	return litmus.Started{}, nil
}
func (p *testingTimeoutProbe) RecipeValidate(ctx context.Context, _ litmus.RecipeValidateRequest) (litmus.Recipe, error) {
	p.budget = remainingRequestBudget(ctx)
	return litmus.Recipe{}, nil
}
func (p *testingTimeoutProbe) Intake(ctx context.Context, _, _ string) (litmus.Run, error) {
	p.budget = remainingRequestBudget(ctx)
	return litmus.Run{}, nil
}
func (p *testingTimeoutProbe) Prepare(ctx context.Context, _ string) (litmus.Run, error) {
	p.budget = remainingRequestBudget(ctx)
	return litmus.Run{}, nil
}
func (p *testingTimeoutProbe) Launch(ctx context.Context, _, _ string) (litmus.Run, error) {
	p.budget = remainingRequestBudget(ctx)
	return litmus.Run{}, nil
}
func (p *testingTimeoutProbe) Submit(ctx context.Context, _, _ string) (litmus.Verdict, error) {
	p.budget = remainingRequestBudget(ctx)
	return litmus.Verdict{}, nil
}
func (p *testingTimeoutProbe) Post(ctx context.Context, _ string) (litmus.Run, error) {
	p.budget = remainingRequestBudget(ctx)
	return litmus.Run{}, nil
}
func (p *testingTimeoutProbe) Cleanup(ctx context.Context, _ string) (litmus.Run, error) {
	p.budget = remainingRequestBudget(ctx)
	return litmus.Run{}, nil
}

func TestTestingRoutesUseServiceBudgets(t *testing.T) {
	const ordinary = 250 * time.Millisecond
	p := &testingTimeoutProbe{}
	router := NewRouterWithControl(config.Config{RequestTimeout: ordinary}, discardLogger(), nil, APIDeps{Testing: p}, ControlDeps{})
	for _, tc := range []struct {
		path string
		want time.Duration
	}{
		{"/start", ordinary},
		{"/recipe/validate", litmus.RecipeValidateBudget + time.Minute},
		{"/intake", litmus.RecipeValidateBudget + time.Minute},
		{"/prepare", litmus.PrepareBudget + time.Minute},
		{"/launch", litmus.DefaultLaunchBudget + time.Minute},
		{"/submit", litmus.SubmitBudget + time.Minute},
		{"/runs/run_w1/post", litmus.PostBudget + time.Minute},
		{"/cleanup", ordinary},
	} {
		req := httptest.NewRequest("POST", "/api/v1/testing"+tc.path, strings.NewReader(`{"sessionId":"w1"}`))
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		if out.Code != 200 || p.budget <= tc.want-time.Second || p.budget > tc.want {
			t.Fatalf("POST %s = %d, budget=%s want=%s", tc.path, out.Code, p.budget, tc.want)
		}
	}
}

func TestTestingRoutesNeedService(t *testing.T) {
	router := NewRouterWithControl(config.Config{}, discardLogger(), nil, APIDeps{}, ControlDeps{})
	out := httptest.NewRecorder()
	router.ServeHTTP(out, httptest.NewRequest("GET", "/api/v1/testing/status", nil))
	if out.Code != http.StatusNotFound {
		t.Fatalf("unwired testing status=%d", out.Code)
	}
}
