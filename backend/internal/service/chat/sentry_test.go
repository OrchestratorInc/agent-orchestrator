package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type chatFaultTransport struct{ events chan *sentry.Event }

func (*chatFaultTransport) Configure(sentry.ClientOptions)        {}
func (*chatFaultTransport) Flush(time.Duration) bool              { return true }
func (*chatFaultTransport) FlushWithContext(context.Context) bool { return true }
func (*chatFaultTransport) Close()                                {}
func (transport *chatFaultTransport) SendEvent(event *sentry.Event) {
	transport.events <- event
}

func TestCaptureChatFaultExcludesExpectedProviderStates(t *testing.T) {
	transport := &chatFaultTransport{events: make(chan *sentry.Event, 1)}
	if err := sentryobs.Init(sentryobs.Config{
		DSN: "https://public@example.com/1", InstallID: "ins_sampled_5", Release: "1.0.0", Environment: "development", Transport: transport,
	}); err != nil {
		t.Fatal(err)
	}
	sentryobs.SetPolicyCheck(func(context.Context) bool { return true })
	t.Cleanup(func() { sentryobs.SetPolicyCheck(nil) })

	for _, test := range []struct {
		name    string
		err     error
		capture bool
	}{
		{"nil", nil, false},
		{"agent signed out", ports.ErrAgentAuthRequired, false},
		{"signed out", ports.NewChatProviderFailure("Sign in required", "", ports.ErrChatAuthRequired), false},
		{"canceled", context.Canceled, false},
		{"Codex usage limit", ports.NewChatProviderFailure("Usage limit exceeded", "Try again later", errors.New("usageLimitExceeded")), false},
		{"Claude session limit", ports.NewChatProviderFailure("You've hit your session limit", "Resets 10pm (UTC)", nil), false},
		{"Claude quota", ports.NewChatProviderFailure("The Claude account has no available quota.", "", nil), false},
		{"unexpected provider failure", ports.NewChatProviderFailure("Provider connection failed", "", errors.New("unexpected transport failure")), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			captureChatFault(ctx, test.err, domain.AgentHarness("codex"), "chat_provider_error", "")
			if err := sentryobs.Drain(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case event := <-transport.events:
				if !test.capture {
					t.Fatalf("expected provider state captured: %+v", event)
				}
				if event.Tags["operation"] != "chat_provider_error" || event.Tags["component"] != "chat" {
					t.Fatalf("unexpected fault tags: %+v", event.Tags)
				}
			default:
				if test.capture {
					t.Fatal("unexpected provider failure was not captured")
				}
			}
		})
	}
}
