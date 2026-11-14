package sessionguard

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type SessionReader interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

type InputLease interface {
	AcquireSessionInput(id domain.SessionID) (release func(), ok bool)
}

type Outcome int

const (
	SuppressedUnknown Outcome = iota
	Sent
	SuppressedNotFound
	SuppressedTerminated
	SuppressedExited
	SuppressedAwaitingUser
	SuppressedBusy
	SuppressedInputGated
	SuppressedStartupPending
	SuppressedComposerBusy
)

func (o Outcome) String() string {
	switch o {
	case Sent:
		return "sent"
	case SuppressedNotFound:
		return "suppressed_not_found"
	case SuppressedTerminated:
		return "suppressed_terminated"
	case SuppressedExited:
		return "suppressed_exited"
	case SuppressedAwaitingUser:
		return "suppressed_awaiting_user"
	case SuppressedBusy:
		return "suppressed_busy"
	case SuppressedInputGated:
		return "suppressed_input_gated"
	case SuppressedStartupPending:
		return "suppressed_startup_pending"
	case SuppressedComposerBusy:
		return "suppressed_composer_busy"
	default:
		return "suppressed_unknown"
	}
}

type Guard struct {
	store     SessionReader
	messenger ports.AgentMessenger
	logger    *slog.Logger

	leaseMu sync.RWMutex
	lease   InputLease

	startupGateMu           sync.RWMutex
	startupSignalGatesInput func(domain.AgentHarness) bool
}

var _ ports.AgentMessenger = (*Guard)(nil)

func (g *Guard) tuiAwaitingStartupInput(rec domain.SessionRecord) bool {
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI || !rec.FirstSignalAt.IsZero() {
		return false
	}
	g.startupGateMu.RLock()
	requiresSignal := g.startupSignalGatesInput
	g.startupGateMu.RUnlock()
	return requiresSignal != nil && requiresSignal(rec.Harness)
}

func (g *Guard) refuseDeliver(rec domain.SessionRecord) (Outcome, bool) {
	if g.tuiAwaitingStartupInput(rec) {
		return SuppressedStartupPending, true
	}
	if rec.Activity.State == domain.ActivityBlocked {
		return SuppressedAwaitingUser, true
	}
	return SuppressedUnknown, false
}

func (g *Guard) refuseNudge(rec domain.SessionRecord) (Outcome, bool) {
	if g.tuiAwaitingStartupInput(rec) {
		return SuppressedStartupPending, true
	}
	if rec.Activity.State.NeedsInput() {
		return SuppressedAwaitingUser, true
	}
	return SuppressedUnknown, false
}

func New(store SessionReader, messenger ports.AgentMessenger, logger *slog.Logger) *Guard {
	if logger == nil {
		logger = slog.Default()
	}
	return &Guard{store: store, messenger: messenger, logger: logger}
}

func (g *Guard) SetInputLease(lease InputLease) {
	g.leaseMu.Lock()
	g.lease = lease
	g.leaseMu.Unlock()
}

func (g *Guard) SetStartupSignalGate(pred func(domain.AgentHarness) bool) {
	g.startupGateMu.Lock()
	g.startupSignalGatesInput = pred
	g.startupGateMu.Unlock()
}

func (g *Guard) Send(ctx context.Context, id domain.SessionID, msg string) error {
	_, err := g.Deliver(ctx, id, msg)
	return err
}

func (g *Guard) Deliver(ctx context.Context, id domain.SessionID, msg string) (Outcome, error) {
	return g.send(ctx, id, msg, g.refuseDeliver)
}

func (g *Guard) DeliverWithComposerCheck(
	ctx context.Context,
	id domain.SessionID,
	msg string,
	composerBusy func(context.Context, domain.SessionRecord) (bool, func()),
	after func(context.Context) error,
) (Outcome, error) {
	return g.sendThenChecked(ctx, id, msg, g.refuseDeliver, composerBusy, after)
}

