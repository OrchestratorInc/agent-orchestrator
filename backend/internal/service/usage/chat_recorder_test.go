package usage

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

func chatRecorderSession(t *testing.T, harness domain.AgentHarness) (*ChatUsageRecorder, domain.SessionID, func() []domain.UsageModelAggregate) {
	t.Helper()
	store := collectorTestStore(t)
	now := time.Now().UTC()
	session, err := store.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: "usage-test", Kind: domain.KindWorker, Harness: harness,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now,
	})
	mustNoError(t, err)
	recorder := NewChatUsageRecorder(store, pricing.NewManager(backfillTestSnapshot(t, "0.000001")))
	aggregates := func() []domain.UsageModelAggregate {
		t.Helper()
		rows, err := store.ListUsageModelAggregates(context.Background(), session.ID)
		mustNoError(t, err)
		return rows
	}
	t.Cleanup(func() {
		watchable, err := store.ListWatchableUsageSources(context.Background())
		mustNoError(t, err)
		for _, source := range watchable {
			if source.Kind == domain.UsageSourceChatTurn {
				t.Errorf("chat-turn source %+v is offered to the transcript watcher", source)
			}
		}
	})
	return recorder, session.ID, aggregates
}

func chatTurnReport(sessionID domain.SessionID, harness domain.AgentHarness, turnID string, uncached int64) ports.ChatTurnUsageReport {
	return ports.ChatTurnUsageReport{
		SessionID: sessionID, Harness: harness, ConversationID: "conversation-1",
		ProviderTurnID: turnID, ModelID: "gpt-test",
		Usage: ports.ChatTurnUsage{UncachedInputTokens: uncached, OutputTokens: 7, TotalTokens: uncached + 7},
		At:    time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC),
	}
}

func TestChatUsageRecorderStoresEachTurnOncePricedByCatalog(t *testing.T) {
	recorder, sessionID, aggregates := chatRecorderSession(t, domain.HarnessOpenCode)
	ctx := context.Background()
	first := chatTurnReport(sessionID, domain.HarnessOpenCode, "turn-1", 1000)
	mustNoError(t, recorder.Record(ctx, first))
	// A replayed report of the same turn is the same bill.
	mustNoError(t, recorder.Record(ctx, first))
	// A retry that reports different counts for the same turn is not counted again.
	retried := chatTurnReport(sessionID, domain.HarnessOpenCode, "turn-1", 5000)
	mustNoError(t, recorder.Record(ctx, retried))
	mustNoError(t, recorder.Record(ctx, chatTurnReport(sessionID, domain.HarnessOpenCode, "turn-2", 1000)))

	rows := aggregates()
	if len(rows) != 1 {
		t.Fatalf("aggregates = %+v, want one model row", rows)
	}
	row := rows[0]
	if row.Harness != domain.HarnessOpenCode || row.ModelID != "gpt-test" || row.Cost.EventCount != 2 {
		t.Fatalf("aggregate = %+v, want two opencode gpt-test events", row)
	}
	if row.Tokens.InputTokens == nil || *row.Tokens.InputTokens != 2000 || row.Tokens.OutputTokens == nil || *row.Tokens.OutputTokens != 14 {
		t.Fatalf("tokens = %+v, want 2000 input and 14 output", row.Tokens)
	}
	// 1000 uncached input tokens at $0.000001 each, twice.
	if row.Cost.PricedEventCount != 2 || row.Cost.PricedTotalNanos != 2_000_000 || row.Cost.InferredCostEventCount != 2 {
		t.Fatalf("cost = %+v, want two catalog-priced events with an inferred provider", row.Cost)
	}
}

func TestChatUsageRecorderKeepsProviderReportedCost(t *testing.T) {
	recorder, sessionID, aggregates := chatRecorderSession(t, domain.HarnessDroid)
	report := chatTurnReport(sessionID, domain.HarnessDroid, "turn-1", 1000)
	report.ModelID = "a-model-no-catalog-lists"
	cost := 0.25
	report.Usage.Cost = &cost
	report.Usage.Currency = "USD"
	mustNoError(t, recorder.Record(context.Background(), report))

	rows := aggregates()
	if len(rows) != 1 || rows[0].Cost.PricedTotalNanos != 250_000_000 || rows[0].Cost.ObservedCostEventCount != 1 {
		t.Fatalf("aggregates = %+v, want the provider's $0.25 kept as reported", rows)
	}
}

func TestChatUsageRecorderIgnoresHarnessesWithNativeUsage(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessQwen, domain.HarnessPi, domain.HarnessKimi} {
		t.Run(string(harness), func(t *testing.T) {
			recorder, sessionID, aggregates := chatRecorderSession(t, harness)
			mustNoError(t, recorder.Record(context.Background(), chatTurnReport(sessionID, harness, "turn-1", 1000)))
			if rows := aggregates(); len(rows) != 0 {
				t.Fatalf("aggregates = %+v, want none: the native parser owns this harness", rows)
			}
		})
	}
}

func TestChatReportedCostNanos(t *testing.T) {
	cost := 0.0123
	if got := chatReportedCostNanos(&cost, "usd"); got == nil || *got != 12_300_000 {
		t.Fatalf("USD cost = %v, want 12300000 nanos", got)
	}
	if got := chatReportedCostNanos(&cost, "EUR"); got != nil {
		t.Fatalf("EUR cost = %v, want none", *got)
	}
	zero := 0.0
	if got := chatReportedCostNanos(&zero, "USD"); got != nil {
		t.Fatalf("zero cost = %v, want none so the catalog prices the turn", *got)
	}
}
