package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type usageLimitAccountReaderFunc func(context.Context, domain.SessionID, domain.AgentHarness, bool) (string, domain.AgentAccountLimits, error)

func (f usageLimitAccountReaderFunc) SessionLimits(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, refresh bool) (string, domain.AgentAccountLimits, error) {
	return f(ctx, id, harness, refresh)
}

func TestUsageLimitAccountDeadlineRefreshAcrossProviders(t *testing.T) {
	for _, provider := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		t.Run(string(provider), func(t *testing.T) {
			h := newHarnessForHarness(t, provider)
			ctx := context.Background()
			source := failUsageLimitedTurn(t, h, timedUsageLimit)
			later := h.now().Add(5 * time.Minute)
			calls := 0
			h.svc.SetUsageLimitAccounts(usageLimitAccountReaderFunc(func(readCtx context.Context, id domain.SessionID, got domain.AgentHarness, refresh bool) (string, domain.AgentAccountLimits, error) {
				calls++
				deadline, ok := readCtx.Deadline()
				if id != testSession || got != provider || !refresh || !ok || time.Until(deadline) > 10*time.Second {
					t.Fatalf("account refresh is not scoped and bounded: %s, %s, %v, %v", id, got, refresh, deadline)
				}
				limits := domain.AgentAccountLimits{FetchedAt: h.now(),
					Windows: []domain.AgentAccountLimitWindow{{ID: "five_hour", UsedPercent: 100}}}
				if calls <= 2 {
					limits.AutoResumeResetsAt = &later
				}
				return "bound-account", limits, nil
			}))
			if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
				t.Fatal(err)
			}
			if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil || calls != 1 || len(h.conv.sentMessages()) != 1 {
				t.Fatalf("pre-deadline early reset check: %d, %v", calls, err)
			}
			h.advance(time.Minute)
			if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
				t.Fatal(err)
			}
			stored, err := h.st.TurnByID(ctx, source.ID)
			if err != nil || stored.UsageLimit == nil || !stored.UsageLimit.AutoResume ||
				!stored.UsageLimit.ResetsAt.Equal(later) || len(h.conv.sentMessages()) != 1 {
				t.Fatalf("updated account deadline was not persisted: %+v, %v", stored.UsageLimit, err)
			}
			if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil || calls != 2 {
				t.Fatalf("postponed deadline was polled: %d, %v", calls, err)
			}
			h.advance(4 * time.Minute)
			if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil || len(h.conv.sentMessages()) != 2 {
				t.Fatalf("extended deadline did not resume once: %v", err)
			}
		})
	}
}

func TestUsageLimitAccountRefreshKeepsKnownResetFallback(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"ambiguous account windows", nil},
		{"probe unavailable", errors.New("provider temporarily unavailable")},
		{"probe deadline", context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			source := failUsageLimitedTurn(t, h, timedUsageLimit)
			h.svc.SetUsageLimitAccounts(usageLimitAccountReaderFunc(func(context.Context, domain.SessionID, domain.AgentHarness, bool) (string, domain.AgentAccountLimits, error) {
				return "bound-account", domain.AgentAccountLimits{}, tc.err
			}))
			if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
				t.Fatal(err)
			}
			h.advance(time.Minute)
			if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil || len(h.conv.sentMessages()) != 2 {
				t.Fatalf("trusted turn reset was lost after optional probe: %v", err)
			}
		})
	}
}

func TestUsageLimitAccountRefreshCannotRearmManualRetry(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	later := h.now().Add(time.Hour)
	h.svc.SetUsageLimitAccounts(usageLimitAccountReaderFunc(func(context.Context, domain.SessionID, domain.AgentHarness, bool) (string, domain.AgentAccountLimits, error) {
		if _, err := h.svc.RetryTurn(ctx, testSession, source.ID); err != nil {
			t.Fatal(err)
		}
		return "bound-account", domain.AgentAccountLimits{AutoResumeResetsAt: &later}, nil
	}))
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || stored.UsageLimit == nil || stored.UsageLimit.AutoResume || stored.UsageLimit.ResumeError != "" || len(h.conv.sentMessages()) != 2 {
		t.Fatalf("manual retry intent was overwritten: %+v, %v", stored.UsageLimit, err)
	}
}

