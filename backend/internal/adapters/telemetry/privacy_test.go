package telemetry

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The desktop renderer computes the same HMAC (frontend telemetry-hash test
// uses this vector), so a project hashes identically on both surfaces.
func TestHashTelemetryIDMatchesDesktopVector(t *testing.T) {
	got := hashTelemetryID(strings.Repeat("0", 64), "agent-orchestrator")
	if want := "e91bc9bcb2b92ea19add18a42c95c4d9c0f882c6f953c4438d86d9c5644bb34f"; got != want {
		t.Fatalf("hashTelemetryID = %s, want %s", got, want)
	}
}

func TestHashSaltIsPrivatePersistentAndInstallScoped(t *testing.T) {
	dir := t.TempDir()
	first, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, hashSaltFile))
	if err != nil {
		t.Fatalf("salt not persisted: %v", err)
	}
	salt := strings.TrimSpace(string(raw))
	if !hashSaltPattern.MatchString(salt) {
		t.Fatalf("salt %q is not 32 random bytes in hex", salt)
	}
	again, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := first.HashID("ao")
	b, _ := again.HashID("ao")
	if a != b {
		t.Fatal("hash changed across restarts of one install")
	}
	other, err := NewIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := other.HashID("ao"); c == a {
		t.Fatal("two installs produced the same project hash")
	}
}

// The install ID rides every exported event, so a hash keyed with it (or with
// nothing) can be recomputed from a list of repo names. Neither may appear.
func TestExportedIDHashesAreNotRecomputableFromExportedValues(t *testing.T) {
	ident, err := NewIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectID := domain.ProjectID("agent-orchestrator")
	sessionID := domain.SessionID("agent-orchestrator-435")
	props := (&PostHogSink{ident: ident}).properties(ports.TelemetryEvent{Name: "ao.sessions.spawned", ProjectID: &projectID, SessionID: &sessionID})
	install := ident.InstallID()
	for key, raw := range map[string]string{"project_id_hash": string(projectID), "session_id_hash": string(sessionID)} {
		got, _ := props[key].(string)
		if got == "" {
			t.Fatalf("%s missing", key)
		}
		for _, guess := range []string{sha256String(raw), sha256String(install + ":" + raw)} {
			if got == guess {
				t.Fatalf("%s is recomputable from exported values", key)
			}
		}
	}
	for key, v := range props {
		if s, ok := v.(string); ok && strings.Contains(s, ident.hashSalt) {
			t.Fatalf("hash salt leaked into %s", key)
		}
	}
}

func TestNoHashWithoutSalt(t *testing.T) {
	ident, err := NewIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ident.hashSalt = ""
	projectID := domain.ProjectID("ao")
	sessionID := domain.SessionID("ao-1")
	props := (&PostHogSink{ident: ident}).properties(ports.TelemetryEvent{Name: "ao.sessions.spawned", ProjectID: &projectID, SessionID: &sessionID})
	for _, key := range []string{"project_id_hash", "session_id_hash"} {
		if _, ok := props[key]; ok {
			t.Fatalf("%s sent without a private salt", key)
		}
	}
}

