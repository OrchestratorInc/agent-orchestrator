package sessionmanager

import (
	"context"
	"fmt"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// integrationChatLauncher is the test-side equivalent of daemon.chatLauncher.
// It keeps the integration tests focused on session-manager boundaries while
// exercising the real Chat service.
type integrationChatLauncher struct{ service *chatsvc.Service }

func (l integrationChatLauncher) SupportsChat(harness domain.AgentHarness) bool {
	return l.service.SupportsChat(harness)
}

func (l integrationChatLauncher) PreflightChat(ctx context.Context, harness domain.AgentHarness, permissions ports.PermissionMode) error {
	return l.service.PreflightChat(ctx, harness, permissions)
}

func (l integrationChatLauncher) StartChat(ctx context.Context, cfg ChatStart) (ChatStarted, error) {
	return l.service.StartChat(ctx, cfg)
}

func (l integrationChatLauncher) StartChatTurn(ctx context.Context, id domain.SessionID, text string) (string, error) {
	return l.service.StartChatTurn(ctx, id, text)
}

func (l integrationChatLauncher) RelaySessionChatTurn(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (string, error) {
	return l.service.RelaySessionChatTurn(ctx, id, text, clientMessageID, options)
}

func (l integrationChatLauncher) HasLiveChatController(id domain.SessionID) bool {
	return l.service.HasLiveChatController(id)
}

func (l integrationChatLauncher) ArmChatHandoff(ctx context.Context, id domain.SessionID, policy domain.SessionInterfaceTransitionPolicy) error {
	return l.service.ArmChatHandoff(ctx, id, policy)
}

func (l integrationChatLauncher) PrepareChatHandoff(ctx context.Context, id domain.SessionID, policy domain.SessionInterfaceTransitionPolicy) error {
	return l.service.PrepareChatHandoff(ctx, id, policy)
}

func (l integrationChatLauncher) AbortChatHandoff(id domain.SessionID) {
	l.service.AbortChatHandoff(id)
}

func (l integrationChatLauncher) StopChat(ctx context.Context, id domain.SessionID) error {
	return l.service.StopChat(ctx, id)
}

func (l integrationChatLauncher) DrainChatQueue(ctx context.Context, id domain.SessionID) error {
	return l.service.DrainQueued(ctx, id)
}

func (l integrationChatLauncher) QueueChatPrompt(ctx context.Context, id domain.SessionID, text string) (string, error) {
	turn, err := l.service.QueueUserMessage(ctx, id, ports.ChatUserMessage{Text: text, Origin: domain.MessageOriginHuman})
	if err != nil {
		return "", err
	}
	return turn.ID, nil
}

type integrationChatConversation struct {
	providerID string
	events     chan ports.ChatEvent
	closeOnce  sync.Once
	mu         sync.Mutex
	sent       []ports.ChatUserMessage
}

func newIntegrationChatConversation(providerID string) *integrationChatConversation {
	return &integrationChatConversation{providerID: providerID, events: make(chan ports.ChatEvent)}
}

func (c *integrationChatConversation) ProviderConversationID() string { return c.providerID }

func (c *integrationChatConversation) Capabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
		ports.ChatCapabilityApprovals: true,
		ports.ChatCapabilityInterrupt: true,
		ports.ChatCapabilityResume:    true,
	}
}

func (c *integrationChatConversation) SendTurn(_ context.Context, message ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, message)
	return ports.ChatTurnRef{ProviderTurnID: fmt.Sprintf("provider-turn-%d", len(c.sent))}, nil
}

func (*integrationChatConversation) Interrupt(context.Context, string) error { return nil }
func (*integrationChatConversation) ResolveRequest(context.Context, string, ports.ChatDecision) error {
	return nil
}
func (c *integrationChatConversation) Events() <-chan ports.ChatEvent { return c.events }
func (c *integrationChatConversation) Close() error {
	c.closeOnce.Do(func() { close(c.events) })
	return nil
}

type integrationChatDriver struct {
	harness domain.AgentHarness
	start   func() ports.ChatConversation
	resume  func() ports.ChatConversation
}

func (d integrationChatDriver) Harness() domain.AgentHarness { return d.harness }
func (integrationChatDriver) Probe(context.Context) (ports.ChatCapabilities, error) {
	return ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
		ports.ChatCapabilityApprovals: true,
		ports.ChatCapabilityInterrupt: true,
		ports.ChatCapabilityResume:    true,
	}, nil
}
func (d integrationChatDriver) Start(context.Context, ports.ChatStartConfig) (ports.ChatConversation, error) {
	return d.start(), nil
}
func (d integrationChatDriver) Resume(context.Context, ports.ChatResumeConfig) (ports.ChatConversation, error) {
	return d.resume(), nil
}

type integrationChatRegistry map[domain.AgentHarness]ports.ChatDriver

func (r integrationChatRegistry) Driver(harness domain.AgentHarness) (ports.ChatDriver, error) {
	driver, ok := r[harness]
	if !ok {
		return nil, ports.ErrChatUnsupported
	}
	return driver, nil
}
func (r integrationChatRegistry) SupportsChat(harness domain.AgentHarness) bool {
	_, ok := r[harness]
	return ok
}

type testAgentRegistry map[domain.AgentHarness]ports.Agent

func (a testAgentRegistry) Agent(harness domain.AgentHarness) (ports.Agent, bool) {
	agent, ok := a[harness]
	return agent, ok
}
