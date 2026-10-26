// Package sentryobs is the daemon-side Sentry integration. It captures genuine
// server faults (5xx and panics) with their Go stack, grouped by the same
// telemetrymeta fingerprint the PostHog path uses so an issue lines up across
// both. It is the surface where the daemon's INTERNAL_ERROR 500 root causes
// finally get a stack instead of an opaque count.
//
// It is a no-op until a DSN is configured (AO_SENTRY_DSN), and deny-by-default
// on privacy: no PII, no server name, no request data, no breadcrumbs, and
// dynamic error text and stack paths are removed before send. Transient
// conditions (503 SERVICE_UNAVAILABLE) are deliberately not captured. They are
// retryable contention, not faults, and would drown the signal.
package sentryobs

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

var enabled atomic.Bool
var installID atomic.Pointer[string]

var captureGate = struct {
	sync.Mutex
	policyEnabled bool
	active        int
	waiters       []chan struct{}
}{}

var (
	safeFingerprint = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// New daemon Sentry capture operations and tags must be registered here.
// The values are fixed by AO, never copied from an error or request.
var operationLabels = map[string]string{
	"http_request":       "HTTP server error",
	"http_request_panic": "http handler panic",
}

// Only literal codes used for reportable 5xx API errors belong here. A new
// error code stays out of Sentry until it is deliberately added to this list.
var safeErrorCodes = map[string]struct{}{
	"AGENT_AUTH_DATA_HOME_UNAVAILABLE":          {},
	"AGENT_AUTH_TERMINAL_UNAVAILABLE":           {},
	"AGENT_AUTH_WORKSPACE_UNAVAILABLE":          {},
	"AUTOMATION_CREATE_FAILED":                  {},
	"AUTOMATION_DELETE_FAILED":                  {},
	"AUTOMATION_LIST_FAILED":                    {},
	"AUTOMATION_READ_FAILED":                    {},
	"AUTOMATION_RUN_LIST_FAILED":                {},
	"AUTOMATION_UPDATE_FAILED":                  {},
	"CLONE_PREPARATION_FAILED":                  {},
	"CODEX_ACCOUNT_LOGOUT_UNSUPPORTED":          {},
	"CODEX_ACCOUNT_MANAGEMENT_UNSUPPORTED":      {},
	"CODEX_GLOBAL_CREDENTIAL_STORE_UNSUPPORTED": {},
	"CODEX_RESET_CREDIT_UNSUPPORTED":            {},
	"GITHUB_AUTH_TERMINAL_UNAVAILABLE":          {},
	"INTERNAL":                                  {},
	"INTERNAL_ERROR":                            {},
	"MEMORY_UNSUPPORTED":                        {},
	"MODEL_DISCOVERY_UNAVAILABLE":               {},
	"NOT_IMPLEMENTED":                           {},
	"PROJECTS_LIST_FAILED":                      {},
	"PROJECT_ADD_FAILED":                        {},
	"PROJECT_CONFIG_UPDATE_FAILED":              {},
	"PROJECT_LOAD_FAILED":                       {},
	"PROJECT_PERMISSIONS_UPDATE_FAILED":         {},
	"PROJECT_REMOVE_FAILED":                     {},
	"PROJECT_SETTINGS_UPDATE_FAILED":            {},
	"RUNTIME_CREATE_FAILED":                     {},
	"SHELL_TERMINAL_NO_PROJECT_LOOKUP":          {},
	"SHELL_TERMINAL_NO_SESSION_LOOKUP":          {},
	"SHELL_TERMINAL_NO_SHELL":                   {},
	"SHELL_TERMINAL_NO_WORKING_DIR":             {},
	"SPAWN_BROWSER_CAPABILITY_FAILED":           {},
	"SPAWN_COMMIT_FAILED":                       {},
	"SPAWN_CREATE_FAILED":                       {},
	"SPAWN_INTERNAL":                            {},
	"SPAWN_LAUNCH_COMMAND_FAILED":               {},
	"SPAWN_PREPARE_FAILED":                      {},
	"SPAWN_PREPARE_LAUNCH_FAILED":               {},
	"SPAWN_PROMPT_DELIVERY_FAILED":              {},
	"SPAWN_PROMPT_FAILED":                       {},
	"SPAWN_SUPERVISOR_FAILED":                   {},
	"SPAWN_SYSTEM_PROMPT_FAILED":                {},
	"TERMINAL_FAILED":                           {},
	"WORKSPACE_GIT_READ_FAILED":                 {},
}

// Config configures the daemon Sentry client.
type Config struct {
	DSN         string
	InstallID   string
	Release     string
	Environment string
	SampleRate  float64 // <=0 or >1 defaults to 1.0
	Transport   sentry.Transport
}

// Init initializes the global Sentry client. A blank DSN leaves it a permanent
// no-op. Safe to call once at daemon startup.
func Init(cfg Config) error {
	enabled.Store(false)
	installID.Store(&cfg.InstallID)
	if cfg.DSN == "" {
		return nil
	}
	rate := cfg.SampleRate
	if rate <= 0 || rate > 1 {
		rate = 1.0
	}
	transport := cfg.Transport
	if transport == nil {
		// A synchronous transport leaves no SDK queue that could send after an
		// opt-out acknowledgement. The policy drain below waits for any send
		// already in progress.
		transport = sentry.NewHTTPSyncTransport()
	}
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:           cfg.DSN,
		Release:       cfg.Release,
		Environment:   cfg.Environment,
		EnableTracing: false,
		SampleRate:    rate,
		Transport:     transport,
		// PII is off by default; BeforeSend additionally clears ServerName and
		// Request and replaces title text, so raw error details are not sent.
		// Deny-by-default: scrub the event before it leaves the process.
		BeforeSend: func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			return scrubEvent(event)
		},
		// No automatic breadcrumbs (they can carry URLs/paths).
		BeforeBreadcrumb: func(_ *sentry.Breadcrumb, _ *sentry.BreadcrumbHint) *sentry.Breadcrumb {
			return nil
		},
	}); err != nil {
		return err
	}
	enabled.Store(true)
	return nil
}

