package openinterpreteracp

import (
	"context"
	"errors"
	"reflect"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestConfigureRefusesIgnoredInstructionOverrides(t *testing.T) {
	for _, prompt := range []string{"private instructions", "\n "} {
		args, env, err := configure(context.Background(), acpdriver.LaunchConfig{SystemPrompt: prompt})
		if !errors.Is(err, ports.ErrChatDriverIncompatible) || args != nil || env != nil {
			t.Fatalf("instruction admission = %v, %v, %v", args, env, err)
		}
	}
	args, env, err := configure(context.Background(), acpdriver.LaunchConfig{Model: "selected-model"})
	if err != nil || env != nil || !reflect.DeepEqual(args, []string{"acp"}) {
		t.Fatalf("native launch = %v, %v, %v", args, env, err)
	}
}

func TestSettingsRejectUnsupportedPermissionAndEffort(t *testing.T) {
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, "unknown"} {
		if err := validateTurnSettings(mode, ports.ChatTurnSettings{Approval: mode}); !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
			t.Fatalf("mode %q: %v", mode, err)
		}
	}
	for _, mode := range []ports.PermissionMode{"", ports.PermissionModeDefault, ports.PermissionModeBypassPermissions} {
		if err := validateTurnSettings(mode, ports.ChatTurnSettings{Approval: mode, Effort: "high"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateTurnSettings(ports.PermissionModeDefault, ports.ChatTurnSettings{Effort: "invented"}); err == nil {
		t.Fatal("accepted unsupported reasoning effort")
	}
	if sessionMode(ports.PermissionModeDefault) != "workspace-write" || sessionMode(ports.PermissionModeBypassPermissions) != "full-access" {
		t.Fatal("native mode mapping changed")
	}
	want := []acpdriver.SessionOption{{ID: "model", Value: "model-a"}, {ID: "reasoning_effort", Value: "high"}}
	if got := sessionOptions(ports.ChatTurnSettings{Model: "model-a", Effort: "high"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %#v", got)
	}
}

func TestInitializeRequiresExactReleasedProduct(t *testing.T) {
	for _, tt := range []struct {
		name, version, title string
		ok                   bool
	}{
		{"codex-acp", "0.0.56", "Open Interpreter", true},
		{"codex-acp", "0.0.57", "Open Interpreter", false},
		{"codex-acp", "0.0.56", "Codex", false},
		{"other", "0.0.56", "Open Interpreter", false},
	} {
		title := tt.title
		err := validateInitialize(acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: tt.name, Version: tt.version, Title: &title}})
		if (err == nil) != tt.ok {
			t.Fatalf("initialize %#v: %v", tt, err)
		}
	}
	if err := validateInitialize(acpsdk.InitializeResponse{}); err == nil {
		t.Fatal("accepted absent native identity")
	}
}

func TestResumeDoesNotLaunchOrFallBackWhenNativeGuardFails(t *testing.T) {
	p := &guardPlugin{err: errors.New("native workspace mismatch")}
	d := New(p, nil)
	_, err := d.Resume(context.Background(), ports.ChatResumeConfig{WorkspacePath: t.TempDir(), ProviderConversationID: "stored-id", FreshIfMissing: true})
	if !errors.Is(err, ports.ErrChatResumeFailed) || p.resolved {
		t.Fatalf("resume = %v; binary resolved = %v", err, p.resolved)
	}
}

type guardPlugin struct {
	err      error
	resolved bool
}

func (p *guardPlugin) ResolveBinary(context.Context) (string, error) {
	p.resolved = true
	return "", errors.New("must not launch")
}
func (*guardPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusUnknown, nil
}
func (p *guardPlugin) ValidateNativeRestore(context.Context, map[string]string, string, string) error {
	return p.err
}

func TestPreparedRestoreGuardsTheActualReplacementProfile(t *testing.T) {
	p := &environmentGuard{}
	base := &recordingDriver{}
	d := &driver{ChatDriver: base, plugin: p}
	calls := 0
	_, err := d.Resume(context.Background(), ports.ChatResumeConfig{
		Env: map[string]string{"INTERPRETER_HOME": "old-valid"},
		PrepareEnv: func(context.Context) (map[string]string, error) {
			calls++
			return map[string]string{"INTERPRETER_HOME": "new-missing"}, nil
		},
		ProviderConversationID: "stored-id", FreshIfMissing: true,
	})
	if !errors.Is(err, ports.ErrChatResumeFailed) || calls != 1 || p.home != "new-missing" || base.fresh {
		t.Fatalf("restore=%v callback=%d guarded=%q fallback=%v", err, calls, p.home, base.fresh)
	}
}

func TestReconnectDelegatesWithoutHistoryProbeOrCredentialRotation(t *testing.T) {
	base := &recordingDriver{}
	d := &driver{ChatDriver: base, plugin: &guardPlugin{err: errors.New("history guard must not run")}}
	_, err := d.Resume(context.Background(), ports.ChatResumeConfig{ReconnectOnly: true, PrepareEnv: func(context.Context) (map[string]string, error) {
		t.Fatal("reconnect rotated credentials")
		return nil, nil
	}})
	if !errors.Is(err, ports.ErrChatHostNotRunning) || !base.reconnected {
		t.Fatalf("reconnect = %v, delegated=%v", err, base.reconnected)
	}
}

func TestStandingInstructionsRejectedBeforeHostAdoption(t *testing.T) {
	d := &driver{} // A rejected request must never touch a provider or plugin.
	_, err := d.Start(context.Background(), ports.ChatStartConfig{SystemPrompt: "private"})
	if !errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("start admission = %v", err)
	}
	_, err = d.Reconnect(context.Background(), ports.ChatResumeConfig{SystemPrompt: "renewed private"})
	if !errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("reconnect admission = %v", err)
	}
	_, err = d.Start(context.Background(), ports.ChatStartConfig{ReadOnly: true})
	if !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
		t.Fatalf("read-only admission = %v", err)
	}
}

type environmentGuard struct {
	guardPlugin
	home string
}

func (p *environmentGuard) ValidateNativeRestore(_ context.Context, env map[string]string, _ string, _ string) error {
	p.home = env["INTERPRETER_HOME"]
	if p.home != "old-valid" {
		return errors.New("native history absent")
	}
	return nil
}

type recordingDriver struct {
	ports.ChatDriver
	fresh, reconnected bool
}

func (d *recordingDriver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	d.fresh = cfg.FreshIfMissing
	if cfg.PrepareEnv != nil {
		if _, err := cfg.PrepareEnv(ctx); err != nil {
			return nil, err
		}
	}
	return nil, nil
}
func (d *recordingDriver) Reconnect(context.Context, ports.ChatResumeConfig) (ports.ChatConversation, error) {
	d.reconnected = true
	return nil, ports.ErrChatHostNotRunning
}

func TestCandidateDoesNotAdvertiseKnownBrokenExecutingInterrupt(t *testing.T) {
	d := New(&probePlugin{}, nil)
	caps, err := d.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if caps.Has(ports.ChatCapabilityInterrupt) {
		t.Fatal("candidate advertises interrupt despite released-native child survival")
	}
}

type probePlugin struct{ guardPlugin }

func (*probePlugin) ResolveBinary(context.Context) (string, error) { return "/owned/interpreter", nil }
