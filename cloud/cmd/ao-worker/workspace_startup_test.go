package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// workerEventRecorder is a control plane that records the worker events it
// receives.
type workerEventRecorder struct {
	mu     sync.Mutex
	events []worker.EventRequest
}

func (r *workerEventRecorder) server(t *testing.T) *client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/worker/events" {
			http.NotFound(w, req)
			return
		}
		var event struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		body, _ := io.ReadAll(req.Body)
		if err := json.Unmarshal(body, &event); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.events = append(r.events, worker.EventRequest{Type: event.Type, Payload: event.Payload})
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)
	return &client{baseURL: server.URL, http: server.Client()}
}

func (r *workerEventRecorder) startup(t *testing.T) []worker.WorkspaceStartupEvent {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var reports []worker.WorkspaceStartupEvent
	for _, event := range r.events {
		if event.Type != worker.WorkspaceStartupEventType {
			continue
		}
		var report worker.WorkspaceStartupEvent
		if err := json.Unmarshal(event.Payload.(json.RawMessage), &report); err != nil {
			t.Fatal(err)
		}
		reports = append(reports, report)
	}
	return reports
}

// A failed Setup step becomes the session's startup error: the step and its
// exit status as the message, the command's output as the detail.
func TestFailedSetupIsReportedAsTheWorkspaceStartupError(t *testing.T) {
	recorder := &workerEventRecorder{}
	c := recorder.server(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	workspace := t.TempDir()
	failed := runProjectSetup(context.Background(), logger, c, t.TempDir(), true,
		[]string{"echo starting stack; echo missing secret >&2; exit 4", "echo never"}, workspace,
		worker.SessionScriptEnvironment{SessionID: "session-1", WorkspaceDir: workspace})
	if failed == nil {
		t.Fatal("setup reported success")
	}
	reportWorkspaceStartup(context.Background(), logger, c, setupStartupFailure(*failed))

	reports := recorder.startup(t)
	if len(reports) != 1 {
		t.Fatalf("startup reports = %+v", reports)
	}
	report := reports[0]
	if report.Status != worker.WorkspaceStartupFailed || report.Code != domain.WorkspaceStartupErrorProjectSetup ||
		!strings.HasPrefix(report.Message, "The project's Setup script failed at step 1 (exit status 4), so the agent did not start.") ||
		!strings.Contains(report.Message, "Project settings > Scripts") ||
		!strings.Contains(report.Detail, "missing secret") || strings.Contains(report.Message, "missing secret") {
		t.Fatalf("report = %+v", report)
	}
}

// A failed Setup step's output and command never carry a credential to the
// control plane, in the outcome event or the startup error: an AWS Secrets
// Manager SecretString's password is masked whole, not cut at its first
// escaped quote, and a secret environment variable's value printed with
// nothing naming it (or traced by set -x) is masked as the step's events mask
// it.
func TestFailedSetupNeverReportsASecret(t *testing.T) {
	t.Setenv("MY_SERVICE_SECRET", "LeakEnvSvc01x")
	t.Setenv("REGISTRY_PASSWORD", "LeakEnvReg02x")
	// Not a secret's name: only the coloured pair it prints names it one.
	t.Setenv("PRINTED_VALUE", "LeakAnsi03x")
	recorder := &workerEventRecorder{}
	c := recorder.server(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	workspace := t.TempDir()
	failed := runProjectSetup(context.Background(), logger, c, t.TempDir(), true,
		[]string{`printf '%s\n' '{"SecretString":"{\"password\":\"hunter2xyz\"}"}' 'mysql --password Leak7flag'; ` +
			`printf '\033[1mSIGNING_KEY\033[0m: %s\n' "$PRINTED_VALUE"; ` +
			`(set -x; : docker login -u ci -p "$REGISTRY_PASSWORD" registry.example) 2>&1; ` +
			`printf '%s' "$MY_SERVICE_SECRET"; exit 3`},
		workspace, worker.SessionScriptEnvironment{SessionID: "session-1", WorkspaceDir: workspace})
	if failed == nil {
		t.Fatal("setup reported success")
	}
	reportWorkspaceStartup(context.Background(), logger, c, setupStartupFailure(*failed))
	recorder.mu.Lock()
	encoded, err := json.Marshal(recorder.events)
	recorder.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hunter2xyz", "Leak7flag", "LeakEnvSvc01x", "LeakEnvReg02x", "LeakAnsi03x"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("%q reported in %s", secret, encoded)
		}
	}
	if !strings.Contains(failed.Output, `"SecretString":[REDACTED]`) {
		t.Fatalf("setup output = %q", failed.Output)
	}
}

