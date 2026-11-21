package usage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

type costBackfillStore interface {
	ListUsageCostCandidates(context.Context, string, string, int64) ([]domain.UsageCostCandidate, error)
	ListUsagePricedVersions(context.Context, string) ([]string, error)
	ListUsageRepriceCandidates(context.Context, string, int64) ([]domain.UsageCostCandidate, error)
	ListUsageModelCostCandidates(context.Context, string, int64) ([]domain.UsageCostCandidate, error)
	ApplyUsageCostUpdates(context.Context, []domain.UsageCostUpdate, time.Time) (int, error)
}

// CostBackfillerConfig supplies lifecycle callbacks and deterministic time.
type CostBackfillerConfig struct {
	Manager   *pricing.Manager
	Clock     func() time.Time
	OnError   func(error)
	RetryWait func(context.Context, time.Duration) error
}

// CostBackfiller prices bounded pages after provider catalog activation.
type CostBackfiller struct {
	store   costBackfillStore
	config  CostBackfillerConfig
	started atomic.Bool
	done    chan struct{}
	wake    chan struct{}
	retryWG sync.WaitGroup

	mu             sync.Mutex
	nextSequence   uint64
	latestSequence map[string]uint64
	latestVersion  map[string]string
	pending        map[string]costBackfillJob
}

type costBackfillJob struct {
	providerID string
	modelID    string
	version    string
	sequence   uint64
	retryDelay time.Duration
}

func (j costBackfillJob) key() string {
	if j.modelID != "" {
		return "model:" + j.modelID
	}
	return j.providerID
}

const (
	costBackfillRetryInitial = time.Minute
	costBackfillRetryMaximum = time.Hour
)

// NewCostBackfiller constructs one provider cost worker.
func NewCostBackfiller(store costBackfillStore, config CostBackfillerConfig) *CostBackfiller {
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.OnError == nil {
		config.OnError = func(error) {}
	}
	if config.RetryWait == nil {
		config.RetryWait = waitCostBackfillRetry
	}
	return &CostBackfiller{
		store:          store,
		config:         config,
		done:           make(chan struct{}),
		wake:           make(chan struct{}, 1),
		latestSequence: make(map[string]uint64),
		latestVersion:  make(map[string]string),
		pending:        make(map[string]costBackfillJob),
	}
}

// Start launches the single backfill worker.
func (b *CostBackfiller) Start(ctx context.Context) error {
	if b.store == nil {
		return errors.New("usage cost backfiller requires a store")
	}
	if b.config.Manager == nil {
		return errors.New("usage cost backfiller requires a pricing manager")
	}
	if !b.started.CompareAndSwap(false, true) {
		return errors.New("usage cost backfiller already started")
	}
	go b.run(ctx)
	return nil
}

// Enqueue queues one pass per provider whose version the snapshot activated.
func (b *CostBackfiller) Enqueue(
	ctx context.Context,
	snapshot *pricing.Snapshot,
	activations []pricing.ProviderActivation,
) error {
	return b.enqueue(ctx, snapshot, activations, false)
}

// Rerun queues a pass for every provider at its active version, even one that already ran, because stored rows changed underneath it: a repair cleared their costs, or a price override moved.
func (b *CostBackfiller) Rerun(ctx context.Context, snapshot *pricing.Snapshot) error {
	if snapshot == nil {
		return errors.New("usage cost backfill snapshot is nil")
	}
	providerIDs := snapshot.ProviderIDs()
	activations := make([]pricing.ProviderActivation, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		activations = append(activations, pricing.ProviderActivation{ProviderID: providerID, Version: snapshot.ProviderVersion(providerID)})
	}
	return b.enqueue(ctx, snapshot, activations, true)
}

func (b *CostBackfiller) enqueue(
	ctx context.Context,
	snapshot *pricing.Snapshot,
	activations []pricing.ProviderActivation,
	force bool,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil {
		return errors.New("usage cost backfill snapshot is nil")
	}
	jobs := make([]costBackfillJob, 0, len(activations))
	b.mu.Lock()
	for _, activation := range activations {
		providerID := pricing.CanonicalProviderID(activation.ProviderID)
		if activation.Version == "" || snapshot.ProviderVersion(providerID) != activation.Version {
			continue
		}
		if !force && b.latestVersion[providerID] == activation.Version {
			continue
		}
		b.latestVersion[providerID] = activation.Version
		jobs = append(jobs, costBackfillJob{providerID: providerID, version: activation.Version})
	}
	b.mu.Unlock()
	b.queue(jobs)
	return nil
}

// RepriceModels queues a pass over each model's events after its price override changed: rows AO priced, or could not, take the new price.
func (b *CostBackfiller) RepriceModels(ctx context.Context, modelIDs []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	jobs := make([]costBackfillJob, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		jobs = append(jobs, costBackfillJob{modelID: modelID})
	}
	b.queue(jobs)
	return nil
}

