package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/coder"
	"github.com/aoagents/agent-orchestrator/cloud/internal/secrets"
	"github.com/go-chi/chi/v5"
)

const (
	devEnvVersionID = "5c8d3f0e-4d2b-4a63-9a8e-3b2b2d6a7c02"
	devEnvProjectID = "00000000-0000-0000-0000-0000000000b2"
)

// The dev-environment fields are accepted by PUT, stored in the connection's
// config JSON, echoed on the response and returned again by GET.
func TestOrgCoderConfigDevEnvironmentRoundTrip(t *testing.T) {
	t.Parallel()
	store := &coderConfigFakeStore{}
	srv, _ := newCoderConfigServer(t, store, false)
	body := `{"token":"` + coderCfgToken + `","baseUrl":"https://coder.acme.example.com","owner":"ao-bot",` +
		`"workerUser":"workspace","readyProbe":"grep -q '^Ready' ~/.bootstrap-status","readyProbeTimeoutSeconds":1200}`
	rec := httptest.NewRecorder()
	srv.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut, body, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d; body = %s", rec.Code, rec.Body.String())
	}
	want := map[string]any{
		"workerUser":               "workspace",
		"readyProbe":               "grep -q '^Ready' ~/.bootstrap-status",
		"readyProbeTimeoutSeconds": float64(1200),
		"reuseExistingCheckout":    true,
	}
	assertDevEnvFields := func(label string, raw []byte) {
		t.Helper()
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		for key, value := range want {
			if !reflect.DeepEqual(decoded[key], value) {
				t.Fatalf("%s %s = %#v, want %#v (%s)", label, key, decoded[key], value, raw)
			}
		}
	}
	assertDevEnvFields("stored config", store.lastConfig)
	var put struct {
		CoderConfig json.RawMessage `json:"coderConfig"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &put); err != nil {
		t.Fatal(err)
	}
	assertDevEnvFields("PUT response", put.CoderConfig)

	store.connections = []domain.ProviderConnection{{
		ID: "conn-1", Provider: sandbox.ProviderCoder, Label: defaultAgentConnectionLabel, Config: store.lastConfig,
	}}
	rec = httptest.NewRecorder()
	srv.getOrgCoderConfig(rec, coderConfigRequest(t, http.MethodGet, "", nil))
	var get struct {
		CoderConfig json.RawMessage `json:"coderConfig"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &get); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body.String())
	}
	assertDevEnvFields("GET response", get.CoderConfig)
}

// An existing connection saved without the fields reads back with the
// historical defaults made explicit.
func TestOrgCoderConfigDevEnvironmentDefaults(t *testing.T) {
	t.Parallel()
	store := &coderConfigFakeStore{}
	srv, _ := newCoderConfigServer(t, store, false)
	rec := httptest.NewRecorder()
	srv.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut, validCoderConfigBody(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	cfg, err := domain.DecodeOrgCoderConfig(store.lastConfig)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkerUser != domain.CoderWorkerUserAOWorker || cfg.ReusesExistingCheckout() || cfg.ReadyProbe != "" || cfg.ReadyProbeTimeoutSeconds != 0 {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestPutOrgCoderConfigRejectsInvalidDevEnvironment(t *testing.T) {
	t.Parallel()
	base := `"token":"` + coderCfgToken + `","baseUrl":"https://coder.acme.example.com","owner":"ao-bot"`
	cases := map[string]struct {
		extra  string
		status int
	}{
		"unknown worker user":       {`,"workerUser":"root"`, http.StatusUnprocessableEntity},
		"ready check too long":      {`,"readyProbe":"` + strings.Repeat("x", domain.MaxCoderReadyProbeLength+1) + `"`, http.StatusUnprocessableEntity},
		"timeout below minimum":     {`,"readyProbe":"true","readyProbeTimeoutSeconds":10`, http.StatusUnprocessableEntity},
		"timeout above maximum":     {`,"readyProbe":"true","readyProbeTimeoutSeconds":7200`, http.StatusUnprocessableEntity},
		"timeout of the wrong type": {`,"readyProbeTimeoutSeconds":"900"`, http.StatusBadRequest},
		"reuse of the wrong type":   {`,"reuseExistingCheckout":"yes"`, http.StatusBadRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := &coderConfigFakeStore{}
			srv, _ := newCoderConfigServer(t, store, false)
			rec := httptest.NewRecorder()
			srv.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut, "{"+base+tc.extra+"}", nil))
			if rec.Code != tc.status || store.upserted != 0 {
				t.Fatalf("status = %d (want %d), upserted = %d; body = %s", rec.Code, tc.status, store.upserted, rec.Body.String())
			}
		})
	}
}

