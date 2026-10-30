package session

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/deliverytelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// createdByStore persists created_by for ao.sessions.ended. It is optional so
// focused service fakes need not implement it.
type createdByStore interface {
	RecordSessionCreatedBy(ctx context.Context, id domain.SessionID, createdBy domain.SessionCreator) error
}

// spawnCreatedBy says who caused this spawn. Only daemon-side facts count: an
// explicit unattended source, a schedule occurrence, or the AO session that ran
// `ao spawn` (validated against the store, never trusted from the caller).
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

// usageSendStore counts human sends into the per-session usage summary that
// ao.usage.messages_sent exports. Optional so focused fakes need not have it.
type usageSendStore interface {
	RecordUsageHumanSend(ctx context.Context, id domain.SessionID, steer bool) error
}

// RecordUserMessage counts a steer or `/send` a person sent to a session.
// Direct Chat messages are already counted where they are durably inserted,
// so the HTTP controllers call this only for the two sends that insert does
// not see, and only for requests no AO session sent. The message itself is
// never read; the request surface rides the context.
func (s *Service) RecordUserMessage(ctx context.Context, id domain.SessionID, isSteer bool) {
	store, ok := s.store.(usageSendStore)
	if !ok {
		return
	}
	if err := store.RecordUsageHumanSend(context.WithoutCancel(ctx), id, isSteer); err != nil && s.logger != nil {
		s.logger.Warn("telemetry: count human send", "session", id, "err", err)
	}
}

// liveSessionBeforeEnd returns the record of a session that is about to be
// ended, or ok=false when it is unknown or already terminated (so a repeated
// kill reports nothing).
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