func (g *Guard) DeliverUnderMutation(ctx context.Context, id domain.SessionID, msg string) (Outcome, error) {
	return g.sendAdmitted(ctx, id, msg, func(rec domain.SessionRecord) (Outcome, bool) {
		return SuppressedAwaitingUser, rec.Activity.State == domain.ActivityBlocked
	}, nil)
}

func (g *Guard) DeliverUnderMutationChecked(
	ctx context.Context,
	id domain.SessionID,
	msg string,
	preWrite func(context.Context, domain.SessionRecord) error,
) (Outcome, error) {
	return g.sendAdmittedChecked(ctx, id, msg, func(rec domain.SessionRecord) (Outcome, bool) {
		return SuppressedAwaitingUser, rec.Activity.State == domain.ActivityBlocked
	}, preWrite, nil)
}

func (g *Guard) CoordinationUnderMutation(
	ctx context.Context,
	id domain.SessionID,
	msg string,
	acceptsWaitingInput func(domain.AgentHarness) bool,
	steersActiveTurn func(domain.AgentHarness) bool,
) (Outcome, error) {
	return g.coordinationUnderMutation(ctx, id, msg, acceptsWaitingInput, steersActiveTurn, nil)
}

func (g *Guard) CoordinationUnderMutationChecked(
	ctx context.Context,
	id domain.SessionID,
	msg string,
	acceptsWaitingInput func(domain.AgentHarness) bool,
	steersActiveTurn func(domain.AgentHarness) bool,
	preWrite func(context.Context, domain.SessionRecord) error,
) (Outcome, error) {
	return g.coordinationUnderMutation(ctx, id, msg, acceptsWaitingInput, steersActiveTurn, preWrite)
}

func (g *Guard) coordinationUnderMutation(
	ctx context.Context,
	id domain.SessionID,
	msg string,
	acceptsWaitingInput func(domain.AgentHarness) bool,
	steersActiveTurn func(domain.AgentHarness) bool,
	preWrite func(context.Context, domain.SessionRecord) error,
) (Outcome, error) {
	return g.sendAdmittedChecked(ctx, id, msg, func(rec domain.SessionRecord) (Outcome, bool) {
		switch rec.Activity.State {
		case domain.ActivityIdle:
			return SuppressedUnknown, false
		case domain.ActivityWaitingInput:
			return SuppressedAwaitingUser, acceptsWaitingInput == nil || !acceptsWaitingInput(rec.Harness)
		case domain.ActivityBlocked:
			return SuppressedAwaitingUser, true
		case domain.ActivityActive:
			return SuppressedBusy, steersActiveTurn == nil || !steersActiveTurn(rec.Harness)
		default:
			return SuppressedUnknown, true
		}
	}, preWrite, nil)
}

func (g *Guard) Nudge(ctx context.Context, id domain.SessionID, msg string) (Outcome, error) {
	return g.send(ctx, id, msg, g.refuseNudge)
}

func (g *Guard) NudgeUrgent(ctx context.Context, id domain.SessionID, msg string, acceptsWaitingInput func(domain.AgentHarness) bool) (Outcome, error) {
	return g.send(ctx, id, msg, func(rec domain.SessionRecord) (Outcome, bool) {
		if g.tuiAwaitingStartupInput(rec) {
			return SuppressedStartupPending, true
		}
		switch rec.Activity.State {
		case domain.ActivityBlocked:
			return SuppressedAwaitingUser, true
		case domain.ActivityWaitingInput:
			return SuppressedAwaitingUser, acceptsWaitingInput == nil || !acceptsWaitingInput(rec.Harness)
		default:
			return SuppressedUnknown, false
		}
	})
}

