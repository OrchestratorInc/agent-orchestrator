package chat

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// BackgroundTaskSnapshot is a read model scoped to the current controller.
// Unavailable is deliberately different from a ready snapshot with no tasks.
type BackgroundTaskSnapshot struct {
	SessionID    domain.SessionID
	Generation   string
	Availability string
	ObservedAt   *time.Time
	Tasks        []ports.ChatBackgroundTask
}

// BackgroundTasks never starts a controller or wakes a hibernated session.
func (s *Service) BackgroundTasks(ctx context.Context, id domain.SessionID) (BackgroundTaskSnapshot, error) {
	out := BackgroundTaskSnapshot{SessionID: id, Availability: "unavailable", Tasks: []ports.ChatBackgroundTask{}}
	rec, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return out, err
	}
	if !found {
		return out, ports.ErrSessionNotFound
	}
	out.Generation = rec.Metadata.ControllerGeneration
	if rec.IsTerminated || rec.Mode != domain.SessionModeChat || rec.HibernatedAt != nil {
		return out, nil
	}
	controller, err := s.Controller(id)
	if err != nil || controller.State() == ports.ChatControllerStopped || controller.generation != out.Generation {
		return out, nil //nolint:nilerr // An unattached controller is an unavailable reading, not a provider read failure.
	}
	inspector, ok := controller.conv.(ports.ChatBackgroundTaskInspector)
	if !ok {
		return out, nil
	}
	// Readers share one provider call, but neither I/O nor waiting holds the lock.
	// A waiting reader can cancel without waiting for another caller's timeout.
	var tasks []ports.ChatBackgroundTask
	var sampledAt time.Time
	var partial bool
	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		controller.backgroundMu.Lock()
		cacheFor := time.Second
		if controller.backgroundErr != nil {
			cacheFor = 5 * time.Second
		}
		if !controller.backgroundSampledAt.IsZero() && s.now().Sub(controller.backgroundSampledAt) < cacheFor {
			readErr := controller.backgroundErr
			tasks = append([]ports.ChatBackgroundTask(nil), controller.backgroundTasks...)
			sampledAt, partial = controller.backgroundSampledAt, controller.backgroundPartial
			controller.backgroundMu.Unlock()
			if errors.Is(readErr, ports.ErrBackgroundTasksUnsupported) {
				return out, nil
			}
			if readErr != nil {
				return out, readErr
			}
			break
		}
		if flight := controller.backgroundInFlight; flight != nil {
			controller.backgroundMu.Unlock()
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-flight:
				continue
			}
		}
		flight := make(chan struct{})
		controller.backgroundInFlight = flight
		version := controller.backgroundVersion
		controller.backgroundMu.Unlock()

		readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		sample, readErr := inspector.BackgroundTasks(readCtx)
		cancel()
		controller.backgroundMu.Lock()
		// A stop invalidates samples already in flight, as well as cached samples.
		// The canceled leader must not cache its cancellation for other readers.
		if ctx.Err() == nil && version == controller.backgroundVersion {
			controller.backgroundPartial = errors.Is(readErr, ports.ErrBackgroundTasksPartial)
			if controller.backgroundPartial {
				readErr = nil
			}
			controller.backgroundErr = readErr
			controller.backgroundTasks = append([]ports.ChatBackgroundTask(nil), sample...)
			controller.backgroundSampledAt = s.now()
		}
		controller.backgroundInFlight = nil
		close(flight)
		controller.backgroundMu.Unlock()
	}
	// Provider I/O can cross termination, a handoff, or a provider replacement.
	fresh, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return out, err
	}
	current, _ := s.Controller(id)
	if !found || fresh.IsTerminated || fresh.Mode != domain.SessionModeChat || fresh.HibernatedAt != nil ||
		fresh.Metadata.ControllerGeneration != out.Generation || current != controller || controller.State() == ports.ChatControllerStopped {
		return out, nil
	}
	out.Availability = "ready"
	if partial {
		out.Availability = "partial"
	}
	at := sampledAt
	out.ObservedAt = &at
	for _, task := range tasks {
		if task.State != "running" && task.State != "paused" && (task.EndedAt == nil || s.now().Sub(*task.EndedAt) >= 30*time.Second) {
			continue
		}
		task.Description = domain.SanitizeTaskDescription(task.Description)
		out.Tasks = append(out.Tasks, task)
	}
	sort.SliceStable(out.Tasks, func(i, j int) bool {
		if (out.Tasks[i].State == "running") != (out.Tasks[j].State == "running") {
			return out.Tasks[i].State == "running"
		}
		return out.Tasks[i].ID < out.Tasks[j].ID
	})
	return out, nil
}

// StopBackgroundTask stops one provider-owned background task. Like a reading,
// it never starts a controller or wakes a hibernated session.
func (s *Service) StopBackgroundTask(ctx context.Context, id domain.SessionID, taskID string) error {
	rec, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return ports.ErrSessionNotFound
	}
	if rec.IsTerminated || rec.Mode != domain.SessionModeChat || rec.HibernatedAt != nil {
		return ports.ErrBackgroundTaskNotStoppable
	}
	controller, err := s.Controller(id)
	if err != nil || controller.State() == ports.ChatControllerStopped || controller.generation != rec.Metadata.ControllerGeneration {
		return ports.ErrBackgroundTaskNotStoppable
	}
	stopper, ok := controller.conv.(ports.ChatBackgroundTaskStopper)
	if !ok {
		return ports.ErrBackgroundTaskNotStoppable
	}
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := stopper.StopBackgroundTask(stopCtx, taskID); err != nil {
		return err
	}
	// The next reading must ask the provider again, not reuse the sample
	// taken before the stop.
	controller.backgroundMu.Lock()
	controller.backgroundVersion++
	controller.backgroundSampledAt = time.Time{}
	controller.backgroundMu.Unlock()
	return nil
}

// ListBackgroundTasks inspects only already attached controllers. Sessions
// omitted from this batch have no reading; clients must not invent a zero.
func (s *Service) ListBackgroundTasks(ctx context.Context, projectID domain.ProjectID) ([]BackgroundTaskSnapshot, error) {
	s.mu.RLock()
	ids := make([]domain.SessionID, 0, len(s.controllers))
	for id := range s.controllers {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	out := make([]BackgroundTaskSnapshot, 0, len(ids))
	var mu sync.Mutex
	group, readCtx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for _, id := range ids {
		group.Go(func() error {
			if projectID != "" {
				rec, found, err := s.sessions.GetSession(readCtx, id)
				if err != nil {
					// Without a verified project association, omit only this session.
					return nil //nolint:nilerr // One failed session lookup must not cancel unrelated reads.
				}
				if !found || rec.ProjectID != projectID {
					return nil
				}
			}
			reading, err := s.BackgroundTasks(readCtx, id)
			if err != nil {
				// One unavailable provider must not hide other sessions' tasks.
				reading = BackgroundTaskSnapshot{SessionID: id, Availability: "unavailable", Tasks: []ports.ChatBackgroundTask{}}
			}
			mu.Lock()
			out = append(out, reading)
			mu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}
