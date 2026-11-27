package pax

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestParseVersion(t *testing.T) {
	cases := []struct{ out, want string }{
		{"pax 0.75.0\n", "0.75.0"},
		{"pax 0.75.1.dev3+abc1234 (main abc1234)", "0.75.1.dev3+abc1234"},
		{"clai2 0.74.2", "0.74.2"},
		{"pax: illegal option -- -", ""},
		{"usage: pax [-cdnvz] [-f archive]", ""},
		{"pax", ""},
		{"other 1.0.0", ""},
		{"pax latest", ""},
	}
	for _, c := range cases {
		if got := parseVersion(c.out); got != c.want {
			t.Errorf("parseVersion(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}

func TestSessionUUIDIsStableAndValid(t *testing.T) {
	a, b := SessionUUID("ao-1"), SessionUUID("ao-1")
	if a != b || !validSessionID(a) {
		t.Fatalf("SessionUUID not stable or invalid: %q %q", a, b)
	}
	if SessionUUID("ao-2") == a {
		t.Fatal("distinct AO sessions share a Pax conversation id")
	}
}

func TestLaunchCommandAssignsSessionAndModel(t *testing.T) {
	bin := fakePax(t, "pax 0.75.0")
	data, workspace := t.TempDir(), t.TempDir()
	p := New()
	argv, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID: "ao-7", DataDir: data, WorkspacePath: workspace,
		Config: ports.AgentConfig{Model: "anthropic:claude-opus-5-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{bin, "-m", "anthropic:claude-opus-5-5", "--session-id", SessionUUID("ao-7")}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %q, want %q", argv, want)
	}
	info, ok, err := p.SessionInfo(context.Background(), ports.SessionRef{ID: "ao-7", DataDir: data})
	if err != nil || !ok || info.AgentSessionID != SessionUUID("ao-7") {
		t.Fatalf("SessionInfo = %+v %v %v", info, ok, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, projectDir)); !os.IsNotExist(err) {
		t.Fatalf("a launch without instructions wrote %s: %v", projectDir, err)
	}
}

func TestLaunchRejectsUnsupportedSettings(t *testing.T) {
	fakePax(t, "pax 0.75.0")
	p := New()
	for i, cfg := range []ports.LaunchConfig{
		{Config: ports.AgentConfig{Effort: "high"}},
		{AllowedTools: []string{"Read"}},
		{Config: ports.AgentConfig{Model: "--agent"}},
	} {
		if _, err := p.GetLaunchCommand(context.Background(), cfg); err == nil {
			t.Errorf("case %d: launch accepted an unsupported setting", i)
		}
	}
}

func TestResolveBinaryRejectsTheArchiver(t *testing.T) {
	fakePax(t, "")
	if _, err := New().ResolveBinary(context.Background()); err == nil {
		t.Fatal("a binary that rejects --version was accepted as Pax")
	}
}

func TestProjectSettingsCarryInstructionsAndStricterPermissions(t *testing.T) {
	workspace := t.TempDir()
	if err := writeProjectSettings(workspace, "You are an AO worker.", ports.PermissionModeAcceptEdits); err != nil {
		t.Fatal(err)
	}
	var got projectFile
	readJSON(t, filepath.Join(workspace, projectDir, projectSettings), &got)
	if got.Instructions != "You are an AO worker." || got.Permissions != "read-only" {
		t.Fatalf("settings = %+v", got)
	}
	ignore, err := os.ReadFile(filepath.Join(workspace, projectDir, ".gitignore"))
	if err != nil || !strings.Contains(string(ignore), "/"+projectSettings) {
		t.Fatalf(".clai/.gitignore = %q, %v", ignore, err)
	}
	// Nothing left to say: AO's own file is removed, not left stale.
	if err := writeProjectSettings(workspace, "", ports.PermissionModeBypassPermissions); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, projectDir, projectSettings)); !os.IsNotExist(err) {
		t.Fatalf("AO's settings file survived: %v", err)
	}
}

func TestProjectSettingsNeverReplaceTheRepositorysFile(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, projectDir, projectSettings)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	const repo = `{"model": "anthropic:claude-sonnet-4-6"}`
	if err := os.WriteFile(path, []byte(repo), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeProjectSettings(workspace, "", ports.PermissionModeDefault); err != nil {
		t.Fatalf("nothing to write still failed: %v", err)
	}
	if err := writeProjectSettings(workspace, "instructions", ports.PermissionModeDefault); err == nil {
		t.Fatal("AO replaced the repository's .clai/settings.json")
	}
	if data, _ := os.ReadFile(path); string(data) != repo { //nolint:gosec // test path
		t.Fatalf("repository file changed: %q", data)
	}
}

func TestProjectSettingsRefuseAForeignGitignore(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, projectDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("cache/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeProjectSettings(workspace, "instructions", ports.PermissionModeDefault); err == nil {
		t.Fatal("AO wrote an unmarked settings file beside the repository's .gitignore")
	}
	if _, err := os.Stat(filepath.Join(dir, projectSettings)); !os.IsNotExist(err) {
		t.Fatalf("settings file written: %v", err)
	}
}

func TestPermissionMappingNeverWidens(t *testing.T) {
	cases := []struct {
		mode ports.PermissionMode
		want string
	}{
		{ports.PermissionModeDefault, ""},
		{"", ""},
		{ports.PermissionModeAcceptEdits, "read-only"},
		{ports.PermissionModeAuto, "auto"},
		{ports.PermissionModeBypassPermissions, ""},
	}
	for _, c := range cases {
		if got := paxPermissions(c.mode); got != c.want {
			t.Errorf("paxPermissions(%q) = %q, want %q", c.mode, got, c.want)
		}
	}
}

func TestRestoreResumesOnlyASavedConversationInTheWorkspace(t *testing.T) {
	bin := fakePax(t, "pax 0.75.0")
	data, workspace := t.TempDir(), t.TempDir()
	p := New()
	if _, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{SessionID: "ao-9", DataDir: data, WorkspacePath: workspace}); err != nil {
		t.Fatal(err)
	}
	ref := ports.SessionRef{ID: "ao-9", DataDir: data, WorkspacePath: workspace}
	restore := ports.RestoreConfig{DataDir: data, Session: ref}

	// Before the first turn Pax has saved nothing: launch fresh.
	if _, ok, err := p.GetRestoreCommand(context.Background(), restore); err != nil || ok {
		t.Fatalf("restore before any turn: ok=%v err=%v", ok, err)
	}

	native := SessionUUID("ao-9")
	saveConversation(t, native, t.TempDir())
	if _, ok, err := p.GetRestoreCommand(context.Background(), restore); err != nil || ok {
		t.Fatalf("restore of a conversation from another folder: ok=%v err=%v", ok, err)
	}

	saveConversation(t, native, workspace)
	argv, ok, err := p.GetRestoreCommand(context.Background(), restore)
	if err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	if want := []string{bin, "--resume", native}; strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %q, want %q", argv, want)
	}

	// The saved id is taken: a fresh relaunch must not reuse it.
	argv, err = p.GetLaunchCommand(context.Background(), ports.LaunchConfig{SessionID: "ao-9", DataDir: data, WorkspacePath: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if fresh := argv[len(argv)-1]; fresh == native || !validSessionID(fresh) {
		t.Fatalf("fresh relaunch reused or broke the id: %q", fresh)
	}
}

func TestRestoreRejectsAMalformedID(t *testing.T) {
	fakePax(t, "pax 0.75.0")
	ref := ports.SessionRef{ID: "ao-1", WorkspacePath: t.TempDir(), Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "../latest"}}
	if _, _, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{DataDir: t.TempDir(), Session: ref}); err == nil {
		t.Fatal("restore accepted a non-UUID native id")
	}
}

