package telemetrymeta

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// ErrorKindAndCode extracts a telemetry-safe error category and optional code.
func ErrorKindAndCode(err error) (kind, code string) {
	kind = "internal"
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		return ErrorKind(apiErr.Kind), apiErr.Code
	}
	return kind, ""
}

var errorKinds = map[apierr.Kind]string{
	apierr.KindInternal:        "internal",
	apierr.KindInvalid:         "invalid",
	apierr.KindNotFound:        "not_found",
	apierr.KindConflict:        "conflict",
	apierr.KindForbidden:       "forbidden",
	apierr.KindTooManyRequests: "too_many_requests",
	apierr.KindNotImplemented:  "not_implemented",
	apierr.KindUnavailable:     "unavailable",
}

// ErrorKind maps API error kinds to coarse telemetry-safe categories.
func ErrorKind(kind apierr.Kind) string {
	if value, ok := errorKinds[kind]; ok {
		return value
	}
	return "internal"
}

// IsErrorKind accepts only the categories produced by ErrorKind.
func IsErrorKind(kind string) bool {
	for _, value := range errorKinds {
		if kind == value {
			return true
		}
	}
	return false
}

// PanicKind classifies panic payloads without exporting their raw contents.
func PanicKind(rec any) string {
	switch rec.(type) {
	case error:
		return "error"
	case string:
		return "string"
	default:
		return "other"
	}
}

// IsPanicKind accepts only the categories produced by PanicKind.
func IsPanicKind(kind string) bool {
	return kind == "error" || kind == "string" || kind == "other"
}

// StatusFamily returns a telemetry-friendly HTTP status bucket like 5xx.
func StatusFamily(status int) string {
	if status < 100 || status > 999 {
		return "unknown"
	}
	return fmt.Sprintf("%dxx", status/100)
}

// SafeRoute is a registered route template or a fixed fallback. Its value cannot
// be built from a request URL path, which may contain user-provided names.
type SafeRoute struct{ pattern string }

// FailureSite is a function name obtained from the running AO binary.
// Request and error strings cannot construct one.
type FailureSite struct {
	function string
	line     int
}

func (site FailureSite) String() string { return site.function }

// Line returns the source line in the running AO binary.
func (site FailureSite) Line() int { return site.line }

// CurrentFailureSite finds the first application frame outside observability
// wrappers. For an HTTP error this is the controller recording the failure;
// during panic recovery it is the interrupted handler frame.
func CurrentFailureSite() FailureSite {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		name := frame.Function
		if strings.Contains(name, "github.com/aoagents/agent-orchestrator/backend/internal/") &&
			!strings.Contains(name, "/telemetrymeta.") &&
			!strings.Contains(name, "/observe/sentryobs.RecoverBackground") &&
			!strings.Contains(name, "/httpd/envelope.") &&
			!strings.Contains(name, "/httpd.recoverTelemetry") {
			return FailureSite{function: name, line: frame.Line}
		}
		if !more {
			return FailureSite{}
		}
	}
}

func (route SafeRoute) String() string {
	if route.pattern == "" {
		return "unmatched"
	}
	return route.pattern
}

// RoutePattern returns the chi route template when available, else "unmatched".
func RoutePattern(r *http.Request) SafeRoute {
	if r == nil {
		return SafeRoute{}
	}
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if pattern := strings.TrimSpace(rc.RoutePattern()); pattern != "" {
			return SafeRoute{pattern: pattern}
		}
	}
	return SafeRoute{}
}

// Fingerprint returns a short stable digest for grouping similar failures.
func Fingerprint(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if len(sum) > 16 {
		return sum[:16]
	}
	return sum
}
