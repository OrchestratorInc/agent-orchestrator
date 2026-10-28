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
	// missingSnapshots makes a create from one of these snapshots 404.
	missingSnapshots map[string]bool
	// createStatus, when set, answers every create with it and createBody.
	createStatus int
	createBody   string
	// execFunc, when set, answers an exec with its stdout and status code;
	// execStatus, when set, answers it with that HTTP status instead.
	execFunc   func(command string) (string, int)
	execStatus int
	// execBlock, when set, holds every exec until it is closed.
	execBlock chan struct{}
	// startTo, when set, is the state a VM's start leaves it in.
	startTo string
	// backgroundPending is how many times a backgrounded exec answers 202
	// (once to the exec, then to each poll) before its result;
	// refuseBackground refuses a backgrounded exec's timeout as an agent
	// without native exec does.
	backgroundPending int
	refuseBackground  bool
	pendingResult     []byte
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

type recorded struct {
	method, path string
	body         map[string]any
	auth         string
	// background is the request's backgrounding header.
	background string
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
	f.requests = append(f.requests, recorded{r.Method, r.URL.Path, body, r.Header.Get("Authorization"),
		r.Header.Get("x-freestyle-background-after-secs")})
	path := strings.TrimPrefix(r.URL.Path, "/v5/vms")
	timeoutMs, _ := body["timeoutMs"].(float64)
	backgrounded := r.Header.Get("x-freestyle-background-after-secs") != ""
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v5/background-requests/"):
		if strings.TrimPrefix(r.URL.Path, "/v5/background-requests/") != "bgr-1" || f.pendingResult == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if f.backgroundPending > 0 {
			f.backgroundPending--
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"requestId":"bgr-1","status":"pending"}`)
			return
		}
		_, _ = w.Write(f.pendingResult)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/exec-await") &&
		timeoutMs > 300000 && (!backgrounded || f.refuseBackground):
		// The real API's inline cap.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":"BAD_REQUEST","message":"timeout_ms cannot exceed 300000 for this exec mode"}`)
	case r.Method == http.MethodPost && path == "" && f.createStatus != 0:
		w.WriteHeader(f.createStatus)
		_, _ = io.WriteString(w, f.createBody)
	case r.Method == http.MethodPost && path == "" && f.missingSnapshots[stringValue(body["snapshotId"])]:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":"SNAPSHOT_NOT_FOUND","message":"snapshot not found"}`)
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
		if f.execBlock != nil {
			f.mu.Unlock()
			select {
			case <-f.execBlock:
			case <-r.Context().Done():
			}
			f.mu.Lock()
		}
		if f.execStatus != 0 {
			w.WriteHeader(f.execStatus)
			return
		}
		stdout, code := f.execOut, f.execCode
		if f.execFunc != nil {
			stdout, code = f.execFunc(stringValue(body["command"]))
		}
		result, _ := json.Marshal(map[string]any{"statusCode": code, "stdout": stdout})
		if backgrounded && f.backgroundPending > 0 {
			f.backgroundPending--
			f.pendingResult = result
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"requestId":"bgr-1","status":"pending"}`)
			return
		}
		_, _ = w.Write(result)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/start") && f.startTo != "":
		if view, ok := f.vms[strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/start")]; ok {
			view.State = f.startTo
			f.vms[view.ID], f.vms[view.Slug] = view, view
		}
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
		"runuser --user 'ao-worker' -- env 'AO_WORKER_BOOTSTRAP_TOKEN=ticket' 'AO_WORKER_PARTIAL_CLONE=1' 'AO_WORKER_PERSIST_AGENT=1' '/usr/local/bin/ao-worker'",
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

// A session planned on a project snapshot that was collected since boots from
// the harness snapshot it was built on rather than failing.
func TestCreateBootsTheFallbackWhenTheSnapshotIsGone(t *testing.T) {
	api := newFakeAPI()
	api.missingSnapshots = map[string]bool{"snap-project": true}
	client := testClient(t, api)
	if _, err := client.Create(context.Background(), sandbox.Spec{
		SessionID: "s", RootFS: "snap-project", FallbackRootFS: "snap-codex",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := api.last(http.MethodPost, "/v5/vms").body["snapshotId"]; got != "snap-codex" {
		t.Fatalf("snapshotId = %v, want the fallback snap-codex", got)
	}

	// Recreate goes through the same fallback.
	if _, err := client.Recreate(context.Background(), "vm-ao-s", sandbox.Spec{
		SessionID: "s", RootFS: "snap-project", FallbackRootFS: "snap-codex",
	}); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if got := api.last(http.MethodPost, "/v5/vms").body["snapshotId"]; got != "snap-codex" {
		t.Fatalf("recreate snapshotId = %v, want the fallback snap-codex", got)
	}

	// Without a fallback the missing snapshot is reported as such.
	_, err := client.Create(context.Background(), sandbox.Spec{SessionID: "s2", RootFS: "snap-project"})
	if !errors.Is(err, sandbox.ErrSnapshotNotFound) {
		t.Fatalf("err = %v, want ErrSnapshotNotFound", err)
	}
}

// Freestyle's account VM limit (which builder VMs count against) is capacity
// the reconciler waits out, not a failed session.
func TestCreateReportsLimitExceededAsCapacity(t *testing.T) {
	api := newFakeAPI()
	api.createStatus = http.StatusForbidden
	api.createBody = `{"code":"LIMIT_EXCEEDED","message":"concurrent VM limit reached"}`
	_, err := testClient(t, api).Create(context.Background(), sandbox.Spec{SessionID: "s"})
	if !errors.Is(err, sandbox.ErrAtCapacity) {
		t.Fatalf("err = %v, want ErrAtCapacity", err)
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || !strings.Contains(err.Error(), "concurrent VM limit") {
		t.Fatalf("err = %v, want the provider message kept", err)
	}
	api.createStatus = http.StatusBadRequest
	api.createBody = `{"code":"INVALID","message":"bad request"}`
	if _, err := testClient(t, api).Create(context.Background(), sandbox.Spec{SessionID: "s"}); errors.Is(err, sandbox.ErrAtCapacity) {
		t.Fatalf("a plain 400 was reported as capacity: %v", err)
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
