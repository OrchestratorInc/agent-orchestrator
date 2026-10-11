package billing

import (
	"context"
	"log/slog"
	"time"
)

const (
	// sampleInterval is the usage ledger's resolution: one row per running
	// session per minute.
	sampleInterval = time.Minute
	// usageRetention keeps a little more than any measured window (weekly)
	// plus a billing period, for support questions.
	usageRetention = 40 * 24 * time.Hour
	// stripeEventRetention keeps processed webhook ids far longer than Stripe
	// retries a delivery (three days), so a late retry is still recognized.
	stripeEventRetention = 90 * 24 * time.Hour
	pruneInterval        = 24 * time.Hour
)

// UsageStore is what the enforcer needs from the store.
type UsageStore interface {
	SampleUsage(ctx context.Context) ([]string, error)
	PauseOverLimit(ctx context.Context, orgID string) (int64, error)
	PruneUsage(ctx context.Context, before time.Time) error
	PruneStripeEvents(ctx context.Context, before time.Time) error
}

// Enforcer meters running sandboxes and pauses organizations that are over
// their plan. It is safe to run on every control-plane replica: samples are
// deduplicated by the ledger's primary key and pausing is idempotent.
type Enforcer struct {
	Store  UsageStore
	Logger *slog.Logger
	now    func() time.Time
}

// Run samples and enforces once a minute until ctx ends.
func (e *Enforcer) Run(ctx context.Context) {
	if e.Logger == nil {
		e.Logger = slog.Default()
	}
	if e.now == nil {
		e.now = time.Now
	}
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	var lastPrune time.Time
	for {
		e.Tick(ctx)
		if now := e.now(); now.Sub(lastPrune) >= pruneInterval {
			if e.Prune(ctx, now) {
				lastPrune = now
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Prune drops usage and processed webhook ids nobody reads any more. It
// reports whether both succeeded.
func (e *Enforcer) Prune(ctx context.Context, now time.Time) bool {
	if e.Logger == nil {
		e.Logger = slog.Default()
	}
	ok := true
	if err := e.Store.PruneUsage(ctx, now.Add(-usageRetention)); err != nil {
		ok = false
		if ctx.Err() == nil {
			e.Logger.Warn("prune usage ledger", "error", err)
		}
	}
	if err := e.Store.PruneStripeEvents(ctx, now.Add(-stripeEventRetention)); err != nil {
		ok = false
		if ctx.Err() == nil {
			e.Logger.Warn("prune processed Stripe events", "error", err)
		}
	}
	return ok
}

// Tick records one usage sample and pauses over-limit organizations.
func (e *Enforcer) Tick(ctx context.Context) {
	if e.Logger == nil {
		e.Logger = slog.Default()
	}
	orgIDs, err := e.Store.SampleUsage(ctx)
	if err != nil {
		if ctx.Err() == nil {
			e.Logger.Warn("sample sandbox usage", "error", err)
		}
		return
	}
	for _, orgID := range orgIDs {
		paused, err := e.Store.PauseOverLimit(ctx, orgID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			e.Logger.Warn("enforce plan limits", "org_id", orgID, "error", err)
			continue
		}
		if paused > 0 {
			e.Logger.Info("paused sessions over plan limits", "org_id", orgID, "paused", paused)
		}
	}
}
