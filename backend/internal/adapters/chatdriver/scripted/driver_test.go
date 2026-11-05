package scripted

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The scripted driver is the substrate every integration assertion rests on, so its
// own claims are tested first: if it lied about a capability or a call count, the
// integration test above it would be measuring its own fabrication.

func TestProbeReportsConfiguredCapabilities(t *testing.T) {
	custom := ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
		ports.ChatCapabilitySteer:     true,
	}
	driver := NewDriver(Config{Harness: domain.HarnessCodex, Capabilities: custom})

	caps, err := driver.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !caps.Has(ports.ChatCapabilitySteer) || caps.Has(ports.ChatCapabilityInterrupt) {
		t.Fatalf("caps = %v, want exactly the configured set", caps)
	}

	// An unconfigured driver still meets the production floor, so a test that does not
	// care about capabilities cannot fail admission for an unrelated reason.
	defaulted := NewDriver(Config{Harness: domain.HarnessClaudeCode})
	caps, err = defaulted.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if missing := ports.MissingProductionCapabilities(caps); len(missing) != 0 {
		t.Fatalf("default capabilities miss the production floor: %v", missing)
	}
	if caps.Has(ports.ChatCapabilitySteer) {
		t.Fatal("default capabilities claim steer; a steered default would make the " +
			"unsupported-steer path untestable")
	}
}

func TestProbeFailureIsAnErrorNotANegativeAnswer(t *testing.T) {
	refusal := errors.New("binary missing")
	driver := NewDriver(Config{Harness: domain.HarnessCodex, ProbeErr: refusal})

	caps, err := driver.Probe(context.Background())
	if !errors.Is(err, refusal) {
		t.Fatalf("err = %v, want the configured failure", err)
	}
	if caps != nil {
		t.Fatalf("caps = %v, want nothing: a failed probe observed nothing", caps)
	}
}

func TestSendTurnReturnsATurnRefAndAnnouncesIt(t *testing.T) {
	driver := NewDriver(Config{Harness: domain.HarnessCodex})
	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "hello"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if ref.ProviderTurnID == "" {
		t.Fatal("SendTurn returned no provider turn id")
	}

	// The turn.started event is what lets the controller acknowledge the turn; a
	// driver that returned an id but never announced it could not exercise steering
	// at all, because a provider refuses to steer a turn it has not started.
	select {
	case event := <-conv.Events():
		if event.Kind != ports.ChatEventTurnStarted {
			t.Fatalf("first event = %q, want %q", event.Kind, ports.ChatEventTurnStarted)
		}
		if event.ProviderTurnID != ref.ProviderTurnID {
			t.Fatalf("announced turn = %q, want %q", event.ProviderTurnID, ref.ProviderTurnID)
		}
	case <-context.Background().Done():
		t.Fatal("no event was published")
	}

	if got := driver.SendCalls(); got != 1 {
		t.Fatalf("SendCalls = %d, want 1", got)
	}
	if got := conv.(*Conversation).LastTurnID(); got != ref.ProviderTurnID {
		t.Fatalf("LastTurnID = %q, want %q", got, ref.ProviderTurnID)
	}
}

// A steerable conversation must echo the pinned turn: the caller named a
// precondition and the provider agreed with it.
func TestSteerReturnsThePinnedTurn(t *testing.T) {
	driver := NewDriver(Config{Harness: domain.HarnessCodex, Steerable: true})
	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	steerer, ok := conv.(ports.ChatSteerer)
	if !ok {
		t.Fatal("Steerable driver produced a conversation that does not implement ChatSteerer")
	}

	ref, err := steerer.Steer(context.Background(), "turn-7", ports.ChatUserMessage{Text: "narrow it"})
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if ref.ProviderTurnID != "turn-7" {
		t.Fatalf("steered turn = %q, want turn-7", ref.ProviderTurnID)
	}
	if got := driver.SteerCalls(); got != 1 {
		t.Fatalf("SteerCalls = %d, want 1", got)
	}
}

// The whole reason Steerable is a separate switch from Capabilities: a harness can
// advertise steering and still have no steerer, and a test must be able to build
// that state without pretending the provider is fine.
func TestUnsteerableDriverDoesNotImplementChatSteerer(t *testing.T) {
	driver := NewDriver(Config{
		Harness: domain.HarnessCodex,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityApprovals: true,
			ports.ChatCapabilityInterrupt: true,
			ports.ChatCapabilityResume:    true,
			ports.ChatCapabilitySteer:     true,
		},
	})
	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := conv.(ports.ChatSteerer); ok {
		t.Fatal("a driver with Steerable unset produced a ChatSteerer")
	}
}

