package stakpakacp

import (
	"context"
	"errors"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// These port-boundary controls do not qualify the released native executable.
func TestColdResumeNeverCallsNativeLoadOrFreshFallback(t *testing.T) {
	native := &recordingDriver{}
	d := &Driver{native: native}
	for _, fresh := range []bool{false, true} {
		_, err := d.Resume(context.Background(), ports.ChatResumeConfig{ProviderConversationID: "native-id", FreshIfMissing: fresh})
		if !errors.Is(err, ports.ErrChatResumeFailed) {
			t.Fatalf("Resume = %v", err)
		}
	}
	if native.starts != 0 || native.resumes != 0 {
		t.Fatal("cold restore reached native load/fresh fallback")
	}
}

func TestFreshStartRejectsUnimplementedContextBeforeAllocating(t *testing.T) {
	for name, change := range map[string]func(*ports.ChatStartConfig){
		"standing instructions": func(c *ports.ChatStartConfig) { c.SystemPrompt = "private" },
		"read only":             func(c *ports.ChatStartConfig) { c.ReadOnly = true },
		"network isolation":     func(c *ports.ChatStartConfig) { c.DisableNetwork = true },
		"ephemeral":             func(c *ports.ChatStartConfig) { c.Ephemeral = true },
		"extra workspace":       func(c *ports.ChatStartConfig) { c.AdditionalDirectories = []string{"/extra"} },
		"MCP":                   func(c *ports.ChatStartConfig) { c.MCPServers = []ports.ChatMCPServerConfig{{Name: "extra"}} },
		"model override":        func(c *ports.ChatStartConfig) { c.Model = "other" },
		"reasoning":             func(c *ports.ChatStartConfig) { c.Effort = "high" },
		"auto approvals":        func(c *ports.ChatStartConfig) { c.Permissions = ports.PermissionModeAuto },
	} {
		t.Run(name, func(t *testing.T) {
			n := &recordingDriver{}
			d := &Driver{native: n}
			cfg := ports.ChatStartConfig{WorkspacePath: t.TempDir()}
			change(&cfg)
			if _, err := d.Start(context.Background(), cfg); err == nil {
				t.Fatal("unsupported start accepted")
			}
			if n.starts != 0 {
				t.Fatal("unsupported start allocated native process")
			}
		})
	}
}

func TestFreshAndLiveReconnectKeepNativeIdentity(t *testing.T) {
	n := &recordingDriver{conv: &recordingConversation{id: "native-owned-id"}}
	d := &Driver{native: n}
	ctx := context.Background()
	conv, err := d.Start(ctx, ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if conv.ProviderConversationID() != "native-owned-id" || n.starts != 1 {
		t.Fatal("fresh start lost native identity")
	}
	conv, err = d.Reconnect(ctx, ports.ChatResumeConfig{ProviderConversationID: "native-owned-id", WorkspacePath: t.TempDir()})
	if err != nil || conv.ProviderConversationID() != "native-owned-id" || n.reconnects != 1 || !n.lastResume.ReconnectOnly || n.resumes != 0 {
		t.Fatalf("reconnect changed lifecycle: %v, %+v", err, n)
	}
	n.reconnectErr = ports.ErrChatHostNotRunning
	if _, err = d.Reconnect(ctx, ports.ChatResumeConfig{ProviderConversationID: "native-owned-id"}); !errors.Is(err, ports.ErrChatHostNotRunning) {
		t.Fatalf("missing host = %v", err)
	}
	if n.starts != 1 || n.resumes != 0 {
		t.Fatal("missing host fell back to launch/load")
	}
}

func TestConversationMasksNativeOverclaimsAndRejectsRichInput(t *testing.T) {
	n := &recordingConversation{id: "exact-id"}
	c := &conversation{ChatConversation: n}
	for _, capability := range []ports.ChatCapability{ports.ChatCapabilityResume, ports.ChatCapabilityHistory, ports.ChatCapabilityInterrupt, ports.ChatCapabilityImages, ports.ChatCapabilityEmbeddedContext, ports.ChatCapabilityResourceLinks, ports.ChatCapabilityApprovals} {
		if c.Capabilities().Has(capability) {
			t.Fatalf("unqualified capability %s advertised", capability)
		}
	}
	if _, ok := any(c).(ports.ChatHistoryReader); ok {
		t.Fatal("native stub history exposed")
	}
	if _, ok := any(c).(ports.ChatConfigOptionController); ok {
		t.Fatal("unqualified live config exposed")
	}
	if _, err := c.SendTurn(context.Background(), ports.ChatUserMessage{Text: "ordinary", Content: []ports.ChatContent{{}}}); !errors.Is(err, ports.ErrChatCapabilityUnavailable) {
		t.Fatalf("rich input = %v", err)
	}
	if n.sends != 0 {
		t.Fatal("unsupported rich content was silently dropped")
	}
	if _, err := c.SendTurn(context.Background(), ports.ChatUserMessage{Text: "ordinary", Settings: ports.ChatTurnSettings{Model: "other"}}); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("model = %v", err)
	}
	if _, err := c.SendTurn(context.Background(), ports.ChatUserMessage{Text: "ordinary"}); err != nil || n.sends != 1 {
		t.Fatalf("plain turn = %v", err)
	}
	if err := c.Interrupt(context.Background(), "turn"); !errors.Is(err, ports.ErrChatCapabilityUnavailable) {
		t.Fatalf("interrupt = %v", err)
	}
	if n.interrupts != 0 {
		t.Fatal("unsafe native interrupt dispatched")
	}
	decision := ports.ChatDecision{ID: "reject"}
	if err := c.ResolveRequest(context.Background(), "permission", decision); err != nil || n.requestID != "permission" || n.decision.ID != "reject" {
		t.Fatal("native decision changed")
	}
	if err := c.Close(); err != nil || n.closed != 1 {
		t.Fatal("detach not delegated")
	}
	if !c.PreservesProviderOnClose() {
		t.Fatal("persistent-host detach semantics lost")
	}
	if err := c.Terminate(); err != nil || n.terminated != 1 {
		t.Fatal("explicit Kill not delegated")
	}
}

func TestConfigureRequiresExplicitOwnedProfile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "profile")
	cfg := launchConfig(root, home)
	if _, _, err := configure(ctx, cfg); err == nil {
		t.Fatal("missing staged profile accepted")
	}
}

