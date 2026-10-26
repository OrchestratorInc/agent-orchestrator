package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/coder"
)

// fakeDynamicTemplateReader is a deployment Coder whose template declares
// other_github_repo only when github_repos contains "Other", like 11x's.
type fakeDynamicTemplateReader struct {
	fakeDeploymentTemplateLister
	evaluateErr error
	evaluated   []map[string]string
}

func (f *fakeDynamicTemplateReader) TemplateParameters(context.Context, string) ([]coder.TemplateParameter, error) {
	return []coder.TemplateParameter{{Name: "github_repos", Type: "list(string)", DefaultValue: `["Alice"]`}, {Name: "region"}}, nil
}

func (f *fakeDynamicTemplateReader) EvaluateTemplateParameters(_ context.Context, _ string, values map[string]string) ([]coder.TemplateParameter, error) {
	f.evaluated = append(f.evaluated, values)
	if f.evaluateErr != nil {
		return nil, f.evaluateErr
	}
	parameters := []coder.TemplateParameter{{Name: "github_repos", Type: "list(string)", DefaultValue: `["Alice"]`}}
	if strings.Contains(values["github_repos"], "Other") {
		parameters = append(parameters, coder.TemplateParameter{Name: "other_github_repo", DisplayName: "Other Github Repository"})
	}
	region := coder.TemplateParameter{Name: "region", DisplayName: "Region"}
	if values["region"] == "mars" {
		region.Diagnostics = []coder.TemplateParameterDiagnostic{{Severity: "error", Summary: "Value must be a valid option", Detail: `the value "mars" must be defined as one of options`}}
	}
	return append(parameters, region), nil
}

