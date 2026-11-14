package acp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeSteerChannel struct {
	mu      sync.Mutex
	ready   bool
	outcome SteerOutcome
	err     error
	got     []string
	session []string
}

func (c *fakeSteerChannel) Ready(string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ready
}

func (c *fakeSteerChannel) Steer(_ context.Context, sessionID, text string) (SteerOutcome, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, text)
	c.session = append(c.session, sessionID)
	return c.outcome, c.err
}

func startExternalSteerConversation(t *testing.T, channel *fakeSteerChannel) (*conversation, string) {
	t.Helper()
	agent := &fakeAgent{promptBlock: true}
	acceptImages(agent)
	driver := New(Config{
		Harness:      domain.HarnessPi,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SteerExternal: &SteerExternal{
			Supported: func(acpsdk.InitializeResponse) bool { return true },
			Open:      func(LaunchConfig) SteerChannel { return channel },
			ReadyWait: 50 * time.Millisecond,
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	ref, err := opened.SendTurn(context.Background(), ports.ChatUserMessage{Text: "edit prod.yaml"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := opened.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	return opened.(*conversation), ref.ProviderTurnID
}

func TestACPSteerExternalHandsTextToTheChannel(t *testing.T) {
	channel := &fakeSteerChannel{ready: true, outcome: SteerInjected}
	conv, turn := startExternalSteerConversation(t, channel)
	if !conv.Capabilities()[ports.ChatCapabilitySteer] {
		t.Fatal("Steer not offered although the channel is up")
	}
	ref, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "use staging"})
	if err != nil || ref.ProviderTurnID != turn {
		t.Fatalf("Steer = %+v, %v", ref, err)
	}
	conv.mu.Lock()
	sessionID, segment := conv.sessionID, conv.steerSegment
	conv.mu.Unlock()
	channel.mu.Lock()
	defer channel.mu.Unlock()
	if len(channel.got) != 1 || channel.got[0] != "use staging" || channel.session[0] != sessionID {
		t.Fatalf("channel got %q for %q, want the text for session %q", channel.got, channel.session, sessionID)
	}
	if segment != 1 {
		t.Fatalf("steer segment = %d, want 1", segment)
	}
}

func TestACPSteerExternalNoRunningTurnAndRefusal(t *testing.T) {
	channel := &fakeSteerChannel{ready: true, outcome: SteerNoRunningTurn}
	conv, turn := startExternalSteerConversation(t, channel)
	if _, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "late"}); !errors.Is(err, ports.ErrChatNoSteerableTurn) {
		t.Fatalf("no running turn = %v, want ErrChatNoSteerableTurn", err)
	}
	channel.mu.Lock()
	channel.err = RefuseSteer(errors.New("pi refused"))
	channel.mu.Unlock()
	_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{Text: "x"})
	var refusal interface{ ChatRefusal() bool }
	if err == nil || !errors.As(err, &refusal) || !refusal.ChatRefusal() {
		t.Fatalf("refused steer = %v, want a refusal", err)
	}
}

func TestACPSteerExternalRefusesImagesWithoutCallingTheChannel(t *testing.T) {
	channel := &fakeSteerChannel{ready: true, outcome: SteerInjected}
	conv, turn := startExternalSteerConversation(t, channel)
	_, err := conv.Steer(context.Background(), turn, ports.ChatUserMessage{
		Text:    "look",
		Content: []ports.ChatContent{{Type: "image", Data: "aGVsbG8=", MIMEType: "image/png"}},
	})
	if !errors.Is(err, ports.ErrChatSteerContentUnsupported) {
		t.Fatalf("image steer = %v, want ErrChatSteerContentUnsupported", err)
	}
	channel.mu.Lock()
	defer channel.mu.Unlock()
	if len(channel.got) != 0 {
		t.Fatalf("channel was called for an image steer: %q", channel.got)
	}
}

func TestACPSteerExternalStaysOffWhenTheChannelIsNotUp(t *testing.T) {
	conv, _ := startExternalSteerConversation(t, &fakeSteerChannel{ready: false})
	if conv.Capabilities()[ports.ChatCapabilitySteer] {
		t.Fatal("Steer offered although the channel never came up")
	}
}
