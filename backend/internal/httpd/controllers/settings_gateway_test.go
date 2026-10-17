package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/gateway"
)

type fakeGateway struct {
	getProjectID string
	setInput     gateway.SetInput
	probeBaseURL string
	probeErr     error
	getErr       error
}

func (f *fakeGateway) Get(_ context.Context, projectID string) (gateway.Config, error) {
	f.getProjectID = projectID
	if f.getErr != nil {
		return gateway.Config{}, f.getErr
	}
	return gateway.Config{App: gateway.ScopeValue{BaseURL: "https://gw.example"}}, nil
}

func (f *fakeGateway) Set(_ context.Context, in gateway.SetInput) (gateway.Config, error) {
	f.setInput = in
	return gateway.Config{App: gateway.ScopeValue{BaseURL: "https://gw.example"}}, nil
}

func (f *fakeGateway) Probe(_ context.Context, baseURL, _ string) (gateway.ProbeResult, error) {
	f.probeBaseURL = baseURL
	if f.probeErr != nil {
		return gateway.ProbeResult{}, f.probeErr
	}
	return gateway.ProbeResult{State: "valid"}, nil
}

func gatewayBody(t *testing.T, w *httptest.ResponseRecorder) GatewayConfigResponse {
	t.Helper()
	var got GatewayConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return got
}

func TestSettingsGatewayUnwiredAnswers501(t *testing.T) {
	c := &SettingsController{}
	for _, route := range []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/v1/settings/gateway", ""},
		{http.MethodPut, "/api/v1/settings/gateway", `{"scope":"app"}`},
		{http.MethodPost, "/api/v1/settings/gateway/probe", `{"baseUrl":"https://gw.example","token":"t"}`},
	} {
		w := httptest.NewRecorder()
		c.getGateway(w, httptest.NewRequest(route.method, route.path, strings.NewReader(route.body)))
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("%s %s: status=%d want 501", route.method, route.path, w.Code)
		}
	}
}

func TestSettingsGatewaySetValidatesScopeAndProject(t *testing.T) {
	svc := &fakeGateway{}
	c := &SettingsController{Gateway: svc}
	for _, body := range []string{
		`{"scope":"galaxy"}`,
		`{"scope":"project"}`,
		`{"scope":""}`,
	} {
		w := httptest.NewRecorder()
		c.setGateway(w, httptest.NewRequest(http.MethodPut, "/api/v1/settings/gateway", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d want 400", body, w.Code)
		}
	}
	w := httptest.NewRecorder()
	c.setGateway(w, httptest.NewRequest(http.MethodPut, "/api/v1/settings/gateway", strings.NewReader(`{"scope":"project","projectId":"p1","baseUrl":"https://gw.example"}`)))
	if w.Code != http.StatusOK || svc.setInput.ProjectID != "p1" || svc.setInput.Scope != gateway.ScopeProject {
		t.Fatalf("valid put: status=%d input=%+v", w.Code, svc.setInput)
	}
}

func TestSettingsGatewayGetPassesProjectQuery(t *testing.T) {
	svc := &fakeGateway{}
	c := &SettingsController{Gateway: svc}
	w := httptest.NewRecorder()
	c.getGateway(w, httptest.NewRequest(http.MethodGet, "/api/v1/settings/gateway?projectId=p2", nil))
	if w.Code != http.StatusOK || svc.getProjectID != "p2" {
		t.Fatalf("status=%d projectId=%q", w.Code, svc.getProjectID)
	}
	if got := gatewayBody(t, w); got.App.BaseURL != "https://gw.example" {
		t.Fatalf("app=%+v", got.App)
	}
}

func TestSettingsGatewayGetPropagatesError(t *testing.T) {
	svc := &fakeGateway{getErr: errors.New("boom")}
	c := &SettingsController{Gateway: svc}
	w := httptest.NewRecorder()
	c.getGateway(w, httptest.NewRequest(http.MethodGet, "/api/v1/settings/gateway", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", w.Code)
	}
}

func TestSettingsGatewayProbeMapsErrorTo400(t *testing.T) {
	c := &SettingsController{Gateway: &fakeGateway{probeErr: errors.New("gateway: base URL and token are required to probe")}}
	w := httptest.NewRecorder()
	c.probeGateway(w, httptest.NewRequest(http.MethodPost, "/api/v1/settings/gateway/probe", strings.NewReader(`{"baseUrl":"","token":""}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
	c = &SettingsController{Gateway: &fakeGateway{}}
	w = httptest.NewRecorder()
	c.probeGateway(w, httptest.NewRequest(http.MethodPost, "/api/v1/settings/gateway/probe", strings.NewReader(`{"baseUrl":"https://gw.example","token":"t"}`)))
	var got GatewayProbeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.State != "valid" {
		t.Fatalf("status=%d response=%+v err=%v", w.Code, got, err)
	}
}
