package apispec_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd/apispec"
)

// TestDefaultLoadsEmbeddedSpec is the smoke test for //go:embed wiring:
// the default Spec must parse the embedded YAML without panicking and
// recognise a known operation.
func TestDefaultLoadsEmbeddedSpec(t *testing.T) {
	op := apispec.Default().Operation("GET", "/api/v1/projects")
	if op == nil {
		t.Fatal("Default().Operation(GET, /api/v1/projects) = nil; embed broken or path missing")
	}
	if got, _ := op["operationId"].(string); got != "listProjects" {
		t.Errorf("operationId = %q, want listProjects", got)
	}
}

// TestOperation_MissingPath returns nil for unknown paths — that's how the
// controller-side test catches "route registered without spec coverage".
func TestOperation_MissingPath(t *testing.T) {
	if op := apispec.Default().Operation("GET", "/api/v1/no-such-route"); op != nil {
		t.Errorf("unknown path returned %v, want nil", op)
	}
}

// TestOperation_MissingMethod returns nil for known path / unknown method.
func TestOperation_MissingMethod(t *testing.T) {
	if op := apispec.Default().Operation("HEAD", "/api/v1/projects"); op != nil {
		t.Errorf("HEAD on a GET-only path returned %v, want nil", op)
	}
}

// TestOperation_InheritsPathParameters covers the bit of behaviour that
// would silently rot otherwise: parameters declared at the path level
// (e.g. the {id} path param shared by GET/PATCH/DELETE) must show up on
// every operation's slice so the 501 response is self-contained.
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

// TestServeYAML serves the raw embedded document; tooling fetches it
// whole rather than reconstructing it from per-operation slices.
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
