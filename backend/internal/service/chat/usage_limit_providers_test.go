package chat_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// Adapter wire tests cover each provider's native error normalization. These
// cases cover the shared durable recovery boundary for both session harnesses.
func TestUsageLimitRecoveryAcrossProviders(t *testing.T) {
	for _, provider := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		t.Run(string(provider), func(t *testing.T) {
			h := newHarnessForHarness(t, provider)
			ctx := context.Background()
			reset := h.now().Add(time.Minute)
			message := timedUsageLimit
			if provider == domain.HarnessCodex {
				message = fmt.Sprintf("Usage limit exceeded\n\nYou've hit your usage limit.\n\nResets at %s", reset.Format(time.RFC3339))
			}
			source := failUsageLimitedTurn(t, h, message)
			if source.UsageLimit == nil || source.UsageLimit.ResetsAt == nil || !source.UsageLimit.ResetsAt.Equal(reset) {
				t.Fatalf("provider deadline = %+v, want %s", source.UsageLimit, reset)
			}
			if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
				t.Fatal(err)
			}
			if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
				t.Fatal(err)
			}
			if len(h.conv.sentMessages()) != 1 {
				t.Fatal("provider resumed before reset")
			}
			h.advance(time.Minute)
			for range 2 {
				if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if len(h.conv.sentMessages()) != 2 {
				t.Fatal("provider did not dispatch exactly one retry at reset")
			}
			stored, err := h.st.TurnByID(ctx, source.ID)
			if err != nil || stored.UsageLimit == nil || stored.UsageLimit.AutoResume || stored.UsageLimit.ResumeError != "" {
				t.Fatalf("consumed provider intent = %+v, %v", stored.UsageLimit, err)
			}
			retryID, found, err := h.st.RetryTurnIDForSource(ctx, source.ConversationID, source.ID)
			if err != nil || !found || retryID == source.ID {
				t.Fatalf("provider retry link = %q, %v, %v", retryID, found, err)
			}
		})
	}
}

func TestUsageLimitUnknownResetAcrossProviders(t *testing.T) {
	for _, tc := range []struct {
		provider domain.AgentHarness
		message  string
	}{
		{domain.HarnessClaudeCode, "You've hit your individual spend limit · run /usage-credits to ask your admin for a higher limit"},
		{domain.HarnessCodex, "Usage limit exceeded\n\nYou've hit your usage limit. Try again at Aug 3rd, 2026 5:15 PM."},
	} {
		t.Run(string(tc.provider), func(t *testing.T) {
			h := newHarnessForHarness(t, tc.provider)
			ctx := context.Background()
			source := failUsageLimitedTurn(t, h, tc.message)
			if source.UsageLimit == nil || source.UsageLimit.ResetsAt != nil {
				t.Fatalf("provider reset was guessed: %+v", source.UsageLimit)
			}
			if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); !errors.Is(err, chatsvc.ErrUsageLimitResetUnknown) {
				t.Fatalf("unknown reset scheduled: %v", err)
			}
			if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
				t.Fatal(err)
			}
			if len(h.conv.sentMessages()) != 1 {
				t.Fatal("provider resumed without consent or a reset")
			}
			if _, err := h.svc.RetryTurn(ctx, testSession, source.ID); err != nil {
				t.Fatal(err)
			}
			if len(h.conv.sentMessages()) != 2 {
				t.Fatal("provider manual recovery did not dispatch")
			}
		})
	}
}
