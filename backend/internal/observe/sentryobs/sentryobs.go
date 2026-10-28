// Package sentryobs is the daemon-side Sentry integration. It captures genuine
// server faults (5xx and panics) with their Go stack, grouped by the AO
// function that recorded the failure when that site is available. It is the
// surface where the daemon's INTERNAL_ERROR 500 root causes
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
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	sqlite3 "modernc.org/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

var enabled atomic.Bool
var installID atomic.Pointer[string]
var release atomic.Pointer[string]
var environment atomic.Pointer[string]
var approvedRoutes sync.Map

var captureGate = struct {
	sync.Mutex
	policyEnabled bool
	active        int
	waiters       []chan struct{}
}{}

var (
	safeFingerprint = regexp.MustCompile(`^[0-9a-f]{16}$`)
	safeRelease     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,79}$`)
	safeRoute       = regexp.MustCompile(`^/[A-Za-z0-9_./{}*:-]+$`)
)

// New daemon Sentry capture operations and tags must be registered here.
// The values are fixed by AO, never copied from an error or request.
var operationLabels = map[string]string{
	"http_request":       "HTTP server error",
	"http_request_panic": "http handler panic",
	"resume_agent":       "Agent resume failed",
	"restore_session":    "Session restore failed",
}

var areaLabels = map[string]string{
	"git":     "Git operation failed",
	"db":      "Database operation failed",
	"agent":   "Agent operation failed",
	"network": "Network operation failed",
}

// These are the fixed relaunch steps produced by session_manager.Manager.
var relaunchSteps = map[string]struct{}{
	"agent adapter": {}, "system prompt": {}, "system prompt file": {},
	"browser capability": {}, "model configuration": {}, "agent hooks": {},
	"launch command": {}, "after-start prompt": {}, "agent binary": {},
	"supervisor": {}, "runtime preparation": {}, "prepare launch": {},
	"controller admission": {}, "runtime": {}, "agent start check": {},
	"record launch": {}, "native initialization": {}, "deliver prompt": {},
}

// Only literal codes used for reportable daemon errors belong here. A new
// error code stays out of Sentry until it is deliberately added to this list.
var safeErrorCodes = map[string]struct{}{
	"AGENT_RESUME_FAILED":                       {},
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
	version := cfg.Release
	if !safeRelease.MatchString(version) {
		version = "unknown"
	}
	channel := cfg.Environment
	switch channel {
	case "stable", "nightly", "development":
	default:
		channel = "unknown"
	}
	release.Store(&version)
	environment.Store(&channel)
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
		Release:       version,
		Environment:   channel,
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
	if event.Tags["operation"] == "http_request" {
		if areaLabel, ok := areaLabels[event.Tags["area"]]; ok {
			label = areaLabel
		}
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
	if id := installID.Load(); id != nil && *id != "" {
		event.User.ID = *id
	}
	if v := release.Load(); v != nil {
		event.Release = *v
	}
	if v := environment.Load(); v != nil {
		event.Environment = *v
	}
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
		return value == "httpd" || value == "session_service"
	case "operation":
		_, ok := operationLabels[value]
		return ok
	case "path":
		// Only route templates supplied by RoutePattern may pass this final edge.
		if value == "unmatched" {
			return true
		}
		_, approved := approvedRoutes.Load(value)
		return approved && safeRoute.MatchString(value)
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
	case "area":
		_, ok := areaLabels[value]
		return ok
	case "step":
		_, ok := relaunchSteps[value]
		return ok
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

// CaptureError captures a reportable daemon error with safe tags and a stable
// AO failure-site fingerprint when available. No-op when disabled or canceled.
func CaptureError(ctx context.Context, err error, tags map[string]string, fingerprint string, route telemetrymeta.SafeRoute, site telemetrymeta.FailureSite) {
	if err == nil || errors.Is(err, context.Canceled) || ctx.Err() != nil || !enterCapture() {
		return
	}
	defer leaveCapture()
	capture(sentry.LevelError, tags, fingerprint, route, site)
}

// CapturePanic captures a recovered panic at fatal level. No-op when disabled.
func CapturePanic(ctx context.Context, tags map[string]string, fingerprint string, route telemetrymeta.SafeRoute, site telemetrymeta.FailureSite) {
	if ctx.Err() != nil || !enterCapture() {
		return
	}
	defer leaveCapture()
	capture(sentry.LevelFatal, tags, fingerprint, route, site)
}

func capture(level sentry.Level, tags map[string]string, fingerprint string, route telemetrymeta.SafeRoute, site telemetrymeta.FailureSite) {
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(level)
		applyTags(scope, tags, route)
		group := fingerprint
		if site.String() != "" {
			parts := []string{"sentry", tags["operation"], site.String()}
			if level == sentry.LevelError {
				parts = append(parts, tags["error_code"])
			}
			group = telemetrymeta.Fingerprint(parts...)
		}
		if group != "" {
			scope.SetFingerprint([]string{group})
		}
		event := sentry.NewEvent()
		event.Level = level
		event.Exception = []sentry.Exception{{Stacktrace: stackAtSite(site)}}
		sentry.CaptureEvent(event)
	})
}

func stackAtSite(site telemetrymeta.FailureSite) *sentry.Stacktrace {
	if site.String() != "" {
		return &sentry.Stacktrace{Frames: []sentry.Frame{{Function: site.String(), Lineno: site.Line(), InApp: true}}}
	}
	return sentry.NewStacktrace()
}

func applyTags(scope *sentry.Scope, tags map[string]string, route telemetrymeta.SafeRoute) {
	scope.SetTag("platform", "daemon")
	for k, v := range tags {
		if k == "install_id" || k == "path" || k == "area" {
			continue
		}
		if v != "" {
			scope.SetTag(k, v)
		}
	}
	if tags["operation"] == "http_request" || tags["operation"] == "http_request_panic" {
		path := route.String()
		if path != "unmatched" && safeRoute.MatchString(path) {
			approvedRoutes.Store(path, struct{}{})
		}
		scope.SetTag("path", path)
	}
	if area := tags["area"]; safeSentryTag("area", area) {
		scope.SetTag("area", area)
	}
	if id := installID.Load(); id != nil && *id != "" {
		scope.SetTag("install_id", *id)
	}
}

// Area classifies only errors with concrete type or code evidence.
func Area(err error, code string) string {
	switch {
	case code == "WORKSPACE_GIT_READ_FAILED":
		return "git"
	case strings.HasPrefix(code, "AGENT_AUTH_"), strings.HasPrefix(code, "RUNTIME_"), strings.HasPrefix(code, "SPAWN_"):
		return "agent"
	}
	var sqliteErr *sqlite3.Error
	if errors.As(err, &sqliteErr) {
		return "db"
	}
	var opErr *net.OpError
	var dnsErr *net.DNSError
	var urlErr *url.Error
	if errors.As(err, &opErr) || errors.As(err, &dnsErr) || errors.As(err, &urlErr) {
		return "network"
	}
	return ""
}

// Flush waits up to timeout for buffered events to send. Call on shutdown.
func Flush(timeout time.Duration) {
	if enabled.Load() {
		sentry.Flush(timeout)
	}
}
