package freestyle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// fakeAPI is a minimal Freestyle API: it records requests and serves VMs from
// a map keyed by id and slug.
type fakeAPI struct {
	mu       sync.Mutex
	vms      map[string]vmView
	requests []recorded
	execOut  string
	execCode int
}

type recorded struct {
	method, path string
	body         map[string]any
	auth         string
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{vms: map[string]vmView{}, execOut: "AO_WORKER_LAUNCHED\n"}
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	f.requests = append(f.requests, recorded{r.Method, r.URL.Path, body, r.Header.Get("Authorization")})
	path := strings.TrimPrefix(r.URL.Path, "/v5/vms")
	switch {
	case r.Method == http.MethodPost && path == "":
		slug, _ := body["slug"].(string)
		if _, taken := f.vms[slug]; taken && slug != "" {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"code":"CONFLICT","message":"conflict: vm slug `+"`"+slug+"`"+` is already in use"}`)
			return
		}
		view := vmView{ID: "vm-" + slug, Slug: slug, State: "running"}
		f.vms[view.ID] = view
		f.vms[slug] = view
		_ = json.NewEncoder(w).Encode(view)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/exec-await"):
		_ = json.NewEncoder(w).Encode(map[string]any{"statusCode": f.execCode, "stdout": f.execOut})
	case r.Method == http.MethodGet:
		view, ok := f.vms[strings.TrimPrefix(path, "/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(view)
	case r.Method == http.MethodDelete:
		id := strings.TrimPrefix(path, "/")
		view, ok := f.vms[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(f.vms, view.ID)
		delete(f.vms, view.Slug)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (f *fakeAPI) last(method, suffix string) recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	for index := len(f.requests) - 1; index >= 0; index-- {
		if f.requests[index].method == method && strings.HasSuffix(f.requests[index].path, suffix) {
			return f.requests[index]
		}
	}
	return recorded{}
}

func testClient(t *testing.T, api *fakeAPI) *Client {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return New(Config{
		BaseURL:         server.URL,
		APIKey:          "fs-key",
		DefaultSnapshot: "snap-default",
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestCreateBootsSessionSnapshotWithEgressOnlyFirewall(t *testing.T) {
	api := newFakeAPI()
	client := testClient(t, api)
	environment, err := client.Create(context.Background(), sandbox.Spec{
		SessionID: "Session-1", RootFS: "snap-codex", AutoPauseSeconds: 0,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if environment.State != sandbox.StateRunning || environment.Name != "ao-session-1" {
		t.Fatalf("environment = %+v", environment)
	}
	request := api.last(http.MethodPost, "/v5/vms")
	if request.auth != "Bearer fs-key" {
		t.Fatalf("authorization = %q", request.auth)
	}
	if request.body["snapshotId"] != "snap-codex" || request.body["slug"] != "ao-session-1" {
		t.Fatalf("create body = %v", request.body)
	}
	if _, ok := request.body["idleTimeoutSeconds"]; ok {
		t.Fatalf("idle timeout must be omitted when auto-pause is off: %v", request.body)
	}
	rules := request.body["firewall"].(map[string]any)["rules"].([]any)
	if len(rules) != 1 || rules[0].(map[string]any)["destination"].(map[string]any)["public"] != true {
		t.Fatalf("firewall = %v, want one public-egress rule", rules)
	}
}

func TestCreateFallsBackToDefaultSnapshot(t *testing.T) {
	api := newFakeAPI()
	if _, err := testClient(t, api).Create(context.Background(), sandbox.Spec{SessionID: "s"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := api.last(http.MethodPost, "/v5/vms").body["snapshotId"]; got != "snap-default" {
		t.Fatalf("snapshotId = %v, want snap-default", got)
	}
}

func TestBootstrapWorkerLaunchesBakedWorkerWithoutUnconditionalChown(t *testing.T) {
	api := newFakeAPI()
	client := testClient(t, api)
	err := client.BootstrapWorker(context.Background(), "vm-1", sandbox.WorkerBootstrap{
		Destination: "/usr/local/bin/ao-worker",
		User:        "ao-worker",
		Environment: map[string]string{"AO_WORKER_BOOTSTRAP_TOKEN": "ticket"},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	command, _ := api.last(http.MethodPost, "/exec-await").body["command"].(string)
	for _, want := range []string{
		// Freestyle keeps the agent alive across the worker restart a wake does.
		"runuser --user 'ao-worker' -- env 'AO_WORKER_BOOTSTRAP_TOKEN=ticket' 'AO_WORKER_PERSIST_AGENT=1' '/usr/local/bin/ao-worker'",
		`[ "$(stat -c %U /workspace)" = 'ao-worker' ] || chown -R`,
		"setsid nohup",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("launch command missing %q:\n%s", want, command)
		}
	}
}

func TestBootstrapWorkerReportsMissingBakedWorker(t *testing.T) {
	api := newFakeAPI()
	api.execOut = "AO_WORKER_ABSENT\n"
	err := testClient(t, api).BootstrapWorker(context.Background(), "vm-1", sandbox.WorkerBootstrap{
		Destination: "/usr/local/bin/ao-worker",
	})
	if err == nil || !strings.Contains(err.Error(), "no worker") {
		t.Fatalf("err = %v, want missing-worker error", err)
	}
}

func TestRecreateDeletesThenCreatesUnderTheSameSlug(t *testing.T) {
	api := newFakeAPI()
	client := testClient(t, api)
	original, err := client.Create(context.Background(), sandbox.Spec{SessionID: "s1", RootFS: "snap"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	replacement, err := client.Recreate(context.Background(), original.ID, sandbox.Spec{SessionID: "s1", RootFS: "snap"})
	if err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if replacement.Name != "ao-s1" {
		t.Fatalf("replacement = %+v", replacement)
	}
	if api.last(http.MethodDelete, "/"+string(original.ID)).method == "" {
		t.Fatal("recreate did not delete the old VM first")
	}
}

func TestFindBySessionReportsMissingVM(t *testing.T) {
	_, found, err := testClient(t, newFakeAPI()).FindBySession(context.Background(), "missing")
	if err != nil || found {
		t.Fatalf("found = %v, err = %v; want not found", found, err)
	}
}

func TestDeleteOfMissingVMSucceeds(t *testing.T) {
	if err := testClient(t, newFakeAPI()).Delete(context.Background(), "vm-gone"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestExecRootSurfacesNonzeroExit(t *testing.T) {
	api := newFakeAPI()
	api.execCode = 2
	_, err := testClient(t, api).ExecRoot(context.Background(), "vm-1", "false", nil, 0)
	var httpErr *HTTPError
	if err == nil || errors.As(err, &httpErr) {
		t.Fatalf("err = %v, want a command failure, not an HTTP error", err)
	}
}

func TestCreateReportsTakenSlugAsAlreadyExists(t *testing.T) {
	client := testClient(t, newFakeAPI())
	if _, err := client.Create(context.Background(), sandbox.Spec{SessionID: "s1"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := client.Create(context.Background(), sandbox.Spec{SessionID: "s1"})
	if !errors.Is(err, sandbox.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
}
