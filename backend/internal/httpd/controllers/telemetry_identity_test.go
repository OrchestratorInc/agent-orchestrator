package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeTelemetryIdentity struct{ snap ports.TelemetryIdentity }

func (f fakeTelemetryIdentity) Snapshot() ports.TelemetryIdentity            { return f.snap }
func (f fakeTelemetryIdentity) OptedOut() bool                               { return f.snap.OptedOut }
func (f fakeTelemetryIdentity) ExportSuppressed() bool                       { return false }
func (f fakeTelemetryIdentity) SetCloudUserID(context.Context, string) error { return nil }
func (f fakeTelemetryIdentity) MarkInstalledEvent(context.Context) (bool, error) {
	return false, nil
}
func (f fakeTelemetryIdentity) SetGitHubLogin(string) {}

func getTelemetryIdentity(t *testing.T, c *TelemetryIdentityController) map[string]any {
	t.Helper()
	r := chi.NewRouter()
	c.Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/telemetry/identity", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestTelemetryIdentityReportsDaemonVersion(t *testing.T) {
	body := getTelemetryIdentity(t, &TelemetryIdentityController{
		Source:        fakeTelemetryIdentity{snap: ports.TelemetryIdentity{InstallID: "ins_1"}},
		DaemonVersion: "1.9.0",
	})
	if body["daemonVersion"] != "1.9.0" {
		t.Fatalf("daemonVersion = %v, want 1.9.0", body["daemonVersion"])
	}
}

// The version names a build, not a person, so an opted-out desktop still
// reports it while every identity field stays empty.
func TestTelemetryIdentityOptedOutKeepsOnlyDaemonVersion(t *testing.T) {
	body := getTelemetryIdentity(t, &TelemetryIdentityController{
		Source:        fakeTelemetryIdentity{snap: ports.TelemetryIdentity{InstallID: "ins_1", CloudUserID: "user_1", OptedOut: true}},
		DaemonVersion: "1.9.0",
	})
	if body["daemonVersion"] != "1.9.0" || body["optedOut"] != true {
		t.Fatalf("body = %v, want daemonVersion and optedOut", body)
	}
	for _, key := range []string{"installId", "cloudUserId", "githubLogin", "isSignedIn"} {
		if _, ok := body[key]; ok {
			t.Fatalf("opted-out body has %q: %v", key, body)
		}
	}
	if body["distinctId"] != "" {
		t.Fatalf("opted-out distinctId = %v, want empty", body["distinctId"])
	}
}

func TestTelemetryIdentityOmitsUnknownDaemonVersion(t *testing.T) {
	body := getTelemetryIdentity(t, &TelemetryIdentityController{
		Source: fakeTelemetryIdentity{snap: ports.TelemetryIdentity{InstallID: "ins_1"}},
	})
	if _, ok := body["daemonVersion"]; ok {
		t.Fatalf("daemonVersion present without a version: %v", body)
	}
}
