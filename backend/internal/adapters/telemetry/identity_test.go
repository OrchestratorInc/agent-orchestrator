package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func newTestSink(t *testing.T, dir string, requests chan map[string]any, hits *atomic.Int32) *PostHogSink {
	t.Helper()
	sink, err := NewPostHogSink(dir, "phc_test", "https://us.i.posthog.com", "1.2.3", "", roundTripClient(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		if hits != nil {
			hits.Add(1)
		}
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		if requests != nil {
			requests <- body
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":1}`))}, nil
	}), nil)
	if err != nil {
		t.Fatalf("NewPostHogSink: %v", err)
	}
	return sink
}

func TestIdentityStampsEveryEventAndSetsPersonOnce(t *testing.T) {
	sink := newTestSink(t, t.TempDir(), nil, nil)
	defer func() { _ = sink.Close(context.Background()) }()

	anon := sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon"})
	for _, k := range []string{"github_actor", "ao_cloud_user_id", "$set"} {
		if _, ok := anon[k]; ok {
			t.Fatalf("anonymous event carries %q", k)
		}
	}
	if anon["$process_person_profile"] != false {
		t.Fatalf("anonymous event is identified: %v", anon["$process_person_profile"])
	}

	sink.Identity().SetGitHubLogin("octocat")
	if !sink.Identity().SetCloudUserID("user_01HABC") {
		t.Fatal("valid WorkOS id rejected")
	}
	first := sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon"})
	if first["github_actor"] != "octocat" || first["ao_cloud_user_id"] != "user_01HABC" {
		t.Fatalf("identity not on event: %#v", first)
	}
	if first["$process_person_profile"] != true {
		t.Fatal("signed-in event must be linked to the person")
	}
	set, _ := first["$set"].(map[string]any)
	if set["github_login"] != "octocat" || set["ao_cloud_user_id"] != "user_01HABC" {
		t.Fatalf("$set = %#v", set)
	}
	if _, ok := set["email"]; ok {
		t.Fatal("daemon must never $set email")
	}

	second := sink.properties(ports.TelemetryEvent{Name: "ao.http.5xx", Source: "httpd"})
	if second["github_actor"] != "octocat" || second["ao_cloud_user_id"] != "user_01HABC" || second["$process_person_profile"] != true {
		t.Fatalf("identity missing on later event: %#v", second)
	}
	if _, ok := second["$set"]; ok {
		t.Fatal("$set resent for unchanged identity")
	}
	for _, key := range []string{"app_version", "ao_version"} {
		if second[key] != "1.2.3" {
			t.Fatalf("%s = %v, want 1.2.3", key, second[key])
		}
	}

	// Sign-out drops the user but the person set is re-evaluated, not stale.
	sink.Identity().SetCloudUserID("")
	out := sink.properties(ports.TelemetryEvent{Name: "ao.http.5xx", Source: "httpd"})
	if _, ok := out["ao_cloud_user_id"]; ok {
		t.Fatal("ao_cloud_user_id survived sign-out")
	}
}

func TestIdentityRejectsMalformedValues(t *testing.T) {
	ident, err := NewIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ident.SetCloudUserID("user@example.com") || ident.SetCloudUserID(strings.Repeat("a", 65)) {
		t.Fatal("email-shaped or oversized cloud user id accepted")
	}
	ident.SetGitHubLogin("not a login!")
	if got := ident.Snapshot(); got.GitHubLogin != "" || got.CloudUserID != "" {
		t.Fatalf("malformed values stored: %#v", got)
	}
}

func TestOptOutStopsExportAndHidesIdentity(t *testing.T) {
	dir := t.TempDir()
	var hits atomic.Int32
	sink := newTestSink(t, dir, nil, &hits)
	sink.Identity().SetGitHubLogin("octocat")
	sink.Identity().SetCloudUserID("user_01HABC")

	if err := os.WriteFile(filepath.Join(dir, OptOutFile), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := sink.Identity().Snapshot()
	if !snap.OptedOut || snap.CloudUserID != "" || snap.GitHubLogin != "" {
		t.Fatalf("opted-out snapshot leaks identity: %#v", snap)
	}
	sink.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon", OccurredAt: time.Now()})
	if err := sink.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Fatalf("opted-out install exported %d events", hits.Load())
	}

	// Opting back in resumes export.
	if err := os.Remove(filepath.Join(dir, OptOutFile)); err != nil {
		t.Fatal(err)
	}
	sink = newTestSink(t, dir, nil, &hits)
	sink.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon", OccurredAt: time.Now()})
	_ = sink.Close(context.Background())
	if hits.Load() != 1 {
		t.Fatalf("export did not resume after opt-in: %d", hits.Load())
	}
}

func TestFreshInstallOnlyBeforeFirstExportedEvent(t *testing.T) {
	dir := t.TempDir()
	ident, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ident.FreshInstall() {
		t.Fatal("new data dir should be a fresh install")
	}
	// An exported event writes the tenure file; the next start is not fresh.
	sink := newTestSink(t, dir, nil, nil)
	sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started"})
	_ = sink.Close(context.Background())
	again, _ := NewIdentity(dir)
	if again.FreshInstall() {
		t.Fatal("second start reported a fresh install")
	}

	// An old install ID with no tenure file (upgrade) is not a new install.
	old := t.TempDir()
	path := filepath.Join(old, "telemetry_install_id")
	if err := os.WriteFile(path, []byte("ins_x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	upgraded, _ := NewIdentity(old)
	if upgraded.FreshInstall() {
		t.Fatal("upgrade counted as a new install")
	}
}

// Email is a person property set once by the signed-in desktop. No daemon event
// may carry it, so it must not be an allowlisted payload key anywhere.
func TestNoEmailInAnyPayloadAllowlist(t *testing.T) {
	for name, allowed := range remotePayloadAllowlist {
		for key := range allowed {
			if strings.Contains(strings.ToLower(key), "email") {
				t.Errorf("%s allowlists %q", name, key)
			}
		}
	}
}