// Enabled reports whether Sentry is active (a DSN was configured).
func Enabled() bool { return enabled.Load() }

// SetPolicyEnabled mirrors the crash-durable telemetry authority. Capture
// paths consult it synchronously so a watcher-observed opt-out closes intake
// even when the SDK itself was initialized earlier in the process.
func SetPolicyEnabled(value bool) {
	captureGate.Lock()
	captureGate.policyEnabled = value
	captureGate.Unlock()
}

func enterCapture() bool {
	if !enabled.Load() {
		return false
	}
	captureGate.Lock()
	defer captureGate.Unlock()
	if !captureGate.policyEnabled {
		return false
	}
	captureGate.active++
	return true
}

func leaveCapture() {
	captureGate.Lock()
	captureGate.active--
	if captureGate.active == 0 {
		for _, waiter := range captureGate.waiters {
			close(waiter)
		}
		captureGate.waiters = nil
	}
	captureGate.Unlock()
}

// Drain waits for every provider call that entered before intake closed.
func Drain(ctx context.Context) error {
	captureGate.Lock()
	if captureGate.active == 0 {
		captureGate.Unlock()
		return nil
	}
	done := make(chan struct{})
	captureGate.waiters = append(captureGate.waiters, done)
	captureGate.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func scrubEvent(event *sentry.Event) *sentry.Event {
	// Error strings can contain project names, session IDs, repository paths,
	// provider output, or panic values. Sentry derives issue titles from these
	// fields, so path matching alone cannot make them safe.
	label := "daemon error"
	if known, ok := operationLabels[event.Tags["operation"]]; ok {
		label = known
	}
	if code := event.Tags["error_code"]; safeErrorCode(code) {
		label += " (" + code + ")"
	}
	event.Message = label
	event.ServerName = "" // never leak the machine hostname
	event.Request = nil   // no URLs/headers/cookies
	// Rebuild the SDK environment context from trusted runtime constants. An
	// arbitrary context, including a raw panic stack, never crosses this edge.
	event.Contexts = map[string]sentry.Context{
		"device":  {"arch": runtime.GOARCH},
		"os":      {"name": runtime.GOOS},
		"runtime": {"name": "go", "version": runtime.Version()},
	}
	event.Transaction = ""
	event.Logger = ""
	event.Breadcrumbs = nil
	event.Threads = nil
	event.Modules = nil
	event.Attachments = nil
	event.Spans = nil
	event.DebugMeta = nil
	event.User = sentry.User{}
	for _, fingerprint := range event.Fingerprint {
		if !safeFingerprint.MatchString(fingerprint) {
			event.Fingerprint = nil
			break
		}
	}
	for key, value := range event.Tags {
		if !safeSentryTag(key, value) {
			delete(event.Tags, key)
		}
	}
	for i := range event.Exception {
		event.Exception[i].Type = label
		event.Exception[i].Value = ""
		event.Exception[i].Module = ""
		event.Exception[i].Mechanism = nil
		if st := event.Exception[i].Stacktrace; st != nil {
			for j := range st.Frames {
				frame := st.Frames[j]
				st.Frames[j] = sentry.Frame{
					Function: frame.Function,
					Module:   frame.Module,
					Lineno:   frame.Lineno,
					Colno:    frame.Colno,
					InApp:    frame.InApp,
				}
			}
		}
	}
	return event
}

func safeSentryTag(key, value string) bool {
	switch key {
	case "platform":
		return value == "daemon"
	case "component":
		return value == "httpd"
	case "operation":
		_, ok := operationLabels[value]
		return ok
	case "path":
		// applyTags replaces caller-supplied paths with a SafeRoute before send.
		return value != ""
	case "method":
		switch value {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
			return true
		}
	case "status":
		status, err := strconv.Atoi(value)
		return err == nil && ShouldCaptureStatus(status)
	case "category":
		return value == "http_5xx"
	case "error_kind":
		return telemetrymeta.IsErrorKind(value)
	case "panic_kind":
		return telemetrymeta.IsPanicKind(value)
	case "error_code":
		return safeErrorCode(value)
	case "install_id":
		id := installID.Load()
		return id != nil && value != "" && value == *id
	}
	return false
}

func safeErrorCode(code string) bool {
	_, ok := safeErrorCodes[code]
	return ok
}

// ShouldCaptureStatus reports whether an HTTP status is a genuine server fault
// worth an issue. 5xx qualifies, except 503 (transient/retryable contention).
func ShouldCaptureStatus(status int) bool {
	return status >= http.StatusInternalServerError && status != http.StatusServiceUnavailable
}

// CaptureHTTPError captures a server-fault error with the given tags and a
// fingerprint identical to the PostHog grouping key. No-op when disabled or the
// error is nil.
func CaptureHTTPError(ctx context.Context, err error, tags map[string]string, fingerprint string, route telemetrymeta.SafeRoute) {
	if err == nil || ctx.Err() != nil || !enterCapture() {
		return
	}
	defer leaveCapture()
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentry.LevelError)
		applyTags(scope, tags, route)
		if fingerprint != "" {
			scope.SetFingerprint([]string{fingerprint})
		}
		sentry.CaptureException(err)
	})
}

// CapturePanic captures a recovered panic at fatal level. No-op when disabled.
func CapturePanic(ctx context.Context, recovered any, tags map[string]string, fingerprint string, route telemetrymeta.SafeRoute) {
	if ctx.Err() != nil || !enterCapture() {
		return
	}
	defer leaveCapture()
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentry.LevelFatal)
		applyTags(scope, tags, route)
		if fingerprint != "" {
			scope.SetFingerprint([]string{fingerprint})
		}
		sentry.CaptureException(fmt.Errorf("panic: %v", recovered))
	})
}

func applyTags(scope *sentry.Scope, tags map[string]string, route telemetrymeta.SafeRoute) {
	scope.SetTag("platform", "daemon")
	for k, v := range tags {
		if k == "install_id" || k == "path" {
			continue
		}
		if v != "" {
			scope.SetTag(k, v)
		}
	}
	scope.SetTag("path", route.String())
	if id := installID.Load(); id != nil && *id != "" {
		scope.SetTag("install_id", *id)
	}
}

// Flush waits up to timeout for buffered events to send. Call on shutdown.
func Flush(timeout time.Duration) {
	if enabled.Load() {
		sentry.Flush(timeout)
	}
}
