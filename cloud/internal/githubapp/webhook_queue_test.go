package githubapp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

// enqueueFilterStore answers repository tracking and records queued deliveries.
type enqueueFilterStore struct {
	Store
	tracked  bool
	trackErr error
	checks   int
	inserted []domain.GitHubWebhookDelivery
}

func (s *enqueueFilterStore) GitHubRepositoryTracked(context.Context, int64, int64) (bool, error) {
	s.checks++
	return s.tracked, s.trackErr
}

func (s *enqueueFilterStore) InsertGitHubWebhook(_ context.Context, delivery domain.GitHubWebhookDelivery, _ []byte) (bool, error) {
	s.inserted = append(s.inserted, delivery)
	return true, nil
}

func quietService(store Store) *Service {
	return &Service{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workerID: "test"}
}

func scmDelivery(event string) domain.GitHubWebhookDelivery {
	return domain.GitHubWebhookDelivery{
		DeliveryID: "delivery", Event: event, GitHubInstallationID: 42, GitHubRepositoryID: 7,
	}
}

func TestEnqueueDropsSCMEventsForUntrackedRepositories(t *testing.T) {
	for _, event := range []string{"push", "check_run", "check_suite", "status", "pull_request", "pull_request_review"} {
		store := &enqueueFilterStore{tracked: false}
		inserted, err := quietService(store).EnqueueVerifiedWebhook(context.Background(), scmDelivery(event))
		if err != nil || inserted {
			t.Fatalf("%s: inserted=%v err=%v; want dropped", event, inserted, err)
		}
		if len(store.inserted) != 0 {
			t.Fatalf("%s: queued an untracked repository's event", event)
		}
	}
}

func TestEnqueueQueuesSCMEventsForTrackedRepositories(t *testing.T) {
	store := &enqueueFilterStore{tracked: true}
	inserted, err := quietService(store).EnqueueVerifiedWebhook(context.Background(), scmDelivery("check_run"))
	if err != nil || !inserted || len(store.inserted) != 1 {
		t.Fatalf("inserted=%v err=%v queued=%d; want queued", inserted, err, len(store.inserted))
	}
}

// Installation events change routing and repository access, so they are never
// filtered by tracking.
func TestEnqueueAlwaysQueuesInstallationEvents(t *testing.T) {
	for _, event := range []string{"installation", "installation_repositories"} {
		store := &enqueueFilterStore{tracked: false}
		if _, err := quietService(store).EnqueueVerifiedWebhook(context.Background(), scmDelivery(event)); err != nil {
			t.Fatal(err)
		}
		if len(store.inserted) != 1 || store.checks != 0 {
			t.Fatalf("%s: queued=%d checks=%d; want queued without a tracking lookup", event, len(store.inserted), store.checks)
		}
	}
}

// A failed lookup must not lose an event that may matter.
func TestEnqueueQueuesWhenTrackingLookupFails(t *testing.T) {
	store := &enqueueFilterStore{trackErr: errors.New("database unavailable")}
	if _, err := quietService(store).EnqueueVerifiedWebhook(context.Background(), scmDelivery("push")); err != nil {
		t.Fatal(err)
	}
	if len(store.inserted) != 1 {
		t.Fatal("event dropped after a failed tracking lookup")
	}
}

// drainStore hands out a fixed backlog, holds each delivery's processing for a
// moment, and records how many were in flight at once.
type drainStore struct {
	Store
	mu       sync.Mutex
	backlog  []domain.GitHubWebhookDelivery
	claims   atomic.Int32
	done     atomic.Int32
	inFlight atomic.Int32
	peak     atomic.Int32
	hold     time.Duration
}

func (s *drainStore) ClaimGitHubWebhook(context.Context, string, time.Time) (domain.GitHubWebhookDelivery, error) {
	s.claims.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.backlog) == 0 {
		return domain.GitHubWebhookDelivery{}, postgres.ErrNotFound
	}
	delivery := s.backlog[0]
	s.backlog = s.backlog[1:]
	return delivery, nil
}

// GitHubInstallationRoutes stands in for processing: no routes, so the
// delivery is retried, after a short hold that makes concurrency observable.
func (s *drainStore) GitHubInstallationRoutes(context.Context, int64) ([]domain.GitHubInstallationRoute, error) {
	now := s.inFlight.Add(1)
	for {
		peak := s.peak.Load()
		if now <= peak || s.peak.CompareAndSwap(peak, now) {
			break
		}
	}
	time.Sleep(s.hold)
	s.inFlight.Add(-1)
	return nil, postgres.ErrNotFound
}

func (s *drainStore) RetryGitHubWebhook(context.Context, string, string, string, time.Time, bool) error {
	s.done.Add(1)
	return nil
}

func TestWebhookWorkersDrainABacklogConcurrently(t *testing.T) {
	store := &drainStore{hold: 50 * time.Millisecond}
	for i := range 24 {
		store.backlog = append(store.backlog, domain.GitHubWebhookDelivery{
			DeliveryID: "d" + strconv.Itoa(i), Event: "push", GitHubInstallationID: 42, GitHubRepositoryID: 7,
		})
	}
	service := quietService(store)
	service.webhookWorkers = 8
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { service.Run(ctx); close(stopped) }()

	// One worker pacing one delivery per second would need 24s.
	deadline := time.Now().Add(3 * time.Second)
	for store.done.Load() < 24 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-stopped
	if got := store.done.Load(); got != 24 {
		t.Fatalf("processed %d of 24 deliveries in 3s", got)
	}
	if peak := store.peak.Load(); peak < 2 {
		t.Fatalf("peak concurrency %d; want several workers in flight", peak)
	}
}

func TestWebhookWorkersIdleOnAnEmptyQueue(t *testing.T) {
	store := &drainStore{}
	service := quietService(store)
	service.webhookWorkers = 4
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	service.Run(ctx)
	// Each worker claims once, finds nothing, and waits a full poll interval.
	if claims := store.claims.Load(); claims > 8 {
		t.Fatalf("%d claims in 300ms on an empty queue; workers are spinning", claims)
	}
}
