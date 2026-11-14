package sentryobs

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"runtime/debug"
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

var sampled atomic.Bool
var release atomic.Pointer[string]
var environment atomic.Pointer[string]
var approvedRoutes sync.Map
var errorBudget = struct {
	sync.Mutex
	day    int
	total  int
	groups map[string]errorBudgetGroup
}{groups: make(map[string]errorBudgetGroup)}

const (
	maxErrorsPerFingerprintMinute = 5
	maxErrorsPerFingerprintDay    = 10
	maxErrorFingerprintsPerDay    = 1024
	maxErrorsPerInstallDay        = 25
	errorSampleBuckets            = 10
)

const (
	sampledErrorRate = "0.1"
	fatalErrorRate   = "1"
)

func InstallInErrorSample(id string) bool {
	if id == "" {
		return false
	}
	sum := sha256.Sum256([]byte(id))
	return binary.BigEndian.Uint32(sum[:4])%errorSampleBuckets == 0
}

type errorBudgetGroup struct {
	minute      int64
	minuteCount int
	dayCount    int
	dropped     int
}

var captureGate = struct {
	sync.Mutex
	policyCheck   func(context.Context) bool
	policyVersion uint64
	active        int
	waiters       []chan struct{}
}{}

var (
	safeFingerprint = regexp.MustCompile(`^[0-9a-f]{16}$`)
	safeRelease     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,79}$`)
	safeRoute       = regexp.MustCompile(`^/[A-Za-z0-9_./{}*:-]+$`)
)

var operationLabels = map[string]string{
	"http_request":       "HTTP server error",
	"http_request_panic": "http handler panic",
	"resume_agent":       "Agent resume failed",
	"restore_session":    "Session restore failed",
	"background_panic":   "daemon background panic",
}

var areaLabels = map[string]string{
	"git":     "Git operation failed",
	"db":      "Database operation failed",
	"agent":   "Agent operation failed",
	"network": "Network operation failed",
}

var relaunchSteps = map[string]struct{}{
	"agent adapter": {}, "system prompt": {}, "system prompt file": {},
	"browser capability": {}, "model configuration": {}, "agent hooks": {},
	"launch command": {}, "after-start prompt": {}, "agent binary": {},
	"supervisor": {}, "runtime preparation": {}, "prepare launch": {},
	"controller admission": {}, "runtime": {}, "agent start check": {},
	"record launch": {}, "native initialization": {}, "deliver prompt": {},
	"workspace roots": {}, "provider ownership": {}, "chat start": {},
}

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
	"REPORT_CREATE_FAILED":                      {},
	"REPORT_LIST_FAILED":                        {},
	"REVIEW_OPERATION_FAILED":                   {},
	"CHAT_DELIVERY_FAILED":                      {},
	"CONVERSATION_READ_FAILED":                  {},
	"ORCHESTRATOR_SPAWN_FAILED":                 {},
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

type Config struct {
	DSN         string
	InstallID   string
	Release     string
	Environment string
	SampleRate  float64
	Transport   sentry.Transport
}

func Init(cfg Config) error {
	enabled.Store(false)
	errorBudget.Lock()
	errorBudget.day = 0
	errorBudget.total = 0
	errorBudget.groups = make(map[string]errorBudgetGroup)
	errorBudget.Unlock()
	installID.Store(&cfg.InstallID)
	sampled.Store(InstallInErrorSample(cfg.InstallID))
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
		transport = sentry.NewHTTPSyncTransport()
	}
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:           cfg.DSN,
		Release:       version,
		Environment:   channel,
		EnableTracing: false,
		SampleRate:    rate,
		Transport:     transport,
		BeforeSend: func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			return scrubEvent(event)
		},
		BeforeBreadcrumb: func(_ *sentry.Breadcrumb, _ *sentry.BreadcrumbHint) *sentry.Breadcrumb {
			return nil
		},
	}); err != nil {
		return err
	}
	enabled.Store(true)
	return nil
}

func Enabled() bool { return enabled.Load() }

func SetPolicyCheck(check func(context.Context) bool) {
	captureGate.Lock()
	captureGate.policyCheck = check
	captureGate.policyVersion++
	captureGate.Unlock()
}

func enterCapture(ctx context.Context) bool {
	if !enabled.Load() || ctx.Err() != nil {
		return false
	}
	captureGate.Lock()
	check, version := captureGate.policyCheck, captureGate.policyVersion
	captureGate.Unlock()
	if check == nil || !check(ctx) {
		return false
	}
	captureGate.Lock()
	defer captureGate.Unlock()
	if version != captureGate.policyVersion || !enabled.Load() || ctx.Err() != nil {
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
	} else if typ := event.Tags["error_type"]; safeErrorType(typ) && typ != "unknown" {
		label += " (" + typ + ")"
	}
	event.Message = label
	event.ServerName = ""
	event.Request = nil
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
		return value == "httpd" || value == "session_service" || value == "daemon" || isBackgroundComponent(value)
	case "operation":
		_, ok := operationLabels[value]
		return ok
	case "path":
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
		return err == nil && status >= http.StatusBadRequest && status <= 599
	case "category":
		return value == "http_5xx" || value == "http_fault" || value == "http_timeout"
	case "area":
		_, ok := areaLabels[value]
		return ok
	case "step":
		_, ok := relaunchSteps[value]
		return ok
	case "error_kind":
		return telemetrymeta.IsErrorKind(value)
	case "error_type":
		return safeErrorType(value)
	case "panic_kind":
		return telemetrymeta.IsPanicKind(value)
	case "error_code":
		return safeErrorCode(value)
	case "sample_rate":
		return value == sampledErrorRate || value == fatalErrorRate
	case "install_id":
		id := installID.Load()
		return id != nil && value != "" && value == *id
	case "dropped_since_last":
		n, err := strconv.Atoi(value)
		return err == nil && n > 0
	case "failed_step":
		_, ok := spawnFailedSteps[value]
		return ok
	case "cause":
		_, ok := spawnFailureCauses[value]
		return ok
	}
	if ok, handled := safeBackgroundTag(key, value); handled {
		return ok
	}
	return false
}

func safeErrorCode(code string) bool {
	_, ok := safeErrorCodes[code]
	return ok
}

func safeErrorType(value string) bool {
	switch value {
	case "unknown", "context_deadline", "context_canceled", "sqlite", "exec_exit", "filesystem", "network", "api_error":
		return true
	}
	return false
}

func ShouldCaptureStatus(status int) bool {
	return status >= http.StatusInternalServerError && status != http.StatusServiceUnavailable
}

func CaptureError(ctx context.Context, err error, tags map[string]string, fingerprint string, route telemetrymeta.SafeRoute, site telemetrymeta.FailureSite) {
	captureErrorFor(ctx, err, withErrorTags(tags, err), fingerprint, route, site, tags["operation"])
}

func withErrorTags(tags map[string]string, err error) map[string]string {
	var tagged interface{ SentryTags() map[string]string }
	if err == nil || !errors.As(err, &tagged) {
		return tags
	}
	out := make(map[string]string, len(tags)+2)
	for k, v := range tags {
		out[k] = v
	}
	for k, v := range tagged.SentryTags() {
		if _, taken := out[k]; !taken {
			out[k] = v
		}
	}
	return out
}

func CaptureErrorAsync(ctx context.Context, err error, tags map[string]string, fingerprint string, route telemetrymeta.SafeRoute, site telemetrymeta.FailureSite) {
	if err == nil || errors.Is(err, context.Canceled) || IsRepeat(err) {
		return
	}
	captureAsync(ctx, withErrorTags(tags, err), fingerprint, route, site, tags["operation"])
}

func captureErrorFor(ctx context.Context, err error, tags map[string]string, fingerprint string, route telemetrymeta.SafeRoute, site telemetrymeta.FailureSite, operation string) {
	if err == nil || errors.Is(err, context.Canceled) || IsRepeat(err) || ctx.Err() != nil || !sampled.Load() || !enterCapture(ctx) {
		return
	}
	defer leaveCapture()
	ok, dropped := admitErrorFingerprint(fingerprint, operation, time.Now().UTC())
	if !ok {
		return
	}
	capture(sentry.LevelError, withDropped(tags, dropped), fingerprint, route, site, false)
}

func withDropped(tags map[string]string, dropped int) map[string]string {
	if dropped <= 0 {
		return tags
	}
	out := make(map[string]string, len(tags)+1)
	for k, v := range tags {
		out[k] = v
	}
	out["dropped_since_last"] = strconv.Itoa(dropped)
	return out
}

func allowErrorFingerprintFor(fingerprint, operation string, now time.Time) bool {
	ok, _ := admitErrorFingerprint(fingerprint, operation, now)
	return ok
}

func admitErrorFingerprint(fingerprint, operation string, now time.Time) (bool, int) {
	overflow := "unknown"
	if _, ok := operationLabels[operation]; ok {
		overflow = "overflow:" + operation
	}
	if !safeFingerprint.MatchString(fingerprint) {
		fingerprint = overflow
	}
	errorBudget.Lock()
	defer errorBudget.Unlock()
	day := now.YearDay() + now.Year()*366
	if errorBudget.day != day {
		groups := make(map[string]errorBudgetGroup)
		for key, group := range errorBudget.groups {
			if group.dropped > 0 {
				groups[key] = errorBudgetGroup{dropped: group.dropped}
			}
		}
		errorBudget.day = day
		errorBudget.total = 0
		errorBudget.groups = groups
	}
	group, known := errorBudget.groups[fingerprint]
	if !known && len(errorBudget.groups) >= maxErrorFingerprintsPerDay {
		fingerprint = overflow
		group = errorBudget.groups[fingerprint]
	}
	minute := now.Unix() / 60
	if group.minute != minute {
		group.minute = minute
		group.minuteCount = 0
	}
	if errorBudget.total >= maxErrorsPerInstallDay || group.minuteCount >= maxErrorsPerFingerprintMinute || group.dayCount >= maxErrorsPerFingerprintDay {
		group.dropped++
		errorBudget.groups[fingerprint] = group
		return false, 0
	}
	group.minuteCount++
	group.dayCount++
	errorBudget.total++
	dropped := group.dropped
	group.dropped = 0
	errorBudget.groups[fingerprint] = group
	return true, dropped
}

func CapturePanic(ctx context.Context, tags map[string]string, fingerprint string, route telemetrymeta.SafeRoute, site telemetrymeta.FailureSite) {
	if ctx.Err() != nil || !enterCapture(ctx) {
		return
	}
	defer leaveCapture()
	capture(sentry.LevelFatal, tags, fingerprint, route, site, true)
}

func RecoverBackground() {
	rec := recover()
	if rec == nil {
		return
	}
	site := telemetrymeta.CurrentFailureSite()
	kind := telemetrymeta.PanicKind(rec)
	log.Printf("daemon background panic: %v\n%s", rec, debug.Stack())
	CapturePanic(context.Background(), map[string]string{
		"component":  "daemon",
		"operation":  "background_panic",
		"panic_kind": kind,
	}, telemetrymeta.Fingerprint("daemon", "background_panic", site.String(), kind), telemetrymeta.SafeRoute{}, site)
	panic(rec)
}

func capture(level sentry.Level, tags map[string]string, fingerprint string, route telemetrymeta.SafeRoute, site telemetrymeta.FailureSite, isPanic bool) {
	hub := sentry.CurrentHub().Clone()
	hub.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(level)
		applyTags(scope, level, tags, route)
		if fingerprint != "" {
			scope.SetFingerprint([]string{fingerprint})
		}
		event := sentry.NewEvent()
		event.Level = level
		if isPanic {
			event.Exception = []sentry.Exception{{Stacktrace: stackAtSite(site, tags["operation"])}}
		} else {
			event.Exception = []sentry.Exception{{}}
		}
		hub.CaptureEvent(event)
	})
}

func stackAtSite(site telemetrymeta.FailureSite, operation string) *sentry.Stacktrace {
	if site.String() != "" {
		return &sentry.Stacktrace{Frames: []sentry.Frame{{Function: site.String(), Lineno: site.Line(), InApp: true}}}
	}
	if _, ok := operationLabels[operation]; !ok {
		operation = "unknown"
	}
	return &sentry.Stacktrace{Frames: []sentry.Frame{{Function: "ao.daemon." + operation, InApp: true}}}
}

func applyTags(scope *sentry.Scope, level sentry.Level, tags map[string]string, route telemetrymeta.SafeRoute) {
	scope.SetTag("platform", "daemon")
	if level == sentry.LevelFatal {
		scope.SetTag("sample_rate", fatalErrorRate)
	} else {
		scope.SetTag("sample_rate", sampledErrorRate)
	}
	for k, v := range tags {
		if k == "install_id" || k == "path" || k == "area" || k == "sample_rate" {
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

func Flush(timeout time.Duration) {
	if enabled.Load() {
		sentry.Flush(timeout)
	}
}
