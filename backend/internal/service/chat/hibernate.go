package chat

import (
	"context"
	"errors"
	"fmt"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const chatViewLease = 30 * time.Second
const chatHibernateGrace = 30 * time.Second

const IdleBeforeHibernate = 10 * time.Minute

type wakeRun struct {
	done    chan struct{}
	err     error
	started time.Time
}

func wakeCallers() string {
	pcs := make([]uintptr, 16)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
	var names []string
	for {
		frame, more := frames.Next()
		if frame.Function != "" {
			names = append(names, path.Base(frame.Function))
		}
		if !more {
			return strings.Join(names, " < ")
		}
	}
}

type hibernationStore interface {
	SetSessionHibernated(context.Context, domain.SessionID, int64, *time.Time) (bool, error)
}

type hibernationTurnReader interface {
	LatestVisibleUserTurnSettled(context.Context, string, domain.SessionID) (bool, error)
}

func (s *Service) SetWakeCallback(wake func(context.Context, domain.SessionID) error) {
	s.wakeChat = wake
}

func (s *Service) SetChatView(ctx context.Context, id domain.SessionID, viewID string, active bool) error {
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	if err := gate.lock(ctx); err != nil {
		return err
	}
	if !active {
		s.setViewLease(id, viewID, false)
		gate.unlock()
		return nil
	}
	rec, err := s.requireChatSession(ctx, id)
	if err != nil {
		gate.unlock()
		return err
	}
	if rec.IsTerminated {
		s.viewMu.Lock()
		delete(s.viewLeases, id)
		s.viewMu.Unlock()
		gate.unlock()
		return nil
	}
	newView := s.setViewLease(id, viewID, true)
	gate.unlock()
	if !newView || !s.hasChatView(id) {
		return nil
	}
	if rec.ProvisionState.WithDefault() != domain.SessionProvisionReady ||
		rec.HibernatedAt == nil || rec.Metadata.ProviderConversationID == "" {
		return nil
	}
	err = s.wakeHibernated(ctx, id)
	s.viewMu.Lock()
	if views := s.viewLeases[id]; views != nil {
		if _, present := views[viewID]; present {
			views[viewID] = s.now().Add(chatViewLease)
		}
	}
	s.viewMu.Unlock()
	return err
}

func (s *Service) setViewLease(id domain.SessionID, viewID string, active bool) (newView bool) {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()
	views := s.liveViewLeasesLocked(id)
	if active {
		delete(s.viewClosedAt, id)
		if views == nil {
			views = make(map[string]time.Time)
			if s.viewLeases == nil {
				s.viewLeases = make(map[domain.SessionID]map[string]time.Time)
			}
			s.viewLeases[id] = views
		}
		_, existing := views[viewID]
		newView = !existing
		views[viewID] = s.now().Add(chatViewLease)
	} else if _, exists := views[viewID]; exists {
		delete(views, viewID)
		if len(views) == 0 {
			if s.viewClosedAt == nil {
				s.viewClosedAt = make(map[domain.SessionID]time.Time)
			}
			s.viewClosedAt[id] = s.now()
		}
	}
	if len(views) == 0 {
		delete(s.viewLeases, id)
		return false
	}
	return newView
}

func (s *Service) hasChatView(id domain.SessionID) bool {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()
	views := s.liveViewLeasesLocked(id)
	if s.viewClosedAt[id].Add(chatHibernateGrace).After(s.now()) {
		return true
	}
	delete(s.viewClosedAt, id)
	return len(views) != 0
}

func (s *Service) liveViewLeasesLocked(id domain.SessionID) map[string]time.Time {
	views := s.viewLeases[id]
	now := s.now()
	for key, expiry := range views {
		if !expiry.After(now) {
			if s.viewClosedAt == nil {
				s.viewClosedAt = make(map[domain.SessionID]time.Time)
			}
			if expiry.After(s.viewClosedAt[id]) {
				s.viewClosedAt[id] = expiry
			}
			delete(views, key)
		}
	}
	if len(views) == 0 {
		delete(s.viewLeases, id)
		return nil
	}
	return views
}

type hibernateOptions struct {
	versionRefresh bool
	forUpdate      bool
	minIdle        time.Duration
	ignoreViews    bool
}

func (o hibernateOptions) enabled(s *Service) bool {
	if o.versionRefresh || o.forUpdate {
		return true
	}
	return s.hibernationEnabled != nil && s.hibernationEnabled()
}

func (o hibernateOptions) eligible(s *Service, rec domain.SessionRecord) bool {
	candidate := rec
	if (o.forUpdate || o.versionRefresh) && candidate.Kind == domain.KindOrchestrator {
		candidate.Kind = ""
	}
	if !candidate.EligibleForChatHibernation() || rec.Activity.LastActivityAt.Add(o.minIdle).After(s.now()) {
		return false
	}
	return !o.versionRefresh || (rec.StaleBinary != nil && rec.StaleBinary.SwitchFailedAt == nil)
}

func (o hibernateOptions) viewed(s *Service, id domain.SessionID) bool {
	return !o.ignoreViews && s.hasChatView(id)
}

func (s *Service) HibernateChat(ctx context.Context, id domain.SessionID) (bool, error) {
	return s.hibernateChat(ctx, id, hibernateOptions{minIdle: IdleBeforeHibernate})
}

func (s *Service) HibernateChatForVersionRefresh(ctx context.Context, id domain.SessionID, minIdle time.Duration) (bool, error) {
	return s.hibernateChat(ctx, id, hibernateOptions{versionRefresh: true, minIdle: max(minIdle, chatHibernateGrace)})
}

func (s *Service) HibernateChatForUpdate(ctx context.Context, id domain.SessionID) (bool, error) {
	return s.hibernateChat(ctx, id, hibernateOptions{forUpdate: true, ignoreViews: true})
}

func (s *Service) hibernateChat(ctx context.Context, id domain.SessionID, opts hibernateOptions) (bool, error) {
	if !opts.enabled(s) {
		return false, nil
	}
	marker, ok := s.sessions.(hibernationStore)
	if !ok {
		return false, errors.New("chat hibernation store is unavailable")
	}
	turns, ok := s.sessions.(hibernationTurnReader)
	if !ok {
		return false, errors.New("chat hibernation turn reader is unavailable")
	}
	owner := domain.SessionConversationOwner(id)
	gate := s.controllerGate(owner)
	if !gate.tryLock() {
		return false, nil
	}
	rec, err := s.requireChatSession(ctx, id)
	if err != nil {
		gate.unlock()
		return false, err
	}
	if !opts.eligible(s, rec) || opts.viewed(s, id) {
		gate.unlock()
		return false, nil
	}
	controller, err := s.Controller(id)
	if err != nil || controller.State() != ports.ChatControllerReady ||
		!controller.Capabilities().Has(ports.ChatCapabilityResume) {
		gate.unlock()
		return false, nil
	}
	hibernator, ok := controller.conv.(ports.ChatProviderHibernator)
	if !ok {
		gate.unlock()
		return false, nil
	}
	release := controller.beginOperation()
	gate.unlock()
	ready, err := hibernator.CanHibernate(ctx)
	release()
	if err != nil || !ready {
		if err != nil {
			return false, fmt.Errorf("check chat provider background work: %w", err)
		}
		return false, nil
	}
	if !gate.tryLock() {
		return false, nil
	}
	defer gate.unlock()
	current, err := s.Controller(id)
	if err != nil || current != controller || opts.viewed(s, id) {
		return false, nil
	}
	if !controller.sendMu.TryLock() {
		return false, nil
	}
	controller.mu.Lock()
	busy := controller.state != ports.ChatControllerReady ||
		controller.handoff != controllerHandoffNone ||
		controller.pendingTurnID != "" || controller.dispatchingTurnID != "" ||
		controller.compactionPending || controller.operations != 0
	controller.mu.Unlock()
	if busy {
		controller.sendMu.Unlock()
		return false, nil
	}
	if _, err := s.store.NextQueuedTurn(ctx, controller.conversation.ID); err == nil {
		controller.sendMu.Unlock()
		return false, nil
	} else if !errors.Is(err, domain.ErrNoQueuedTurn) {
		controller.sendMu.Unlock()
		return false, fmt.Errorf("check queued chat turns: %w", err)
	}
	if running, err := s.store.ListVisibleRunningTurnProviderIDs(ctx, controller.conversation.ID); err != nil {
		controller.sendMu.Unlock()
		return false, fmt.Errorf("check running chat turns: %w", err)
	} else if len(running) != 0 {
		controller.sendMu.Unlock()
		return false, nil
	}
	if pending, err := s.store.HasPendingConversationInteractions(ctx, controller.conversation.ID); err != nil {
		controller.sendMu.Unlock()
		return false, fmt.Errorf("check pending chat interactions: %w", err)
	} else if pending {
		controller.sendMu.Unlock()
		return false, nil
	}
	settled, err := turns.LatestVisibleUserTurnSettled(ctx, controller.conversation.ID, id)
	if err != nil {
		controller.sendMu.Unlock()
		return false, fmt.Errorf("check latest chat turn: %w", err)
	}
	if !settled {
		controller.sendMu.Unlock()
		return false, nil
	}
	fresh, err := s.requireChatSession(ctx, id)
	if err != nil {
		controller.sendMu.Unlock()
		return false, err
	}
	if !opts.eligible(s, fresh) {
		controller.sendMu.Unlock()
		return false, nil
	}
	if !opts.enabled(s) {
		controller.sendMu.Unlock()
		return false, nil
	}
	at := s.now()
	applied, err := marker.SetSessionHibernated(ctx, id, fresh.Revision, &at)
	if err != nil || !applied {
		controller.sendMu.Unlock()
		return false, err
	}
	controller.mu.Lock()
	controller.handoff = controllerHandoffHibernate
	controller.suppressStoppedActivity = true
	controller.mu.Unlock()
	controller.sendMu.Unlock()

	stopErr := hibernator.Hibernate()
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if stopErr == nil {
		select {
		case <-controller.stopped:
		case <-finishCtx.Done():
			stopErr = finishCtx.Err()
		}
	}
	if stopErr != nil {
		if s.stopProviderHost == nil {
			return false, fmt.Errorf("hibernate chat provider: %w", stopErr)
		}
		recoveryCtx, recoveryCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer recoveryCancel()
		if err := s.stopProviderHost(recoveryCtx, id); err != nil {
			return false, fmt.Errorf("confirm hibernated chat shutdown: %w", errors.Join(stopErr, err))
		}
		select {
		case <-controller.stopped:
		case <-recoveryCtx.Done():
			return false, fmt.Errorf("wait for hibernated chat controller: %w", recoveryCtx.Err())
		}
	}
	if opts.versionRefresh {
		s.noteVersionSwitch(id, fresh.StaleBinary.Version)
		s.log.Info("chat session stopped to move to a new harness release", "session", id, "harness", fresh.Harness, "version", fresh.StaleBinary.Version)
		return true, nil
	}
	if opts.forUpdate {
		s.wakeMu.Lock()
		if s.updateStops == nil {
			s.updateStops = make(map[domain.SessionID]bool)
		}
		s.updateStops[id] = true
		s.wakeMu.Unlock()
		s.log.Info("chat session stopped for a harness update", "session", id, "harness", fresh.Harness)
		return true, nil
	}
	s.log.Info("chat session hibernated", "session", id, "harness", fresh.Harness)
	return true, nil
}

func (s *Service) clearHibernation(ctx context.Context, id domain.SessionID) error {
	s.wakeMu.Lock()
	delete(s.updateStops, id)
	s.wakeMu.Unlock()
	if s.sessions == nil {
		return nil
	}
	marker, ok := s.sessions.(hibernationStore)
	for range 3 {
		rec, found, err := s.sessions.GetSession(ctx, id)
		if err != nil || !found || rec.HibernatedAt == nil {
			return err
		}
		if !ok {
			return errors.New("chat hibernation store is unavailable")
		}
		cleared, err := marker.SetSessionHibernated(ctx, id, rec.Revision, nil)
		if err != nil {
			return err
		}
		if cleared {
			return nil
		}
	}
	return errors.New("chat hibernation marker changed concurrently")
}

func (s *Service) readingController(ctx context.Context, id domain.SessionID) (*Controller, domain.SessionRecord, func(), error) {
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	if err := gate.lock(ctx); err != nil {
		return nil, domain.SessionRecord{}, nil, err
	}
	rec, err := s.requireChatSession(ctx, id)
	if err != nil {
		gate.unlock()
		return nil, domain.SessionRecord{}, nil, err
	}
	if rec.HibernatedAt != nil {
		gate.unlock()
		return nil, domain.SessionRecord{}, nil, ErrNoController
	}
	controller, err := s.Controller(id)
	if err != nil || controller.State() == ports.ChatControllerStopped {
		gate.unlock()
		return nil, domain.SessionRecord{}, nil, ErrNoController
	}
	release := controller.beginOperation()
	gate.unlock()
	return controller, rec, release, nil
}

var errAsleep = errors.New("chat session is asleep")

func (s *Service) workingController(ctx context.Context, id domain.SessionID) (*Controller, func(), error) {
	return s.admitController(ctx, id, true)
}

func (s *Service) admitController(ctx context.Context, id domain.SessionID, wakeAsleep bool) (*Controller, func(), error) {
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	for {
		if !gate.tryLock() {
			if controller, err := s.Controller(id); err == nil {
				controller.mu.Lock()
				handoff := controller.handoff
				controller.mu.Unlock()
				if handoff != controllerHandoffNone && handoff != controllerHandoffHibernate {
					return nil, nil, ErrControllerHandoff
				}
			}
			if err := gate.lock(ctx); err != nil {
				return nil, nil, err
			}
		}
		rec, err := s.requireChatSession(ctx, id)
		if err != nil {
			gate.unlock()
			return nil, nil, err
		}
		if rec.HibernatedAt == nil {
			controller, err := s.Controller(id)
			if err == nil {
				controller.mu.Lock()
				hibernating := controller.handoff == controllerHandoffHibernate
				stopped := controller.state == ports.ChatControllerStopped
				forkDone := controller.forkDone
				controller.mu.Unlock()
				if forkDone != nil {
					gate.unlock()
					select {
					case <-forkDone:
						continue
					case <-ctx.Done():
						return nil, nil, ctx.Err()
					}
				}
				if hibernating {
					gate.unlock()
					select {
					case <-controller.stopped:
						continue
					case <-ctx.Done():
						return nil, nil, ctx.Err()
					}
				}
				if !stopped {
					release := controller.beginOperation()
					gate.unlock()
					return controller, release, nil
				}
			}
			if !s.isWaking(id) {
				gate.unlock()
				return nil, nil, ErrNoController
			}
		}
		gate.unlock()
		if rec.HibernatedAt != nil && !wakeAsleep {
			return nil, nil, errAsleep
		}
		if err := s.wakeHibernated(ctx, id); err != nil {
			return nil, nil, err
		}
	}
}

func (s *Service) wakeHibernated(ctx context.Context, id domain.SessionID) error {
	if s.wakeChat == nil {
		return ErrNoController
	}
	if controller, err := s.Controller(id); err == nil {
		controller.mu.Lock()
		hibernating := controller.handoff == controllerHandoffHibernate
		controller.mu.Unlock()
		if hibernating {
			waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			gate := s.controllerGate(domain.SessionConversationOwner(id))
			if err := gate.lock(waitCtx); err != nil {
				return err
			}
			select {
			case <-controller.stopped:
			default:
				current, _ := s.Controller(id)
				if current == controller && s.stopProviderHost != nil {
					if err := s.stopProviderHost(waitCtx, id); err != nil {
						gate.unlock()
						return fmt.Errorf("finish hibernated chat shutdown: %w", err)
					}
				}
			}
			gate.unlock()
			select {
			case <-controller.stopped:
			case <-waitCtx.Done():
				return waitCtx.Err()
			}
		}
	}
	s.wakeMu.Lock()
	if run := s.wakeRuns[id]; run != nil {
		s.wakeMu.Unlock()
		joined := time.Now()
		s.log.Info("chat wake joined an in-flight wake", "session", id,
			"wakeRunningFor", joined.Sub(run.started).Round(time.Millisecond))
		select {
		case <-run.done:
			if run.err != nil {
				s.log.Warn("joined chat wake failed", "session", id,
					"waited", time.Since(joined).Round(time.Millisecond), "err", run.err)
			}
			return run.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	rec, err := s.requireChatSession(ctx, id)
	if err != nil {
		s.wakeMu.Unlock()
		return err
	}
	if rec.HibernatedAt == nil {
		s.wakeMu.Unlock()
		if s.HasLiveChatController(id) {
			return nil
		}
		return ErrNoController
	}
	run := &wakeRun{done: make(chan struct{}), started: time.Now()}
	s.wakeRuns[id] = run
	s.waking[id]++
	s.wakeMu.Unlock()
	s.log.Info("chat wake started", "session", id, "callers", wakeCallers())
	finish := func(resultErr error) error {
		if resultErr != nil {
			s.log.Warn("chat wake failed", "session", id,
				"took", time.Since(run.started).Round(time.Millisecond), "err", resultErr)
			captureChatFault(ctx, resultErr, "", "chat_wake", "wake")
		} else {
			s.log.Info("chat wake finished", "session", id,
				"took", time.Since(run.started).Round(time.Millisecond))
		}
		s.wakeMu.Lock()
		run.err = resultErr
		delete(s.wakeRuns, id)
		s.waking[id]--
		if s.waking[id] == 0 {
			delete(s.waking, id)
		}
		close(run.done)
		s.wakeMu.Unlock()
		return resultErr
	}
	wakeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	if err := s.wakeChat(wakeCtx, id); err != nil {
		return finish(err)
	}
	if !s.HasLiveChatController(id) {
		return finish(ErrNoController)
	}
	return finish(nil)
}

func (s *Service) startBackgroundWake(id domain.SessionID) {
	s.wakeMu.Lock()
	if s.backgroundWakes[id] {
		s.wakeMu.Unlock()
		return
	}
	s.backgroundWakes[id] = true
	s.wakeMu.Unlock()
	go func() {
		defer func() {
			s.wakeMu.Lock()
			delete(s.backgroundWakes, id)
			s.wakeMu.Unlock()
		}()
		ctx := context.Background()
		if err := s.wakeHibernated(ctx, id); err != nil {
			if s.stoppedForUpdate(id) {
				s.log.Warn("chat session could not wake during a harness update; queued messages kept for its resume", "session", id, "err", err)
				return
			}
			if s.versionSwitchFailed(ctx, id, err) {
				return
			}
			s.log.Warn("chat session could not wake; failing its queued messages", "session", id, "err", err)
			s.failQueuedTurns(ctx, id, fmt.Sprintf("could not wake the agent: %v", err))
			return
		}
		if version, switched := s.takeVersionSwitch(id); switched {
			s.observeVersionSwitch(id, version, "succeeded")
		}
		if err := s.DrainQueued(ctx, id); err != nil {
			s.log.Warn("chat session woke but could not deliver its queued messages", "session", id, "err", err)
			captureChatFault(ctx, err, "", "chat_wake", "drain")
			s.failQueuedTurns(ctx, id, fmt.Sprintf("could not deliver the message: %v", err))
		}
	}()
}

func (s *Service) failQueuedTurns(ctx context.Context, id domain.SessionID, message string) {
	conversation, err := s.store.ConversationForSession(ctx, id)
	if err != nil {
		return
	}
	for {
		queued, err := s.store.NextQueuedTurn(ctx, conversation.ID)
		if errors.Is(err, domain.ErrNoQueuedTurn) {
			return
		}
		if err != nil {
			return
		}
		if err := s.store.SettleTurnByID(ctx, queued.TurnID, domain.TurnStateFailed, message, s.now()); err != nil {
			return
		}
	}
}

func (s *Service) isWaking(id domain.SessionID) bool {
	live := s.HasLiveChatController(id)
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	if live {
		delete(s.updateStops, id)
	}
	return s.waking[id] > 0 || s.updateStops[id]
}

func (s *Service) stoppedForUpdate(id domain.SessionID) bool {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	return s.updateStops[id]
}
