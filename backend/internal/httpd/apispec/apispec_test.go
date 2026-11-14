package apispec_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
)

func TestDefaultLoadsEmbeddedSpec(t *testing.T) {
	op := apispec.Default().Operation("GET", "/api/v1/projects")
	if op == nil {
		t.Fatal("Default().Operation(GET, /api/v1/projects) = nil; embed broken or path missing")
	}
	if got, _ := op["operationId"].(string); got != "listProjects" {
		t.Errorf("operationId = %q, want listProjects", got)
	}
}

func TestOperation_MissingPath(t *testing.T) {
	if op := apispec.Default().Operation("GET", "/api/v1/no-such-route"); op != nil {
		t.Errorf("unknown path returned %v, want nil", op)
	}
}

func TestOperation_MissingMethod(t *testing.T) {
	if op := apispec.Default().Operation("HEAD", "/api/v1/projects"); op != nil {
		t.Errorf("HEAD on a GET-only path returned %v, want nil", op)
	}
}

func TestOperation_InheritsPathParameters(t *testing.T) {
	op := apispec.Default().Operation("GET", "/api/v1/projects/{id}")
	if op == nil {
		t.Fatal("expected operation slice")
	}
	params, ok := op["parameters"].([]any)
	if !ok || len(params) == 0 {
		t.Fatalf("expected inherited path-level parameters, got %#v", op["parameters"])
	}
}

func TestServeYAML(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil)
	apispec.ServeYAML(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Errorf("Content-Type = %q, want application/yaml*", ct)
	}
	if !strings.Contains(rec.Body.String(), "openapi: 3.1.0") {
		t.Errorf("body did not begin with an OpenAPI 3.1 doc")
	}
}
