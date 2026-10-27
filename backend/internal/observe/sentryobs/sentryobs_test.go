package sentryobs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5"
	sqlite3 "modernc.org/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

func TestShouldCaptureStatus(t *testing.T) {
	t.Parallel()
	cases := map[int]bool{
		200: false,
		404: false,
		500: true,
		502: true,
		503: false, // transient contention (see #4325) — never an issue
		504: true,
	}
	for status, want := range cases {
		if got := ShouldCaptureStatus(status); got != want {
			t.Fatalf("ShouldCaptureStatus(%d) = %v, want %v", status, got, want)
		}
	}
}

func TestInitNoDSNIsNoOp(t *testing.T) {
	if err := Init(Config{}); err != nil {
		t.Fatalf("Init with empty DSN: %v", err)
	}
	if Enabled() {
		t.Fatal("Sentry should be disabled without a DSN")
	}
	// Capture calls must be safe no-ops when disabled.
	CaptureHTTPError(context.TODO(), errString("boom"), map[string]string{"path": "/x"}, "fp", telemetrymeta.RoutePattern(nil))
	CapturePanic(context.TODO(), "kaboom", nil, "fp", telemetrymeta.RoutePattern(nil))
	Flush(0)
}

func TestCaptureTagsAuthoritativeInstallID(t *testing.T) {
	transport := &recordingTransport{events: make(chan *sentry.Event, 1)}
	if err := Init(Config{DSN: "https://public@example.com/1", InstallID: "ins_stable", Release: "1.2.3", Environment: "stable", Transport: transport}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetPolicyEnabled(false); enabled.Store(false) })
	SetPolicyEnabled(true)
	CaptureHTTPError(context.Background(), errString("boom"), map[string]string{"install_id": "ins_spoofed"}, "fp", telemetrymeta.RoutePattern(nil))
	select {
	case event := <-transport.events:
		if event.Tags["install_id"] != "ins_stable" {
			t.Fatalf("install tag = %q", event.Tags["install_id"])
		}
		if event.User.ID != "ins_stable" || event.User.Email != "" || event.Release != "1.2.3" || event.Environment != "stable" {
			t.Fatalf("Sentry identity or build context = user=%+v release=%q environment=%q", event.User, event.Release, event.Environment)
		}
	case <-time.After(time.Second):
		t.Fatal("Sentry event missing")
	}
}

func TestCaptureWithoutInstallIDKeepsErrorReportingEnabled(t *testing.T) {
	transport := &recordingTransport{events: make(chan *sentry.Event, 1)}
	if err := Init(Config{DSN: "https://public@example.com/1", Transport: transport}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetPolicyEnabled(false); enabled.Store(false) })
	SetPolicyEnabled(true)
	if !Enabled() {
		t.Fatal("missing installation ID disabled Sentry")
	}
	CaptureHTTPError(context.Background(), errString("boom"), map[string]string{"install_id": "ins_spoofed"}, "fp", telemetrymeta.RoutePattern(nil))
	select {
	case event := <-transport.events:
		if _, ok := event.Tags["install_id"]; ok {
			t.Fatal("event used a caller supplied installation ID")
		}
		if event.User.ID != "" {
			t.Fatalf("event used a caller supplied user ID: %+v", event.User)
		}
		if event.Release != "unknown" || event.Environment != "unknown" {
			t.Fatalf("missing build context fallback: release=%q environment=%q", event.Release, event.Environment)
		}
	case <-time.After(time.Second):
		t.Fatal("Sentry event missing")
	}
}

type recordingTransport struct{ events chan *sentry.Event }

func (*recordingTransport) Configure(sentry.ClientOptions)        {}
func (*recordingTransport) Flush(time.Duration) bool              { return true }
func (*recordingTransport) FlushWithContext(context.Context) bool { return true }
func (*recordingTransport) Close()                                {}
func (t *recordingTransport) SendEvent(event *sentry.Event)       { t.events <- event }

