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
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
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
	safeErrorCode   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,79}$`)
	safeFingerprint = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

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
	switch event.Tags["operation"] {
	case "http_request_panic":
		label = "http handler panic"
	case "http_request":
		label = "HTTP server error"
	}
	if code := event.Tags["error_code"]; safeErrorCode.MatchString(code) {
		label += " (" + code + ")"
	}
	event.Message = label
	event.ServerName = "" // never leak the machine hostname
	event.Request = nil   // no URLs/headers/cookies
	event.Contexts = nil
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
		event.Exception[i].Value = label
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
		return value == "http_request" || value == "http_request_panic"
	case "method":
		switch value {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
			return true
		}
	case "status":
		return len(value) == 3 && value[0] == '5' && value != "503" && value[1] >= '0' && value[1] <= '9' && value[2] >= '0' && value[2] <= '9'
	case "category":
		return value == "http_5xx"
	case "error_kind":
		switch value {
		case "internal", "invalid", "not_found", "conflict", "forbidden", "too_many_requests", "not_implemented", "unavailable":
			return true
		}
	case "panic_kind":
		return value == "error" || value == "string" || value == "other"
	case "error_code":
		return safeErrorCode.MatchString(value)
	case "install_id":
		id := installID.Load()
		return id != nil && value != "" && value == *id
	}
	return false
}

// ShouldCaptureStatus reports whether an HTTP status is a genuine server fault
// worth an issue. 5xx qualifies, except 503 (transient/retryable contention).
func ShouldCaptureStatus(status int) bool {
	return status >= http.StatusInternalServerError && status != http.StatusServiceUnavailable
}

// CaptureHTTPError captures a server-fault error with the given tags and a
// fingerprint identical to the PostHog grouping key. No-op when disabled or the
// error is nil.
func CaptureHTTPError(ctx context.Context, err error, tags map[string]string, fingerprint string) {
	if err == nil || ctx.Err() != nil || !enterCapture() {
		return
	}
	defer leaveCapture()
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentry.LevelError)
		applyTags(scope, tags)
		if fingerprint != "" {
			scope.SetFingerprint([]string{fingerprint})
		}
		sentry.CaptureException(err)
	})
}

// CapturePanic captures a recovered panic at fatal level. No-op when disabled.
func CapturePanic(ctx context.Context, recovered any, _ string, tags map[string]string, fingerprint string) {
	if ctx.Err() != nil || !enterCapture() {
		return
	}
	defer leaveCapture()
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentry.LevelFatal)
		applyTags(scope, tags)
		if fingerprint != "" {
			scope.SetFingerprint([]string{fingerprint})
		}
		sentry.CaptureException(fmt.Errorf("panic: %v", recovered))
	})
}

func applyTags(scope *sentry.Scope, tags map[string]string) {
	scope.SetTag("platform", "daemon")
	for k, v := range tags {
		if k == "install_id" {
			continue
		}
		if v != "" {
			scope.SetTag(k, v)
		}
	}
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
