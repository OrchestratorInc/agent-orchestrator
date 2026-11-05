package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// TestOperatorRefusesInsideAnAgentSession is the hard refusal: when the environment shows this
// process was launched by AO as a session or worker attempt, the CLI exits non-zero BEFORE any
// HTTP call. The server counts requests and fails the test if it sees even one.
func TestOperatorRefusesInsideAnAgentSession(t *testing.T) {
	cfg := setConfigEnv(t)
	// Every /api/v1 request is counted. The CLI's own telemetry emitter (POST
	// /internal/telemetry/cli-invoked) runs in PersistentPreRunE for every command and is not an
	// API call, so it is excluded rather than faked: what must not happen is a request that could
	// obtain approval authority.
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			requests.Add(1)
			http.Error(w, "the CLI must not call the daemon from an agent session", http.StatusTeapot)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	if err := runfile.Write(cfg.runFile, runfile.Info{
		PID: os.Getpid(), Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	for _, marker := range []string{"AO_SESSION_ID", "AO_ATTEMPT_ID", "AO_WORKFLOW_ID", "AO_WORKFLOW_LEASE"} {
		t.Run(marker, func(t *testing.T) {
			t.Setenv(marker, "session-or-attempt-value")
			_, stderr, err := executeCLI(t, Deps{}, "workflow", "operator", "bind",
				"--workflow", "w1", "--attempt", "a1", "--idempotency-key", "bind-1")
			if err == nil {
				t.Fatal("the CLI did not refuse inside an agent session")
			}
			if ExitCode(err) != exitAgentSession {
				t.Fatalf("exit code = %d, want %d", ExitCode(err), exitAgentSession)
			}
			if !strings.Contains(err.Error(), "agent session") {
				t.Fatalf("refusal does not say why: %v", err)
			}
			if stderr != "" && strings.Contains(stderr, "operator.token") {
				t.Fatalf("stderr mentions the token file: %s", stderr)
			}
			if got := requests.Load(); got != 0 {
				t.Fatalf("the CLI made %d API requests before refusing", got)
			}
		})
	}
	// No marker at all: the guard releases, and the command is allowed to proceed.
	if isAgent, markers := operatorEnvGuard(func(string) string { return "" }); isAgent {
		t.Fatalf("a clean environment was treated as an agent session: %v", markers)
	}
}

// TestOperatorApprovalFlowSubmits with an approval header after a human confirms proves the whole
// client loop against a fake daemon: create → print id/code → poll → submit with the header, and
// the submitted body is byte-identical to the digested one.
func TestOperatorApprovalFlowSubmitsWithApprovalHeader(t *testing.T) {
	cfg := setConfigEnv(t)
	digest := principal.PayloadDigest([]byte(`{"idempotencyKey":"bind-1"}`))

	var (
		created   bool
		confirmed bool
		submitted atomic.Bool
		gotHeader string
		gotBody   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(principal.HeaderOperatorToken) != "" {
			t.Error("CLI sent an operator token")
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/operator/approvals":
			body := map[string]string{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["operationMethod"] != http.MethodPost ||
				body["operationPath"] != "/api/v1/workflows/w1/attempts/a1/bind" {
				t.Errorf("approval names %v", body)
			}
			if body["payload"] != `{"idempotencyKey":"bind-1"}` || body["payloadDigest"] != principal.PayloadDigest([]byte(body["payload"])) {
				t.Errorf("digest = %q, want %q", body["payloadDigest"], digest)
			}
			created = true
			writeTestJSON(w, http.StatusCreated, map[string]any{"approval": map[string]any{
				"approvalId": "approval-1", "code": "ABCD1234", "state": "pending",
				"operation":     map[string]string{"method": http.MethodPost, "path": "/api/v1/workflows/w1/attempts/a1/bind"},
				"payloadDigest": digest,
				"expiresAt":     time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339),
			}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/confirm"):
			var confirmation map[string]string
			_ = json.NewDecoder(r.Body).Decode(&confirmation)
			if confirmation["code"] != "ABCD1234" || confirmation["payloadDigest"] != digest {
				t.Errorf("wrong confirmation binding: %v", confirmation)
			}
			confirmed = true
			writeTestJSON(w, http.StatusOK, map[string]any{"approval": map[string]any{
				"approvalId": "approval-1", "state": "confirmed",
				"operation":     map[string]string{"method": http.MethodPost, "path": "/api/v1/workflows/w1/attempts/a1/bind"},
				"payloadDigest": digest,
				"expiresAt":     time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339),
			}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/bind"):
			if !confirmed {
				t.Error("submitted without native confirmation")
			}
			gotHeader = r.Header.Get(principal.HeaderOperatorApproval)
			raw, _ := io.ReadAll(r.Body)
			gotBody = string(raw)
			submitted.Store(true)
			writeTestJSON(w, http.StatusOK, map[string]any{
				"binding": map[string]any{"attemptId": "a1", "state": "bound_ready", "sessionId": "s1"},
				"spawned": true, "frozen": false, "reasonCode": "ready",
			})
		case strings.HasPrefix(r.URL.Path, "/internal/"):
			// The CLI's telemetry emitter, not an API call.
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	if err := runfile.Write(cfg.runFile, runfile.Info{
		PID: os.Getpid(), Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := executeCLI(t, Deps{}, "workflow", "operator", "bind",
		"--workflow", "w1", "--attempt", "a1", "--idempotency-key", "bind-1")
	if err != nil {
		t.Fatalf("bind: %v (stderr=%s)", err, stderr)
	}
	if !created || !confirmed || !submitted.Load() {
		t.Fatalf("flow incomplete: created=%t confirmed=%t submitted=%t", created, confirmed, submitted.Load())
	}
	// The approval id, the code, and the operation must all be printed: a human compares them
	// against the desktop prompt, which is the actual control.
	for _, want := range []string{"approval-1", "ABCD1234", "/api/v1/workflows/w1/attempts/a1/bind", digest} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("the prompt omits %q:\n%s", want, stdout)
		}
	}
	if gotHeader != "approval-1" {
		t.Fatalf("submitted approval header = %q", gotHeader)
	}
	if gotBody != `{"idempotencyKey":"bind-1"}` {
		t.Fatalf("submitted body = %q, want the exact digested bytes", gotBody)
	}
	// The CLI never reads the token file, so it can never print it.
	if strings.Contains(stdout, "operator.token") || strings.Contains(stderr, "operator.token") {
		t.Fatalf("the CLI mentioned the token file: stdout=%s stderr=%s", stdout, stderr)
	}
	// 通用正式入口必须走相同确认链，并保留文件中的精确字节。
	payloadFile := filepath.Join(t.TempDir(), "operator-payload.json")
	if err := os.WriteFile(payloadFile, []byte(`{"idempotencyKey":"bind-1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	created, confirmed = false, false
	submitted.Store(false)
	_, stderr, err = executeCLI(t, Deps{}, "workflow", "operator", "request", "--method", "POST",
		"--path", "/api/v1/workflows/w1/attempts/a1/bind", "--body-file", payloadFile)
	if err != nil || !created || !confirmed || !submitted.Load() || gotBody != `{"idempotencyKey":"bind-1"}` {
		t.Fatalf("operator request failed: err=%v stderr=%s body=%s", err, stderr, gotBody)
	}
}

// TestOperatorApprovalTimeoutFailsClosed proves a human who never confirms leaves nothing
// submitted: the poll loop ends when the approval expires and the action is never sent.
func TestOperatorApprovalTimeoutFailsClosed(t *testing.T) {
	cfg := setConfigEnv(t)
	var submitted atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/operator/approvals":
			// Already expired: the CLI must give up without polling forever.
			writeTestJSON(w, http.StatusCreated, map[string]any{"approval": map[string]any{
				"approvalId": "approval-2", "code": "DEADBEEF", "state": "pending",
				"operation":     map[string]string{"method": http.MethodPost, "path": "/api/v1/workflows/w1/attempts/a1/bind"},
				"payloadDigest": principal.PayloadDigest([]byte(`{"idempotencyKey":"bind-1"}`)),
				"expiresAt":     time.Now().UTC().Add(-time.Second).Format(time.RFC3339),
			}})
		case strings.HasSuffix(r.URL.Path, "/confirm"):
			writeTestJSON(w, http.StatusOK, map[string]any{"approval": map[string]any{
				"approvalId": "approval-2", "state": "expired",
			}})
		case strings.HasSuffix(r.URL.Path, "/bind"):
			submitted.Store(true)
			writeTestJSON(w, http.StatusOK, map[string]any{})
		case strings.HasPrefix(r.URL.Path, "/internal/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	if err := runfile.Write(cfg.runFile, runfile.Info{
		PID: os.Getpid(), Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCLI(t, Deps{}, "workflow", "operator", "bind",
		"--workflow", "w1", "--attempt", "a1", "--idempotency-key", "bind-1")
	if err == nil {
		t.Fatal("the CLI reported success without a confirmation")
	}
	if ExitCode(err) != 1 {
		t.Fatalf("exit code = %d, want 1", ExitCode(err))
	}
	if submitted.Load() {
		t.Fatal("the CLI submitted the action without a confirmed approval")
	}
}

// TestOperatorTypedVerbsStayWithinApprovalAllowlist 保证正式类型化命令没有意外指向其他 HTTP 能力。
func TestOperatorTypedVerbsStayWithinApprovalAllowlist(t *testing.T) {
	for _, action := range operatorActions {
		path := strings.NewReplacer("{workflowId}", "w1", "{attemptId}", "a1", "{requestId}", "r1").Replace(action.pattern)
		if !principal.IsOperatorOperation(action.method, path) {
			t.Fatalf("%s %s is outside the approval allowlist", action.method, path)
		}
	}
}

func writeTestJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