func TestScrubEventStripsPathsAndContext(t *testing.T) {
	t.Parallel()
	event := &sentry.Event{
		Message:    "failed at /Users/dave/ao/main.go",
		ServerName: "daves-macbook.local",
		Request:    &sentry.Request{URL: "http://127.0.0.1:3001/api/v1/x"},
		Contexts:   map[string]sentry.Context{"runtime": {"go_stack": "secret stack argument"}},
		Tags:       map[string]string{"path": "unmatched", "operation": "http_request"},
		Exception: []sentry.Exception{{
			Value: "open /home/eve/ws/file: denied",
			Stacktrace: &sentry.Stacktrace{Frames: []sentry.Frame{
				{Function: "safeFunction", Lineno: 42, AbsPath: "/Users/dave/ao/backend/internal/x.go", Filename: "/Users/dave/ao/backend/internal/x.go", ContextLine: "secret source", Vars: map[string]interface{}{"secret": "value"}},
			}},
		}},
	}
	out := scrubEvent(event)
	if out.Message != "HTTP server error" {
		t.Fatalf("message not scrubbed: %q", out.Message)
	}
	if out.ServerName != "" {
		t.Fatalf("server name not cleared: %q", out.ServerName)
	}
	if out.Request != nil {
		t.Fatal("request context not dropped")
	}
	if out.Exception[0].Value != "" || out.Exception[0].Type != "HTTP server error" {
		t.Fatalf("exception value not scrubbed: %q", out.Exception[0].Value)
	}
	f := out.Exception[0].Stacktrace.Frames[0]
	if f.AbsPath != "" || f.Filename != "" || f.ContextLine != "" || len(f.Vars) != 0 || f.Function != "safeFunction" || f.Lineno != 42 {
		t.Fatalf("frame paths not scrubbed: %+v", f)
	}
	if len(out.Contexts) != 3 || out.Contexts["runtime"]["go_stack"] != nil || out.Contexts["runtime"]["version"] == "" || out.Contexts["device"]["arch"] == "" || out.Contexts["os"]["name"] == "" || len(out.Tags) != 2 {
		t.Fatalf("dynamic context or tag survived: contexts=%+v tags=%+v", out.Contexts, out.Tags)
	}
}

func TestCaptureBoundaryKeepsDiagnosticsWithoutUserNames(t *testing.T) {
	transport := &recordingTransport{events: make(chan *sentry.Event, 3)}
	if err := Init(Config{DSN: "https://public@example.com/1", InstallID: "ins_stable", Transport: transport}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetPolicyEnabled(false); enabled.Store(false) })
	SetPolicyEnabled(true)

	const secret = "PrivateProjectOrSession_Z9"
	tags := map[string]string{
		"component": "httpd", "operation": "http_request", "method": "GET", "category": "http_5xx", "platform": "daemon",
		"status": "500", "error_kind": "internal", "error_code": "INTERNAL_ERROR",
		"path": "/api/v1/projects/" + secret, "request_id": secret,
	}
	for _, err := range []error{
		fmt.Errorf("get project %s: storage failed", secret),
		fmt.Errorf("get session %s: repo %s failed", secret, secret),
		fmt.Errorf("open /tmp/%s/worktree: access denied", secret),
	} {
		CaptureHTTPError(context.Background(), err, tags, "0123456789abcdef", telemetrymeta.RoutePattern(nil))
	}
	for i := 0; i < 3; i++ {
		event := <-transport.events
		if event.Message != "HTTP server error (INTERNAL_ERROR)" || len(event.Exception) == 0 || event.Exception[0].Value != "" || event.Exception[0].Type != event.Message {
			t.Fatalf("unsafe title fields: message=%q exception=%+v", event.Message, event.Exception)
		}
		if event.Tags["method"] != "GET" || event.Tags["error_code"] != "INTERNAL_ERROR" || event.Tags["install_id"] != "ins_stable" || event.Tags["category"] != "http_5xx" || event.Tags["platform"] != "daemon" || event.Tags["status"] != "500" || len(event.Exception[0].Stacktrace.Frames) == 0 {
			t.Fatalf("diagnostics lost: %+v", event)
		}
		if event.Fingerprint[0] != "0123456789abcdef" {
			t.Fatalf("fingerprint lost: %+v", event.Fingerprint)
		}
		if event.Tags["path"] != "unmatched" {
			t.Fatalf("unsafe route fallback: %+v", event.Tags)
		}
		if _, ok := event.Tags["request_id"]; ok {
			t.Fatalf("untrusted request id leaked: %+v", event.Tags)
		}
		encoded, marshalErr := event.MarshalJSON()
		if marshalErr != nil || strings.Contains(string(encoded), secret) {
			t.Fatalf("sensitive value in outbound event: %s, marshal error: %v", encoded, marshalErr)
		}
	}

	panicSite := telemetrymeta.CurrentFailureSite()
	CapturePanic(context.Background(), "panic in "+secret, map[string]string{
		"component": "httpd", "operation": "http_request_panic", "panic_kind": "string", "path": "/" + secret,
	}, "fedcba9876543210", telemetrymeta.RoutePattern(nil), panicSite)
	event := <-transport.events
	if event.Message != "http handler panic" || event.Exception[0].Value != "" || event.Exception[0].Type != event.Message {
		t.Fatalf("unsafe panic title: %+v", event)
	}
	if event.User.ID != "ins_stable" || event.Release == "" || event.Environment == "" {
		t.Fatalf("panic lost Sentry identity or build context: %+v", event)
	}
	if len(event.Fingerprint) != 1 || event.Fingerprint[0] != telemetrymeta.Fingerprint("sentry", "http_request_panic", panicSite.String()) || event.Exception[0].Stacktrace.Frames[0].Function != panicSite.String() {
		t.Fatalf("panic did not group at AO failure site: %+v", event)
	}
	encoded, err := event.MarshalJSON()
	if err != nil || strings.Contains(string(encoded), secret) {
		t.Fatalf("sensitive panic value in outbound event: %s, marshal error: %v", encoded, err)
	}

	CaptureHTTPError(context.Background(), errString(secret), map[string]string{
		"operation": secret, "error_code": "PRIVATEPROJECTORSESSION_Z9", "path": "/" + secret,
	}, secret, telemetrymeta.RoutePattern(nil))
	event = <-transport.events
	if event.Message != "daemon error" || event.Exception[0].Type != "daemon error" || event.Exception[0].Value != "" || len(event.Fingerprint) != 0 || event.Tags["error_code"] != "" || event.Tags["operation"] != "" || event.Tags["path"] != "unmatched" {
		t.Fatalf("unknown code, operation, or fingerprint survived: %+v", event)
	}
	encoded, err = event.MarshalJSON()
	if err != nil || strings.Contains(string(encoded), secret) {
		t.Fatalf("sensitive fallback value in outbound event: %s, marshal error: %v", encoded, err)
	}
}

