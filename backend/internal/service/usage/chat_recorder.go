package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

// ChatReportedPricingVersion marks an event whose cost the provider reported.
// It names no catalog, so no catalog activation ever revisits that cost.
const ChatReportedPricingVersion = "provider_reported"

// chatUnknownModel stands in for a turn whose model nobody named. It is in no
// catalog, so the turn keeps its tokens and stays unpriced.
const chatUnknownModel = "unknown"

type chatUsageStore interface {
	GetUsageBinding(context.Context, domain.SessionID, domain.AgentHarness, string) (domain.UsageBindingRecord, bool, error)
	UpsertUsageBinding(context.Context, domain.UsageBindingRecord) (domain.UsageBindingRecord, error)
	ListUsageSourcesForBinding(context.Context, int64) ([]domain.UsageSourceRecord, error)
	InsertUsageSource(context.Context, domain.UsageSourceRecord) (domain.UsageSourceRecord, error)
	ApplyUsageChunk(context.Context, int64, int64, time.Time, domain.SourceCursorState, []domain.ModelUsageEvent) error
}

// ChatUsageRecorder stores the per-turn usage a chat harness reports over AO's
// own chat connection, for harnesses with no native transcript to parse. Each
// conversation gets one binding and one chat_turn source; each billed exchange
// is one event keyed by its turn, so a replayed or retried report is stored
// once.
type ChatUsageRecorder struct {
	store   chatUsageStore
	pricing *pricing.Manager
	now     func() time.Time
	// mu serializes reports: the source cursor is a compare-and-swap guard,
	// and two reports racing for it would make one of them retry.
	mu sync.Mutex
}

// NewChatUsageRecorder builds a recorder. A nil pricing manager stores tokens
// without a billing provider or cost.
func NewChatUsageRecorder(store chatUsageStore, manager *pricing.Manager) *ChatUsageRecorder {
	return &ChatUsageRecorder{store: store, pricing: manager, now: time.Now}
}

// Record stores one reported exchange. Reports for a harness with a native
// usage pipeline are dropped, so no turn is counted twice.
func (r *ChatUsageRecorder) Record(ctx context.Context, report ports.ChatTurnUsageReport) error {
	if r == nil || !ChatReportedHarness(report.Harness) {
		return nil
	}
	conversationID := strings.TrimSpace(report.ConversationID)
	if report.SessionID == "" || conversationID == "" || strings.TrimSpace(report.ProviderTurnID) == "" {
		return nil
	}
	event, err := chatTurnUsageEvent(report)
	if err != nil {
		return err
	}
	costNanos := chatReportedCostNanos(report.Usage.Cost, report.Usage.Currency)

	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().UTC()
	source, err := r.chatSource(ctx, report, conversationID, event.ModelID, now)
	if err != nil {
		return err
	}
	apply := func(snapshot *pricing.Snapshot) error {
		priced := priceChatTurnEvent(snapshot, event, report.Harness, costNanos)
		for attempt := 0; ; attempt++ {
			err := r.store.ApplyUsageChunk(ctx, source.ID, source.ByteOffset, source.UpdatedAt,
				domain.SourceCursorState{
					// The offset counts applied reports; it is only a cursor guard.
					ByteOffset:      source.ByteOffset + 1,
					State:           domain.UsageSourceActive,
					ParserStateJSON: source.ParserStateJSON,
					FailureCount:    source.FailureCount,
					AnomalyCount:    source.AnomalyCount,
					UpdatedAt:       now,
				}, []domain.ModelUsageEvent{priced})
			switch {
			case err == nil:
				return nil
			case errors.Is(err, domain.ErrUsageSourceEventConflict):
				// The turn is already stored with other counts: the first report
				// of a turn is the one that counts.
				return nil
			case attempt == 0 && (errors.Is(err, domain.ErrUsageSourceOffsetConflict) ||
				errors.Is(err, domain.ErrUsageSourceRevisionConflict)):
				if source, err = r.chatSource(ctx, report, conversationID, event.ModelID, now); err != nil {
					return err
				}
			default:
				return fmt.Errorf("record chat usage for session %s turn %q: %w", report.SessionID, report.ProviderTurnID, err)
			}
		}
	}
	if r.pricing == nil {
		return apply(nil)
	}
	return r.pricing.WithSnapshot(ctx, apply)
}

// chatSource returns the conversation's chat_turn source, creating the binding
// and source the first time the conversation reports usage.
func (r *ChatUsageRecorder) chatSource(
	ctx context.Context,
	report ports.ChatTurnUsageReport,
	conversationID, modelID string,
	now time.Time,
) (domain.UsageSourceRecord, error) {
	binding, ok, err := r.store.GetUsageBinding(ctx, report.SessionID, report.Harness, conversationID)
	if err != nil {
		return domain.UsageSourceRecord{}, err
	}
	if !ok {
		initialModel := modelID
		if initialModel == chatUnknownModel {
			initialModel = ""
		}
		binding, err = r.store.UpsertUsageBinding(ctx, domain.UsageBindingRecord{
			SessionID:      report.SessionID,
			Harness:        report.Harness,
			NativeRootID:   conversationID,
			InitialModelID: initialModel,
			State:          domain.UsageBindingActive,
			UpdatedAt:      now,
		})
		if err != nil {
			return domain.UsageSourceRecord{}, err
		}
	}
	artifactPath := domain.ChatTurnArtifactPrefix + conversationID
	sources, err := r.store.ListUsageSourcesForBinding(ctx, binding.ID)
	if err != nil {
		return domain.UsageSourceRecord{}, err
	}
	for index := len(sources) - 1; index >= 0; index-- {
		if sources[index].Kind == domain.UsageSourceChatTurn && sources[index].ArtifactPath == artifactPath {
			return sources[index], nil
		}
	}
	return r.store.InsertUsageSource(ctx, domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            domain.UsageSourceChatTurn,
		NativeSessionID: conversationID,
		ArtifactPath:    artifactPath,
		ParserStateJSON: "{}",
		State:           domain.UsageSourceActive,
		UpdatedAt:       now,
	})
}

