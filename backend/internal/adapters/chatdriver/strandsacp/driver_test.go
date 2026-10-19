package strandsacp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Only CI runs these controlled protocol tests. The helper is not a native
// provider qualification and deliberately implements no model/tool behavior.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--acp-server" {
		os.Exit(runProtocolHelper())
	}
	if len(os.Args) >= 9 && os.Args[1] == "chat-host" && os.Args[7] == "--" {
		err := persistenthost.Run(context.Background(), persistenthost.Config{
			SessionID: os.Args[2], DataDir: os.Args[3], Workdir: os.Args[4],
			Env: os.Environ(), Argv: os.Args[8:], Protocol: persistenthost.ProtocolACP,
			OwnershipFingerprint: os.Args[6],
		})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runProtocolHelper() int {
	log, err := os.OpenFile(os.Getenv("AO_STRANDS_TEST_METHODS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 2
	}
	defer func() { _ = log.Close() }()
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			return 3
		}
		_, _ = fmt.Fprintln(log, request.Method)
		if request.Method == "initialize" {
			_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentInfo":{"name":"@strands-agents/cli","version":"0.0.1"},"agentCapabilities":{"loadSession":true}}}`+"\n", request.ID)
		} else {
			// A candidate which reaches session/new/load is unsafe even if
			// the real provider would return success or a plausible identity.
			return 4
		}
	}
	return 0
}

type plugin struct {
	binary string
	err    error
	status ports.AgentAuthStatus
	calls  int
}

func (p *plugin) ResolveBinary(context.Context) (string, error)             { p.calls++; return p.binary, p.err }
func (p *plugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) { return p.status, nil }

func TestProbeNeverAdvertisesUnqualifiedCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    plugin
		want error
	}{
		{"configured is not qualified", plugin{binary: "/native/strands", status: ports.AgentAuthStatusConfigured}, ports.ErrChatDriverIncompatible},
		{"authorized is not qualified", plugin{binary: "/native/strands", status: ports.AgentAuthStatusAuthorized}, ports.ErrChatDriverIncompatible},
		{"missing native binary", plugin{err: ports.ErrAgentBinaryNotFound}, ports.ErrChatDriverUnavailable},
		{"unauthorized", plugin{binary: "/native/strands", status: ports.AgentAuthStatusUnauthorized}, ports.ErrChatAuthRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caps, err := NewCandidate(&tc.p, nil).Probe(t.Context())
			if !errors.Is(err, tc.want) || caps != nil {
				t.Fatalf("Probe = %v, %v", caps, err)
			}
		})
	}
}

func TestStartAndResumeRejectBeforeCreatingOrLoadingSession(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprintf("resume=%t", resume), func(t *testing.T) {
			dataDir, workspace := t.TempDir(), t.TempDir()
			methods := filepath.Join(dataDir, "methods")
			d := NewCandidate(&plugin{binary: binary}, nil)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			var conv ports.ChatConversation
			if resume {
				conv, err = d.Resume(ctx, ports.ChatResumeConfig{SessionID: "candidate", DataDir: dataDir,
					WorkspacePath: workspace, ProviderConversationID: "exact-native-id", FreshIfMissing: true,
					Env: map[string]string{"AO_STRANDS_TEST_METHODS": methods}})
			} else {
				conv, err = d.Start(ctx, ports.ChatStartConfig{SessionID: "candidate", DataDir: dataDir,
					WorkspacePath: workspace, Env: map[string]string{"AO_STRANDS_TEST_METHODS": methods}})
			}
			if conv != nil || !errors.Is(err, ports.ErrChatDriverIncompatible) {
				t.Fatalf("open = %v, %v", conv, err)
			}
			wire, readErr := os.ReadFile(methods)
			if readErr != nil || strings.TrimSpace(string(wire)) != "initialize" {
				t.Fatalf("wire methods = %q, %v; must stop before native session creation/load", wire, readErr)
			}
		})
	}
}

func TestReconnectDoesNotLaunchReplacement(t *testing.T) {
	p := &plugin{binary: "must-not-be-executed"}
	d := NewCandidate(p, nil)
	conv, err := d.Reconnect(t.Context(), ports.ChatResumeConfig{SessionID: "absent-host", DataDir: t.TempDir(),
		WorkspacePath: t.TempDir(), ProviderConversationID: "exact-native-id"})
	if conv != nil || !errors.Is(err, ports.ErrChatHostNotRunning) || p.calls != 0 {
		t.Fatalf("Reconnect = %v, %v; binary resolutions = %d", conv, err, p.calls)
	}
}

func TestSettingsAndInstructionsAreRejectedWithoutVisibleFallback(t *testing.T) {
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAuto, ports.PermissionModeAcceptEdits, ports.PermissionModeBypassPermissions, "invented"} {
		if err := validateSettings(mode, ports.ChatTurnSettings{}); !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
			t.Fatalf("initial mode %q: %v", mode, err)
		}
		if err := validateSettings("", ports.ChatTurnSettings{Approval: mode}); !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
			t.Fatalf("turn mode %q: %v", mode, err)
		}
	}
	for _, settings := range []ports.ChatTurnSettings{{Model: "provider/model"}, {Effort: "high"}} {
		if err := validateSettings("", settings); !errors.Is(err, ports.ErrChatCapabilityUnavailable) {
			t.Fatalf("settings %v: %v", settings, err)
		}
	}
	cfg := acpdriver.LaunchConfig{DataDir: t.TempDir(), WorkspacePath: t.TempDir(), SystemPrompt: "private instructions"}
	args, _, err := configure(t.Context(), cfg)
	if args != nil || !errors.Is(err, ports.ErrChatCapabilityUnavailable) {
		t.Fatalf("private instructions: %v, %v", args, err)
	}
	cfg.SystemPrompt = ""
	args, env, err := configure(t.Context(), cfg)
	if err != nil || env != nil || len(args) != 5 || args[0] != "--acp-server" || args[4] != "session.dir="+filepath.Join(cfg.DataDir, "agents", "strands", "sessions") {
		t.Fatalf("native launch shape = %v, %v, %v", args, env, err)
	}
}