func TestCaptureBoundaryKeepsRegisteredRouteTemplate(t *testing.T) {
	transport := &recordingTransport{events: make(chan *sentry.Event, 1)}
	if err := Init(Config{DSN: "https://public@example.com/1", Transport: transport}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetPolicyEnabled(false); enabled.Store(false) })
	SetPolicyEnabled(true)

	router := chi.NewRouter()
	router.Get("/api/v1/projects/{projectID}", func(w http.ResponseWriter, r *http.Request) {
		CaptureHTTPError(r.Context(), errString("get project PrivateProjectOrSession_Z9 failed"), map[string]string{
			"operation": "http_request", "path": r.URL.Path,
		}, "0123456789abcdef", telemetrymeta.RoutePattern(r))
		w.WriteHeader(http.StatusInternalServerError)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/projects/PrivateProjectOrSession_Z9", nil))
	event := <-transport.events
	if event.Tags["path"] != "/api/v1/projects/{projectID}" || event.Message != "HTTP server error" {
		t.Fatalf("registered route or operation lost: %+v", event)
	}
	encoded, err := event.MarshalJSON()
	if err != nil || strings.Contains(string(encoded), "PrivateProjectOrSession_Z9") {
		t.Fatalf("request path leaked through registered route: %s, marshal error: %v", encoded, err)
	}
}

func TestCaptureBoundaryScrubsAgentRelaunchFailure(t *testing.T) {
	transport := &recordingTransport{events: make(chan *sentry.Event, 1)}
	if err := Init(Config{DSN: "https://public@example.com/1", InstallID: "ins_stable", Release: "2.0.0", Environment: "stable", Transport: transport}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetPolicyEnabled(false); enabled.Store(false) })
	SetPolicyEnabled(true)
	const secret = "PrivateProjectOrSession_Z9"
	CaptureHTTPError(context.Background(), fmt.Errorf("resume session %s: %s", secret, secret), map[string]string{
		"component": "session_service", "operation": "resume_agent", "area": "agent",
		"error_code": "AGENT_RESUME_FAILED", "request_id": secret, "cause_type": secret,
	}, "0123456789abcdef", telemetrymeta.RoutePattern(nil))
	event := <-transport.events
	if event.Message != "Agent resume failed (AGENT_RESUME_FAILED)" || event.Exception[0].Value != "" ||
		event.Tags["component"] != "session_service" || event.Tags["operation"] != "resume_agent" ||
		event.Tags["error_code"] != "AGENT_RESUME_FAILED" || event.Tags["area"] != "agent" ||
		len(event.Fingerprint) != 1 || event.Fingerprint[0] != "0123456789abcdef" {
		t.Fatalf("agent relaunch diagnostics = %+v", event)
	}
	encoded, err := event.MarshalJSON()
	if err != nil || strings.Contains(string(encoded), secret) {
		t.Fatalf("agent relaunch leaked sensitive context: %s, marshal error: %v", encoded, err)
	}
}

