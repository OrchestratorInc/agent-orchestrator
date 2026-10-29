package telemetrymeta

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
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

// SQLite primary result codes for a busy/locked database. Extended codes carry
// these in their low byte. The modernc.org/sqlite error is matched structurally
// by its Code() method so this package does not depend on the driver.
const (
	sqliteBusy   = 5
	sqliteLocked = 6
)

// TransientCause names why err is a retryable contention/timeout condition
// (sqlite_busy, sqlite_locked or deadline), or "" when it is not. Context
// cancellation is excluded: the client went away and a retry would not help.
func TransientCause(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	var coder interface{ Code() int }
	if errors.As(err, &coder) {
		switch coder.Code() & 0xFF {
		case sqliteBusy:
			return "sqlite_busy"
		case sqliteLocked:
			return "sqlite_locked"
		}
	}
	return ""
}

// ErrorKind maps API error kinds to coarse telemetry-safe categories.
func ErrorKind(kind apierr.Kind) string {
	switch kind {
	case apierr.KindInvalid:
		return "invalid"
	case apierr.KindNotFound:
		return "not_found"
	case apierr.KindConflict:
		return "conflict"
	case apierr.KindForbidden:
		return "forbidden"
	case apierr.KindTooManyRequests:
		return "too_many_requests"
	case apierr.KindNotImplemented:
		return "not_implemented"
	case apierr.KindUnavailable:
		return "unavailable"
	default:
		return "internal"
	}
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

// StatusFamily returns a telemetry-friendly HTTP status bucket like 5xx.
func StatusFamily(status int) string {
	if status < 100 || status > 999 {
		return "unknown"
	}
	return fmt.Sprintf("%dxx", status/100)
}

// RoutePattern returns the chi route template when available, else the URL path.
func RoutePattern(r *http.Request) string {
	if r == nil {
		return ""
	}
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if pattern := strings.TrimSpace(rc.RoutePattern()); pattern != "" {
			return pattern
		}
	}
	if r.URL == nil {
		return ""
	}
	return r.URL.Path
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
