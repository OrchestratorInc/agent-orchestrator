package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type projectCapture struct {
	method string
	path   string
	body   []byte
}

func projectServer(t *testing.T, status int, respBody string) (*httptest.Server, *projectCapture) {
	t.Helper()
	capture := &projectCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.method = r.Method
		capture.path = r.URL.Path
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		capture.body = data
		if !strings.HasPrefix(r.URL.Path, "/api/v1/projects") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

func TestProjectSetConfig_TrackerIntakeFlags(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--tracker-intake", "--tracker-repo", "acme/demo", "--tracker-assignee", "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPut || capture.path != "/api/v1/projects/demo/config" {
		t.Fatalf("request = %s %s, want PUT /api/v1/projects/demo/config", capture.method, capture.path)
	}
	var got setConfigRequest
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request: %v\nbody=%s", err, capture.body)
	}
	// Provider is omitted: the daemon infers it from the project's git origin.
	if !got.Config.TrackerIntake.Enabled || got.Config.TrackerIntake.Provider != "" || got.Config.TrackerIntake.Repo != "acme/demo" || got.Config.TrackerIntake.Assignee != "alice" {
		t.Fatalf("tracker intake request = %#v", got.Config.TrackerIntake)
	}
}

func TestProjectSetConfig_TrackerIntakeJSON(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--config-json", `{"worker":{"agent":"amp","agentConfig":{"mode":"ultra"}},"trackerIntake":{"enabled":true,"provider":"github","assignee":"alice"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got setConfigRequest
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request: %v\nbody=%s", err, capture.body)
	}
	if !got.Config.TrackerIntake.Enabled || got.Config.TrackerIntake.Provider != "github" || got.Config.TrackerIntake.Assignee != "alice" {
		t.Fatalf("tracker intake request = %#v", got.Config.TrackerIntake)
	}
	if got.Config.Worker.Agent != "amp" || got.Config.Worker.AgentConfig.Mode != "ultra" {
		t.Fatalf("worker config = %#v, want preserved amp ultra mode", got.Config.Worker)
	}
}

func TestBuildProjectConfigTrackerIntakeFlags(t *testing.T) {
	changed := func(name string) bool {
		return name == "tracker-intake" || name == "tracker-repo" || name == "tracker-assignee"
	}
	current := func() (projectConfig, error) { return projectConfig{}, nil }
	got, err := buildProjectConfig(projectSetConfigOptions{
		trackerIntake:   true,
		trackerRepo:     "acme/demo",
		trackerAssignee: "alice",
	}, changed, current)
	if err != nil {
		t.Fatal(err)
	}
	// Provider is omitted: the daemon infers it from the project's git origin.
	if !got.TrackerIntake.Enabled || got.TrackerIntake.Provider != "" || got.TrackerIntake.Repo != "acme/demo" || got.TrackerIntake.Assignee != "alice" {
		t.Fatalf("tracker intake config = %#v", got.TrackerIntake)
	}
}

// TestProjectConfigMirrorsDomainJSONKeys fails when the CLI's hand-written
// projectConfig drifts from domain.ProjectConfig. A key the CLI does not model
// is silently dropped by every set-config round trip.
func TestProjectConfigMirrorsDomainJSONKeys(t *testing.T) {
	want := map[string]bool{}
	jsonKeyPaths(reflect.TypeOf(domain.ProjectConfig{}), "", want)
	got := map[string]bool{}
	jsonKeyPaths(reflect.TypeOf(projectConfig{}), "", got)
	for key := range want {
		if !got[key] {
			t.Errorf("CLI projectConfig is missing domain key %q", key)
		}
	}
	for key := range got {
		if !want[key] {
			t.Errorf("CLI projectConfig has key %q that domain.ProjectConfig does not", key)
		}
	}
}

// jsonKeyPaths records every JSON key path typ encodes, descending through
// pointers, slices, and nested structs so keys like worker.agentConfig.effort
// are compared too. It does not support embedded structs, which encoding/json
// promotes inline, and it compares key names only, not types.
func jsonKeyPaths(typ reflect.Type, prefix string, out map[string]bool) {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if !field.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		out[prefix+name] = true
		jsonKeyPaths(field.Type, prefix+name+".", out)
	}
}

// TestProjectConfigFlagsListsEveryFieldFlag fails when a set-config field flag
// is registered but missing from projectConfigFlags, which would make a call
// with only that flag a "provide at least one config flag" usage error.
func TestProjectConfigFlagsListsEveryFieldFlag(t *testing.T) {
	notFieldFlags := map[string]bool{"config-json": true, "clear": true, "json": true}
	cmd := newProjectSetConfigCommand(&commandContext{})
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if !notFieldFlags[f.Name] && !slices.Contains(projectConfigFlags, f.Name) {
			t.Errorf("set-config flag --%s is missing from projectConfigFlags", f.Name)
		}
	})
	for _, name := range projectConfigFlags {
		if cmd.LocalFlags().Lookup(name) == nil {
			t.Errorf("projectConfigFlags has %q, which set-config does not register", name)
		}
	}
}

