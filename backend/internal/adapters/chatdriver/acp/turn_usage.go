package acp

import (
	"math"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// acpCost is one cumulative session cost reading from usage_update.
type acpCost struct {
	amount   float64
	currency string
}

// trackCost records the provider's latest cumulative session cost. The SDK
// delivers every notification sent before a prompt response ahead of that
// response, so the reading a turn ends on is already here when it settles.
func (c *conversation) trackCost(amount float64, currency string) {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessionCost = &acpCost{amount: amount, currency: strings.ToUpper(strings.TrimSpace(currency))}
}

// costSince is what the session spent between two cumulative readings. With no
// reading at the start the turn's share cannot be told from what came before
// (a resumed session reports its whole history), so it is unknown rather than
// the full cumulative figure.
func costSince(start, end *acpCost) *acpCost {
	if start == nil || end == nil || start.currency != end.currency || end.amount < start.amount {
		return nil
	}
	return &acpCost{amount: end.amount - start.amount, currency: end.currency}
}

// acpTurnUsage normalizes ACP's per-turn usage into AO's vocabulary.
//
// ACP does not say whether inputTokens already counts cache reads and writes,
// or whether outputTokens counts thought tokens, and agents differ. The
// reported total settles it: whichever reading sums to totalTokens is the one
// the agent used. When none does, cache and thought counts are taken as
// separate from input and output, which is what the agents that report them
// send.
func acpTurnUsage(usage acpsdk.Usage, model string, cost *acpCost) *ports.ChatTurnUsage {
	input := int64(max(usage.InputTokens, 0))
	output := int64(max(usage.OutputTokens, 0))
	read := optionalTokens(usage.CachedReadTokens)
	write := optionalTokens(usage.CachedWriteTokens)
	thought := optionalTokens(usage.ThoughtTokens)
	var readCount, writeCount, thoughtCount int64
	if read != nil {
		readCount = *read
	}
	if write != nil {
		writeCount = *write
	}
	if thought != nil {
		thoughtCount = *thought
	}
	total := int64(max(usage.TotalTokens, 0))

	cacheInInput, thoughtInOutput := false, false
	if total > 0 {
		for _, reading := range []struct{ cacheInInput, thoughtInOutput bool }{
			{false, false}, {false, true}, {true, false}, {true, true},
		} {
			sum := input + output
			if !reading.cacheInInput {
				sum += readCount + writeCount
			}
			if !reading.thoughtInOutput {
				sum += thoughtCount
			}
			if sum == total {
				cacheInInput, thoughtInOutput = reading.cacheInInput, reading.thoughtInOutput
				break
			}
		}
	}
	if cacheInInput && input < readCount+writeCount {
		cacheInInput = false
	}
	if thought == nil {
		thoughtInOutput = true
	}

	turn := &ports.ChatTurnUsage{
		ModelID:           strings.TrimSpace(model),
		CachedInputTokens: readCount,
		CacheWriteTokens:  write,
		OutputTokens:      output,
		TotalTokens:       total,
	}
	if cacheInInput {
		turn.UncachedInputTokens = input - readCount
	} else {
		turn.UncachedInputTokens = input + writeCount
	}
	if !thoughtInOutput {
		turn.OutputTokens += thoughtCount
	}
	if thought != nil {
		reasoning := thoughtCount
		turn.ReasoningTokens = &reasoning
	}
	if cost != nil {
		amount := cost.amount
		turn.Cost = &amount
		turn.Currency = cost.currency
	}
	return turn
}

func optionalTokens(value *int) *int64 {
	if value == nil {
		return nil
	}
	tokens := int64(max(*value, 0))
	return &tokens
}
