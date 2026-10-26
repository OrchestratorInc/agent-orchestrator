package coder

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const evaluateVersionID = "9da8c495-9a69-4893-997c-342afbb22f6e"

// fakeDynamicCoder answers the template, owner and evaluate endpoints. The
// template declares other_github_repo only when github_repos selects "Other".
func fakeDynamicCoder(t *testing.T, classic bool, evaluateStatus int, body *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v2/templates/" + testTemplateID:
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": testTemplateID, "active_version_id": evaluateVersionID, "use_classic_parameter_flow": classic})
		case "/api/v2/users/ao-integration":
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": "owner-uuid"})
		case "/api/v2/templateversions/" + evaluateVersionID + "/dynamic-parameters/evaluate":
			if request.Method != http.MethodPost {
				http.Error(writer, "method", http.StatusMethodNotAllowed)
				return
			}
			if evaluateStatus != 0 {
				http.Error(writer, "nope", evaluateStatus)
				return
			}
			var payload map[string]any
			_ = json.NewDecoder(request.Body).Decode(&payload)
			if body != nil {
				*body = payload
			}
			inputs, _ := payload["inputs"].(map[string]any)
			if inputs["github_repos"] == "boom" {
				_, _ = io.WriteString(writer, `{"id":1,"diagnostics":[{"severity":"error","summary":"Failed to fetch workspace owner","detail":"","extra":{"code":"owner_not_found"}}],"parameters":null}`)
				return
			}
			parameters := `{"name":"github_repos","display_name":"Github Repositories","type":"list(string)","form_type":"multi-select","mutable":false,"required":false,
				"default_value":{"value":"[\"Alice\"]","valid":true},"options":[{"name":"Other","description":"","value":{"value":"Other","valid":true}},{"name":"Alice","description":"","value":{"value":"Alice","valid":true}}],"diagnostics":[]}`
			if s, _ := inputs["github_repos"].(string); strings.Contains(s, "Other") {
				parameters += `,{"name":"other_github_repo","display_name":"Other Github Repository","type":"string","form_type":"input","default_value":{"value":"","valid":true},"options":[],"diagnostics":[]}`
			}
			parameters += `,{"name":"architecture","display_name":"CPU Architecture","type":"string","form_type":"radio","default_value":{"value":"arm64","valid":true},"options":[],
				"diagnostics":[{"severity":"error","summary":"Value must be a valid option","detail":"the value \"sparc\" must be defined as one of options","extra":{"code":""}}]}`
			_, _ = io.WriteString(writer, `{"id":1,"diagnostics":[],"parameters":[`+parameters+`]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
}

func TestEvaluateTemplateParametersAppliesValues(t *testing.T) {
	t.Parallel()
	var sent map[string]any
	server := fakeDynamicCoder(t, false, 0, &sent)
	defer server.Close()
	client := newTestClient(t, server.URL, nil)
	parameters, err := client.EvaluateTemplateParameters(context.Background(), testTemplateID, map[string]string{"github_repos": `["Other"]`})
	if err != nil {
		t.Fatal(err)
	}
	if sent["owner_id"] != "owner-uuid" || sent["inputs"].(map[string]any)["github_repos"] != `["Other"]` {
		t.Fatalf("evaluate body = %v", sent)
	}
	if len(parameters) != 3 || parameters[1].Name != "other_github_repo" || parameters[1].DisplayName != "Other Github Repository" || parameters[1].Order != 1 {
		t.Fatalf("parameters = %+v", parameters)
	}
	repos := parameters[0]
	if repos.DefaultValue != `["Alice"]` || len(repos.Options) != 2 || repos.Options[0] != (TemplateParameterOption{Name: "Other", Value: "Other"}) || repos.Diagnostics != nil {
		t.Fatalf("github_repos = %+v", repos)
	}
	if arch := parameters[2]; len(arch.Diagnostics) != 1 || arch.Diagnostics[0].Severity != "error" || arch.Diagnostics[0].Summary != "Value must be a valid option" {
		t.Fatalf("architecture diagnostics = %+v", arch.Diagnostics)
	}
	// Without the trigger the conditional parameter is not declared.
	parameters, err = client.EvaluateTemplateParameters(context.Background(), testTemplateID, nil)
	if err != nil || len(parameters) != 2 {
		t.Fatalf("parameters = %+v, err = %v", parameters, err)
	}
	if sent["inputs"] == nil {
		t.Fatal("nil values sent as null inputs")
	}
}

func TestEvaluateTemplateParametersUnsupportedAndFailures(t *testing.T) {
	t.Parallel()
	classic := fakeDynamicCoder(t, true, 0, nil)
	defer classic.Close()
	if _, err := newTestClient(t, classic.URL, nil).EvaluateTemplateParameters(context.Background(), testTemplateID, nil); !errors.Is(err, ErrDynamicParametersUnsupported) {
		t.Fatalf("classic template error = %v", err)
	}
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		old := fakeDynamicCoder(t, false, status, nil)
		if _, err := newTestClient(t, old.URL, nil).EvaluateTemplateParameters(context.Background(), testTemplateID, nil); !errors.Is(err, ErrDynamicParametersUnsupported) {
			t.Fatalf("evaluate %d error = %v, want unsupported", status, err)
		}
		old.Close()
	}
	server := fakeDynamicCoder(t, false, 0, nil)
	defer server.Close()
	client := newTestClient(t, server.URL, nil)
	if _, err := client.EvaluateTemplateParameters(context.Background(), testTemplateID, map[string]string{"github_repos": "boom"}); err == nil ||
		errors.Is(err, ErrDynamicParametersUnsupported) || !strings.Contains(err.Error(), "Failed to fetch workspace owner") {
		t.Fatalf("top-level diagnostic error = %v", err)
	}
	if _, err := client.EvaluateTemplateParameters(context.Background(), "6f1d2c3b-0000-4000-8000-000000000000", nil); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("unknown template error = %v", err)
	}
}