// storedProjectConfig carries fields set in the desktop app that no
// set-config flag manages (effort, preRemove, workersRequestReview).
const storedProjectConfig = `{"sessionPrefix":"old","env":{"KEEP":"1"},"preRemove":["make clean"],` +
	`"agentConfig":{"model":"m1","effort":"high"},"worker":{"agent":"codex","agentConfig":{"effort":"medium"}},` +
	`"reviewers":[{"harness":"claude-code","agentConfig":{"effort":"max"}}],"workersRequestReview":true}`

type projectConfigCapture struct {
	requests []string
	putBody  []byte
}

// projectConfigServer serves getBody for GET /api/v1/projects/demo and records
// the API requests it sees plus the PUT /api/v1/projects/demo/config body.
func projectConfigServer(t *testing.T, getBody string) (*httptest.Server, *projectConfigCapture) {
	t.Helper()
	capture := &projectConfigCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			capture.requests = append(capture.requests, r.Method+" "+r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, getBody)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/projects/demo/config":
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read request body: %v", err)
			}
			capture.putBody = data
			_, _ = io.WriteString(w, `{"project":{"id":"demo"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

// putConfig decodes the captured PUT body as the daemon does, into
// domain.ProjectConfig, so the assertion sees what the daemon would store.
func putConfig(t *testing.T, capture *projectConfigCapture) domain.ProjectConfig {
	t.Helper()
	var body struct {
		Config domain.ProjectConfig `json:"config"`
	}
	if err := json.Unmarshal(capture.putBody, &body); err != nil {
		t.Fatalf("decode PUT body: %v\nbody=%s", err, capture.putBody)
	}
	return body.Config
}

func decodeStoredProjectConfig(t *testing.T) domain.ProjectConfig {
	t.Helper()
	var cfg domain.ProjectConfig
	if err := json.Unmarshal([]byte(storedProjectConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestProjectSetConfig_FlagsUpdateOnlyChangedFields(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		change func(cfg *domain.ProjectConfig)
	}{
		{
			name: "changed flags overwrite only their fields",
			args: []string{"--session-prefix", "new", "--model", "m2"},
			change: func(cfg *domain.ProjectConfig) {
				cfg.SessionPrefix = "new"
				cfg.AgentConfig.Model = "m2"
			},
		},
		{
			name: "an empty flag value clears that field",
			args: []string{"--session-prefix="},
			change: func(cfg *domain.ProjectConfig) {
				cfg.SessionPrefix = ""
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv, capture := projectConfigServer(t, `{"status":"ok","project":{"id":"demo","config":`+storedProjectConfig+`}}`)
			writeRunFileFor(t, cfg, srv)

			args := append([]string{"project", "set-config", "demo"}, tt.args...)
			_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
			if err != nil {
				t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
			}
			wantRequests := []string{"GET /api/v1/projects/demo", "PUT /api/v1/projects/demo/config"}
			if !reflect.DeepEqual(capture.requests, wantRequests) {
				t.Fatalf("requests = %q, want %q", capture.requests, wantRequests)
			}
			want := decodeStoredProjectConfig(t)
			tt.change(&want)
			if got := putConfig(t, capture); !reflect.DeepEqual(got, want) {
				t.Fatalf("PUT config = %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestProjectSetConfig_ConfigJSONKeepsEffortAndPreRemove(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectConfigServer(t, `{"status":"ok","project":{"id":"demo"}}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--config-json", storedProjectConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	// --config-json is a full replacement, so it never reads the current config.
	if want := []string{"PUT /api/v1/projects/demo/config"}; !reflect.DeepEqual(capture.requests, want) {
		t.Fatalf("requests = %q, want %q", capture.requests, want)
	}
	want := decodeStoredProjectConfig(t)
	if got := putConfig(t, capture); !reflect.DeepEqual(got, want) {
		t.Fatalf("PUT config = %+v\nwant %+v", got, want)
	}
}

