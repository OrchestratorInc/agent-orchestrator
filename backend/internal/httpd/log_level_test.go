package httpd

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Polls from the desktop app are fast, successful reads; at the daemon's default
// debug level they must not produce a line each, while anything worth a look
// still logs at info.
func TestRequestLoggerKeepsRoutineReadsAtTrace(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		status   int
		delay    time.Duration
		wantLine bool
	}{
		{name: "fast successful read", method: http.MethodGet, status: http.StatusOK},
		{name: "not modified read", method: http.MethodGet, status: http.StatusNotModified},
		{name: "preflight", method: http.MethodOptions, status: http.StatusNoContent},
		{name: "write", method: http.MethodPost, status: http.StatusOK, wantLine: true},
		{name: "client error", method: http.MethodGet, status: http.StatusNotFound, wantLine: true},
		{name: "slow read", method: http.MethodGet, status: http.StatusOK, delay: routineRequestMaxDuration + 50*time.Millisecond, wantLine: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			handler := requestLogger(log, &captureSink{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(tc.delay)
				w.WriteHeader(tc.status)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, "/api/v1/awake", nil))
			if got := strings.Contains(buf.String(), `msg="http request"`); got != tc.wantLine {
				t.Fatalf("logged at debug level = %v, want %v; log: %q", got, tc.wantLine, buf.String())
			}
			if tc.wantLine && !strings.Contains(buf.String(), "level=INFO") {
				t.Fatalf("a request worth a look logged below info: %q", buf.String())
			}
		})
	}
}
