package telemetry

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

func shareGitHub(t *testing.T, dir string, on bool) {
	t.Helper()
	path := filepath.Join(dir, ShareGitHubLoginFile)
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
	snap := ident.Snapshot()
	if snap.GitHubLogin != "" || !snap.GitHubSignedIn {
		t.Fatalf("snapshot with sharing off = %#v, want no login but signed in", snap)
	}

	shareGitHub(t, dir, true)
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

func TestTurningGitHubSharingOffClearsThePersonLogin(t *testing.T) {
	dir := t.TempDir()
	sink := newTestSink(t, dir, nil, nil)
	defer func() { _ = sink.Close(context.Background()) }()
	shareGitHub(t, dir, true)
	sink.Identity().SetGitHubLogin("octocat")
	sink.send(context.Background(), ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon"})

	shareGitHub(t, dir, false)
	props := sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon"})
	unset, _ := props["$unset"].([]string)
	if !slices.Contains(unset, "github_actor") || !slices.Contains(unset, "github_login") {
		t.Fatalf("person login not cleared after sharing off: %#v", props)
	}
	if _, ok := props["github_actor"]; ok {
		t.Fatal("github_actor still on the event after sharing off")
	}
	sink.send(context.Background(), ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon"})
	if _, ok := sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started"})["$unset"]; ok {
		t.Fatal("person cleanup resent on every event")
	}
}

func TestSignedInPersonDropsGitHubLoginWhenNotShared(t *testing.T) {
	sink := newTestSink(t, t.TempDir(), nil, nil)
	defer func() { _ = sink.Close(context.Background()) }()
	sink.Identity().SetGitHubLogin("octocat")
	if err := sink.Identity().SetCloudUserID(context.Background(), "user_01HABC"); err != nil {
		t.Fatal(err)
	}
	props := sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started"})
	set, _ := props["$set"].(map[string]any)
	if _, ok := set["github_login"]; ok {
		t.Fatalf("github_login set on person with sharing off: %#v", set)
	}
	unset, _ := props["$unset"].([]string)
	if !slices.Contains(unset, "github_login") {
		t.Fatalf("stale github_login not cleared: %#v", unset)
	}
}
