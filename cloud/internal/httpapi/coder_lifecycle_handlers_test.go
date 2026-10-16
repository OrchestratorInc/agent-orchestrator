package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestPutOrgCoderConfigStoresLifecycleSettings(t *testing.T) {
	t.Parallel()
	store := &coderConfigFakeStore{}
	srv, _ := newCoderConfigServer(t, store, false)
	body := `{"token":"` + coderCfgToken + `","baseUrl":"https://coder.acme.example.com","owner":"ao-bot",` +
		`"workerUser":"workspace","workingDirectory":"~/work/{repo}"}`
	rec := httptest.NewRecorder()
	srv.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut, body, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	cfg, err := domain.DecodeOrgCoderConfig(store.lastConfig)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkingDirectory != "~/work/{repo}" || !cfg.ProtectsSecretFiles() {
		t.Fatalf("stored = %+v", cfg)
	}
	var response struct {
		CoderConfig map[string]any `json:"coderConfig"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.CoderConfig["protectSecretFiles"] != true || response.CoderConfig["workingDirectory"] != "~/work/{repo}" {
		t.Fatalf("response = %v", response.CoderConfig)
	}
}

// A working directory needs the workspace user; it is refused for the
// ao-worker user with a code the desktop can explain.
func TestPutOrgCoderConfigRefusesLifecycleSettingsWithoutWorkspaceUser(t *testing.T) {
	t.Parallel()
	base := `"token":"` + coderCfgToken + `","baseUrl":"https://coder.acme.example.com","owner":"ao-bot"`
	for name, extra := range map[string]string{
		"working directory":  `,"workingDirectory":"~/work/{repo}"`,
		"explicit ao-worker": `,"workerUser":"ao-worker","workingDirectory":"/srv/app"`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := &coderConfigFakeStore{}
			srv, _ := newCoderConfigServer(t, store, false)
			rec := httptest.NewRecorder()
			srv.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut, "{"+base+extra+"}", nil))
			if rec.Code != http.StatusUnprocessableEntity || store.upserted != 0 ||
				!strings.Contains(rec.Body.String(), "coder_setting_requires_workspace_user") {
				t.Fatalf("status = %d, upserted = %d; body = %s", rec.Code, store.upserted, rec.Body.String())
			}
		})
	}
	for name, extra := range map[string]string{
		"relative directory": `,"workerUser":"workspace","workingDirectory":"work/{repo}"`,
		"protect wrong type": `,"protectSecretFiles":"yes"`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := &coderConfigFakeStore{}
			srv, _ := newCoderConfigServer(t, store, false)
			rec := httptest.NewRecorder()
			srv.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut, "{"+base+extra+"}", nil))
			if rec.Code < 400 || store.upserted != 0 {
				t.Fatalf("status = %d, upserted = %d; body = %s", rec.Code, store.upserted, rec.Body.String())
			}
		})
	}
}

// An older desktop that sends only the base URL and token keeps the stored
// working directory and protection choice.
func TestPutOrgCoderConfigKeepsStoredLifecycleSettings(t *testing.T) {
	t.Parallel()
	coderURL := devEnvCoderServer(t).URL
	srv, store := newDevEnvServer(t, coderURL)
	off := false
	config, err := domain.EncodeOrgCoderConfig(domain.OrgCoderConfig{
		BaseURL: coderURL, Owner: "ao-bot", TemplateID: coderCfgTemplate, WorkerUser: domain.CoderWorkerUserWorkspace,
		WorkingDirectory: "~/work/{repo}", ProtectSecretFiles: &off,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.config = config
	rec := httptest.NewRecorder()
	srv.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut,
		`{"baseUrl":"`+coderURL+`","token":"`+coderCfgToken+`"}`, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	cfg, err := domain.DecodeOrgCoderConfig(store.lastConfig)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkingDirectory != "~/work/{repo}" || cfg.ProtectsSecretFiles() {
		t.Fatalf("stored = %+v, want the omitted settings kept", cfg)
	}
}

func TestResolveCoderPlacementCarriesLifecycleSettings(t *testing.T) {
	t.Parallel()
	connectionConfig, err := domain.EncodeOrgCoderConfig(domain.OrgCoderConfig{
		BaseURL: "https://coder.acme.example", Owner: "dev", WorkerUser: "workspace",
		WorkingDirectory: "~/work/{repo}",
	})
	if err != nil {
		t.Fatal(err)
	}
	placement, err := resolveCoderPlacement(
		json.RawMessage(`{"coder":{"templateId":"`+coderCfgTemplate+`"}}`),
		[]domain.ProviderConnection{{ID: "conn-1", Provider: sandbox.ProviderCoder, Label: defaultAgentConnectionLabel, Config: connectionConfig}},
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	override := placement.override
	if override == nil || override.WorkingDirectory != "~/work/{repo}" || !override.ProtectSecretFiles {
		t.Fatalf("override = %+v", override)
	}
}

// setupEventStore records the session events the worker publishes.
type setupEventStore struct {
	Store
	mu     sync.Mutex
	events map[string]json.RawMessage
}

func (s *setupEventStore) AppendSessionEvent(_ context.Context, _, _, eventType string, payload json.RawMessage) (domain.ClientEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.events == nil {
		s.events = map[string]json.RawMessage{}
	}
	s.events[eventType] = payload
	return domain.ClientEvent{Type: eventType, Payload: payload}, nil
}

// A project Setup outcome reaches the session's event stream, with its
// output redacted; anything else under that type is refused.
func TestWorkerEventRecordsSetupOutcome(t *testing.T) {
	t.Parallel()
	store := &setupEventStore{}
	srv := testServer(store)
	body := `{"type":"workspace.setup","payload":{"status":"failed","step":2,"exitCode":2,"durationMs":1500,` +
		`"output":"compose up failed\nGITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789"}}`
	rec := httptest.NewRecorder()
	srv.workerEvent(rec, workerRequest(t, http.MethodPost, "/worker/events", body, "worker:event"))
	if rec.Code >= 300 {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var recorded worker.SetupEvent
	if err := json.Unmarshal(store.events[worker.SetupEventType], &recorded); err != nil {
		t.Fatalf("event = %s: %v", store.events[worker.SetupEventType], err)
	}
	if recorded.Status != worker.SetupFailed || recorded.Step != 2 || recorded.ExitCode != 2 ||
		!strings.Contains(recorded.Output, "compose up failed") || strings.Contains(recorded.Output, "abcdefghijklmnop") {
		t.Fatalf("recorded = %+v", recorded)
	}

	for name, payload := range map[string]string{
		"unknown status": `{"status":"done","exitCode":0,"durationMs":1}`,
		"not an object":  `[]`,
		"negative step":  `{"status":"failed","step":-1,"exitCode":1,"durationMs":1}`,
	} {
		rec := httptest.NewRecorder()
		srv.workerEvent(rec, workerRequest(t, http.MethodPost, "/worker/events",
			`{"type":"workspace.setup","payload":`+payload+`}`, "worker:event"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d; body = %s", name, rec.Code, rec.Body.String())
		}
	}
}

// The project context names the session's real working directory, which only
// the worker knows: a worker that substitutes it gets a placeholder, and an
// older worker (which shows the prompt as is) the path it always showed.
func TestLaunchContextLeavesTheWorkspaceToTheWorker(t *testing.T) {
	t.Parallel()
	spec := domain.WorkerLaunch{
		SessionID: "session-1", ProjectID: "project-1", ProjectName: "alice", Kind: "worker",
		Harness: "claude-code", RepositoryURL: "https://github.com/11x-ai/alice", DefaultBranch: "main",
	}
	launch, err := launchContextFrom(spec, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(launch.SystemPrompt, "- Workspace: "+worker.WorkspacePathPlaceholder) ||
		strings.Contains(launch.SystemPrompt, worker.LegacyWorkspacePath) {
		t.Fatalf("system prompt workspace line:\n%s", launch.SystemPrompt)
	}
	if launch.ProjectName != "alice" {
		t.Fatalf("project name = %q", launch.ProjectName)
	}
	legacy, err := launchContextFrom(spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(legacy.SystemPrompt, "- Workspace: "+worker.LegacyWorkspacePath) ||
		strings.Contains(legacy.SystemPrompt, worker.WorkspacePathPlaceholder) {
		t.Fatalf("older worker's workspace line:\n%s", legacy.SystemPrompt)
	}
	request := httptest.NewRequest(http.MethodGet, "/worker/reconnect", nil)
	if headerReportsCapability(request, worker.CapabilityWorkspacePathPlaceholder) {
		t.Fatal("a request without the header reported the capability")
	}
	request.Header.Set(worker.CapabilitiesHeader, "worker.events, "+worker.CapabilityWorkspacePathPlaceholder)
	if !headerReportsCapability(request, worker.CapabilityWorkspacePathPlaceholder) {
		t.Fatal("the header's capability was not found")
	}
}

// The project's Setup and Cleanup commands reach the worker from the project
// config; blank commands and values of the wrong shape are dropped.
func TestLaunchContextCarriesProjectScripts(t *testing.T) {
	t.Parallel()
	launch, err := launchContextFrom(domain.WorkerLaunch{
		SessionID: "session-1", ProjectID: "project-1", Kind: "worker", Harness: "claude-code",
		ProjectConfig: json.RawMessage(`{"postCreate":["npm ci","  ","make up"],"preRemove":["make down"],"coder":{"templateId":"x"}}`),
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(launch.PostCreate, "|") != "npm ci|make up" || strings.Join(launch.PreRemove, "|") != "make down" {
		t.Fatalf("scripts = %q / %q", launch.PostCreate, launch.PreRemove)
	}
	launch, err = launchContextFrom(domain.WorkerLaunch{
		SessionID: "session-1", Kind: "worker", Harness: "claude-code",
		ProjectConfig: json.RawMessage(`{"postCreate":"npm ci"}`),
	}, true)
	if err != nil || launch.PostCreate != nil || launch.PreRemove != nil {
		t.Fatalf("malformed scripts: launch = %+v, err = %v", launch, err)
	}
}