// A Setup step refused Docker says so in the startup message, and the detail
// names the command, the user it ran as, and its full log.
func TestSetupPermissionFailureExplainsWhoRanIt(t *testing.T) {
	recorder := &workerEventRecorder{}
	c := recorder.server(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	workspace, dataDir := t.TempDir(), t.TempDir()
	failed := runProjectSetup(context.Background(), logger, c, dataDir, true,
		[]string{"docker compose up -d || { echo 'permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock' >&2; exit 1; }"},
		workspace, worker.SessionScriptEnvironment{SessionID: "session-1", WorkspaceDir: workspace})
	if failed == nil {
		t.Fatal("setup reported success")
	}
	report := setupStartupFailure(*failed)
	user := worker.CurrentScriptIdentity().User
	if !strings.Contains(report.Message, "This step ran as "+user+", which cannot use Docker") {
		t.Fatalf("message = %q", report.Message)
	}
	for _, want := range []string{"Step 1: docker compose up -d", "Ran as " + user, " in " + workspace, "Full output: " + dataDir, "docker.sock"} {
		if !strings.Contains(report.Detail, want) {
			t.Fatalf("detail %q lacks %q", report.Detail, want)
		}
	}
}

// A checkout failure names its cause: the Coder connection's working
// directory, or the repository checkout with the redacted error as detail.
func TestCheckoutFailuresHaveTheirOwnStartupErrors(t *testing.T) {
	directory := checkoutStartupFailure(fmt.Errorf("%w: /home/ubuntu/work/alice is not a checkout of the repository", worker.ErrWorkingDirectoryUnusable))
	if directory.Code != domain.WorkspaceStartupErrorWorkingDirectory ||
		!strings.Contains(directory.Message, "/home/ubuntu/work/alice is not a checkout of the repository") {
		t.Fatalf("working directory failure = %+v", directory)
	}
	checkout := checkoutStartupFailure(errors.New("git fetch: https://x-access-token:ghs_abcdefghijklmnopqrstuvwxyz0123456789@github.com/acme/app: 403"))
	if checkout.Code != domain.WorkspaceStartupErrorCheckout || checkout.Status != worker.WorkspaceStartupFailed ||
		!strings.Contains(checkout.Detail, "403") || strings.Contains(checkout.Detail, "abcdefghijklmnop") {
		t.Fatalf("checkout failure = %+v", checkout)
	}
	branch := checkoutStartupFailure(fmt.Errorf("configure: %w: feature/x exists on the repository but could not be checked out from origin/feature/x: README.md would be overwritten", worker.ErrSessionBranchSwitch))
	if branch.Status != worker.WorkspaceStartupFailed || !strings.Contains(branch.Message, "session's branch") ||
		!strings.Contains(branch.Detail, "feature/x") || !strings.Contains(branch.Detail, "README.md") {
		t.Fatalf("session branch failure = %+v", branch)
	}
}

// A workspace that is ready is reported so a failure from an earlier attempt
// is cleared.
func TestReadyWorkspaceIsReported(t *testing.T) {
	recorder := &workerEventRecorder{}
	c := recorder.server(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if failed := runProjectSetup(context.Background(), logger, c, t.TempDir(), true, []string{"true"}, t.TempDir(),
		worker.SessionScriptEnvironment{SessionID: "session-1"}); failed != nil {
		t.Fatalf("setup failed: %+v", failed)
	}
	reportWorkspaceStartup(context.Background(), logger, c, worker.WorkspaceStartupEvent{Status: worker.WorkspaceStartupReady})
	reports := recorder.startup(t)
	if len(reports) != 1 || reports[0].Status != worker.WorkspaceStartupReady || reports[0].Code != "" {
		t.Fatalf("reports = %+v", reports)
	}
}

// The staging re-test's Setup steps, verbatim (see the worker package's
// stagingRetestSteps): the failed run's outcome event, the startup error the
// worker reports (its Detail is what Copy details copies) and that Detail as
// the control plane stores it hold none of the steps' secrets, and still show
// what is not secret.
func TestStagingRetestStartupErrorNeverReportsASecret(t *testing.T) {
	steps := []string{
		`echo '{"SecretString":"{\"password\":\"FAKEVAL1\"}"}'`,
		`echo "docker login -p FAKEVAL2 registry.example.com || true"`,
		`echo "mysql -pFAKEVAL3"`,
		`echo "sshpass -p FAKEVAL4 ssh -p 22 host"`,
		`echo "curl -u admin:FAKEVAL5 https://x"`,
		`echo "https://` + "gh" + "p_" + `FAKEVAL6aaaaaaaaaaaaaaaaaaaaaaaaaaaa@github.com/o/r"`,
		`echo "https://acct.blob.core.windows.net/c?sv=1&sig=FAKEVAL7"`,
		`printf 'apiVersion: v1\nkind: Secret\nmetadata:\n  name: demo\ndata:\n  foo: RkFLRVZBTDg=\n'`,
		`echo '["--token","FAKEVAL9"]'`,
		`printf 'private_key: |\n  FAKEVAL10-line-one\n  FAKEVAL10-line-two\nafter_block: visible\n'`,
		`echo hf_FAKEVAL11aaaaaaaaaaaaaaaaaaaaaaaa`,
		`echo "API_KEY=FAKEVAL12;next=1"`,
		`printf '\033[32mPASSWORD\033[0m=FAKEVAL13\n'`,
		`export MY_SERVICE_SECRET=FAKEVAL14; printf '%s\n' "$MY_SERVICE_SECRET"`,
		`echo "docker run -p 8080:80 nginx"`,
	}
	combined := `echo "== summary step =="; ` + strings.Join(steps, "; ") + `; set -x; true --password FAKEVAL15; echo "about to fail"; exit 3`
	recorder := &workerEventRecorder{}
	c := recorder.server(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	workspace := t.TempDir()
	failed := runProjectSetup(context.Background(), logger, c, t.TempDir(), true, append(steps, combined),
		workspace, worker.SessionScriptEnvironment{SessionID: "session-1", WorkspaceDir: workspace})
	if failed == nil {
		t.Fatal("setup reported success")
	}
	startup := setupStartupFailure(*failed)
	reportWorkspaceStartup(context.Background(), logger, c, startup)
	recorder.mu.Lock()
	encoded, err := json.Marshal(recorder.events)
	recorder.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// What the control plane stores (worker_handlers.go): the detail through
	// the script redaction again.
	stored := worker.RedactScriptOutput(startup.Detail)
	published := string(encoded) + "\n" + startup.Detail + "\n" + startup.Message + "\n" + stored
	for index := 1; index <= 15; index++ {
		secret := fmt.Sprintf("FAKEVAL%d", index)
		if index == 8 {
			secret = "RkFLRVZBTDg"
		}
		if strings.Contains(published, secret) {
			t.Errorf("%q reported", secret)
		}
	}
	for _, kept := range []string{"ssh -p 22 host", "docker run -p 8080:80 nginx", "API_KEY=[REDACTED];next=1", "after_block: visible"} {
		if !strings.Contains(startup.Detail, kept) || !strings.Contains(stored, kept) {
			t.Errorf("%q is not in the startup error's detail", kept)
		}
	}
	if t.Failed() {
		t.Logf("detail:\n%s", startup.Detail)
	}
}

// A session opened while Setup was still running must not count as a retry
// once that Setup fails, or Setup silently runs again and the agent starts.
func TestAnOpenSavedBeforeAFailedStartIsNotARetry(t *testing.T) {
	reopen := make(chan struct{}, 1)
	dropPendingReopen(reopen) // nothing saved: returns at once
	reopen <- struct{}{}      // opened while the failed attempt ran
	dropPendingReopen(reopen)
	select {
	case <-reopen:
		t.Fatal("an open saved before the failure was read as a retry")
	default:
	}
}