func newDynamicParametersServer(t *testing.T, reader CoderTemplateLister) *Server {
	t.Helper()
	return New(Options{
		Store:                     &fakeCoderConnStore{err: postgres.ErrNotFound},
		SecretCipher:              newTestCipher(t),
		SandboxProvider:           sandbox.ProviderCoder,
		AvailableSandboxProviders: []string{sandbox.ProviderCoder},
		CoderTemplates:            reader,
		Logger:                    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestCoderParameterProblemAcceptsAConditionalParameterOnceItsTriggerIsSet(t *testing.T) {
	t.Parallel()
	reader := &fakeDynamicTemplateReader{}
	srv := newDynamicParametersServer(t, reader)
	ctx := coderConfigRequest(t, http.MethodPost, "", []string{sandbox.ProviderCoder}).Context()
	values := map[string]string{"github_repos": `["Other"]`, "other_github_repo": "https://github.com/acme/places"}
	if problem := srv.coderTemplateParameterProblem(ctx, coderCfgOrgID, coderCfgTemplate, values, "req"); problem != "" {
		t.Fatalf("problem = %q, want the conditional parameter accepted", problem)
	}
	if len(reader.evaluated) != 1 || reader.evaluated[0]["github_repos"] != `["Other"]` {
		t.Fatalf("evaluated with %v, want the submitted values", reader.evaluated)
	}
	// Without its trigger the conditional parameter is not declared.
	problem := srv.coderTemplateParameterProblem(ctx, coderCfgOrgID, coderCfgTemplate, map[string]string{"other_github_repo": "x"}, "req")
	if !strings.Contains(problem, "does not declare these parameters: other_github_repo") {
		t.Fatalf("problem = %q, want other_github_repo rejected", problem)
	}
	if problem := srv.coderTemplateParameterProblem(ctx, coderCfgOrgID, coderCfgTemplate, map[string]string{"guess": "x"}, "req"); !strings.Contains(problem, "guess") {
		t.Fatalf("problem = %q, want an unknown name rejected", problem)
	}
}

func TestCoderParameterProblemReportsCoderValueErrors(t *testing.T) {
	t.Parallel()
	srv := newDynamicParametersServer(t, &fakeDynamicTemplateReader{})
	ctx := coderConfigRequest(t, http.MethodPost, "", []string{sandbox.ProviderCoder}).Context()
	problem := srv.coderTemplateParameterProblem(ctx, coderCfgOrgID, coderCfgTemplate, map[string]string{"region": "mars"}, "req")
	if !strings.Contains(problem, "rejected these values: Region: Value must be a valid option") || !strings.Contains(problem, `"mars"`) {
		t.Fatalf("problem = %q, want Coder's diagnostic", problem)
	}
}

// A classic-flow template, an older Coder, or a failed evaluation falls back to
// the static rich parameters: a conditional name is then unknown, but a Coder
// hiccup never blocks a name the template always declares.
func TestCoderParameterProblemFallsBackToStaticParameters(t *testing.T) {
	t.Parallel()
	for name, evaluateErr := range map[string]error{
		"classic or no dynamic API": coder.ErrDynamicParametersUnsupported,
		"evaluation failed":         errors.New("coder: evaluate template parameters: boom"),
	} {
		srv := newDynamicParametersServer(t, &fakeDynamicTemplateReader{evaluateErr: evaluateErr})
		ctx := coderConfigRequest(t, http.MethodPost, "", []string{sandbox.ProviderCoder}).Context()
		if problem := srv.coderTemplateParameterProblem(ctx, coderCfgOrgID, coderCfgTemplate, map[string]string{"region": "mars"}, "req"); problem != "" {
			t.Fatalf("%s: problem = %q, want a static name accepted without value checks", name, problem)
		}
		problem := srv.coderTemplateParameterProblem(ctx, coderCfgOrgID, coderCfgTemplate,
			map[string]string{"github_repos": `["Other"]`, "other_github_repo": "x"}, "req")
		if !strings.Contains(problem, "other_github_repo") {
			t.Fatalf("%s: problem = %q, want the static list to decide", name, problem)
		}
	}
	// A reader that cannot evaluate at all uses the static list too.
	srv := newDynamicParametersServer(t, &fakeDeploymentTemplateReader{})
	ctx := coderConfigRequest(t, http.MethodPost, "", []string{sandbox.ProviderCoder}).Context()
	if problem := srv.coderTemplateParameterProblem(ctx, coderCfgOrgID, coderCfgTemplate, map[string]string{"size": "large"}, "req"); problem != "" {
		t.Fatalf("problem = %q, want size accepted", problem)
	}
}

func evaluateParametersRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := coderConfigRequest(t, http.MethodPost, body, []string{sandbox.ProviderCoder})
	chi.RouteContext(req.Context()).URLParams.Add("templateId", coderCfgTemplate)
	return req
}

func decodeParameterNames(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Parameters []coder.TemplateParameter `json:"parameters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(response.Parameters))
	for _, parameter := range response.Parameters {
		if parameter.Options == nil {
			t.Fatalf("%s options = nil, want []", parameter.Name)
		}
		names = append(names, parameter.Name)
	}
	return names
}

func TestEvaluateCoderTemplateParametersListsConditionalParameters(t *testing.T) {
	t.Parallel()
	srv := newDynamicParametersServer(t, &fakeDynamicTemplateReader{})
	rec := httptest.NewRecorder()
	srv.evaluateCoderTemplateParameters(rec, evaluateParametersRequest(t, `{"values":{"github_repos":"[\"Other\"]"}}`))
	if names := strings.Join(decodeParameterNames(t, rec), ","); names != "github_repos,other_github_repo,region" {
		t.Fatalf("parameters = %s", names)
	}
	rec = httptest.NewRecorder()
	srv.evaluateCoderTemplateParameters(rec, evaluateParametersRequest(t, `{"values":{}}`))
	if names := strings.Join(decodeParameterNames(t, rec), ","); names != "github_repos,region" {
		t.Fatalf("parameters without the trigger = %s", names)
	}
	// Without dynamic parameters the endpoint answers like the GET.
	srv = newDynamicParametersServer(t, &fakeDynamicTemplateReader{evaluateErr: coder.ErrDynamicParametersUnsupported})
	rec = httptest.NewRecorder()
	srv.evaluateCoderTemplateParameters(rec, evaluateParametersRequest(t, `{"values":{"github_repos":"[\"Other\"]"}}`))
	if names := strings.Join(decodeParameterNames(t, rec), ","); names != "github_repos,region" {
		t.Fatalf("static fallback parameters = %s", names)
	}
	rec = httptest.NewRecorder()
	srv.evaluateCoderTemplateParameters(rec, evaluateParametersRequest(t, `{"values":{"github_repos":7}}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-string value status = %d, want 400", rec.Code)
	}
}