func shareIdentityMarker(t *testing.T, dir string, on bool) {
	t.Helper()
	path := filepath.Join(dir, ShareIdentityFile)
	if on {
		if err := os.WriteFile(path, []byte("1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestGitHubIdentityIsOffByDefaultAndGatedByTheToggle(t *testing.T) {
	dir := t.TempDir()
	sink := newTestSink(t, dir, nil, nil)
	defer func() { _ = sink.Close(context.Background()) }()
	ident := sink.Identity()
	ident.SetGitHubLogin("octocat")

	spawned := ports.TelemetryEvent{Name: "ao.sessions.spawned", Payload: map[string]any{"kind": "worker", "github_actor": "octocat"}}
	created := ports.TelemetryEvent{Name: "ao.projects.created", Payload: map[string]any{"kind": "git", "github_org": "octocat"}}

	// Default: no username or org anywhere, while sign-in is still known.
	for _, ev := range []ports.TelemetryEvent{spawned, created, {Name: "ao.daemon.started"}} {
		props := sink.properties(ev)
		for _, key := range []string{"github_actor", "github_org", "$set"} {
			if _, ok := props[key]; ok {
				t.Fatalf("%s carries %s with sharing off: %#v", ev.Name, key, props)
			}
		}
	}
	if props := sink.properties(spawned); props["kind"] != "worker" {
		t.Fatalf("other allowlisted fields dropped: %#v", props)
	}
	if err := ident.SetCloudUserID(context.Background(), "user_01HABC"); err != nil {
		t.Fatal(err)
	}
	if props := sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started"}); props["ao_cloud_user_id"] != nil {
		t.Fatalf("Cloud user ID exported with sharing off: %#v", props)
	}
	snap := ident.Snapshot()
	if snap.GitHubLogin != "" || !snap.GitHubSignedIn {
		t.Fatalf("snapshot with sharing off = %#v, want no login but signed in", snap)
	}

	shareIdentityMarker(t, dir, true)
	if props := sink.properties(spawned); props["github_actor"] != "octocat" {
		t.Fatalf("github_actor missing with sharing on: %#v", props)
	}
	if props := sink.properties(created); props["github_org"] != "octocat" {
		t.Fatalf("github_org missing with sharing on: %#v", props)
	}
	daemon := sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started"})
	if daemon["github_actor"] != "octocat" {
		t.Fatalf("resolved login not stamped with sharing on: %#v", daemon)
	}
	if got := ident.Snapshot().GitHubLogin; got != "octocat" {
		t.Fatalf("snapshot login with sharing on = %q", got)
	}
}

// Matches T3 Code and Orca: no PostHog person profile and no GeoIP on any
// event, whether signed out, signed in, or sharing account identity.
func TestNoPersonProfileOrGeoIPOnAnyEvent(t *testing.T) {
	dir := t.TempDir()
	sink := newTestSink(t, dir, nil, nil)
	defer func() { _ = sink.Close(context.Background()) }()
	ident := sink.Identity()
	check := func(stage string) {
		t.Helper()
		props := sink.properties(ports.TelemetryEvent{Name: "ao.sessions.spawned", Payload: map[string]any{"kind": "worker", "github_actor": "octocat"}})
		if props["$process_person_profile"] != false || props["$geoip_disable"] != true {
			t.Fatalf("%s: person=%v geoip_disable=%v", stage, props["$process_person_profile"], props["$geoip_disable"])
		}
		for _, key := range []string{"$set", "$set_once", "$unset", "email"} {
			if _, ok := props[key]; ok {
				t.Fatalf("%s: event carries %s", stage, key)
			}
		}
	}
	check("anonymous")
	ident.SetGitHubLogin("octocat")
	if err := ident.SetCloudUserID(context.Background(), "user_01HABC"); err != nil {
		t.Fatal(err)
	}
	check("signed in")
	if props := sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started"}); props["ao_cloud_user_id"] != nil || props["is_signed_in"] != true {
		t.Fatalf("signed in without sharing: %#v", props)
	}
	shareIdentityMarker(t, dir, true)
	check("sharing identity")
	props := sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started"})
	if props["ao_cloud_user_id"] != "user_01HABC" || props["github_actor"] != "octocat" {
		t.Fatalf("shared identity missing: %#v", props)
	}
}

func TestOnExportedReceivesExactlyWhatPostHogAccepted(t *testing.T) {
	requests := make(chan map[string]any, 1)
	sink := newTestSink(t, t.TempDir(), requests, nil)
	defer func() { _ = sink.Close(context.Background()) }()
	var gotName, gotDistinct string
	var gotProps map[string]any
	sink.OnExported(func(name, distinctID string, props map[string]any) {
		gotName, gotDistinct, gotProps = name, distinctID, props
	})
	sink.send(context.Background(), ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon"})
	body := <-requests
	sent, _ := body["properties"].(map[string]any)
	if gotName != "ao.daemon.started" || gotDistinct != body["distinct_id"] || gotProps["install_id"] != sent["install_id"] || len(gotProps) != len(sent) {
		t.Fatalf("recorded %q %q %#v, sent %#v", gotName, gotDistinct, gotProps, body)
	}
}