func (g *Guard) NudgeCoordination(ctx context.Context, id domain.SessionID, msg string, steersActiveTurn func(domain.AgentHarness) bool) (Outcome, error) {
	return g.send(ctx, id, msg, func(rec domain.SessionRecord) (Outcome, bool) {
		if g.tuiAwaitingStartupInput(rec) {
			return SuppressedStartupPending, true
		}
		if rec.Activity.State.NeedsInput() {
			return SuppressedAwaitingUser, true
		}
		if rec.Activity.State == domain.ActivityActive {
			return SuppressedBusy, steersActiveTurn == nil || !steersActiveTurn(rec.Harness)
		}
		return SuppressedUnknown, false
	})
}

func (g *Guard) send(ctx context.Context, id domain.SessionID, msg string, refuse func(domain.SessionRecord) (Outcome, bool)) (Outcome, error) {
	return g.sendThenChecked(ctx, id, msg, refuse, nil, nil)
}

func (g *Guard) sendThenChecked(ctx context.Context, id domain.SessionID, msg string, refuse func(domain.SessionRecord) (Outcome, bool), composerBusy func(context.Context, domain.SessionRecord) (bool, func()), after func(context.Context) error) (Outcome, error) {
	g.leaseMu.RLock()
	lease := g.lease
	g.leaseMu.RUnlock()
	if lease != nil {
		release, ok := lease.AcquireSessionInput(id)
		if !ok {
			g.logger.Info("sessionguard: write suppressed", "sessionID", id, "reason", SuppressedInputGated.String())
			return SuppressedInputGated, nil
		}
		defer release()
	}
	outcome, err := g.sendAdmitted(ctx, id, msg, refuse, composerBusy)
	if err == nil && outcome == Sent && after != nil {
		if afterErr := after(ctx); afterErr != nil {
			return outcome, afterErr
		}
	}
	return outcome, err
}

func (g *Guard) sendAdmitted(ctx context.Context, id domain.SessionID, msg string, refuse func(domain.SessionRecord) (Outcome, bool), composerBusy func(context.Context, domain.SessionRecord) (bool, func())) (Outcome, error) {
	return g.sendAdmittedChecked(ctx, id, msg, refuse, nil, composerBusy)
}

func (g *Guard) sendAdmittedChecked(ctx context.Context, id domain.SessionID, msg string, refuse func(domain.SessionRecord) (Outcome, bool), preWrite func(context.Context, domain.SessionRecord) error, composerBusy func(context.Context, domain.SessionRecord) (bool, func())) (Outcome, error) {
	rec, ok, err := g.store.GetSession(ctx, id)
	if err != nil {
		return SuppressedUnknown, fmt.Errorf("guard %s: read session: %w", id, err)
	}
	if !ok {
		g.logger.Info("sessionguard: write suppressed", "sessionID", id, "reason", "not_found")
		return SuppressedNotFound, nil
	}
	if rec.IsTerminated {
		g.logger.Info("sessionguard: write suppressed", "sessionID", id, "reason", "terminated")
		return SuppressedTerminated, nil
	}
	if rec.Activity.State == domain.ActivityExited {
		g.logger.Info("sessionguard: write suppressed", "sessionID", id, "reason", "agent_exited")
		return SuppressedExited, nil
	}
	if outcome, deny := refuse(rec); deny {
		g.logger.Info("sessionguard: write suppressed", "sessionID", id, "reason", outcome.String(), "state", string(rec.Activity.State))
		return outcome, nil
	}
	if preWrite != nil {
		if err := preWrite(ctx, rec); err != nil {
			return SuppressedUnknown, fmt.Errorf("guard %s: pre-write check: %w", id, err)
		}
	}
	if composerBusy != nil {
		busy, release := composerBusy(ctx, rec)
		if release != nil {
			defer release()
		}
		if busy {
			g.logger.Info("sessionguard: write suppressed", "sessionID", id, "reason", SuppressedComposerBusy.String(), "state", string(rec.Activity.State))
			return SuppressedComposerBusy, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return SuppressedUnknown, fmt.Errorf("guard %s: %w", id, err)
	}
	if err := g.messenger.Send(ctx, id, msg); err != nil {
		return Sent, fmt.Errorf("guard %s: send: %w", id, err)
	}
	return Sent, nil
}
