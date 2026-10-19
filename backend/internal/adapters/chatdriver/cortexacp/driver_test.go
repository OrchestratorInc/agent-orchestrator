package cortexacp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func isolatedConfig(t *testing.T) ports.ChatStartConfig {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "profile")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return ports.ChatStartConfig{SessionID: "cortex-owned", DataDir: root, WorkspacePath: t.TempDir(), Env: map[string]string{"HOME": home}, Permissions: ports.PermissionModeDefault}
}

func TestCandidateNeverEntersProductionRegistry(t *testing.T) {
	d := NewCandidate(nil, nil)
	if d.Harness() != harness {
		t.Fatalf("harness = %q", d.Harness())
	}
	if caps, err := d.Probe(context.Background()); !errors.Is(err, ports.ErrChatDriverIncompatible) || caps != nil {
		t.Fatalf("Probe = %v, %v; want closed admission", caps, err)
	}
	if registry.Build(nil, nil).SupportsChat(harness) {
		t.Fatal("unqualified candidate is registered")
	}
}

func TestStandingInstructionsAreRejectedBeforeAnyDriverCall(t *testing.T) {
	cfg := isolatedConfig(t)
	cfg.SystemPrompt = "private standing context"
	d := NewCandidate(nil, nil)
	if _, err := d.Start(context.Background(), cfg); !errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("Start = %v", err)
	}
	resume := ports.ChatResumeConfig{ProviderConversationID: "native-id", SystemPrompt: cfg.SystemPrompt}
	if _, err := d.Resume(context.Background(), resume); !errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("Resume = %v", err)
	}
	if _, err := d.(ports.ChatDriverReconnector).Reconnect(context.Background(), resume); !errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("Reconnect = %v", err)
	}
}

func TestUnsupportedLaunchAndTurnSettingsFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ports.ChatStartConfig)
	}{
		{"effort", func(c *ports.ChatStartConfig) { c.Effort = "high" }},
		{"auto", func(c *ports.ChatStartConfig) { c.Permissions = ports.PermissionModeAuto }},
		{"accept-edits", func(c *ports.ChatStartConfig) { c.Permissions = ports.PermissionModeAcceptEdits }},
		{"bypass", func(c *ports.ChatStartConfig) { c.Permissions = ports.PermissionModeBypassPermissions }},
		{"unknown approval", func(c *ports.ChatStartConfig) { c.Permissions = "unknown" }},
		{"read-only", func(c *ports.ChatStartConfig) { c.ReadOnly = true }},
		{"network isolation", func(c *ports.ChatStartConfig) { c.DisableNetwork = true }},
		{"ephemeral", func(c *ports.ChatStartConfig) { c.Ephemeral = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := isolatedConfig(t)
			tt.change(&cfg)
			if _, err := NewCandidate(nil, nil).Start(context.Background(), cfg); !errors.Is(err, ports.ErrChatUnsupported) {
				t.Fatalf("Start = %v", err)
			}
		})
	}
	for _, s := range []ports.ChatTurnSettings{{Effort: "high"}, {Approval: ports.PermissionModeAuto}, {Approval: ports.PermissionModeAcceptEdits}, {Approval: ports.PermissionModeBypassPermissions}, {Approval: "unknown"}} {
		if err := validateSettings(ports.PermissionModeDefault, s); !errors.Is(err, ports.ErrChatUnsupported) {
			t.Fatalf("settings %v = %v", s, err)
		}
	}
}

func TestResumeNeverUsesFreshFallback(t *testing.T) {
	d := NewCandidate(nil, nil)
	for _, cfg := range []ports.ChatResumeConfig{{}, {ProviderConversationID: " "}, {ProviderConversationID: "native-id", FreshIfMissing: true}} {
		if _, err := d.Resume(context.Background(), cfg); !errors.Is(err, ports.ErrChatResumeFailed) {
			t.Fatalf("Resume = %v", err)
		}
		if _, err := d.(ports.ChatDriverReconnector).Reconnect(context.Background(), cfg); !errors.Is(err, ports.ErrChatResumeFailed) {
			t.Fatalf("Reconnect = %v", err)
		}
	}
}

