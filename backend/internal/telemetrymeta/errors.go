package telemetrymeta

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	sqlite3 "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func ErrorKindAndCode(err error) (kind, code string) {
	kind = "internal"
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		return ErrorKind(apiErr.Kind), apiErr.Code
	}
	return kind, ""
}

func ErrorType(err error) string {
	if err == nil {
		return "unknown"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context_deadline"
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	var sqliteErr *sqlite3.Error
	if errors.As(err, &sqliteErr) {
		return "sqlite"
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return "exec_exit"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return "filesystem"
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return "network"
	}
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		if apiErr.Cause != nil {
			return "unknown"
		}
		return "api_error"
	}
	return "unknown"
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

func ErrorKind(kind apierr.Kind) string {
	if value, ok := errorKinds[kind]; ok {
		return value
	}
	return "internal"
}

func IsErrorKind(kind string) bool {
	for _, value := range errorKinds {
		if kind == value {
			return true
		}
	}
	return false
}

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

func IsPanicKind(kind string) bool {
	return kind == "error" || kind == "string" || kind == "other"
}

func StatusFamily(status int) string {
	if status < 100 || status > 999 {
		return "unknown"
	}
	return fmt.Sprintf("%dxx", status/100)
}

type SafeRoute struct{ pattern string }

type FailureSite struct {
	function string
	line     int
}

func (site FailureSite) String() string { return site.function }

func (site FailureSite) Line() int { return site.line }

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
