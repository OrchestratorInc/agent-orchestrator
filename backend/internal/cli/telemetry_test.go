package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

func telemetryDeps(t *testing.T, handle func(req *http.Request) (*http.Response, error)) Deps {
	t.Helper()
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: os.Getpid(), Port: 3001, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	return Deps{
		HTTPClient:   &http.Client{Transport: roundTripFunc(handle)},
		ProcessAlive: func(pid int) bool { return pid == os.Getpid() },
	}
}

func TestTelemetryStatusShowsEachSwitch(t *testing.T) {
	var paths []string
	deps := telemetryDeps(t, func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		if req.URL.Path == "/internal/telemetry/status" {
			return jsonResponse(http.StatusOK, `{"analyticsSending":true,"remoteConfigured":true,"errorReports":true,"localLog":true,"retentionDays":30,"installId":"ins_1"}`), nil
		}
		return jsonResponse(http.StatusNotFound, ""), nil
	})
	out, _, err := executeCLI(t, deps, "telemetry", "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Share usage analytics:     on", "Include account identity:  off", "Share error events:        on", "kept 30 days", "ins_1", "docs/telemetry.md"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
	for _, path := range paths {
		if path == "/internal/telemetry/cli-invoked" {
			t.Fatal("ao telemetry status reported itself as CLI usage")
		}
	}
}

func TestTelemetryLogPrintsEveryPropertyAndFilters(t *testing.T) {
	var query string
	deps := telemetryDeps(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/internal/telemetry/log" {
			query = req.URL.RawQuery
			if req.URL.Query().Get("event") == "ao.routes.viewed" {
				return jsonResponse(http.StatusOK, `{"events":[{"occurredAt":"2026-11-26T09:00:00Z","event":"ao.routes.viewed","surface":"desktop","properties":{"surface":"board"}}]}`), nil
			}
			return jsonResponse(http.StatusOK, `{"events":[
				{"occurredAt":"2026-11-26T10:00:00Z","event":"ao.app.active","surface":"daemon","distinctId":"ins_1","properties":{"client":"cli","$geoip_disable":true}},
				{"occurredAt":"2026-11-26T09:00:00Z","event":"ao.routes.viewed","surface":"desktop","properties":{"surface":"board"}}]}`), nil
		}
		return jsonResponse(http.StatusNotFound, ""), nil
	})
	out, _, err := executeCLI(t, deps, "telemetry", "log", "--limit", "5", "--since", "24h")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "limit=5") || !strings.Contains(query, "since=") {
		t.Fatalf("query = %q", query)
	}
	for _, want := range []string{"ao.app.active  (daemon)", "distinct_id: ins_1", `client: "cli"`, "$geoip_disable: true", "ao.routes.viewed  (desktop)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log output missing %q:\n%s", want, out)
		}
	}
	out, _, err = executeCLI(t, deps, "telemetry", "log", "--event", "ao.routes.viewed", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "event=ao.routes.viewed") {
		t.Fatalf("event filter not sent to the daemon: %q", query)
	}
	var events []map[string]any
	if err := json.Unmarshal([]byte(out), &events); err != nil || len(events) != 1 || events[0]["event"] != "ao.routes.viewed" {
		t.Fatalf("json log = %s (%v)", out, err)
	}
	if _, _, err := executeCLI(t, deps, "telemetry", "log", "--limit", "0"); err == nil || ExitCode(err) != 2 {
		t.Fatalf("--limit 0 err = %v, want usage error", err)
	}
}

func TestTelemetryOffAndOnPostTheOptOut(t *testing.T) {
	var bodies []string
	deps := telemetryDeps(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/internal/telemetry/opt-out" && req.Method == http.MethodPost {
			raw, _ := io.ReadAll(req.Body)
			bodies = append(bodies, string(raw))
			optedOut := strings.Contains(string(raw), "true")
			return jsonResponse(http.StatusOK, `{"optedOut":`+map[bool]string{true: "true", false: "false"}[optedOut]+`,"remoteConfigured":true}`), nil
		}
		return jsonResponse(http.StatusNotFound, ""), nil
	})
	out, _, err := executeCLI(t, deps, "telemetry", "off")
	if err != nil || !strings.Contains(out, "Share usage analytics:     off") {
		t.Fatalf("off: %v\n%s", err, out)
	}
	if _, _, err := executeCLI(t, deps, "telemetry", "on"); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] != `{"optedOut":true}` || bodies[1] != `{"optedOut":false}` {
		t.Fatalf("bodies = %#v", bodies)
	}
}
