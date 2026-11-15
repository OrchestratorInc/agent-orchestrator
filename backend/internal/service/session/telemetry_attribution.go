package session

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/deliverytelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type createdByStore interface {
	RecordSessionCreatedBy(ctx context.Context, id domain.SessionID, createdBy domain.SessionCreator) error
}

func (s *Service) spawnCreatedBy(ctx context.Context, cfg ports.SpawnConfig) domain.SessionCreator {
	switch {
	case cfg.CreatedBy.Valid():
		return cfg.CreatedBy
	case cfg.AutomationRunID != nil:
		return domain.SessionCreatedByScheduled
	case cfg.ParentSessionID != "":
		if s.store != nil {
			if parent, ok, err := s.store.GetSession(ctx, cfg.ParentSessionID); err == nil && ok && parent.Kind != domain.KindOrchestrator {
				return domain.SessionCreatedByAutomation
			}
		}
		return domain.SessionCreatedByOrchestrator
	default:
		return domain.SessionCreatedByUser
	}
}

func (s *Service) recordCreatedBy(ctx context.Context, id domain.SessionID, createdBy domain.SessionCreator) {
	store, ok := s.store.(createdByStore)
	if !ok || id == "" {
		return
	}
	if err := store.RecordSessionCreatedBy(context.WithoutCancel(ctx), id, createdBy); err != nil && s.logger != nil {
		s.logger.Warn("telemetry: record created_by", "session", id, "err", err)
	}
}

type usageSendStore interface {
	RecordUsageHumanSend(ctx context.Context, id domain.SessionID, steer bool) error
}

func (s *Service) RecordUserMessage(ctx context.Context, id domain.SessionID, isSteer bool) {
	store, ok := s.store.(usageSendStore)
	if !ok {
		return
	}
	if err := store.RecordUsageHumanSend(context.WithoutCancel(ctx), id, isSteer); err != nil && s.logger != nil {
		s.logger.Warn("telemetry: count human send", "session", id, "err", err)
	}
}

func (s *Service) liveSessionBeforeEnd(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool) {
	if s.delivery == nil || s.store == nil {
		return domain.SessionRecord{}, false
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil || !ok || rec.IsTerminated {
		return domain.SessionRecord{}, false
	}
	return rec, true
}

func (s *Service) recordKilled(ctx context.Context, before domain.SessionRecord, live bool) {
	if !live {
		return
	}
	s.delivery.SessionEnded(ctx, before, deliverytelemetry.EndKilled)
}

func (s *Service) recordKillOutcome(ctx context.Context, before domain.SessionRecord, live bool, killErr error) {
	if !live {
		return
	}
	if killErr != nil {
		after, ok, err := s.store.GetSession(context.WithoutCancel(ctx), before.ID)
		if err != nil || !ok || !after.IsTerminated {
			return
		}
	}
	s.recordKilled(ctx, before, true)
}
