package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

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
		json.RawMessage(`{"coder":{"templateId":"`+coderCfgTemplate+`"}}`), "https://github.com/acme/alice",
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

// A Setup step's events reach the session's event stream with their output
// redacted again and bounded; a malformed step, or a Cleanup step (which only
// the control plane reports), is refused.
func TestWorkerEventRecordsScriptSteps(t *testing.T) {
	t.Parallel()
	store := &setupEventStore{}
	srv := testServer(store)
	body := `{"type":"workspace.script.step","payload":{"phase":"setup","runId":"run-1","step":2,"total":3,` +
		`"command":"make   up","repository":"api","status":"failed","exitCode":2,"startedAt":"2026-10-18T10:00:00.000Z",` +
		`"durationMs":1500,"output":"\u001b[31mcompose up failed\u001b[0m\nGITHUB_TOKEN=` + "gh" + "p_" + `abcdefghijklmnopqrstuvwxyz0123456789",` +
		`"outputTruncated":false,"logPath":"/data/setup-logs/step-2.log","user":"coder","groups":["coder"],"dir":"/w/api",` +
		`"hint":"h","extra":"dropped"}}`
	rec := httptest.NewRecorder()
	srv.workerEvent(rec, workerRequest(t, http.MethodPost, "/worker/events", body, "worker:event"))
	if rec.Code >= 300 {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	raw := store.events[worker.ScriptStepEventType]
	var recorded worker.ScriptStepEvent
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatalf("event = %s: %v", raw, err)
	}
	if recorded.Phase != worker.ScriptPhaseSetup || recorded.RunID != "run-1" || recorded.Step != 2 || recorded.Total != 3 ||
		recorded.Status != worker.SetupFailed || *recorded.ExitCode != 2 || *recorded.DurationMS != 1500 ||
		recorded.Command != "make up" || recorded.Repository != "api" || recorded.User != "coder" ||
		recorded.Output != "compose up failed\nGITHUB_TOKEN=[REDACTED]" || strings.Contains(string(raw), "extra") {
		t.Fatalf("recorded = %s", raw)
	}

	// A running step's output age becomes OutputAt by this control plane's
	// clock; a time the worker sent itself is never kept.
	running := `{"phase":"setup","runId":"r","step":1,"total":1,"command":"c","repository":"","status":"running","output":"x","outputTruncated":false,` +
		`"outputAgeMs":30000,"outputAt":"2001-01-01T00:00:00Z"}`
	before := time.Now()
	rec = httptest.NewRecorder()
	srv.workerEvent(rec, workerRequest(t, http.MethodPost, "/worker/events",
		`{"type":"workspace.script.step","payload":`+running+`}`, "worker:event"))
	if rec.Code >= 300 {
		t.Fatalf("running step: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	raw = store.events[worker.ScriptStepEventType]
	var live worker.ScriptStepEvent
	if err := json.Unmarshal(raw, &live); err != nil {
		t.Fatal(err)
	}
	outputAt, err := time.Parse(time.RFC3339Nano, live.OutputAt)
	if err != nil || live.OutputAgeMS != nil || strings.Contains(string(raw), "outputAgeMs") ||
		outputAt.Before(before.Add(-31*time.Second)) || outputAt.After(time.Now().Add(-29*time.Second)) {
		t.Fatalf("live update = %s", raw)
	}

	for name, payload := range map[string]string{
		"cleanup phase":     `{"phase":"cleanup","runId":"r","step":1,"total":1,"status":"succeeded","exitCode":0,"durationMs":1}`,
		"unknown status":    `{"phase":"setup","runId":"r","step":1,"total":1,"status":"done","exitCode":0}`,
		"step past total":   `{"phase":"setup","runId":"r","step":3,"total":2,"status":"skipped"}`,
		"no run":            `{"phase":"setup","runId":"","step":1,"total":1,"status":"running"}`,
		"finished, no exit": `{"phase":"setup","runId":"r","step":1,"total":1,"status":"failed"}`,
		"not an object":     `[]`,
	} {
		rec := httptest.NewRecorder()
		srv.workerEvent(rec, workerRequest(t, http.MethodPost, "/worker/events",
			`{"type":"workspace.script.step","payload":`+payload+`}`, "worker:event"))
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
	launch, err := launchContextFrom(spec, launchCapabilities{resolvesWorkspacePath: true})
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
	legacy, err := launchContextFrom(spec, launchCapabilities{})
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
	}, launchCapabilities{resolvesWorkspacePath: true, multiRepo: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(launch.PostCreate, "|") != "npm ci|make up" || strings.Join(launch.PreRemove, "|") != "make down" {
		t.Fatalf("scripts = %q / %q", launch.PostCreate, launch.PreRemove)
	}
	if launch.PostCreateTimeoutMinutes != nil || launch.PreRemoveTimeoutMinutes != nil {
		t.Fatalf("default limits were sent: %v / %v", launch.PostCreateTimeoutMinutes, launch.PreRemoveTimeoutMinutes)
	}
	// A step's own time limit goes beside the plain commands, which an older
	// worker runs with the default.
	launch, err = launchContextFrom(domain.WorkerLaunch{
		SessionID: "session-1", ProjectID: "project-1", Kind: "worker", Harness: "claude-code",
		ProjectConfig: json.RawMessage(`{"postCreate":["npm ci","# ao-timeout: 45m\nmake up"],"preRemove":["# ao-timeout: 120m\nmake down"]}`),
	}, launchCapabilities{resolvesWorkspacePath: true, multiRepo: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(launch.PostCreate, "|") != "npm ci|make up" || strings.Join(launch.PreRemove, "|") != "make down" ||
		!reflect.DeepEqual(launch.PostCreateTimeoutMinutes, []int{0, 45}) || !reflect.DeepEqual(launch.PreRemoveTimeoutMinutes, []int{120}) {
		t.Fatalf("timed scripts = %q %v / %q %v", launch.PostCreate, launch.PostCreateTimeoutMinutes, launch.PreRemove, launch.PreRemoveTimeoutMinutes)
	}
	launch, err = launchContextFrom(domain.WorkerLaunch{
		SessionID: "session-1", Kind: "worker", Harness: "claude-code",
		ProjectConfig: json.RawMessage(`{"postCreate":"npm ci"}`),
	}, launchCapabilities{resolvesWorkspacePath: true, multiRepo: true})
	if err != nil || launch.PostCreate != nil || launch.PreRemove != nil {
		t.Fatalf("malformed scripts: launch = %+v, err = %v", launch, err)
	}
}

// agentStartedStore is a setupEventStore whose worker epoch reported (or not)
// its coding agent started.
type agentStartedStore struct {
	setupEventStore
	started bool
	asked   []string
}

func (s *agentStartedStore) WorkerAgentStarted(_ context.Context, orgID, sessionID, workerID string, epoch int64) (bool, error) {
	s.asked = append(s.asked, fmt.Sprintf("%s/%s/%s/%d", orgID, sessionID, workerID, epoch))
	return s.started, nil
}

// A worker runs Setup before its coding agent starts, so once its epoch
// reported agent.ready a running Setup step from it is refused: it would keep
// the session reading as working. A step's final event is still taken.
func TestWorkerEventRefusesARunningSetupStepAfterTheAgentStarted(t *testing.T) {
	t.Parallel()
	running := `{"type":"workspace.script.step","payload":{"phase":"setup","runId":"r","step":1,"total":1,"command":"c","repository":"","status":"running","output":"","outputTruncated":false}}`
	finished := `{"type":"workspace.script.step","payload":{"phase":"setup","runId":"r","step":1,"total":1,"command":"c","repository":"","status":"succeeded","exitCode":0,"durationMs":5,"output":"","outputTruncated":false}}`
	for _, started := range []bool{false, true} {
		store := &agentStartedStore{started: started}
		srv := testServer(store)
		rec := httptest.NewRecorder()
		srv.workerEvent(rec, workerRequest(t, http.MethodPost, "/worker/events", running, "worker:event"))
		_, recorded := store.events[worker.ScriptStepEventType]
		if started {
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "AGENT_ALREADY_STARTED") || recorded {
				t.Fatalf("after agent.ready: status = %d, recorded = %v; body = %s", rec.Code, recorded, rec.Body.String())
			}
		} else if rec.Code >= 300 || !recorded {
			t.Fatalf("before agent.ready: status = %d, recorded = %v; body = %s", rec.Code, recorded, rec.Body.String())
		}
		if len(store.asked) != 1 || store.asked[0] != testOrgID+"/"+testOrchestratorID+"/w1/1" {
			t.Fatalf("asked = %v, want the request's own worker epoch", store.asked)
		}
		rec = httptest.NewRecorder()
		srv.workerEvent(rec, workerRequest(t, http.MethodPost, "/worker/events", finished, "worker:event"))
		if _, recorded := store.events[worker.ScriptStepEventType]; rec.Code >= 300 || !recorded || len(store.asked) != 1 {
			t.Fatalf("final step (agent started %v): status = %d, recorded = %v, asked = %v", started, rec.Code, recorded, store.asked)
		}
	}
}