func TestProjectSetConfig_NoConfigFlagIsUsageErrorWithoutRequests(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectConfigServer(t, `{"status":"ok","project":{"id":"demo"}}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--json")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d (err %v), want 2", got, err)
	}
	if len(capture.requests) != 0 {
		t.Fatalf("requests = %q, want none before the usage check passes", capture.requests)
	}
}

func TestProjectSetConfig_FlagsRefuseDegradedProject(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectConfigServer(t, `{"status":"degraded","project":{"id":"demo","resolveError":"config missing"}}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--session-prefix", "new")
	if err == nil {
		t.Fatal("expected an error for a degraded project")
	}
	if want := []string{"GET /api/v1/projects/demo"}; !reflect.DeepEqual(capture.requests, want) {
		t.Fatalf("requests = %q, want only the GET; a degraded project has no config to merge into", capture.requests)
	}
}

func TestProjectList_Success(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"projects":[{"id":"zeta","name":"Zeta","sessionPrefix":"zeta"},{"id":"alpha","name":"Alpha","sessionPrefix":"alpha","resolveError":"config missing"}]}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodGet || capture.path != "/api/v1/projects" {
		t.Fatalf("request = %s %s, want GET /api/v1/projects", capture.method, capture.path)
	}
	if !strings.Contains(out, "ID") || !strings.Contains(out, "SESSION PREFIX") {
		t.Fatalf("output missing table header:\n%s", out)
	}
	if strings.Index(out, "alpha") > strings.Index(out, "zeta") {
		t.Fatalf("projects should be sorted by id in output:\n%s", out)
	}
	if !strings.Contains(out, "degraded: config missing") {
		t.Fatalf("output missing degraded status:\n%s", out)
	}
}

func TestProjectList_JSON(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := projectServer(t, http.StatusOK, `{"projects":[{"id":"demo","name":"Demo","sessionPrefix":"demo"}]}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "ls", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got projectListResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode json output: %v\nout=%s", err, out)
	}
	if len(got.Projects) != 1 || got.Projects[0].ID != "demo" {
		t.Fatalf("projects = %#v, want demo", got.Projects)
	}
}

func TestProjectList_Empty(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := projectServer(t, http.StatusOK, `{"projects":[]}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "No projects registered") || !strings.Contains(out, "ao project add --path") {
		t.Fatalf("empty output missing hint:\n%s", out)
	}
}

func TestProjectGet_Success(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"git@example.com:demo.git","defaultBranch":"main","agent":"codex"}}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "get", "demo")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodGet || capture.path != "/api/v1/projects/demo" {
		t.Fatalf("request = %s %s, want GET /api/v1/projects/demo", capture.method, capture.path)
	}
	for _, want := range []string{"Project demo (ok)", "name: Demo", "path: /repo/demo", "default branch: main", "agent: codex"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestProjectGet_JSON(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"status":"degraded","project":{"id":"demo","name":"Demo","path":"/repo/demo","resolveError":"config missing","config":{"worker":{"agent":"amp","agentConfig":{"mode":"high"}}}}}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "get", "demo", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodGet || capture.path != "/api/v1/projects/demo" {
		t.Fatalf("request = %s %s, want GET /api/v1/projects/demo", capture.method, capture.path)
	}
	var got projectGetResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode json output: %v\nout=%s", err, out)
	}
	if got.Status != "degraded" || got.Project.ID != "demo" || got.Project.ResolveError != "config missing" {
		t.Fatalf("get json = %#v, want degraded demo with resolve error", got)
	}
	if got.Project.Config == nil || got.Project.Config.Worker.AgentConfig.Mode != "high" {
		t.Fatalf("get json worker config = %#v, want preserved amp high mode", got.Project.Config)
	}
}

func TestProjectGet_MissingArg(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "project", "get")
	if err == nil {
		t.Fatal("expected missing arg error")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestProjectGet_NotFound(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := projectServer(t, http.StatusNotFound, `{"error":"not_found","code":"PROJECT_NOT_FOUND","message":"Unknown project"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "get", "missing")
	if err == nil {
		t.Fatal("expected not found error")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "PROJECT_NOT_FOUND") && !strings.Contains(errOut, "PROJECT_NOT_FOUND") {
		t.Fatalf("error did not surface not found envelope: %v\nstderr=%s", err, errOut)
	}
}

func TestProjectSetConfig_RulesFlags(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"status":"ok","project":{"id":"demo","config":{"agentRules":"Run tests.","agentRulesFile":"docs/rules.md","orchestratorRules":"Delegate."}}}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo",
		"--agent-rules", "Run tests.",
		"--agent-rules-file", "docs/rules.md",
		"--orchestrator-rules", "Delegate.",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPut || capture.path != "/api/v1/projects/demo/config" {
		t.Fatalf("request = %s %s, want PUT /api/v1/projects/demo/config", capture.method, capture.path)
	}
	var got setConfigRequest
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request body: %v\nbody=%s", err, capture.body)
	}
	if got.Config.AgentRules != "Run tests." || got.Config.AgentRulesFile != "docs/rules.md" || got.Config.OrchestratorRules != "Delegate." {
		t.Fatalf("rules config = %#v", got.Config)
	}
	if !strings.Contains(out, "updated config for project demo") {
		t.Fatalf("output missing update message:\n%s", out)
	}
}

func TestProjectSetConfig_ReviewerJSON(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"status":"ok","project":{"id":"demo","config":{"sessionPrefix":"work","reviewers":[{"harness":"claude-code"}]}}}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--config-json", `{"reviewers":[{"harness":"claude-code"}],"sessionPrefix":"work"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got setConfigRequest
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request body: %v\nbody=%s", err, capture.body)
	}
	if len(got.Config.Reviewers) != 1 || got.Config.Reviewers[0].Harness != "claude-code" {
		t.Fatalf("reviewers config = %#v, want claude-code reviewer preserved", got.Config.Reviewers)
	}
}

func TestProjectSetConfig_ReviewerFlags(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"status":"ok","project":{"id":"demo"}}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--reviewer", "claude-code")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got setConfigRequest
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request body: %v\nbody=%s", err, capture.body)
	}
	if len(got.Config.Reviewers) != 1 || got.Config.Reviewers[0].Harness != "claude-code" {
		t.Fatalf("reviewers config = %#v, want single claude-code reviewer", got.Config.Reviewers)
	}
}