// The refusal path: the provider answers "no steerable turn", which is an ordinary
// outcome AO reports as such rather than an internal failure.
func TestSteerCanRefuseWithATypedOutcome(t *testing.T) {
	driver := NewDriver(Config{
		Harness:   domain.HarnessCodex,
		Steerable: true,
		SteerErr:  ports.ErrChatNoSteerableTurn,
	})
	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	steerer := conv.(ports.ChatSteerer)

	_, err = steerer.Steer(context.Background(), "turn-1", ports.ChatUserMessage{Text: "too late"})
	if !errors.Is(err, ports.ErrChatNoSteerableTurn) {
		t.Fatalf("err = %v, want ErrChatNoSteerableTurn", err)
	}
	// The attempt still counted: the test needs to distinguish "AO never asked" from
	// "AO asked and was refused".
	if got := driver.SteerCalls(); got != 1 {
		t.Fatalf("SteerCalls = %d, want the refused attempt recorded once", got)
	}
}

// A provider that answers with a different turn is how AO's mismatch detection is
// exercised: the caller must see the provider's answer, not its own assumption.
func TestSteerCanLandOnADifferentTurn(t *testing.T) {
	driver := NewDriver(Config{
		Harness:    domain.HarnessCodex,
		Steerable:  true,
		SteerLands: "somebody-elses-turn",
	})
	conv, _ := driver.Start(context.Background(), ports.ChatStartConfig{})

	ref, err := conv.(ports.ChatSteerer).Steer(
		context.Background(), "turn-1", ports.ChatUserMessage{Text: "narrow it"})
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if ref.ProviderTurnID != "somebody-elses-turn" {
		t.Fatalf("steered turn = %q, want the configured different turn",
			ref.ProviderTurnID)
	}
}

func TestInterruptEndsTheTurn(t *testing.T) {
	driver := NewDriver(Config{Harness: domain.HarnessCodex})
	conv, _ := driver.Start(context.Background(), ports.ChatStartConfig{})
	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "long job"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	<-conv.Events()

	if err := conv.Interrupt(context.Background(), ref.ProviderTurnID); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if got := driver.InterruptCalls(); got != 1 {
		t.Fatalf("InterruptCalls = %d, want 1", got)
	}
	// Honouring a stop means announcing the turn ended, which is what settles the
	// durable row instead of leaving a Working bar nobody can dismiss.
	select {
	case event := <-conv.Events():
		if event.Kind != ports.ChatEventTurnCompleted {
			t.Fatalf("event after interrupt = %q, want %q",
				event.Kind, ports.ChatEventTurnCompleted)
		}
	default:
		t.Fatal("interrupt published no completion")
	}

	if err := conv.Interrupt(context.Background(), ""); !errors.Is(err, ports.ErrChatNoActiveTurn) {
		t.Fatalf("empty-turn interrupt err = %v, want ErrChatNoActiveTurn", err)
	}
}

func TestRegistryServesExactlyItsDrivers(t *testing.T) {
	codex := NewDriver(Config{Harness: domain.HarnessCodex, Steerable: true})
	claude := NewDriver(Config{Harness: domain.HarnessClaudeCode})
	registry := Registry(codex, claude)

	if !registry.SupportsChat(domain.HarnessCodex) || !registry.SupportsChat(domain.HarnessClaudeCode) {
		t.Fatal("registry does not report its own drivers as supported")
	}
	if registry.SupportsChat(domain.HarnessOMP) {
		t.Fatal("registry claims a harness it was never given")
	}
	driver, err := registry.Driver(domain.HarnessCodex)
	if err != nil {
		t.Fatalf("Driver: %v", err)
	}
	if driver != codex {
		t.Fatal("registry returned a different driver than the one registered")
	}
	if _, err := registry.Driver(domain.HarnessOMP); !errors.Is(err, ports.ErrChatUnsupported) {
		t.Fatalf("unregistered harness err = %v, want ErrChatUnsupported", err)
	}
}

// The two drivers must be distinguishable, or a test using both would be measuring
// one harness twice.
func TestTwoHarnessesCarryDifferentCapabilities(t *testing.T) {
	steering := NewDriver(Config{Harness: domain.HarnessCodex, Steerable: true})
	queueing := NewDriver(Config{Harness: domain.HarnessClaudeCode})

	steeringConv, _ := steering.Start(context.Background(), ports.ChatStartConfig{})
	queueingConv, _ := queueing.Start(context.Background(), ports.ChatStartConfig{})

	_, steeringCan := steeringConv.(ports.ChatSteerer)
	_, queueingCan := queueingConv.(ports.ChatSteerer)
	if !steeringCan {
		t.Fatal("the steering harness cannot steer")
	}
	if queueingCan {
		t.Fatal("the queueing harness can steer; the degradation path would be unreachable")
	}
	if steering.Harness() != domain.HarnessCodex || queueing.Harness() != domain.HarnessClaudeCode {
		t.Fatal("the two drivers do not name different harnesses")
	}
}