type recordingDriver struct {
	starts, resumes, reconnects int
	lastResume                  ports.ChatResumeConfig
	reconnectErr                error
	conv                        *recordingConversation
}

func (*recordingDriver) Harness() domain.AgentHarness { return "stakpak" }
func (*recordingDriver) Probe(context.Context) (ports.ChatCapabilities, error) {
	return candidateCapabilities(), nil
}
func (d *recordingDriver) Start(context.Context, ports.ChatStartConfig) (ports.ChatConversation, error) {
	d.starts++
	return d.conv, nil
}
func (d *recordingDriver) Resume(context.Context, ports.ChatResumeConfig) (ports.ChatConversation, error) {
	d.resumes++
	return d.conv, nil
}
func (d *recordingDriver) Reconnect(_ context.Context, c ports.ChatResumeConfig) (ports.ChatConversation, error) {
	d.reconnects++
	d.lastResume = c
	return d.conv, d.reconnectErr
}

type recordingConversation struct {
	deferred, discarded, activated, acknowledged string
	id                                           string
	sends, interrupts, closed, terminated        int
	requestID                                    string
	decision                                     ports.ChatDecision
}

func (c *recordingConversation) ProviderConversationID() string { return c.id }
func (*recordingConversation) Capabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{ports.ChatCapabilityResume: true, ports.ChatCapabilityHistory: true, ports.ChatCapabilityImages: true, ports.ChatCapabilityApprovals: true}
}
func (c *recordingConversation) SendTurn(context.Context, ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	c.sends++
	return ports.ChatTurnRef{}, nil
}
func (c *recordingConversation) Interrupt(context.Context, string) error { c.interrupts++; return nil }
func (c *recordingConversation) ResolveRequest(_ context.Context, id string, d ports.ChatDecision) error {
	c.requestID = id
	c.decision = d
	return nil
}
func (*recordingConversation) Events() <-chan ports.ChatEvent { return nil }
func (c *recordingConversation) Close() error                 { c.closed++; return nil }
func (*recordingConversation) PreservesProviderOnClose() bool { return true }
func (c *recordingConversation) Terminate() error             { c.terminated++; return nil }

func launchConfig(root, home string) acpdriver.LaunchConfig {
	return acpdriver.LaunchConfig{DataDir: root, Env: map[string]string{"HOME": home}}
}
func TestConfigureUsesOnlyStagedOwnedNativeProfile(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "profile")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, "config.toml")
	if err := os.WriteFile(config, []byte("# native settings staged by owner\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args, env, err := configure(context.Background(), launchConfig(root, home))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args, []string{"--config", filepath.Join(resolved, "config.toml"), "acp"}) || env["HOME"] != resolved || env["USERPROFILE"] != resolved {
		t.Fatalf("unexpected launch: %v, %v", args, env)
	}
	if _, _, err := configure(context.Background(), launchConfig(root, t.TempDir())); err == nil {
		t.Fatal("external source profile accepted")
	}
	if _, _, err := configure(context.Background(), launchConfig(root, root)); err == nil {
		t.Fatal("AO data root accepted as native home")
	}
}
func TestConstructorProbeKeepsCandidateBelowProductionFloor(t *testing.T) {
	d := New(testPlugin{}, nil)
	caps, err := d.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ports.MissingProductionCapabilities(caps)) == 0 {
		t.Fatal("candidate clears production floor")
	}
}

type testPlugin struct{}

func (testPlugin) ResolveBinary(context.Context) (string, error) { return "/isolated/stakpak", nil }
func (testPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusUnknown, nil
}

func (c *recordingConversation) StartDeferredTurn(id string) error { c.deferred = id; return nil }
func (c *recordingConversation) DiscardDeferredTurn(id string)     { c.discarded = id }
func (*recordingConversation) ReconnectedLive() bool               { return true }
func (c *recordingConversation) ActivateLiveReconnect(_ context.Context, id string) error {
	c.activated = id
	return nil
}
func (c *recordingConversation) AcknowledgeProviderEvent(_ context.Context, id string) error {
	c.acknowledged = id
	return nil
}
func TestConversationRetainsDurableTransportLifecycle(t *testing.T) {
	n := &recordingConversation{}
	c := &conversation{ChatConversation: n}
	ctx := context.Background()
	if err := c.StartDeferredTurn("turn"); err != nil || n.deferred != "turn" {
		t.Fatal("deferred dispatch lost")
	}
	c.DiscardDeferredTurn("discard")
	if n.discarded != "discard" {
		t.Fatal("discard lost")
	}
	if !c.ReconnectedLive() {
		t.Fatal("live host identity lost")
	}
	if err := c.ActivateLiveReconnect(ctx, "turn"); err != nil || n.activated != "turn" {
		t.Fatal("replay activation lost")
	}
	if err := c.AcknowledgeProviderEvent(ctx, "event"); err != nil || n.acknowledged != "event" {
		t.Fatal("durable acknowledgement lost")
	}
}
