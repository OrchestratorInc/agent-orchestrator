package triage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	chatregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type diagnosticTestDriver struct {
	ports.ChatDriver
	start func(ports.ChatStartConfig) (ports.ChatConversation, error)
}

func (*diagnosticTestDriver) Harness() domain.AgentHarness { return domain.HarnessCodex }
func (d *diagnosticTestDriver) Start(_ context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	return d.start(cfg)
}

type diagnosticCapabilitiesConversation struct {
	*fakeTriageConversation
	caps ports.ChatCapabilities
}

func (c *diagnosticCapabilitiesConversation) Capabilities() ports.ChatCapabilities { return c.caps }

func TestDiagnosticLaunchStagesInstructionsAndPreservesNativeSettings(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_SESSION_ID", "unrelated-session")
	t.Setenv("GH_TOKEN", "native-github-login")
	for _, name := range []string{"ao.db", "config.yaml", "running.json"} {
		if err := os.WriteFile(filepath.Join(dataDir, name), []byte("invalid daemon state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	conv := fakeConversation()
	selection := Selection{Harness: "codex", Model: "selected-model", Effort: "xhigh", Permissions: "auto", SessionMode: "chat"}
	run := Run{ID: "test-run", Request: StartRequest{Selection: selection, Observation: "Observed a crash"}, Evidence: []Evidence{{Text: "Scrubbed evidence"}}}
	driver := &diagnosticTestDriver{start: func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
		if cfg.DataDir != filepath.Join(dataDir, "reports", "triage", "provider-hosts") || cfg.WorkspacePath != filepath.Join(dataDir, "reports", "triage", "runs", run.ID, "workspace") {
			t.Fatalf("wrong private directories: %#v", cfg)
		}
		if cfg.ReadOnly || cfg.DisableNetwork || len(cfg.MCPServers) != 0 || cfg.Model != selection.Model || cfg.Effort != selection.Effort || cfg.Permissions != selection.Permissions || cfg.SystemPrompt != "" {
			t.Fatal("native agent settings changed")
		}
		if cfg.Env["AO_SESSION_ID"] != "" || cfg.Env["GH_TOKEN"] != "native-github-login" || cfg.Env["CODEX_HOME"] != "/selected/account" {
			t.Fatal("native login or delegation environment changed")
		}
		raw, err := os.ReadFile(filepath.Join(cfg.WorkspacePath, "triage.md"))
		if err != nil || !strings.Contains(string(raw), "Keep the human's observations separate") || strings.Contains(string(raw), "Scrubbed evidence") {
			t.Fatalf("staged instructions = %s, %v", raw, err)
		}
		raw, err = os.ReadFile(filepath.Join(cfg.WorkspacePath, "diagnostics.json"))
		if err != nil || !strings.Contains(string(raw), "Observed a crash") || !strings.Contains(string(raw), "/private/logs") || !strings.Contains(string(raw), "Scrubbed evidence") {
			t.Fatalf("staged snapshot = %s, %v", raw, err)
		}
		return conv, nil
	}}
	got, err := startDiagnosticConversation(context.Background(), dataDir, driver, run, map[string]string{"CODEX_HOME": "/selected/account"}, map[string]string{"logs": "/private/logs"})
	if err != nil || got != conv {
		t.Fatalf("launch = %v, %v", got, err)
	}
}

func TestDiagnosticRegistryUsesEveryRegisteredChatDriver(t *testing.T) {
	registry := chatregistry.Build(slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	catalog := NewCatalog(t.TempDir())
	for _, harness := range registry.Harnesses() {
		choices := catalog.filter([]Choice{{Harness: string(harness)}})
		if len(choices) != 1 || choices[0].SessionMode != domain.SessionModeChat {
			t.Fatalf("registered %s excluded: %#v", harness, choices)
		}
	}
	if got := catalog.filter([]Choice{{Harness: "amp"}}); len(got) != 1 || got[0].SessionMode != domain.SessionModeTUI {
		t.Fatalf("terminal-only default = %#v", got)
	}
}

func TestDiagnosticCapabilityAdmissionStopsOnlyInvalidNativeController(t *testing.T) {
	for _, valid := range []bool{false, true} {
		conv := fakeConversation()
		caps := ports.ChatCapabilities{ports.ChatCapabilityStreaming: true}
		if valid {
			caps[ports.ChatCapabilityInterrupt] = true
		}
		driver := &diagnosticTestDriver{start: func(ports.ChatStartConfig) (ports.ChatConversation, error) {
			return &diagnosticCapabilitiesConversation{conv, caps}, nil
		}}
		_, err := startDiagnosticConversation(context.Background(), t.TempDir(), driver, Run{ID: "admission"}, nil, nil)
		if (err == nil) != valid {
			t.Fatalf("capability admission valid %v: %v", valid, err)
		}
		select {
		case <-conv.terminated:
			if valid {
				t.Fatal("valid native controller terminated")
			}
		default:
			if !valid {
				t.Fatal("invalid native controller leaked")
			}
		}
	}
}

func TestDiagnosticEnvKeepsNativeGithubAndSelectedAccountAfterMerge(t *testing.T) {
	t.Setenv("AO_MCP_TOKEN", "private-delegation-value")
	t.Setenv("AO_SESSION", "private-delegation-value")
	t.Setenv("AO_BROWSER_CAPABILITY", "private-delegation-value")
	t.Setenv("AO_PREVIEW_CAPABILITY", "private-delegation-value")
	t.Setenv("GH_TOKEN", "native-github-value")
	env := diagnosticEnv(os.Environ(), map[string]string{"ANTHROPIC_API_KEY": "", "CLAUDE_CONFIG_DIR": "/selected/account"})
	if env["GH_TOKEN"] != "native-github-value" || env["CLAUDE_CONFIG_DIR"] != "/selected/account" {
		t.Fatal("native account was changed")
	}
	for _, entry := range processenv.Merge(env) {
		if strings.Contains(entry, "private-delegation-value") {
			t.Fatal("driver restored cleared delegation")
		}
	}
}

func TestDiagnosticReconciliationIsPrivateBoundedAndPropagatesFailure(t *testing.T) {
	dataDir := t.TempDir()
	want := errors.New("owned provider could not be stopped")
	err := reconcileDiagnosticHosts(context.Background(), dataDir, func(ctx context.Context, root string, keep map[string]struct{}) error {
		if root != filepath.Join(dataDir, "reports", "triage", "provider-hosts") || len(keep) != 0 {
			t.Fatalf("reconciliation escaped ownership: %q %#v", root, keep)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 10*time.Second {
			t.Fatal("cleanup is unbounded")
		}
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("cleanup failure = %v", err)
	}
}

func TestFailedNativeStartClosesPartiallyCreatedController(t *testing.T) {
	conv := fakeConversation()
	want := errors.New("partial native startup")
	driver := &diagnosticTestDriver{start: func(ports.ChatStartConfig) (ports.ChatConversation, error) { return conv, want }}
	got, err := startDiagnosticConversation(context.Background(), t.TempDir(), driver, Run{ID: "partial-start"}, nil, nil)
	if got != nil || !errors.Is(err, want) {
		t.Fatalf("partial start = %v, %v", got, err)
	}
	awaitSignal(t, conv.terminated)
}