// devEnvCoderServer is an organization's Coder answering the template and
// template-version reads the parameters endpoint needs.
func devEnvCoderServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/users/me":
			if r.Header.Get("Coder-Session-Token") != coderCfgToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"username": "ao-bot"})
		case "/api/v2/templates":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": coderCfgTemplate, "name": "dev", "active_version_id": devEnvVersionID}})
		case "/api/v2/templates/" + coderCfgTemplate:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": coderCfgTemplate, "active_version_id": devEnvVersionID})
		case "/api/v2/templateversions/" + devEnvVersionID + "/rich-parameters":
			_, _ = io.WriteString(w, `[
				{"name":"region","display_name":"Region","description":"Where it runs","type":"string","form_type":"dropdown",
				 "mutable":false,"required":true,"default_value":"eu","options":[{"name":"Europe","value":"eu","description":"EU"}]},
				{"name":"gpu","display_name":"GPU","type":"bool","form_type":"switch","mutable":true,"default_value":"false","options":null}
			]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// devEnvStore serves an organization's Coder connection and one project for
// the parameters endpoint and the project-settings validation.
type devEnvStore struct {
	coderConfigFakeStore
	encrypted, nonce, config []byte
	project                  domain.Project
	settingsCalls            int
}

func (s *devEnvStore) CoderConnectionForService(context.Context, string) ([]byte, []byte, []byte, string, error) {
	if s.config == nil {
		return nil, nil, nil, "", postgres.ErrNotFound
	}
	return s.encrypted, s.nonce, s.config, "conn-1", nil
}

func (s *devEnvStore) GetProject(context.Context, domain.Principal, string, string) (domain.Project, error) {
	return s.project, nil
}

func (s *devEnvStore) UpdateProjectSettings(_ context.Context, _ domain.Principal, _, _ string, _ domain.ProjectSettingsPatch) (domain.Project, error) {
	s.settingsCalls++
	return s.project, nil
}

func newDevEnvServer(t *testing.T, coderURL string) (*Server, *devEnvStore) {
	t.Helper()
	cipher := newTestCipher(t)
	store := &devEnvStore{}
	if coderURL != "" {
		config, err := domain.EncodeOrgCoderConfig(domain.OrgCoderConfig{BaseURL: coderURL, Owner: "ao-bot"})
		if err != nil {
			t.Fatal(err)
		}
		encrypted, nonce, err := cipher.Encrypt([]byte(coderCfgToken),
			secrets.ProviderConnectionAssociatedData(coderCfgOrgID, sandbox.ProviderCoder, coder.OrgConnectionLabel))
		if err != nil {
			t.Fatal(err)
		}
		store.encrypted, store.nonce, store.config = encrypted, nonce, config
	}
	return newCoderTemplatesServer(t, cipher, store, nil), store
}

func templateParametersRequest(t *testing.T, templateID string, caps []string) *http.Request {
	t.Helper()
	request := coderConfigRequest(t, http.MethodGet, "", caps)
	chi.RouteContext(request.Context()).URLParams.Add("templateId", templateID)
	return request
}

// The parameters endpoint reads the template's active version from the
// organization's own Coder and returns the form-ready shape.
func TestGetCoderTemplateParametersFromOrgConnection(t *testing.T) {
	t.Parallel()
	srv, _ := newDevEnvServer(t, devEnvCoderServer(t).URL)
	rec := httptest.NewRecorder()
	srv.getCoderTemplateParameters(rec, templateParametersRequest(t, coderCfgTemplate, []string{sandbox.ProviderCoder}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"parameters": []any{
		map[string]any{
			"name": "region", "displayName": "Region", "description": "Where it runs", "type": "string",
			"formType": "dropdown", "mutable": false, "required": true, "defaultValue": "eu", "order": float64(0),
			"options": []any{map[string]any{"name": "Europe", "value": "eu", "description": "EU"}},
		},
		map[string]any{
			"name": "gpu", "displayName": "GPU", "description": "", "type": "bool",
			"formType": "switch", "mutable": true, "required": false, "defaultValue": "false", "order": float64(1),
			"options": []any{},
		},
	}}
	if !reflect.DeepEqual(response, want) {
		t.Fatalf("response = %s", rec.Body.String())
	}
}

func TestGetCoderTemplateParametersErrors(t *testing.T) {
	t.Parallel()
	coderURL := devEnvCoderServer(t).URL
	cases := map[string]struct {
		coderURL, templateID string
		caps                 []string
		status               int
		code                 string
	}{
		"unknown template":    {coderURL, "6f1d2c3b-0000-4000-8000-000000000000", []string{sandbox.ProviderCoder}, http.StatusNotFound, "coder_template_not_found"},
		"no coder configured": {"", coderCfgTemplate, []string{sandbox.ProviderCoder}, http.StatusNotFound, "coder_template_not_found"},
		"bad template ID":     {coderURL, "not-a-uuid", []string{sandbox.ProviderCoder}, http.StatusBadRequest, "invalid_request"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv, _ := newDevEnvServer(t, tc.coderURL)
			rec := httptest.NewRecorder()
			srv.getCoderTemplateParameters(rec, templateParametersRequest(t, tc.templateID, tc.caps))
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("status = %d, body = %s; want %d %s", rec.Code, rec.Body.String(), tc.status, tc.code)
			}
		})
	}
}

// Like every Coder endpoint, the parameters endpoint is 403 provider_forbidden
// for an organization that is not entitled to Coder.
func TestGetCoderTemplateParametersForbiddenWithoutCoder(t *testing.T) {
	t.Parallel()
	// Template parameters are open to every organization that may run on Coder,
	// so only a deployment without Coder refuses them.
	srv := New(Options{
		Store:                     &coderConfigFakeStore{},
		SecretCipher:              newTestCipher(t),
		SandboxProvider:           sandbox.ProviderFreestyle,
		AvailableSandboxProviders: []string{sandbox.ProviderFreestyle},
		Logger:                    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	rec := httptest.NewRecorder()
	srv.getCoderTemplateParameters(rec, templateParametersRequest(t, coderCfgTemplate, []string{sandbox.ProviderCoder}))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "provider_forbidden") {
		t.Fatalf("status = %d, body = %s; want 403 provider_forbidden", rec.Code, rec.Body.String())
	}
}

// A project config's coder.parameters need a chosen template and must be
// parameters that template declares; an unreadable template is not a reason
// to reject.
func TestValidateProjectCoderParameters(t *testing.T) {
	t.Parallel()
	coderURL := devEnvCoderServer(t).URL
	cases := map[string]struct {
		coderURL, config string
		ok               bool
	}{
		"no parameters":        {coderURL, `{"coder":{"templateId":"` + coderCfgTemplate + `"}}`, true},
		"declared parameters":  {coderURL, `{"coder":{"templateId":"` + coderCfgTemplate + `","parameters":{"region":"us","gpu":"true"}}}`, true},
		"undeclared parameter": {coderURL, `{"coder":{"templateId":"` + coderCfgTemplate + `","parameters":{"region":"us","zone":"a"}}}`, false},
		"no template":          {coderURL, `{"coder":{"parameters":{"region":"us"}}}`, false},
		"template list unread": {"", `{"coder":{"templateId":"` + coderCfgTemplate + `","parameters":{"zone":"a"}}}`, true},
		"not a coder config":   {coderURL, `{"sandboxProvider":"freestyle"}`, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv, _ := newDevEnvServer(t, tc.coderURL)
			rec := httptest.NewRecorder()
			ok := srv.validateProjectCoderParameters(rec, coderConfigRequest(t, http.MethodPost, "", nil), coderCfgOrgID, json.RawMessage(tc.config))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v; body = %s", ok, tc.ok, rec.Body.String())
			}
			if !ok && (rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "validation_error")) {
				t.Fatalf("rejection = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
	if err := validateProjectCoderConfig(json.RawMessage(`{"coder":{"templateId":"` + coderCfgTemplate + `","parameters":{"":"x"}}}`)); err == nil {
		t.Fatal("malformed parameter name accepted")
	}
}

func TestParseCoderConfigInputParameters(t *testing.T) {
	t.Parallel()
	cfg, err := parseCoderConfigInput(&coderConfigInput{
		TemplateID: coderCfgTemplate, Parameters: map[string]string{"region": "us", "repos": `["a"]`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Parameters, map[string]string{"region": "us", "repos": `["a"]`}) {
		t.Fatalf("parameters = %v", cfg.Parameters)
	}
	if _, err := parseCoderConfigInput(&coderConfigInput{Parameters: map[string]string{"region": "us"}}); err == nil {
		t.Fatal("parameters without a template accepted")
	}
	if _, err := parseCoderConfigInput(&coderConfigInput{
		TemplateID: coderCfgTemplate, Parameters: map[string]string{"region": strings.Repeat("x", domain.MaxCoderTemplateParameterValueSize+1)},
	}); err == nil {
		t.Fatal("oversized parameter value accepted")
	}
}

// Project settings may change the template parameters, checked against the
// project's template; a project without a chosen template cannot take them.
func TestUpdateProjectSettingsValidatesCoderParameters(t *testing.T) {
	t.Parallel()
	coderURL := devEnvCoderServer(t).URL
	withTemplate := json.RawMessage(`{"coder":{"templateId":"` + coderCfgTemplate + `"}}`)
	cases := map[string]struct {
		projectConfig json.RawMessage
		body          string
		status, calls int
	}{
		"declared":    {withTemplate, `{"config":{"coder":{"parameters":{"region":"us"}}}}`, http.StatusOK, 1},
		"cleared":     {withTemplate, `{"config":{"coder":{"parameters":{}}}}`, http.StatusOK, 1},
		"undeclared":  {withTemplate, `{"config":{"coder":{"parameters":{"zone":"a"}}}}`, http.StatusUnprocessableEntity, 0},
		"no template": {json.RawMessage(`{}`), `{"config":{"coder":{"parameters":{"region":"us"}}}}`, http.StatusUnprocessableEntity, 0},
		"non-string":  {withTemplate, `{"config":{"coder":{"parameters":{"region":1}}}}`, http.StatusUnprocessableEntity, 0},
		"prefix only": {json.RawMessage(`{}`), `{"config":{"coder":{"workspaceNamePrefix":"dev"}}}`, http.StatusOK, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv, store := newDevEnvServer(t, coderURL)
			store.project = domain.Project{ID: devEnvProjectID, OrgID: coderCfgOrgID, Config: tc.projectConfig}
			router := chi.NewRouter()
			router.Patch("/orgs/{orgId}/projects/{projectId}/settings", srv.updateProjectSettings)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch,
				"/orgs/"+coderCfgOrgID+"/projects/"+devEnvProjectID+"/settings", strings.NewReader(tc.body)))
			if rec.Code != tc.status || store.settingsCalls != tc.calls {
				t.Fatalf("status = %d (want %d), calls = %d (want %d); body = %s",
					rec.Code, tc.status, store.settingsCalls, tc.calls, rec.Body.String())
			}
		})
	}
}

// Editing a saved connection may omit the token: the desktop sends the full
// stored config back (owner omitted, re-derived from the kept token). A first
// save or a different deployment still needs a token.
func TestPutOrgCoderConfigKeepsStoredTokenForSameDeployment(t *testing.T) {
	t.Parallel()
	coderURL := devEnvCoderServer(t).URL
	desktopBody := `{"baseUrl":"` + coderURL + `","templateId":"` + coderCfgTemplate + `","agentName":"dev",` +
		`"parameters":{"region":"eu"},"durableRoot":"$HOME","requireMountedDurableRoot":false,"startupTimeoutSeconds":1200,` +
		`"endpointServiceName":"","region":"","workerUser":"workspace","reuseExistingCheckout":false,` +
		`"readyProbe":"test -f ~/.ready","readyProbeTimeoutSeconds":600}`

	srv, store := newDevEnvServer(t, coderURL)
	rec := httptest.NewRecorder()
	srv.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut, desktopBody, nil))
	if rec.Code != http.StatusOK || store.upserted != 1 {
		t.Fatalf("status = %d, upserted = %d; body = %s", rec.Code, store.upserted, rec.Body.String())
	}
	plain, err := srv.secretCipher.Decrypt(store.lastEncrypted, store.lastNonce, providerSecretAssociatedData(coderCfgOrgID, sandbox.ProviderCoder))
	if err != nil || string(plain) != coderCfgToken {
		t.Fatalf("stored token = %q, %v; want the kept token", plain, err)
	}
	cfg, err := domain.DecodeOrgCoderConfig(store.lastConfig)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Owner != "ao-bot" || cfg.WorkerUser != domain.CoderWorkerUserWorkspace || cfg.ReusesExistingCheckout() ||
		cfg.ReadyProbeTimeoutSeconds != 600 || cfg.StartupTimeoutSeconds != 1200 || cfg.Parameters["region"] != "eu" {
		t.Fatalf("stored config = %+v", cfg)
	}

	moved, store := newDevEnvServer(t, coderURL)
	rec = httptest.NewRecorder()
	moved.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut,
		strings.Replace(desktopBody, coderURL, "https://elsewhere.example.com", 1), nil))
	if rec.Code != http.StatusUnprocessableEntity || store.upserted != 0 {
		t.Fatalf("deployment change without a token: status = %d, upserted = %d", rec.Code, store.upserted)
	}

	fresh, store := newDevEnvServer(t, "")
	rec = httptest.NewRecorder()
	fresh.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut, desktopBody, nil))
	if rec.Code != http.StatusUnprocessableEntity || store.upserted != 0 {
		t.Fatalf("first save without a token: status = %d, upserted = %d", rec.Code, store.upserted)
	}
}

// The project's parameters and the connection's dev-environment options reach
// the session placement.
func TestResolveCoderPlacementCarriesDevEnvironment(t *testing.T) {
	t.Parallel()
	connectionConfig, err := domain.EncodeOrgCoderConfig(domain.OrgCoderConfig{
		BaseURL: "https://coder.acme.example", Owner: "dev", WorkerUser: "workspace",
		ReadyProbe: "test -f ~/.ready", Parameters: map[string]string{"region": "eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	placement, err := resolveCoderPlacement(
		json.RawMessage(`{"coder":{"templateId":"`+coderCfgTemplate+`","parameters":{"region":"us"}}}`), "https://github.com/acme/alice",
		[]domain.ProviderConnection{{ID: "conn-1", Provider: sandbox.ProviderCoder, Label: defaultAgentConnectionLabel, Config: connectionConfig}},
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if placement.options == nil || placement.options.Parameters["region"] != "us" {
		t.Fatalf("options = %+v", placement.options)
	}
	override := placement.override
	if override == nil || override.WorkerUser != "workspace" || !override.ReuseExistingCheckout ||
		override.ReadyProbe != "test -f ~/.ready" || override.ReadyProbeTimeoutSeconds != domain.DefaultCoderReadyProbeTimeoutSeconds ||
		override.Parameters["region"] != "eu" {
		t.Fatalf("override = %+v", override)
	}
}

// A project's working directory wins over the connection's, and only applies
// when AO runs as the workspace user.
func TestResolveCoderPlacementPrefersTheProjectWorkingDirectory(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, workerUser, project, connection, want string
	}{
		{"project wins", domain.CoderWorkerUserWorkspace, "~/work/{repo}", "~/code/{repo}", "~/work/{repo}"},
		{"connection kept", domain.CoderWorkerUserWorkspace, "", "~/code/{repo}", "~/code/{repo}"},
		{"needs the workspace user", "", "~/work/{repo}", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connectionConfig, err := domain.EncodeOrgCoderConfig(domain.OrgCoderConfig{
				BaseURL: "https://coder.acme.example", Owner: "dev", WorkerUser: tc.workerUser, WorkingDirectory: tc.connection,
			})
			if err != nil {
				t.Fatal(err)
			}
			projectConfig := `{}`
			if tc.project != "" {
				projectConfig = `{"workingDirectory":"` + tc.project + `"}`
			}
			placement, err := resolveCoderPlacement(json.RawMessage(projectConfig), "https://github.com/acme/alice",
				[]domain.ProviderConnection{{ID: "conn-1", Provider: sandbox.ProviderCoder, Label: defaultAgentConnectionLabel, Config: connectionConfig}}, "")
			if err != nil {
				t.Fatal(err)
			}
			if placement.override == nil || placement.override.WorkingDirectory != tc.want {
				t.Fatalf("override = %+v, want working directory %q", placement.override, tc.want)
			}
		})
	}
}

// With the dedicated ao-worker user (a bring-your-own connection's default,
// or the deployment's Coder), the project's working folder becomes the
// session's checkout folder with {repo} resolved, plus the additional
// repositories' folders beside it. The workspace user keeps its working
// directory, and a project without a working folder or a repository keeps the
// default checkout.
func TestResolveCoderPlacementStampsTheWorkingFolderCheckoutForAOWorker(t *testing.T) {
	t.Parallel()
	byo := func(workerUser string) []domain.ProviderConnection {
		config, err := domain.EncodeOrgCoderConfig(domain.OrgCoderConfig{
			BaseURL: "https://coder.acme.example", Owner: "dev", WorkerUser: workerUser,
		})
		if err != nil {
			t.Fatal(err)
		}
		return []domain.ProviderConnection{{ID: "conn-1", Provider: sandbox.ProviderCoder, Label: defaultAgentConnectionLabel, Config: config}}
	}
	withExtras := `{"workingDirectory":"~/work/{repo}","repos":[{"url":"https://github.com/acme/billing"},{"url":"https://github.com/acme/.dotfiles"},{"url":"https://github.com/other/billing.git"}]}`
	for _, tc := range []struct {
		name         string
		config       string
		repository   string
		connections  []domain.ProviderConnection
		wantCheckout string
		wantSiblings []string
	}{
		{"byo ao-worker", withExtras, "https://github.com/acme/alice.git", byo(""), "~/work/alice", []string{"billing"}},
		{"managed coder", `{"workingDirectory":"/srv/{repo}/src"}`, "https://github.com/acme/alice", nil, "/srv/alice/src", nil},
		{"workspace user", withExtras, "https://github.com/acme/alice", byo(domain.CoderWorkerUserWorkspace), "", nil},
		{"no working folder", `{}`, "https://github.com/acme/alice", byo(""), "", nil},
		{"standalone", `{"workingDirectory":"~/work/scratch"}`, "https://scratch.ao.local/abc", byo(""), "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			placement, err := resolveCoderPlacement(json.RawMessage(tc.config), tc.repository, tc.connections, "")
			if err != nil {
				t.Fatal(err)
			}
			checkout, siblings := "", []string(nil)
			if placement.options != nil {
				checkout, siblings = placement.options.CheckoutDirectory, placement.options.CheckoutSiblings
			}
			if checkout != tc.wantCheckout || !slices.Equal(siblings, tc.wantSiblings) {
				t.Fatalf("checkout = %q %q, want %q %q", checkout, siblings, tc.wantCheckout, tc.wantSiblings)
			}
			if placement.override != nil && tc.wantCheckout != "" && placement.override.WorkingDirectory != "" {
				t.Fatalf("ao-worker session got a working directory: %+v", placement.override)
			}
		})
	}
}

// A working folder the ao-worker checkout refuses (here, inside a hidden
// folder, which a project saved before that rule may hold) is not dropped:
// the session carries the reason, so it stops with a startup error instead of
// starting in the durable root checkout. The workspace user keeps the folder.
func TestResolveCoderPlacementRecordsAnUnusableWorkingFolder(t *testing.T) {
	t.Parallel()
	config := json.RawMessage(`{"workingDirectory":"~/.config/{repo}"}`)
	placement, err := resolveCoderPlacement(config, "https://github.com/acme/alice", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if placement.options == nil || placement.options.CheckoutDirectory != "" ||
		placement.options.CheckoutDirectoryUnusable != "AO couldn't use this project's working folder (~/.config/{repo}): "+
			"The working folder must not be inside a hidden folder. Change it in Project settings > Repositories, then start a new session." {
		t.Fatalf("placement options = %+v", placement.options)
	}
	workspace, err := domain.EncodeOrgCoderConfig(domain.OrgCoderConfig{
		BaseURL: "https://coder.acme.example", Owner: "dev", WorkerUser: domain.CoderWorkerUserWorkspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	connections := []domain.ProviderConnection{{ID: "conn-1", Provider: sandbox.ProviderCoder, Label: defaultAgentConnectionLabel, Config: workspace}}
	placement, err = resolveCoderPlacement(config, "https://github.com/acme/alice", connections, "")
	if err != nil {
		t.Fatal(err)
	}
	if placement.options != nil && placement.options.CheckoutDirectoryUnusable != "" {
		t.Fatalf("workspace-user placement options = %+v", placement.options)
	}
	if placement.override == nil || placement.override.WorkingDirectory != "~/.config/{repo}" {
		t.Fatalf("workspace-user override = %+v", placement.override)
	}
}

// A client that sends only the base URL and a new token (an older desktop)
// keeps the connection's stored settings instead of resetting them.
func TestPutOrgCoderConfigKeepsStoredSettingsTheRequestOmits(t *testing.T) {
	t.Parallel()
	coderURL := devEnvCoderServer(t).URL
	srv, store := newDevEnvServer(t, coderURL)
	reuse := false
	config, err := domain.EncodeOrgCoderConfig(domain.OrgCoderConfig{
		BaseURL: coderURL, Owner: "ao-bot", TemplateID: coderCfgTemplate, WorkerUser: domain.CoderWorkerUserWorkspace,
		ReadyProbe: "test -f ~/.ready", ReadyProbeTimeoutSeconds: 600, ReuseExistingCheckout: &reuse,
		StartupTimeoutSeconds: 1200, Region: "eu-north-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	store.config = config

	rec := httptest.NewRecorder()
	srv.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut,
		`{"baseUrl":"`+coderURL+`","token":"`+coderCfgToken+`","readyProbe":""}`, nil))
	if rec.Code != http.StatusOK || store.upserted != 1 {
		t.Fatalf("status = %d, upserted = %d; body = %s", rec.Code, store.upserted, rec.Body.String())
	}
	cfg, err := domain.DecodeOrgCoderConfig(store.lastConfig)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TemplateID != coderCfgTemplate || cfg.WorkerUser != domain.CoderWorkerUserWorkspace || cfg.ReusesExistingCheckout() ||
		cfg.StartupTimeoutSeconds != 1200 || cfg.Region != "eu-north-1" {
		t.Fatalf("stored config = %+v, want the omitted settings kept", cfg)
	}
	if cfg.ReadyProbe != "" {
		t.Fatalf("ready probe = %q, want the explicit clear applied", cfg.ReadyProbe)
	}
}

// nonAdminDevEnvStore is the organization's connection seen by a member who is
// not an admin.
type nonAdminDevEnvStore struct {
	devEnvStore
	connectionRead bool
}

func (s *nonAdminDevEnvStore) RequireOrgAdmin(context.Context, domain.Principal, string) error {
	return postgres.ErrForbidden
}

func (s *nonAdminDevEnvStore) CoderConnectionForService(ctx context.Context, orgID string) ([]byte, []byte, []byte, string, error) {
	s.connectionRead = true
	return s.devEnvStore.CoderConnectionForService(ctx, orgID)
}

// A non-admin cannot test a connection: the stored token is never read and
// nothing is saved.
func TestPutOrgCoderConfigRefusesNonAdminBeforeReadingTheToken(t *testing.T) {
	t.Parallel()
	coderURL := devEnvCoderServer(t).URL
	_, base := newDevEnvServer(t, coderURL)
	store := &nonAdminDevEnvStore{devEnvStore: *base}
	srv := newCoderTemplatesServer(t, newTestCipher(t), store, nil)
	rec := httptest.NewRecorder()
	srv.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut, `{"baseUrl":"`+coderURL+`"}`, nil))
	if rec.Code != http.StatusForbidden || store.upserted != 0 || store.connectionRead {
		t.Fatalf("status = %d, upserted = %d, connection read = %v", rec.Code, store.upserted, store.connectionRead)
	}
}

// A project working folder outside the durable root of the organization's
// Coder connection is refused when it is saved, for a connection that runs
// the agent as ao-worker: its checkout would not survive a restart. The
// workspace user keeps any folder (it adopts the template's own checkout).
// With AO_CLOUD_CODER_WORKER_WORKING_FOLDER off an ao-worker session ignores
// the working folder, so the folder is saved as it was before the flag.
func TestUpdateProjectSettingsRefusesAWorkingFolderOutsideTheDurableRoot(t *testing.T) {
	t.Parallel()
	connection := func(t *testing.T, durableRoot, workerUser string) []domain.ProviderConnection {
		t.Helper()
		config, err := domain.EncodeOrgCoderConfig(domain.OrgCoderConfig{
			BaseURL: "https://coder.acme.test", Owner: "ao-bot", DurableRoot: durableRoot, WorkerUser: workerUser,
		})
		if err != nil {
			t.Fatal(err)
		}
		return []domain.ProviderConnection{{ID: "conn-1", Provider: sandbox.ProviderCoder, Label: "default", Config: config}}
	}
	for name, tc := range map[string]struct {
		durableRoot, workerUser, folder string
		status                          int
		message                         string
	}{
		"home, docker folder":      {"$HOME", "", "/workspace/{repo}", http.StatusUnprocessableEntity, "The working folder must be inside your home folder (~) so the agent's work survives restarts. Choose a folder there, for example ~/work/{repo}."},
		"home, home folder":        {"$HOME", "", "~/work/{repo}", http.StatusOK, ""},
		"absolute root, outside":   {"/srv/dev", "", "/srv/other/{repo}", http.StatusUnprocessableEntity, "The working folder must be inside /srv/dev so the agent's work survives restarts. Choose a folder there, for example /srv/dev/work/{repo}."},
		"absolute root, inside":    {"/srv/dev", "", "/srv/dev/work/{repo}", http.StatusOK, ""},
		"absolute root, home path": {"/srv/dev", "", "~/work/{repo}", http.StatusOK, ""},
		"workspace user":           {"$HOME", domain.CoderWorkerUserWorkspace, "/workspace/{repo}", http.StatusOK, ""},
	} {
		for _, flag := range []bool{true, false} {
			name, tc := name+", working folder flag on", tc
			if !flag {
				name, tc.status, tc.message = strings.TrimSuffix(name, "on")+"off", http.StatusOK, ""
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				store := &adminDevEnvStore{}
				store.connections = connection(t, tc.durableRoot, tc.workerUser)
				store.project = domain.Project{ID: devEnvProjectID, OrgID: coderCfgOrgID, Config: json.RawMessage(`{}`)}
				srv := newCoderTemplatesServer(t, newTestCipher(t), store, nil)
				srv.provisioning.Coder.WorkerWorkingFolder = flag
				router := chi.NewRouter()
				router.Patch("/orgs/{orgId}/projects/{projectId}/settings", srv.updateProjectSettings)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch,
					"/orgs/"+coderCfgOrgID+"/projects/"+devEnvProjectID+"/settings",
					strings.NewReader(`{"config":{"workingDirectory":"`+tc.folder+`"}}`)))
				if rec.Code != tc.status {
					t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.status, rec.Body.String())
				}
				if tc.message != "" && (!strings.Contains(rec.Body.String(), domain.CoderWorkingFolderNotDurableCode) ||
					!strings.Contains(rec.Body.String(), tc.message)) {
					t.Fatalf("body = %s, want %q", rec.Body.String(), tc.message)
				}
			})
		}
	}
}

// A project working folder inside a hidden folder is refused when it is
// saved for an organization whose Coder runs the agent as ao-worker (a
// bring-your-own connection's default, or the deployment's Coder): the
// bootstrap would let ao-worker enter that folder. The workspace user, a
// standalone project and AO_CLOUD_CODER_WORKER_WORKING_FOLDER off keep it.
func TestUpdateProjectSettingsRefusesAWorkingFolderInsideAHiddenFolder(t *testing.T) {
	t.Parallel()
	connection := func(t *testing.T, workerUser string) []domain.ProviderConnection {
		t.Helper()
		config, err := domain.EncodeOrgCoderConfig(domain.OrgCoderConfig{
			BaseURL: "https://coder.acme.test", Owner: "ao-bot", DurableRoot: "$HOME", WorkerUser: workerUser,
		})
		if err != nil {
			t.Fatal(err)
		}
		return []domain.ProviderConnection{{ID: "conn-1", Provider: sandbox.ProviderCoder, Label: "default", Config: config}}
	}
	const refused = "The working folder must not be inside a hidden folder: AO checks the repository out there as its own ao-worker user"
	for name, tc := range map[string]struct {
		byo        bool
		workerUser string
		repository string
		folder     string
		flag       bool
		status     int
	}{
		"byo ao-worker, hidden parent":         {true, "", "https://github.com/acme/alice", "~/.config/{repo}", true, http.StatusUnprocessableEntity},
		"deployment coder, hidden parent":      {false, "", "https://github.com/acme/alice", "~/.config/{repo}", true, http.StatusUnprocessableEntity},
		"byo ao-worker, hidden checkout":       {true, "", "https://github.com/acme/.github", "~/work/{repo}", true, http.StatusOK},
		"byo ao-worker, hidden folder in home": {true, "", "https://github.com/acme/.local", "~/{repo}", true, http.StatusUnprocessableEntity},
		"byo ao-worker, plain folder":          {true, "", "https://github.com/acme/alice", "~/work/{repo}", true, http.StatusOK},
		"workspace user, hidden parent":        {true, domain.CoderWorkerUserWorkspace, "https://github.com/acme/alice", "~/.config/{repo}", true, http.StatusOK},
		"standalone, hidden parent":            {true, "", "https://scratch.ao.local/abc", "~/.config/scratch", true, http.StatusOK},
		"working folder flag off, hidden path": {true, "", "https://github.com/acme/alice", "~/.config/{repo}", false, http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := &adminDevEnvStore{}
			if tc.byo {
				store.connections = connection(t, tc.workerUser)
			}
			store.project = domain.Project{ID: devEnvProjectID, OrgID: coderCfgOrgID, RepositoryURL: tc.repository, Config: json.RawMessage(`{}`)}
			srv := newCoderTemplatesServer(t, newTestCipher(t), store, nil)
			srv.provisioning.Coder.WorkerWorkingFolder = tc.flag
			router := chi.NewRouter()
			router.Patch("/orgs/{orgId}/projects/{projectId}/settings", srv.updateProjectSettings)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch,
				"/orgs/"+coderCfgOrgID+"/projects/"+devEnvProjectID+"/settings",
				strings.NewReader(`{"config":{"workingDirectory":"`+tc.folder+`"}}`)))
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.status, rec.Body.String())
			}
			want := refused
			if strings.HasPrefix(tc.folder, "~/{repo}") {
				// The folder itself is hidden, not inside a hidden folder.
				want = "The working folder must not be a hidden folder directly in the home folder: AO would create it as its own ao-worker user"
			}
			if tc.status != http.StatusOK && (!strings.Contains(rec.Body.String(), domain.WorkspaceStartupErrorWorkingDirectory) ||
				!strings.Contains(rec.Body.String(), want)) {
				t.Fatalf("body = %s, want %q", rec.Body.String(), want)
			}
		})
	}
}

// adminDevEnvStore is a devEnvStore whose caller is an organization admin.
type adminDevEnvStore struct{ devEnvStore }

func (s *adminDevEnvStore) RequireOrgAdmin(context.Context, domain.Principal, string) error {
	return nil
}
