package hermesacp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func provision(t *testing.T, data string, session domain.SessionID) string {
	t.Helper()
	home, err := profilePath(data, session)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("plugins:\n  enabled: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestConfigurePreservesNativeConfigAndRefreshesPrivateContext(t *testing.T) {
	data := t.TempDir()
	home := provision(t, data, "session")
	cfg := acpdriver.LaunchConfig{DataDir: data, SessionID: "session", SystemPrompt: "first", Env: map[string]string{"HERMES_HOME": home}}
	for _, prompt := range []string{"first", "new after kill"} {
		cfg.SystemPrompt = prompt
		args, env, err := configure(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if len(args) != 1 || args[0] != "acp" || env["HERMES_HOME"] != home {
			t.Fatalf("unexpected launch %v", args)
		}
		raw, err := os.ReadFile(filepath.Join(home, "plugins", pluginName, "instructions.txt"))
		if err != nil || string(raw) != prompt {
			t.Fatalf("private instructions not refreshed: %v", err)
		}
		if _, ok := env["HERMES_EPHEMERAL_SYSTEM_PROMPT"]; ok {
			t.Fatal("ignored classic prompt route used")
		}
	}
}

func TestConfigureRefusesSourceProfile(t *testing.T) {
	source := t.TempDir()
	_, _, err := configure(context.Background(), acpdriver.LaunchConfig{DataDir: t.TempDir(), SessionID: "session", Env: map[string]string{"HERMES_HOME": source}})
	if err == nil {
		t.Fatal("source profile accepted")
	}
	entries, _ := os.ReadDir(source)
	if len(entries) != 0 {
		t.Fatal("source profile mutated")
	}
}

func TestConfigureRefusesModifiedManagedPlugin(t *testing.T) {
	data := t.TempDir()
	home := provision(t, data, "session")
	cfg := acpdriver.LaunchConfig{DataDir: data, SessionID: "session", Env: map[string]string{"HERMES_HOME": home}}
	if _, _, err := configure(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "plugins", pluginName, "__init__.py"), []byte("user edits"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := configure(context.Background(), cfg); err == nil {
		t.Fatal("overwrote modified plugin")
	}
}

func TestUnsupportedSettingsFailBeforeProvider(t *testing.T) {
	for _, setting := range []ports.ChatTurnSettings{{Model: "unqualified"}, {Effort: "high"}, {Approval: ports.PermissionModeAuto}, {Approval: ports.PermissionModeBypassPermissions}, {Approval: ports.PermissionModeAcceptEdits}} {
		if err := validateTurnSettings(ports.PermissionModeDefault, setting); !errors.Is(err, acpdriver.ErrACPSetterUnsupported) {
			t.Fatalf("unsupported setting accepted: %+v: %v", setting, err)
		}
	}
}

type recordingDriver struct {
	ports.ChatDriver
	resumes      int
	conversation ports.ChatConversation
	fresh        bool
}

func (d *recordingDriver) Resume(_ context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	d.resumes++
	d.fresh = cfg.FreshIfMissing
	return d.conversation, nil
}
func TestResumeMissingHistoryNeverDelegatesEvenWithFreshFallback(t *testing.T) {
	data := t.TempDir()
	provision(t, data, "session")
	delegate := &recordingDriver{}
	d := &Driver{ChatDriver: delegate}
	_, err := d.Resume(context.Background(), ports.ChatResumeConfig{DataDir: data, SessionID: "session", WorkspacePath: t.TempDir(), ProviderConversationID: "missing", FreshIfMissing: true})
	if !errors.Is(err, ports.ErrChatResumeFailed) || delegate.resumes != 0 {
		t.Fatalf("missing history reached provider: %v", err)
	}
}

func TestSafetyRequestsFailBeforeAnyProviderLaunch(t *testing.T) {
	d := New(fixturePlugin{}, nil)
	for _, cfg := range []ports.ChatStartConfig{{ReadOnly: true}, {DisableNetwork: true}, {Ephemeral: true}} {
		if _, err := d.Start(context.Background(), cfg); !errors.Is(err, ports.ErrChatDriverIncompatible) {
			t.Fatalf("unsupported guarantee accepted: %v", err)
		}
	}
	if _, err := d.Resume(context.Background(), ports.ChatResumeConfig{ReadOnly: true}); !errors.Is(err, ports.ErrChatResumeFailed) || !strings.Contains(err.Error(), "read-only restore") {
		t.Fatal(err)
	}
	if _, err := d.Reconnect(context.Background(), ports.ChatResumeConfig{ReadOnly: true}); !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatal(err)
	}
}
func TestInitializationRejectsOtherNativeContracts(t *testing.T) {
	valid := acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: "hermes-agent", Version: "0.21.6"}, AgentCapabilities: acpsdk.AgentCapabilities{LoadSession: true}}
	if err := validateInitialize(valid); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []acpsdk.InitializeResponse{{}, {AgentInfo: &acpsdk.Implementation{Name: "hermes-agent", Version: "0.22.2"}, AgentCapabilities: valid.AgentCapabilities}, {AgentInfo: valid.AgentInfo}} {
		if !errors.Is(validateInitialize(bad), ports.ErrChatDriverIncompatible) {
			t.Fatal("unqualified contract admitted")
		}
	}
}

type replayConversation struct {
	ports.ChatConversation
	id                 string
	history            []ports.ChatEvent
	readErr            error
	closed, terminated bool
}

func (c *replayConversation) ProviderConversationID() string { return c.id }
func (c *replayConversation) ReadHistory(context.Context) ([]ports.ChatEvent, error) {
	return c.history, c.readErr
}
func (c *replayConversation) Close() error     { c.closed = true; return nil }
func (c *replayConversation) Terminate() error { c.terminated = true; return nil }
func TestResumeRejectsNativeSuccessfulButEmptyOrUnreadableReplay(t *testing.T) {
	for _, kind := range []string{"empty", "metadata only", "unreadable", "wrong identity", "valid"} {
		t.Run(kind, func(t *testing.T) {
			data, workspace := t.TempDir(), t.TempDir()
			home := provision(t, data, "session")
			seedNativeHistory(t, home, workspace)
			c := &replayConversation{id: "native-id", history: []ports.ChatEvent{{Kind: ports.ChatEventUserMessageCompleted, Text: "native history"}}}
			switch kind {
			case "empty":
				c.history = nil
			case "metadata only":
				c.history = []ports.ChatEvent{{Kind: ports.ChatEventUsage}}
			case "unreadable":
				c.readErr = ports.ErrChatHistoryUnavailable
			case "wrong identity":
				c.id = "other"
			}
			delegate := &recordingDriver{conversation: c}
			d := &Driver{ChatDriver: delegate}
			got, err := d.Resume(context.Background(), ports.ChatResumeConfig{DataDir: data, SessionID: "session", WorkspacePath: workspace, ProviderConversationID: "native-id", FreshIfMissing: true})
			if delegate.resumes != 1 || delegate.fresh {
				t.Fatal("restore not exact")
			}
			if kind == "valid" {
				if err != nil || got != c || c.closed || c.terminated {
					t.Fatalf("valid replay refused: %v", err)
				}
			} else if !errors.Is(err, ports.ErrChatResumeFailed) || !c.closed || !c.terminated {
				t.Fatalf("invalid native replay admitted or replacement leaked: %v", err)
			}
		})
	}
}
