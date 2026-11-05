// Package scripted implements a deterministic offline Chat driver.
//
// It is an adapter, not a provider adaptor: it speaks AO's own ports.ChatDriver
// vocabulary and nothing else. No process is launched, no network call is made, no
// credential is read, and no provider protocol is spoken. Every answer it gives is
// the answer it was configured to give.
//
// It exists so an integration test can drive the REAL Chat service — real SQLite
// store, real controller admission, real idempotency and steering paths — without a
// provider, a token, or a budget. A test that hand-assembles fakes above the service
// proves the test's own arithmetic; a test that runs the service proves the service.
//
// It must never be registered on a live daemon. A daemon wired with this driver
// would report agent work that never happened, so nothing outside tests constructs
// it.
package scripted

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// DefaultCapabilities is the production floor every harness must meet before a
// mutating session may run in Chat mode, plus nothing else. Steering is opt-in
// because "this provider has no steer" is one of the states the Chat service has to
// handle, and a driver that always claimed it could steer could not exercise it.
func DefaultCapabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
		ports.ChatCapabilityApprovals: true,
		ports.ChatCapabilityInterrupt: true,
		ports.ChatCapabilityResume:    true,
	}
}

// ErrConversationClosed reports a turn sent to a conversation whose stream is
// already closed. It is a caller bug in the harness, not a provider outcome, so it
// carries its own sentinel rather than borrowing one that means something else.
var ErrConversationClosed = errors.New("scripted conversation is closed")

// Config describes one scripted harness.
type Config struct {
	// Harness is the agent this driver answers for.
	Harness domain.AgentHarness
	// Capabilities is what Probe and every conversation report. Nil means
	// DefaultCapabilities.
	Capabilities ports.ChatCapabilities
	// Steerable makes conversations implement ports.ChatSteerer. It is separate
	// from Capabilities on purpose: a harness can advertise steering and still have
	// no steerer (a driver mid-migration), and that must be distinguishable.
	Steerable bool
	// SteerErr, when set, is what Steer returns. Used to drive the "provider
	// refuses this turn" path without a provider.
	SteerErr error
	// SteerLands names a different turn than the one requested, so a test can prove
	// the caller sees the provider's answer rather than its own assumption.
	SteerLands string
	// ProbeErr, when set, makes Probe fail. A probe failure is an error, never a
	// negative observation.
	ProbeErr error
}

// Driver is a deterministic ports.ChatDriver.
type Driver struct {
	cfg  Config
	caps ports.ChatCapabilities

	mu     sync.Mutex
	sends  int
	steers int
	stops  int
	probes int
	// conversations lists every conversation this driver opened, oldest first, so a
	// test can settle a turn through it.
	conversations []*Conversation
}

// NewDriver builds a scripted driver for one harness.
func NewDriver(cfg Config) *Driver {
	caps := cfg.Capabilities
	if caps == nil {
		caps = DefaultCapabilities()
	}
	return &Driver{cfg: cfg, caps: caps}
}

// Harness implements ports.ChatDriver.
func (d *Driver) Harness() domain.AgentHarness { return d.cfg.Harness }

// Probe implements ports.ChatDriver. It answers from configuration, which is why a
// test can prove AO's handling of every advertised capability set without asking a
// provider anything.
func (d *Driver) Probe(context.Context) (ports.ChatCapabilities, error) {
	d.mu.Lock()
	d.probes++
	d.mu.Unlock()
	if d.cfg.ProbeErr != nil {
		return nil, d.cfg.ProbeErr
	}
	return d.caps, nil
}

// Start implements ports.ChatDriver.
func (d *Driver) Start(context.Context, ports.ChatStartConfig) (ports.ChatConversation, error) {
	return d.open(), nil
}

// Resume implements ports.ChatDriver. It returns a conversation carrying the
// requested handle, because a scripted driver has no provider-side state to lose.
func (d *Driver) Resume(_ context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	conv := d.open()
	if cfg.ProviderConversationID != "" {
		d.base(conv).setProviderConversationID(cfg.ProviderConversationID)
	}
	return conv, nil
}

// open records a new conversation and returns it in the shape Config asked for.
func (d *Driver) open() ports.ChatConversation {
	d.mu.Lock()
	defer d.mu.Unlock()
	conv := &Conversation{
		driver:                 d,
		events:                 make(chan ports.ChatEvent, 64),
		providerConversationID: fmt.Sprintf("%s-thread-%d", d.cfg.Harness, len(d.conversations)+1),
	}
	d.conversations = append(d.conversations, conv)
	if d.cfg.Steerable {
		return &SteerableConversation{Conversation: conv}
	}
	return conv
}

// SendCalls reports how many turns reached the driver. A test asserting "the
// provider was called once" must be able to prove it, not infer it from timing.
func (d *Driver) SendCalls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sends
}

// SteerCalls reports how many steers reached the driver.
func (d *Driver) SteerCalls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.steers
}

// InterruptCalls reports how many stops reached the driver.
func (d *Driver) InterruptCalls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stops
}

// Conversations returns every conversation opened so far, oldest first.
func (d *Driver) Conversations() []*Conversation {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]*Conversation(nil), d.conversations...)
}

// base unwraps a conversation to the concrete type whether or not it is steerable.
func (d *Driver) base(conv ports.ChatConversation) *Conversation {
	if steerable, ok := conv.(*SteerableConversation); ok {
		return steerable.Conversation
	}
	return conv.(*Conversation)
}

