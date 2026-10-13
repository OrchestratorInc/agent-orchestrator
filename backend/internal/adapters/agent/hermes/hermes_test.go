package hermes

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManifest(t *testing.T) {
	m := (&Plugin{}).Manifest()
	if m.ID != "hermes" {
		t.Fatalf("ID = %q, want hermes", m.ID)
	}
	if m.Name != "Hermes" {
		t.Fatalf("Name = %q, want Hermes", m.Name)
	}
	hasAgent := false
	for _, c := range m.Capabilities {
		if c == adapters.CapabilityAgent {
			hasAgent = true
		}
	}
	if !hasAgent {
		t.Fatal("missing CapabilityAgent")
	}
}

func TestGetConfigSpecReportsModelField(t *testing.T) {
	spec, err := (&Plugin{}).GetConfigSpec(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []ports.ConfigField{{
		Key:         "model",
		Type:        ports.ConfigFieldString,
		Description: "Model override passed to `hermes chat -m` (e.g. provider/model).",
	}}
	if !reflect.DeepEqual(spec.Fields, want) {
		t.Fatalf("config fields\nwant: %#v\n got: %#v", want, spec.Fields)
	}
}

func TestGetPromptDeliveryStrategyIsInCommand(t *testing.T) {
	got, err := (&Plugin{}).GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != ports.PromptDeliveryInCommand {
		t.Fatalf("strategy = %q, want %q", got, ports.PromptDeliveryInCommand)
	}
}

func TestGetLaunchCommandSeedsInteractiveSessionWithPrompt(t *testing.T) {
	p := &Plugin{resolvedBinary: "hermes"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt: "add a health check",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"hermes", "chat", "--source", "tool", "-q", "add a health check"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandAppliesModelSystemPromptAndBypass(t *testing.T) {
	p := &Plugin{resolvedBinary: "hermes"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SystemPrompt: "follow repo rules",
		Config:       ports.AgentConfig{Model: "  anthropic/claude-sonnet-4  "},
		Permissions:  ports.PermissionModeBypassPermissions,
		Prompt:       "implement it",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"env", "HERMES_EPHEMERAL_SYSTEM_PROMPT=follow repo rules",
		"hermes", "chat", "--source", "tool", "-m", "anthropic/claude-sonnet-4", "--yolo", "-q", "implement it",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandOmitsFlagsForNonBypassModes(t *testing.T) {
	for _, mode := range []ports.PermissionMode{
		ports.PermissionModeDefault,
		ports.PermissionModeAcceptEdits,
		ports.PermissionModeAuto,
		ports.PermissionMode("bogus"),
	} {
		p := &Plugin{resolvedBinary: "hermes"}
		cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Permissions: mode})
		if err != nil {
			t.Fatal(err)
		}
		for _, arg := range cmd {
			if arg == "--yolo" {
				t.Fatalf("mode %q emitted --yolo: %#v", mode, cmd)
			}
		}
	}
}

func TestGetLaunchCommandReadsSystemPromptFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "system.md")
	if err := os.WriteFile(file, []byte("file prompt"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Plugin{resolvedBinary: "hermes"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SystemPromptFile: file,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"env", "HERMES_EPHEMERAL_SYSTEM_PROMPT=file prompt",
		"hermes", "chat", "--source", "tool",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetRestoreCommandResumesNativeSession(t *testing.T) {
	p := &Plugin{resolvedBinary: "hermes"}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		SystemPrompt: "restore rules",
		Config:       ports.AgentConfig{Model: "openai/gpt-5-codex"},
		Prompt:       "keep going",
		Session: ports.SessionRef{
			Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "20261013_015615_a8e0764f"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("ok=false, want true")
	}
	want := []string{
		"env", "HERMES_EPHEMERAL_SYSTEM_PROMPT=restore rules",
		"hermes", "chat", "--source", "tool", "--resume", "20261013_015615_a8e0764f",
		"-m", "openai/gpt-5-codex", "-q", "keep going",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetRestoreCommandWithoutNativeSessionIDReturnsNotOK(t *testing.T) {
	p := &Plugin{resolvedBinary: "hermes"}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if ok || cmd != nil {
		t.Fatalf("cmd=%#v ok=%v, want nil false", cmd, ok)
	}
}

func TestSessionInfoSurfacesStandardMetadata(t *testing.T) {
	p := &Plugin{}
	info, ok, err := p.SessionInfo(context.Background(), ports.SessionRef{
		Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-1", ports.MetadataKeyTitle: "Title"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || info.AgentSessionID != "native-1" || info.Title != "Title" {
		t.Fatalf("info = %#v ok = %v, want native session id and title", info, ok)
	}

	_, ok, err = p.SessionInfo(context.Background(), ports.SessionRef{})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("ok = true without metadata, want false")
	}
}

func TestNativeConversationExistsRequiresPersistedSessionRow(t *testing.T) {
	home := t.TempDir()
	if err := createHermesStateDB(filepath.Join(home, "state.db"), "20261013_015615_a8e0764f"); err != nil {
		t.Fatal(err)
	}

	p := &Plugin{}
	exists, err := p.NativeConversationExists(context.Background(), ports.SessionRef{}, "20261013_015615_a8e0764f", map[string]string{"HERMES_HOME": home})
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("exists = false for persisted session row, want true")
	}

	exists, err = p.NativeConversationExists(context.Background(), ports.SessionRef{}, "20261013_010000_0000000", map[string]string{"HERMES_HOME": home})
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("exists = true for unknown session id, want false")
	}

	exists, err = p.NativeConversationExists(context.Background(), ports.SessionRef{}, "  ", map[string]string{"HERMES_HOME": home})
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("exists = true for blank session id, want false")
	}
}

func TestNativeConversationExistsFalseWithoutStateDB(t *testing.T) {
	exists, err := (&Plugin{}).NativeConversationExists(
		context.Background(), ports.SessionRef{}, "20261013_015615_a8e0764f",
		map[string]string{"HERMES_HOME": t.TempDir()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("exists = true with no state.db, want false")
	}
}

func TestNativeConversationExistsHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Plugin{}).NativeConversationExists(ctx, ports.SessionRef{}, "some-id", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestIsOfficialHermesBinaryChecksHelpBanner(t *testing.T) {
	original := hermesIdentityCommand
	t.Cleanup(func() { hermesIdentityCommand = original })

	hermesIdentityCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("Hermes Agent - AI assistant with tool-calling capabilities\n"), nil
	}
	if !isOfficialHermesBinary(context.Background(), "/fake/hermes") {
		t.Fatal("identity = false for Hermes help banner, want true")
	}

	hermesIdentityCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("some other CLI\n"), nil
	}
	if isOfficialHermesBinary(context.Background(), "/fake/hermes") {
		t.Fatal("identity = true for unrelated help banner, want false")
	}

	hermesIdentityCommand = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("exec failed")
	}
	if isOfficialHermesBinary(context.Background(), "/fake/hermes") {
		t.Fatal("identity = true for failing probe, want false")
	}
}

func TestAuthStatusReadsAuthJSONAndEnvKeys(t *testing.T) {
	original := hermesIdentityCommand
	t.Cleanup(func() { hermesIdentityCommand = original })
	hermesIdentityCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("Hermes Agent\n"), nil
	}

	p := &Plugin{resolvedBinary: "hermes"}

	// Blank every credential signal the probe reads so a developer machine's
	// own environment cannot flip an assertion.
	for _, name := range hermesProviderKeyEnvVars {
		t.Setenv(name, "")
	}
	t.Setenv("HERMES_HOME", "")

	home := t.TempDir()
	t.Setenv("HERMES_HOME", home)
	if status, err := p.AuthStatus(context.Background()); err != nil || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("AuthStatus with empty home = (%q, %v), want unknown", status, err)
	}

	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"providers":{"zai":{"detected_endpoint":{"key_hash":"abc"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, err := p.AuthStatus(context.Background()); err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("AuthStatus with provider entry = (%q, %v), want authorized", status, err)
	}

	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"credential_pool":{"anthropic":[{"id":"c1"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, err := p.AuthStatus(context.Background()); err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("AuthStatus with credential pool = (%q, %v), want authorized", status, err)
	}

	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	if status, err := p.AuthStatus(context.Background()); err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("AuthStatus with env key = (%q, %v), want authorized", status, err)
	}
}

// createHermesStateDB builds a minimal Hermes state database containing one
// session row, so the read-only probe test never touches the user's real one.
func createHermesStateDB(path, sessionID string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, started_at REAL)`); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO sessions (id, source, started_at) VALUES (?, 'cli', 1)`, sessionID)
	return err
}