// chatTurnProviderUsage is the bounded usage object stored with a chat event:
// the adapter's normalized counts, plus the cost it reported.
type chatTurnProviderUsage struct {
	CachedReadTokens    int64    `json:"cachedReadTokens"`
	CachedWriteTokens   *int64   `json:"cachedWriteTokens,omitempty"`
	UncachedInputTokens int64    `json:"uncachedInputTokens"`
	OutputTokens        int64    `json:"outputTokens"`
	ThoughtTokens       *int64   `json:"thoughtTokens,omitempty"`
	TotalTokens         int64    `json:"totalTokens"`
	Cost                *float64 `json:"cost,omitempty"`
	Currency            string   `json:"currency,omitempty"`
}

func chatTurnUsageEvent(report ports.ChatTurnUsageReport) (domain.ModelUsageEvent, error) {
	turn := report.Usage
	cached := max(turn.CachedInputTokens, 0)
	uncached := max(turn.UncachedInputTokens, 0)
	output := max(turn.OutputTokens, 0)
	if cached > math.MaxInt64-uncached {
		return domain.ModelUsageEvent{}, errors.New("chat usage input tokens overflow")
	}
	input := cached + uncached
	providerUsage := chatTurnProviderUsage{
		CachedReadTokens:    cached,
		CachedWriteTokens:   turn.CacheWriteTokens,
		UncachedInputTokens: uncached,
		OutputTokens:        output,
		ThoughtTokens:       turn.ReasoningTokens,
		TotalTokens:         max(turn.TotalTokens, 0),
	}
	if turn.Cost != nil && !math.IsNaN(*turn.Cost) && !math.IsInf(*turn.Cost, 0) {
		cost := *turn.Cost
		providerUsage.Cost = &cost
		providerUsage.Currency = strings.TrimSpace(turn.Currency)
	}
	raw, err := json.Marshal(providerUsage)
	if err != nil {
		return domain.ModelUsageEvent{}, fmt.Errorf("encode chat provider usage: %w", err)
	}
	model := strings.TrimSpace(report.ModelID)
	if model == "" {
		model = chatUnknownModel
	}
	key := "turn:" + strings.TrimSpace(report.ProviderTurnID)
	if responseID := strings.TrimSpace(turn.ResponseID); responseID != "" {
		key += "/" + responseID
	}
	createdAt := report.At
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return domain.ModelUsageEvent{
		ProviderID:      domain.UsageProviderOpenAI,
		ModelID:         model,
		MeasurementKind: domain.UsageMeasurementNativeReported,
		Tokens: domain.UsageTokenMetrics{
			InputTokens:         &input,
			CachedInputTokens:   &cached,
			UncachedInputTokens: &uncached,
			OutputTokens:        &output,
		},
		ProviderUsageJSON: string(raw),
		CreatedAt:         createdAt.UTC(),
		SourceEventKey:    key,
	}, nil
}

// priceChatTurnEvent attributes and prices one chat event. The billing provider
// is inferred from the model, since a chat connection names no route. A cost
// the provider reported is kept as reported; otherwise the catalog prices the
// tokens, now or when a later catalog can.
func priceChatTurnEvent(
	snapshot *pricing.Snapshot,
	event domain.ModelUsageEvent,
	harness domain.AgentHarness,
	costNanos *int64,
) domain.ModelUsageEvent {
	if provider := snapshot.ProviderForModel(event.ModelID); provider != "" {
		event.BillingProviderID = provider
		event.BillingProviderSource = domain.UsageBillingProviderInferred
	}
	if event.BillingProviderID == "anthropic" {
		// Anthropic's catalog rows read tiers and cache writes in its own
		// vocabulary; every other provider reads OpenAI's.
		event.ProviderID = domain.UsageProviderAnthropic
	}
	if costNanos != nil {
		if event.BillingProviderID == "" {
			// The harness itself reported what it charged.
			event.BillingProviderID = string(harness)
			event.BillingProviderSource = domain.UsageBillingProviderObserved
		}
		total := *costNanos
		event.Costs = domain.UsageEventCosts{EstimatedCostNanos: &total, PricingVersion: ChatReportedPricingVersion}
		return event
	}
	if snapshot == nil || event.BillingProviderID == "" {
		return event
	}
	estimate, err := snapshot.Estimate(event)
	if err != nil {
		event.Costs.PricingVersion = snapshot.ProviderVersion(event.BillingProviderID)
		return event
	}
	event.Costs = domain.UsageEventCosts{
		InputCostNanos:       estimate.InputNanos,
		CachedInputCostNanos: estimate.CachedInputNanos,
		OutputCostNanos:      estimate.OutputNanos,
		EstimatedCostNanos:   estimate.TotalNanos,
		PricingVersion:       estimate.PricingVersion,
	}
	return event
}

// chatReportedCostNanos converts a provider-reported USD charge to nano-USD.
// A zero charge is not kept: subscription-backed agents report zero for work
// the catalog can still estimate, and the estimate is the more useful figure.
func chatReportedCostNanos(cost *float64, currency string) *int64 {
	if cost == nil || !strings.EqualFold(strings.TrimSpace(currency), "USD") {
		return nil
	}
	nanos := *cost * 1e9
	if math.IsNaN(nanos) || math.IsInf(nanos, 0) || nanos < 1 || nanos >= math.MaxInt64 {
		return nil
	}
	rounded := int64(math.Round(nanos))
	return &rounded
}
