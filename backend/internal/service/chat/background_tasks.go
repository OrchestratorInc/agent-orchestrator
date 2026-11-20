package chat

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/backgroundtasks"
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
		return out, nil
	}
	inspector, ok := controller.conv.(ports.ChatBackgroundTaskInspector)
	if !ok {
		return out, nil
	}
	// Shared across board/detail polling and scoped to this exact controller.
	controller.backgroundMu.Lock()
	defer controller.backgroundMu.Unlock()
	cacheFor := time.Second
	if controller.backgroundErr != nil {
		cacheFor = 5 * time.Second
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if controller.backgroundSampledAt.IsZero() || s.now().Sub(controller.backgroundSampledAt) >= cacheFor {
		readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		tasks, readErr := inspector.BackgroundTasks(readCtx)
		cancel()
		if errors.Is(readErr, ports.ErrBackgroundTasksUnsupported) {
			return out, nil
		}
		if readErr != nil && !errors.Is(readErr, ports.ErrBackgroundTasksPartial) {
			// Keep a failed sample longer than the read timeout, so concurrent and
			// repeated readers share this attempt instead of queueing for their
			// own. A caller's own cancellation is not the provider's failure.
			if ctx.Err() == nil {
				controller.backgroundErr = readErr
				controller.backgroundSampledAt = s.now()
			}
			return out, readErr
		}
		controller.backgroundErr = nil
		controller.backgroundPartial = errors.Is(readErr, ports.ErrBackgroundTasksPartial)
		controller.backgroundTasks = append([]ports.ChatBackgroundTask(nil), tasks...)
		for _, task := range tasks {
			controller.rememberBackgroundCommand(task)
		}
		controller.backgroundSampledAt = s.now()
	}
	if controller.backgroundErr != nil {
		return out, controller.backgroundErr
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
	if controller.backgroundPartial {
		out.Availability = "partial"
	}
	at := controller.backgroundSampledAt
	out.ObservedAt = &at
	for _, task := range controller.backgroundTasks {
		if task.State != "running" && task.State != "paused" && (task.EndedAt == nil || s.now().Sub(*task.EndedAt) >= 30*time.Second) {
			continue
		}
		task.Description = domain.SanitizeTaskDescription(task.Description)
		task.Command = domain.SanitizeTaskCommand(task.Command)
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
	controller, err := s.attachedChatController(ctx, id)
	if err != nil {
		return err
	}
	if controller == nil {
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
	controller.backgroundSampledAt = time.Time{}
	controller.backgroundMu.Unlock()
	return nil
}

// BackgroundTaskOutput reads one task's recent output. Like a reading, it
// never starts a controller or wakes a hibernated session; output that cannot
// be read without one, or that the provider failed to return, is unavailable.
func (s *Service) BackgroundTaskOutput(ctx context.Context, id domain.SessionID, taskID string) (ports.ChatBackgroundTaskOutput, error) {
	controller, err := s.attachedChatController(ctx, id)
	if err != nil {
		return ports.ChatBackgroundTaskOutput{}, err
	}
	if controller == nil {
		return ports.ChatBackgroundTaskOutput{}, ports.ErrBackgroundTaskOutputUnavailable
	}
	output, err := ports.ChatBackgroundTaskOutput{}, ports.ErrBackgroundTaskOutputUnavailable
	if reader, ok := controller.conv.(ports.ChatBackgroundTaskOutputReader); ok {
		readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		output, err = reader.BackgroundTaskOutput(readCtx, taskID)
		cancel()
	}
	if err := ctx.Err(); err != nil {
		return ports.ChatBackgroundTaskOutput{}, err
	}
	if err != nil {
		// A panel polls this: a controller closing mid-read, a timeout or a
		// provider error is a moment without output, not a server failure.
		if !errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) {
			s.log.Debug("background task output unavailable", "session", id, "task", taskID, "error", err)
		}
		output, err = ports.ChatBackgroundTaskOutput{}, ports.ErrBackgroundTaskOutputUnavailable
	}
	// A command that writes a log file prints little of its own: the log is its
	// output. Whatever the task did print follows it, such as stderr the command
	// left alone or Claude Code's "[killed]" once the task is stopped.
	if logged, ok := controller.redirectedBackgroundOutput(taskID); ok {
		if own := strings.Trim(output.Text, "\n"); own != "" {
			output = backgroundtasks.TailOutput(logged.Text+"\n"+own, logged.Truncated || output.Truncated)
		} else {
			output = logged
		}
		err = nil
	}
	if err != nil {
		return ports.ChatBackgroundTaskOutput{}, err
	}
	// The read can cross termination, a handoff, or a provider replacement.
	if current, err := s.attachedChatController(ctx, id); err != nil || current != controller {
		return ports.ChatBackgroundTaskOutput{}, ports.ErrBackgroundTaskOutputUnavailable
	}
	return output, nil
}

// attachedChatController returns the session's live controller for its
// current generation, never starting or waking one. It returns nil without an
// error when the session has none, and ErrSessionNotFound for an unknown session.
func (s *Service) attachedChatController(ctx context.Context, id domain.SessionID) (*Controller, error) {
	rec, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ports.ErrSessionNotFound
	}
	if rec.IsTerminated || rec.Mode != domain.SessionModeChat || rec.HibernatedAt != nil {
		return nil, nil
	}
	controller, err := s.Controller(id)
	if err != nil || controller.State() == ports.ChatControllerStopped || controller.generation != rec.Metadata.ControllerGeneration {
		return nil, nil //nolint:nilerr // No attached controller is an answer, not a failure.
	}
	return controller, nil
}

// redirectedBackgroundOutput reads the log file a background task's command
// sends its output to. The command comes from the controller's task readings,
// so it is known only once the task has been read, and stays known after the
// task leaves the inventory. A settled task shows the log as it was when a
// reading first saw it settled, not whatever a later run wrote to that path.
func (c *Controller) redirectedBackgroundOutput(taskID string) (ports.ChatBackgroundTaskOutput, bool) {
	c.backgroundMu.Lock()
	entry, ok := c.backgroundCommands[taskID]
	c.backgroundMu.Unlock()
	if !ok {
		return ports.ChatBackgroundTaskOutput{}, false
	}
	if entry.settled {
		return entry.finalLog, entry.hasLog
	}
	return backgroundtasks.RedirectedOutput(entry.task.Command, c.workspacePath, entry.task.StartedAt)
}

// backgroundCommand is what a task's readings leave for its output: its
// command and, once it settled, the bounded tail its log had then.
type backgroundCommand struct {
	task     ports.ChatBackgroundTask
	settled  bool
	finalLog ports.ChatBackgroundTaskOutput
	hasLog   bool
}

// backgroundCommandsKept bounds how many tasks keep their command.
const backgroundCommandsKept = 32

// rememberBackgroundCommand keeps a read task's command after the task leaves
// the 30-second inventory, so an open tab keeps showing its log file. The
// oldest goes first. Caller holds backgroundMu.
func (c *Controller) rememberBackgroundCommand(task ports.ChatBackgroundTask) {
	if task.Command == "" {
		return
	}
	if c.backgroundCommands == nil {
		c.backgroundCommands = make(map[string]backgroundCommand)
	}
	entry, ok := c.backgroundCommands[task.ID]
	if !ok {
		if len(c.backgroundCommandOrder) >= backgroundCommandsKept {
			delete(c.backgroundCommands, c.backgroundCommandOrder[0])
			c.backgroundCommandOrder = c.backgroundCommandOrder[1:]
		}
		c.backgroundCommandOrder = append(c.backgroundCommandOrder, task.ID)
	}
	entry.task = task
	if !entry.settled && task.State != "running" && task.State != "paused" {
		// Keep this run's final tail: a restart that reuses the log path must
		// not show its output under the run that ended.
		entry.settled = true
		entry.finalLog, entry.hasLog = backgroundtasks.RedirectedOutput(task.Command, c.workspacePath, task.StartedAt)
	}
	c.backgroundCommands[task.ID] = entry
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
