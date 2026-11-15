package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Store interface {
	CreateNotification(ctx context.Context, rec domain.NotificationRecord) (domain.NotificationRecord, bool, error)
	ResolveSessionNotifications(
		ctx context.Context,
		id domain.SessionID,
		typ domain.NotificationType,
		at time.Time,
	) ([]domain.NotificationRecord, error)
	ResolvePRNotifications(
		ctx context.Context,
		prURL string,
		typ domain.NotificationType,
		at time.Time,
	) ([]domain.NotificationRecord, error)
	ReconcileResolvedNotifications(ctx context.Context, at time.Time) ([]domain.NotificationRecord, error)
}

type Publisher interface {
	Publish(ctx context.Context, event domain.NotificationEvent) error
}

type Intent = ports.NotificationIntent

type Resolution = ports.NotificationResolution

type Manager struct {
	store     Store
	publisher Publisher
	barrier   sync.Locker
	clock     func() time.Time
	newID     func() string
}

type Deps struct {
	Store     Store
	Publisher Publisher
	Barrier   sync.Locker
	Clock     func() time.Time
	NewID     func() string
}

func New(d Deps) *Manager {
	m := &Manager{store: d.Store, publisher: d.Publisher, barrier: d.Barrier, clock: d.Clock, newID: d.NewID}
	if m.barrier == nil {
		m.barrier = &sync.Mutex{}
	}
	if m.clock == nil {
		m.clock = time.Now
	}
	if m.newID == nil {
		m.newID = func() string { return "ntf_" + uuid.NewString() }
	}
	return m
}

func (m *Manager) Notify(ctx context.Context, intent Intent) error {
	if m == nil || m.store == nil {
		return errors.New("notify: store is required")
	}
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = m.clock().UTC()
	}
	rec, err := enrich(intent)
	if err != nil {
		return fmt.Errorf("notify enrich: %w", err)
	}
	rec.ID = m.newID()
	m.barrier.Lock()
	defer m.barrier.Unlock()
	created, inserted, err := m.store.CreateNotification(ctx, rec)
	if err != nil {
		return fmt.Errorf("notify store: %w", err)
	}
	if !inserted || m.publisher == nil {
		return nil
	}
	if err := m.publisher.Publish(ctx, domain.NotificationEvent{Kind: domain.NotificationCreated, Record: created}); err != nil {
		return fmt.Errorf("notify publish: %w", err)
	}
	return nil
}

func (m *Manager) Resolve(ctx context.Context, res Resolution) error {
	if m == nil || m.store == nil {
		return errors.New("notify: store is required")
	}
	if !res.Type.Valid() {
		return fmt.Errorf("notify resolve: %w", domain.ErrInvalidNotificationType)
	}
	at := res.ResolvedAt
	if at.IsZero() {
		at = m.clock().UTC()
	}
	m.barrier.Lock()
	defer m.barrier.Unlock()
	var (
		resolved []domain.NotificationRecord
		err      error
	)
	if res.PRURL != "" {
		resolved, err = m.store.ResolvePRNotifications(ctx, res.PRURL, res.Type, at)
	} else {
		if res.SessionID == "" {
			return errors.New("notify resolve: session id or pr url is required")
		}
		resolved, err = m.store.ResolveSessionNotifications(ctx, res.SessionID, res.Type, at)
	}
	if err != nil {
		return fmt.Errorf("notify resolve: %w", err)
	}
	return m.publishResolved(ctx, resolved)
}

func (m *Manager) Reconcile(ctx context.Context) error {
	if m == nil || m.store == nil {
		return errors.New("notify: store is required")
	}
	m.barrier.Lock()
	defer m.barrier.Unlock()
	resolved, err := m.store.ReconcileResolvedNotifications(ctx, m.clock().UTC())
	if err != nil {
		return fmt.Errorf("notify reconcile: %w", err)
	}
	return m.publishResolved(ctx, resolved)
}

func (m *Manager) publishResolved(ctx context.Context, resolved []domain.NotificationRecord) error {
	if m.publisher == nil {
		return nil
	}
	for _, rec := range resolved {
		event := domain.NotificationEvent{Kind: domain.NotificationResolved, Record: rec}
		if err := m.publisher.Publish(ctx, event); err != nil {
			return fmt.Errorf("notify publish resolved: %w", err)
		}
	}
	return nil
}