func TestCaptureBoundaryUsesFailureSiteAndFixedAreaLabel(t *testing.T) {
	transport := &recordingTransport{events: make(chan *sentry.Event, 2)}
	if err := Init(Config{DSN: "https://public@example.com/1", Release: "2.0.0", Environment: "nightly", Transport: transport}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetPolicyEnabled(false); enabled.Store(false) })
	SetPolicyEnabled(true)
	const secret = "PrivateProjectOrSession_Z9"
	tags := map[string]string{"operation": "http_request", "error_code": "WORKSPACE_GIT_READ_FAILED", "area": "git", "path": "/" + secret}
	site := telemetrymeta.CurrentFailureSite()
	CaptureHTTPError(context.Background(), fmt.Errorf("get project %s: git failed", secret), tags, "0123456789abcdef", telemetrymeta.RoutePattern(nil), site)
	event := <-transport.events
	if site.String() == "" || strings.Contains(site.String(), "sentryobs.CaptureHTTPError") {
		t.Fatalf("failure site = %q", site.String())
	}
	if event.Message != "Git operation failed (WORKSPACE_GIT_READ_FAILED)" || event.Tags["area"] != "git" {
		t.Fatalf("fixed diagnostics lost: %+v", event)
	}
	wantGroup := telemetrymeta.Fingerprint("sentry", "http_request", site.String(), "WORKSPACE_GIT_READ_FAILED")
	if len(event.Fingerprint) != 1 || event.Fingerprint[0] != wantGroup {
		t.Fatalf("failure-site fingerprint = %v, want %q", event.Fingerprint, wantGroup)
	}
	frames := event.Exception[0].Stacktrace.Frames
	if len(frames) != 1 || frames[0].Function != site.String() || frames[0].Lineno != site.Line() || strings.Contains(frames[0].Function, "sentryobs.CaptureHTTPError") {
		t.Fatalf("failure stack = %+v, want AO site %q:%d", frames, site.String(), site.Line())
	}
	encoded, err := event.MarshalJSON()
	if err != nil || strings.Contains(string(encoded), secret) {
		t.Fatalf("sensitive outbound event: %s, marshal error: %v", encoded, err)
	}

	CaptureHTTPError(context.Background(), fmt.Errorf("request canceled: %w", context.Canceled), tags, "0123456789abcdef", telemetrymeta.RoutePattern(nil), site)
	select {
	case event := <-transport.events:
		t.Fatalf("canceled request captured: %+v", event)
	default:
	}
}

func TestSentryTagRules(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]bool{"500": true, "502": true, "503": false, "499": false, "500x": false} {
		if got := safeSentryTag("status", value); got != want {
			t.Fatalf("status %q allowed = %v, want %v", value, got, want)
		}
	}
	if !safeErrorCode("INTERNAL_ERROR") || safeErrorCode("PRIVATEPROJECTORSESSION_Z9") {
		t.Fatal("error code allowlist accepted a synthetic code or rejected a known code")
	}
}

func TestAreaRequiresErrorEvidence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		code string
		want string
	}{
		{errString("project PrivateProjectOrSession_Z9"), "", ""},
		{errString("git failure"), "WORKSPACE_GIT_READ_FAILED", "git"},
		{errString("agent failure"), "SPAWN_CREATE_FAILED", "agent"},
		{&sqlite3.Error{}, "", "db"},
	} {
		if got := Area(tc.err, tc.code); got != tc.want {
			t.Fatalf("Area(%q, %q) = %q, want %q", tc.err, tc.code, got, tc.want)
		}
	}
}

func TestPolicyDisableDrainsEnteredProviderCallAndRejectsLaterCapture(t *testing.T) {
	transport := &blockingTransport{started: make(chan struct{}, 1), release: make(chan struct{})}
	if err := Init(Config{DSN: "https://public@example.com/1", Transport: transport}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() {
		SetPolicyEnabled(false)
		enabled.Store(false)
	})
	SetPolicyEnabled(true)

	captured := make(chan struct{})
	go func() {
		CaptureHTTPError(context.Background(), errString("before opt-out"), nil, "fp", telemetrymeta.RoutePattern(nil))
		close(captured)
	}()
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("provider call did not start")
	}

	SetPolicyEnabled(false)
	drained := make(chan error, 1)
	go func() { drained <- Drain(context.Background()) }()
	select {
	case err := <-drained:
		t.Fatalf("drain completed while provider call was active: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(transport.release)
	select {
	case <-captured:
	case <-time.After(time.Second):
		t.Fatal("capture did not finish")
	}
	if err := <-drained; err != nil {
		t.Fatalf("Drain: %v", err)
	}

	CaptureHTTPError(context.Background(), errString("after opt-out"), nil, "fp", telemetrymeta.RoutePattern(nil))
	if got := transport.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want only the call entered before opt-out", got)
	}
}

type blockingTransport struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func (t *blockingTransport) Configure(sentry.ClientOptions) {}
func (t *blockingTransport) Flush(time.Duration) bool       { return true }
func (t *blockingTransport) FlushWithContext(context.Context) bool {
	return true
}
func (t *blockingTransport) Close() {}
func (t *blockingTransport) SendEvent(*sentry.Event) {
	t.calls.Add(1)
	t.started <- struct{}{}
	<-t.release
}

type errString string

func (e errString) Error() string { return string(e) }