func TestUsageLimitAccountChangeStopsScheduledRetry(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	h.svc.SetUsageLimitAccounts(usageLimitAccountReaderFunc(func(context.Context, domain.SessionID, domain.AgentHarness, bool) (string, domain.AgentAccountLimits, error) {
		return "", domain.AgentAccountLimits{}, ports.ErrAgentAccountChanged
	}))
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || stored.UsageLimit == nil || stored.UsageLimit.AutoResume || stored.UsageLimit.ResumeError == "" || len(h.conv.sentMessages()) != 1 {
		t.Fatalf("account change did not stop recovery: %+v, %v", stored.UsageLimit, err)
	}
}

func TestUsageLimitAccountRefreshCannotRearmConsumedConsent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	later := h.now().Add(time.Hour)
	h.svc.SetUsageLimitAccounts(usageLimitAccountReaderFunc(func(context.Context, domain.SessionID, domain.AgentHarness, bool) (string, domain.AgentAccountLimits, error) {
		// Account binding consumes consent independently of the scheduler lock.
		if err := h.st.SetTurnAutoResume(ctx, source.ConversationID, source.ID, nil, false, ""); err != nil {
			t.Fatal(err)
		}
		return "bound-account", domain.AgentAccountLimits{AutoResumeResetsAt: &later}, nil
	}))
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || stored.UsageLimit == nil || stored.UsageLimit.AutoResume ||
		!stored.UsageLimit.ResetsAt.Equal(*source.UsageLimit.ResetsAt) || len(h.conv.sentMessages()) != 1 {
		t.Fatalf("account refresh recreated consumed intent: %+v, %v", stored.UsageLimit, err)
	}
}

func TestUsageLimitAccountResetResumesBeforeDeadline(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	reset := h.now().Add(2 * time.Hour)
	source := failUsageLimitedTurnAt(t, h, "Usage limit reached", reset)
	used, readErr, reads := 100.0, error(nil), 0
	h.svc.SetUsageLimitAccounts(usageLimitAccountReaderFunc(func(_ context.Context, _ domain.SessionID, _ domain.AgentHarness, refresh bool) (string, domain.AgentAccountLimits, error) {
		reads++
		if !refresh {
			t.Fatal("early reset check used a cached reading")
		}
		return "bound-account", domain.AgentAccountLimits{FetchedAt: h.now(),
			Windows: []domain.AgentAccountLimitWindow{{ID: "five_hour", UsedPercent: used}}}, readErr
	}))
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	resume := func(wantSent, wantReads int, msg string) {
		t.Helper()
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil || len(h.conv.sentMessages()) != wantSent || reads != wantReads {
			t.Fatalf("%s: sent=%d reads=%d err=%v", msg, len(h.conv.sentMessages()), reads, err)
		}
	}
	resume(1, 1, "resumed while the account was still limited")
	used, readErr = 2, errors.New("claude ended without answering get_usage")
	resume(1, 1, "read again inside the early-check interval")
	h.advance(5 * time.Minute)
	resume(1, 2, "resumed on a failed reading")
	stored, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || stored.UsageLimit == nil || !stored.UsageLimit.ResetsAt.Equal(reset) {
		t.Fatalf("failed reading moved the deadline: %+v, %v", stored.UsageLimit, err)
	}
	readErr = nil
	h.advance(5 * time.Minute)
	resume(2, 3, "did not resume after the account reset")
	stored, err = h.st.TurnByID(ctx, source.ID)
	if err != nil || stored.UsageLimit == nil || stored.UsageLimit.AutoResume {
		t.Fatalf("consent was not consumed: %+v, %v", stored.UsageLimit, err)
	}
}
