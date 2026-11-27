package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

func TestTelemetryLocalRoutes(t *testing.T) {
	var optedOut bool
	var recorded []telemetryadapter.ExportedEvent
	var listLimit int
	var listSince time.Time
	var listEvent string
	local := &TelemetryLocal{
		Status: func(context.Context) TelemetryStatus { return TelemetryStatus{OptedOut: optedOut, RetentionDays: 30} },
		List: func(_ context.Context, since time.Time, limit int, event string) ([]telemetryadapter.ExportedEvent, error) {
			listSince, listLimit, listEvent = since, limit, event
			return []telemetryadapter.ExportedEvent{{Event: "ao.app.active", Surface: "daemon", Properties: map[string]any{"client": "cli"}}}, nil
		},
		Record:      func(_ context.Context, ev telemetryadapter.ExportedEvent) { recorded = append(recorded, ev) },
		SetOptedOut: func(_ context.Context, v bool) error { optedOut = v; return nil },
	}
	r := NewRouterWithControl(config.Config{DataDir: t.TempDir()}, discardLogger(), nil, APIDeps{TelemetryLocal: local}, ControlDeps{})
	do := func(method, target, origin, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1:3001"+target, strings.NewReader(body))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	for _, route := range []struct{ method, path string }{{http.MethodGet, "/internal/telemetry/status"}, {http.MethodGet, "/internal/telemetry/log"}, {http.MethodPost, "/internal/telemetry/opt-out"}, {http.MethodPost, "/internal/telemetry/exported"}} {
		if rec := do(route.method, route.path, "app://renderer", `{}`); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s from a browser origin = %d, want 403", route.method, route.path, rec.Code)
		}
	}

	if rec := do(http.MethodPost, "/internal/telemetry/opt-out", "", `{"optedOut":true}`); rec.Code != http.StatusOK || !optedOut || !strings.Contains(rec.Body.String(), `"optedOut":true`) {
		t.Fatalf("opt-out = %d %s", rec.Code, rec.Body)
	}
	if rec := do(http.MethodPost, "/internal/telemetry/opt-out", "", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("opt-out without a value = %d, want 400", rec.Code)
	}

	rec := do(http.MethodGet, "/internal/telemetry/log?limit=5&since=2026-11-01T00:00:00Z&event=ao.app.active", "", "")
	if rec.Code != http.StatusOK || listLimit != 5 || listEvent != "ao.app.active" || !listSince.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("log = %d limit=%d since=%s event=%q", rec.Code, listLimit, listSince, listEvent)
	}
	var body struct {
		Events []telemetryadapter.ExportedEvent `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Events) != 1 || body.Events[0].Properties["client"] != "cli" {
		t.Fatalf("log body = %s (%v)", rec.Body, err)
	}
	for _, bad := range []string{"?limit=0", "?limit=5000", "?since=yesterday", "?event=has%20space"} {
		if rec := do(http.MethodGet, "/internal/telemetry/log"+bad, "", ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("log%s = %d, want 400", bad, rec.Code)
		}
	}

	if rec := do(http.MethodPost, "/internal/telemetry/exported", "", `{"event":"ao.routes.viewed","distinctId":"ins_x","properties":{"surface":"board"}}`); rec.Code != http.StatusNoContent {
		t.Fatalf("exported = %d %s", rec.Code, rec.Body)
	}
	if len(recorded) != 1 || recorded[0].Surface != "desktop" || recorded[0].Properties["surface"] != "board" {
		t.Fatalf("recorded = %#v", recorded)
	}
	for _, bad := range []string{`{"event":"has space"}`, `{"event":"ok","extra":1}`, `{"event":""}`} {
		if rec := do(http.MethodPost, "/internal/telemetry/exported", "", bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("exported %s = %d, want 400", bad, rec.Code)
		}
	}
}