func TestAuthStatusIsConfiguredOnlyFromLocalEvidence(t *testing.T) {
	fakePax(t, "pax 0.75.0")
	for _, name := range providerKeyEnv {
		t.Setenv(name, "")
	}
	p := New()
	if status, err := p.AuthStatus(context.Background()); err != nil || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("no evidence: %q %v", status, err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	if status, _ := p.AuthStatus(context.Background()); status != ports.AgentAuthStatusConfigured {
		t.Fatalf("env key: %q", status)
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), configDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials-main.enc"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, _ := p.AuthStatus(context.Background()); status != ports.AgentAuthStatusConfigured {
		t.Fatalf("saved credentials: %q", status)
	}
}

func TestDeriveActivityState(t *testing.T) {
	cases := []struct {
		event string
		want  domain.ActivityState
	}{
		{"turn-start", domain.ActivityActive},
		{"before-tool", domain.ActivityActive},
		{"after-tool", domain.ActivityActive},
		{"turn-end", domain.ActivityIdle},
	}
	for _, c := range cases {
		if got, ok := DeriveActivityState(c.event, nil); !ok || got != c.want {
			t.Errorf("DeriveActivityState(%q) = %q %v, want %q", c.event, got, ok, c.want)
		}
	}
	if _, ok := DeriveActivityState("session-start", nil); ok {
		t.Error("an event AO's table does not install reported activity")
	}
}

func TestGitignoreSentinelMarksOwnership(t *testing.T) {
	dir := t.TempDir()
	if err := hookutil.EnsureWorkspaceGitignore(dir, projectSettings); err != nil {
		t.Fatal(err)
	}
	if owned, err := aoOwnsProjectSettings(dir); err != nil || !owned {
		t.Fatalf("owned = %v, %v", owned, err)
	}
}

// saveConversation writes one row as Pax's SqliteConversationStore does.
func saveConversation(t *testing.T, id, workspace string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), configDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, sessionsDatabase))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	metadata, _ := json.Marshal(map[string]string{"workspace": workspace})
	for _, stmt := range []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE IF NOT EXISTS conversations (id TEXT PRIMARY KEY, revision INTEGER NOT NULL, metadata TEXT NOT NULL, messages TEXT NOT NULL, search_text TEXT NOT NULL, updated_at TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO conversations VALUES (?, 1, ?, '[]', '', '2026-11-27T00:00:00Z')`, id, string(metadata)); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}