func (b *CostBackfiller) queue(jobs []costBackfillJob) {
	if len(jobs) == 0 {
		return
	}
	b.mu.Lock()
	for _, job := range jobs {
		b.nextSequence++
		job.sequence = b.nextSequence
		b.latestSequence[job.key()] = job.sequence
		b.pending[job.key()] = job
	}
	b.mu.Unlock()
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// Wait joins the worker after its context is canceled.
func (b *CostBackfiller) Wait() {
	if !b.started.Load() {
		return
	}
	<-b.done
	b.retryWG.Wait()
}

func (b *CostBackfiller) run(ctx context.Context) {
	defer close(b.done)
	for {
		if job, ok := b.nextJob(); ok {
			if err := b.process(ctx, job); err != nil && ctx.Err() == nil {
				b.config.OnError(err)
				b.scheduleRetry(ctx, job)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-b.wake:
		}
	}
}

func (b *CostBackfiller) scheduleRetry(ctx context.Context, job costBackfillJob) {
	if ctx.Err() != nil || b.superseded(job) {
		return
	}
	delay := job.retryDelay
	if delay <= 0 {
		delay = costBackfillRetryInitial
	}
	job.retryDelay = min(delay*2, costBackfillRetryMaximum)
	b.retryWG.Add(1)
	go func() {
		defer b.retryWG.Done()
		if err := b.config.RetryWait(ctx, delay); err != nil || ctx.Err() != nil {
			return
		}
		b.mu.Lock()
		if b.latestSequence[job.key()] != job.sequence {
			b.mu.Unlock()
			return
		}
		b.pending[job.key()] = job
		b.mu.Unlock()
		select {
		case b.wake <- struct{}{}:
		default:
		}
	}()
}

func (b *CostBackfiller) nextJob() (costBackfillJob, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var next costBackfillJob
	found := false
	for _, job := range b.pending {
		if !found || job.sequence < next.sequence {
			next = job
			found = true
		}
	}
	if found {
		delete(b.pending, next.key())
	}
	return next, found
}

func (b *CostBackfiller) superseded(job costBackfillJob) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.latestSequence[job.key()] != job.sequence
}

func (b *CostBackfiller) process(ctx context.Context, job costBackfillJob) error {
	if job.modelID != "" {
		return b.pricePages(ctx, job, func(afterID int64) ([]domain.UsageCostCandidate, error) {
			return b.store.ListUsageModelCostCandidates(ctx, job.modelID, afterID)
		})
	}
	if err := b.pricePages(ctx, job, func(afterID int64) ([]domain.UsageCostCandidate, error) {
		return b.store.ListUsageCostCandidates(ctx, job.providerID, job.version, afterID)
	}); err != nil {
		return err
	}
	if b.superseded(job) {
		return nil
	}
	versions, err := b.store.ListUsagePricedVersions(ctx, job.providerID)
	if err != nil {
		return err
	}
	for _, version := range versions {
		if version == job.version {
			continue
		}
		if err := b.pricePages(ctx, job, func(afterID int64) ([]domain.UsageCostCandidate, error) {
			return b.store.ListUsageRepriceCandidates(ctx, version, afterID)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (b *CostBackfiller) pricePages(
	ctx context.Context,
	job costBackfillJob,
	list func(afterID int64) ([]domain.UsageCostCandidate, error),
) error {
	afterID := int64(0)
	for {
		if b.superseded(job) {
			return nil
		}
		complete := false
		err := b.config.Manager.WithSnapshot(ctx, func(active *pricing.Snapshot) error {
			if job.modelID == "" && active.ProviderVersion(job.providerID) != job.version {
				complete = true
				return nil
			}
			candidates, err := list(afterID)
			if err != nil {
				return err
			}
			if len(candidates) == 0 {
				complete = true
				return nil
			}
			updates := make([]domain.UsageCostUpdate, 0, len(candidates))
			for _, candidate := range candidates {
				if update, ok := b.costUpdate(active, candidate, job); ok {
					updates = append(updates, update)
				}
			}
			if _, err := b.store.ApplyUsageCostUpdates(ctx, updates, b.config.Clock()); err != nil {
				return err
			}
			afterID = candidates[len(candidates)-1].ID
			complete = len(candidates) < 256
			return nil
		})
		if err != nil {
			return err
		}
		if complete {
			return nil
		}
	}
}

func (b *CostBackfiller) costUpdate(snapshot *pricing.Snapshot, candidate domain.UsageCostCandidate, job costBackfillJob) (domain.UsageCostUpdate, bool) {
	estimate, err := snapshot.Estimate(domain.ModelUsageEvent{
		ProviderID:        candidate.ProviderID,
		BillingProviderID: candidate.BillingProviderID,
		ModelID:           candidate.ModelID,
		MeasurementKind:   candidate.MeasurementKind,
		Tokens:            candidate.Tokens,
		ProviderUsageJSON: candidate.ProviderUsageJSON,
		ServiceTier:       candidate.ServiceTier,
		SourceEventKey:    candidate.SourceEventKey,
	})
	if err != nil {
		b.config.OnError(err)
	}
	if job.modelID == "" && candidate.EstimatedCostNanos != nil && (err != nil || estimate.TotalNanos == nil) {
		return domain.UsageCostUpdate{}, false
	}
	update := domain.UsageCostUpdate{Candidate: candidate, Costs: domain.UsageEventCosts{PricingVersion: job.version}}
	if err != nil {
		return update, true
	}
	if estimate.PricingVersion != "" {
		update.Costs.PricingVersion = estimate.PricingVersion
	}
	update.Costs.InputCostNanos = estimate.InputNanos
	update.Costs.CachedInputCostNanos = estimate.CachedInputNanos
	update.Costs.OutputCostNanos = estimate.OutputNanos
	update.Costs.EstimatedCostNanos = estimate.TotalNanos
	return update, true
}

func waitCostBackfillRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