func TestCandidateRejectsSourceOrSymlinkedHome(t *testing.T) {
	cfg := isolatedConfig(t)
	for _, home := range []string{"", ".", cfg.DataDir, t.TempDir()} {
		if err := validateLaunch(cfg.DataDir, cfg.WorkspacePath, "", map[string]string{"HOME": home}); !errors.Is(err, ports.ErrChatDriverIncompatible) {
			t.Fatalf("HOME %q = %v", home, err)
		}
	}
	link := filepath.Join(cfg.DataDir, "escape")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := validateLaunch(cfg.DataDir, cfg.WorkspacePath, "", map[string]string{"HOME": link}); !errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("symlink escape = %v", err)
	}
}

func TestNativeLaunchAndAdvertisedSettings(t *testing.T) {
	cfg := isolatedConfig(t)
	cfg.Env["USERPROFILE"] = t.TempDir()
	cfg.Env["XDG_CONFIG_HOME"] = t.TempDir()
	launch := acpdriver.LaunchConfig{DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, Model: "provider-owned-model"}
	args, env, err := configure(context.Background(), launch)
	want := []string{"acp", "serve", "--workdir", cfg.WorkspacePath, "--model", "provider-owned-model"}
	if err != nil || !reflect.DeepEqual(args, want) || !reflect.DeepEqual(env, map[string]string{"HOME": cfg.Env["HOME"], "USERPROFILE": cfg.Env["HOME"], "XDG_CONFIG_HOME": filepath.Join(cfg.Env["HOME"], ".config")}) {
		t.Fatalf("configure = %v, %v, %v", args, env, err)
	}
	options := sessionOptions(ports.ChatTurnSettings{Model: launch.Model})
	if !reflect.DeepEqual(options, []acpdriver.SessionOption{{ID: "mode", Value: "standard"}, {ID: "model", Value: launch.Model}}) {
		t.Fatalf("options = %v", options)
	}
}

type recordingDriver struct {
	start     ports.ChatStartConfig
	resume    ports.ChatResumeConfig
	reconnect ports.ChatResumeConfig
	err       error
}

func (d *recordingDriver) Harness() domain.AgentHarness                          { return harness }
func (d *recordingDriver) Probe(context.Context) (ports.ChatCapabilities, error) { return nil, d.err }
func (d *recordingDriver) Start(_ context.Context, c ports.ChatStartConfig) (ports.ChatConversation, error) {
	d.start = c
	return nil, d.err
}
func (d *recordingDriver) Resume(_ context.Context, c ports.ChatResumeConfig) (ports.ChatConversation, error) {
	d.resume = c
	return nil, d.err
}
func (d *recordingDriver) Reconnect(_ context.Context, c ports.ChatResumeConfig) (ports.ChatConversation, error) {
	d.reconnect = c
	return nil, d.err
}

func TestLifecycleDelegatesExactIdentityAndReconnectOnly(t *testing.T) {
	sentinel := errors.New("native failure")
	inner := &recordingDriver{err: sentinel}
	d := &candidate{driver: inner, reconnect: inner}
	cfg := isolatedConfig(t)
	if _, err := d.Start(context.Background(), cfg); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inner.start, cfg) {
		t.Fatal("Start changed native launch context")
	}
	resume := ports.ChatResumeConfig{SessionID: cfg.SessionID, ProviderConversationID: "native-owned-id", DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, HasPriorTurns: true}
	if _, err := d.Resume(context.Background(), resume); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inner.resume, resume) {
		t.Fatal("Resume changed history identity")
	}
	if _, err := d.Reconnect(context.Background(), resume); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	resume.ReconnectOnly = true
	if !reflect.DeepEqual(inner.reconnect, resume) {
		t.Fatal("Reconnect may launch or changed native identity")
	}
}