// Conversation is one scripted provider conversation.
//
// It emits exactly one event per accepted turn: turn.started. It deliberately does
// NOT emit turn.completed, because a turn that has already finished cannot be
// steered, and "steer the running turn" is the behaviour under test. A test that
// needs a settled turn calls CompleteTurn, which is the same signal a real provider
// sends when the work ends.
type Conversation struct {
	driver *Driver

	mu                     sync.Mutex
	providerConversationID string
	turns                  int
	lastTurnID             string
	closed                 bool
	closeOnce              sync.Once
	events                 chan ports.ChatEvent
}

// ProviderConversationID implements ports.ChatConversation.
func (c *Conversation) ProviderConversationID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.providerConversationID
}

func (c *Conversation) setProviderConversationID(id string) {
	c.mu.Lock()
	c.providerConversationID = id
	c.mu.Unlock()
}

// Capabilities implements ports.ChatConversation. A conversation reports what it
// negotiated, which for a scripted driver is what it was configured with.
func (c *Conversation) Capabilities() ports.ChatCapabilities { return c.driver.caps }

// SendTurn implements ports.ChatConversation. It accepts the turn, publishes
// turn.started for it, and returns its provider id — the same two facts a real
// provider produces, and the only two the controller needs to bind the turn.
func (c *Conversation) SendTurn(_ context.Context, _ ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	c.driver.mu.Lock()
	c.driver.sends++
	turnID := fmt.Sprintf("%s-turn-%d", c.driver.cfg.Harness, c.driver.sends)
	c.driver.mu.Unlock()

	c.mu.Lock()
	c.turns++
	c.lastTurnID = turnID
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ports.ChatTurnRef{}, ErrConversationClosed
	}

	c.emit(ports.ChatEvent{
		Kind:                   ports.ChatEventTurnStarted,
		ProviderTurnID:         turnID,
		ProviderConversationID: c.ProviderConversationID(),
	})
	return ports.ChatTurnRef{ProviderTurnID: turnID}, nil
}

// Interrupt implements ports.ChatConversation by ending the named turn, which is
// what a provider does when it honours a stop.
func (c *Conversation) Interrupt(_ context.Context, providerTurnID string) error {
	c.driver.mu.Lock()
	c.driver.stops++
	c.driver.mu.Unlock()
	if providerTurnID == "" {
		return ports.ErrChatNoActiveTurn
	}
	c.CompleteTurn(providerTurnID)
	return nil
}

// ResolveRequest implements ports.ChatConversation. A scripted conversation has no
// pending approvals, so answering one is a client bug rather than a provider event.
func (c *Conversation) ResolveRequest(_ context.Context, requestID string, _ ports.ChatDecision) error {
	return fmt.Errorf("%w: %s", ports.ErrChatRequestNotPending, requestID)
}

// Events implements ports.ChatConversation.
func (c *Conversation) Events() <-chan ports.ChatEvent { return c.events }

// Close implements ports.ChatConversation by closing the event stream, which is how
// the controller learns the conversation ended.
func (c *Conversation) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.closeOnce.Do(func() { close(c.events) })
	return nil
}

// SteerableConversation is a Conversation that also implements ports.ChatSteerer.
//
// Steering lives on a separate type rather than on Conversation behind a config
// check because that is exactly how AO models the real thing: a driver either
// implements the interface or it does not. A conversation that implemented Steer
// unconditionally and merely refused at runtime would let a test pass the
// type assertion on a harness that genuinely has no steering, and the "degrade to
// the queue" path would be untestable.
type SteerableConversation struct{ *Conversation }

// Steer implements ports.ChatSteerer.
//
// The returned ref is the provider's answer, not an echo of the request: a provider
// that steered a different turn must be visible to the caller, which is the only way
// AO's mismatch detection can be exercised at all.
func (c *SteerableConversation) Steer(
	_ context.Context,
	providerTurnID string,
	_ ports.ChatUserMessage,
) (ports.ChatTurnRef, error) {
	c.driver.mu.Lock()
	c.driver.steers++
	steerErr := c.driver.cfg.SteerErr
	lands := c.driver.cfg.SteerLands
	c.driver.mu.Unlock()

	if steerErr != nil {
		return ports.ChatTurnRef{}, steerErr
	}
	if lands != "" {
		return ports.ChatTurnRef{ProviderTurnID: lands}, nil
	}
	return ports.ChatTurnRef{ProviderTurnID: providerTurnID}, nil
}

// CompleteTurn publishes turn.completed for one turn, settling it.
func (c *Conversation) CompleteTurn(providerTurnID string) {
	c.emit(ports.ChatEvent{
		Kind:                   ports.ChatEventTurnCompleted,
		ProviderTurnID:         providerTurnID,
		ProviderConversationID: c.ProviderConversationID(),
		TurnState:              domain.TurnStateCompleted,
	})
}

// LastTurnID is the provider turn id of the most recently accepted turn, or "".
func (c *Conversation) LastTurnID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastTurnID
}

// emit delivers one event unless the stream is already closed. Sending on a closed
// channel panics, and a scripted driver that panicked on a late event would turn a
// test ordering quirk into a crash.
func (c *Conversation) emit(event ports.ChatEvent) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	events := c.events
	c.mu.Unlock()
	select {
	case events <- event:
	default:
		// A full buffer means the consumer stopped reading. Dropping the event is
		// honest for a scripted driver: it models a provider that outran its client,
		// not one that blocked its own process.
	}
}