func TestProjectRemove_RequiresID(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "project", "rm")
	if err == nil {
		t.Fatal("expected missing id error")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestProjectRemove_NotFound(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := projectServer(t, http.StatusNotFound, `{"error":"not_found","code":"PROJECT_NOT_FOUND","message":"Unknown project"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "rm", "missing", "--yes")
	if err == nil {
		t.Fatal("expected not found error")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "PROJECT_NOT_FOUND") && !strings.Contains(errOut, "PROJECT_NOT_FOUND") {
		t.Fatalf("error did not surface not found envelope: %v\nstderr=%s", err, errOut)
	}
}

func TestProjectRemove_AbortsWhenConfirmationDoesNotMatch(t *testing.T) {
	setConfigEnv(t)
	out, _, err := executeCLI(t, Deps{
		In: strings.NewReader("nope\n"),
	}, "project", "rm", "demo")
	if err != nil {
		t.Fatalf("unexpected abort error: %v", err)
	}
	if !strings.Contains(out, "Type the project id to confirm") || !strings.Contains(out, "aborted") {
		t.Fatalf("output missing prompt/abort:\n%s", out)
	}
}

func TestProjectRemove_DeletesAfterConfirmation(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"ok":true,"id":"demo"}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader("demo\n"),
		ProcessAlive: func(int) bool { return true },
	}, "project", "rm", "demo")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodDelete || capture.path != "/api/v1/projects/demo" {
		t.Fatalf("request = %s %s, want DELETE /api/v1/projects/demo", capture.method, capture.path)
	}
	if !strings.Contains(out, "removed project demo") {
		t.Fatalf("output missing removal message:\n%s", out)
	}
}

func TestProjectRemove_JSONDocumentedEnvelope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"ok":true,"id":"demo"}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader("wrong\n"),
		ProcessAlive: func(int) bool { return true },
	}, "project", "rm", "demo", "--yes", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodDelete || capture.path != "/api/v1/projects/demo" {
		t.Fatalf("request = %s %s, want DELETE /api/v1/projects/demo", capture.method, capture.path)
	}
	var got projectRemoveResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode json output: %v\nout=%s", err, out)
	}
	if !got.OK || got.ID != "demo" || got.ProjectID != "" {
		t.Fatalf("remove json = %#v, want documented ok/id envelope", got)
	}
}

func TestProjectRemove_JSONBackendEnvelope(t *testing.T) {
	cfg := setConfigEnv(t)
	removedStorageDir := false
	srv, _ := projectServer(t, http.StatusOK, `{"projectId":"demo","removedStorageDir":false}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "rm", "demo", "--yes", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got projectRemoveResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode json output: %v\nout=%s", err, out)
	}
	if got.ProjectID != "demo" || got.RemovedStorageDir == nil || *got.RemovedStorageDir != removedStorageDir {
		t.Fatalf("remove json = %#v, want backend projectId/removedStorageDir envelope", got)
	}
}

func TestProjectRemove_EmptySuccessFallsBackToRequestedID(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := projectServer(t, http.StatusNoContent, ``)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "rm", "demo", "--yes")
	if err != nil {
		t.Fatalf("unexpected error for empty 2xx body: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "removed project demo") {
		t.Fatalf("output missing fallback removal id:\n%s", out)
	}
}

func TestProjectRemove_YesSkipsConfirmationAndSupportsBackendRemoveEnvelope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"projectId":"demo","removedStorageDir":false}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader("wrong\n"),
		ProcessAlive: func(int) bool { return true },
	}, "project", "rm", "demo", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodDelete || capture.path != "/api/v1/projects/demo" {
		t.Fatalf("request = %s %s, want DELETE /api/v1/projects/demo", capture.method, capture.path)
	}
	if strings.Contains(out, "Type the project id") || !strings.Contains(out, "removed project demo") {
		t.Fatalf("--yes output should skip prompt and print removal:\n%s", out)
	}
}
