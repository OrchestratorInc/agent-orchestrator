package daemon

import (
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

// The daemon keeps its debug detail by default; trace adds routine reads.
func TestLogLevelFromEnvDefaultsToDebug(t *testing.T) {
	cases := map[string]slog.Level{
		"":       slog.LevelDebug,
		"bogus":  slog.LevelDebug,
		"trace":  httpd.LevelTrace,
		"TRACE":  httpd.LevelTrace,
		"debug":  slog.LevelDebug,
		"DEBUG":  slog.LevelDebug,
		" warn ": slog.LevelWarn,
		"error":  slog.LevelError,
		"info":   slog.LevelInfo,
	}
	for value, want := range cases {
		if got := logLevelFromEnv(value); got != want {
			t.Errorf("logLevelFromEnv(%q) = %v, want %v", value, got, want)
		}
	}
}
