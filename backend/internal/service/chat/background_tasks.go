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
	// Runs that ended in this reading keep their final log. Deferred before the
	// unlock below, so it runs after it: reading files must not hold up every
	// other reading and output poll waiting on backgroundMu.
	var over []string
	defer func() { controller.keepFinalLogs(over) }()
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
		over = controller.rememberBackgroundCommands(tasks)
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
	if err != nil && ctx.Err() != nil {
		// The panel cancelled its poll; that is not a server failure.
		return ports.ChatBackgroundTaskOutput{}, ports.ErrBackgroundTaskOutputUnavailable
	}
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
	if ctx.Err() != nil {
		// The panel cancelled its poll: a moment without output, not a server failure.
		return ports.ChatBackgroundTaskOutput{}, ports.ErrBackgroundTaskOutputUnavailable
	}
	// readFailed: the provider was asked and failed, rather than not reporting
	// this task's output at all.
	readFailed := false
	if err != nil {
		// A panel polls this: a controller closing mid-read, a timeout or a
		// provider error is a moment without output, not a server failure.
		if !errors.Is(err, ports.ErrBackgroundTaskOutputUnavailable) {
			readFailed = true
			s.log.Warn("background task output read failed", "session", id, "task", taskID, "error", err)
		}
		output, err = ports.ChatBackgroundTaskOutput{}, ports.ErrBackgroundTaskOutputUnavailable
	}
	// A command that writes a log file prints little of its own: the log is its
	// output. Whatever the task did print follows it, such as stderr the command
	// left alone or Claude Code's "[killed]" once the task is stopped.
	logged, ok := controller.redirectedBackgroundOutput(taskID)
	switch {
	case ok:
		if own := strings.Trim(output.Text, "\n"); own != "" {
			output = backgroundtasks.TailOutput(logged.Text+"\n"+own, logged.Truncated || output.Truncated)
		} else {
			output = logged
		}
		err = nil
	case logged.LogOmitted && !readFailed:
		// Say that the log was left out, so the panel can keep the one it read.
		output.LogOmitted, err = true, nil
	case logged.LogOutsideWorkspace && !readFailed:
		// Say why the log isn't shown, rather than that there is no output. A
		// failed read stays unavailable instead: the panel keeps what it read.
		output.LogOutsideWorkspace, err = true, nil
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
// task leaves the inventory. A run that is over shows the log as it was when a
// reading first saw it end or leave, not whatever a later run wrote to that
// path. A log outside the workspace is not read, and the result says so.
func (c *Controller) redirectedBackgroundOutput(taskID string) (ports.ChatBackgroundTaskOutput, bool) {
	c.backgroundMu.Lock()
	entry, ok := c.backgroundCommands[taskID]
	c.backgroundMu.Unlock()
	if !ok {
		return ports.ChatBackgroundTaskOutput{}, false
	}
	if entry.frozen {
		return entry.finalLog, entry.hasLog
	}
	logged, ok := backgroundtasks.RedirectedOutput(entry.task.Command, c.workspacePath, entry.task.StartedAt)
	if ok && !entry.over {
		// The tail seen while the run is live: what it keeps if, by the time a
		// reading sees it end, its log is no longer its own.
		c.backgroundMu.Lock()
		if current, found := c.backgroundCommands[taskID]; found && current.run == entry.run && !current.over {
			current.liveLog, current.hasLive = logged, true
			c.backgroundCommands[taskID] = current
		}
		c.backgroundMu.Unlock()
	}
	return logged, ok
}

// backgroundCommand is what a task's readings leave for its output: its
// command and, once its run is over, the bounded tail its log had then.
type backgroundCommand struct {
	task ports.ChatBackgroundTask
	// over: a reading saw the run settle, or leave the readings while live.
	// vanished: the latter, which a reading listing it live again undoes.
	over, vanished bool
	// run changes whenever the entry is marked over or starts a new run, so a
	// log read for an earlier state is not stored under a later one.
	run uint64
	// liveLog: the last tail read while the run was live.
	liveLog ports.ChatBackgroundTaskOutput
	hasLive bool
	// frozen: finalLog and hasLog hold the log as the run left it.
	frozen   bool
	finalLog ports.ChatBackgroundTaskOutput
	hasLog   bool
}

// backgroundCommandsKept bounds how many tasks no longer listed keep their
// command; every listed task keeps its own.
const backgroundCommandsKept = 32

// rememberBackgroundCommands keeps each read task's command after the task
// leaves the 30-second inventory, so an open tab keeps showing its log file.
// Listed tasks always keep theirs; of the others, the oldest goes first. It
// returns the tasks whose run is now over, whose final log keepFinalLogs reads
// once backgroundMu is released. A task that leaves the readings while live is
// over too: Codex drops a vanished terminal, and a task can settle and age out
// between two readings. Caller holds backgroundMu.
func (c *Controller) rememberBackgroundCommands(tasks []ports.ChatBackgroundTask) []string {
	if c.backgroundCommands == nil {
		c.backgroundCommands = make(map[string]backgroundCommand)
	}
	var over []string
	listed := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		listed[task.ID] = true
		if task.Command == "" {
			continue
		}
		entry, ok := c.backgroundCommands[task.ID]
		if !ok {
			c.backgroundCommandOrder = append(c.backgroundCommandOrder, task.ID)
		} else if !sameRun(entry.task, task) {
			// The provider reused the ID for a new run: nothing of the last
			// run's log is this one's.
			entry = backgroundCommand{run: entry.run + 1}
		}
		entry.task = task
		live := task.State == "running" || task.State == "paused"
		switch {
		case entry.vanished && live:
			// Listed live again: its log is its own to follow again.
			entry = backgroundCommand{task: task, run: entry.run + 1, liveLog: entry.liveLog, hasLive: entry.hasLive}
		case !entry.over && !live:
			entry.over, entry.run = true, entry.run+1
			over = append(over, task.ID)
		}
		c.backgroundCommands[task.ID] = entry
	}
	for id, entry := range c.backgroundCommands {
		if listed[id] || entry.over {
			continue
		}
		entry.over, entry.vanished, entry.run = true, true, entry.run+1
		c.backgroundCommands[id] = entry
		over = append(over, id)
	}
	// Bound only the tasks no longer listed, oldest first.
	unlisted := 0
	for _, id := range c.backgroundCommandOrder {
		if !listed[id] {
			unlisted++
		}
	}
	order := c.backgroundCommandOrder[:0]
	for _, id := range c.backgroundCommandOrder {
		if unlisted > backgroundCommandsKept && !listed[id] {
			delete(c.backgroundCommands, id)
			unlisted--
			continue
		}
		order = append(order, id)
	}
	c.backgroundCommandOrder = order
	return over
}

// sameRun reports whether two readings of a task ID are one run: a provider
// can reuse an ID, and then gives the new run its own run ID or start time.
func sameRun(a, b ports.ChatBackgroundTask) bool {
	if a.RunID != "" && b.RunID != "" && a.RunID != b.RunID {
		return false
	}
	return a.StartedAt == nil || b.StartedAt == nil || a.StartedAt.Equal(*b.StartedAt)
}

// keepFinalLogs reads and keeps the final log tail of each run that is over,
// so a restart that reuses the log path never shows its output under the run
// that ended. It reads files, so the caller must not hold backgroundMu.
//
// The file is the run's only while nothing else has written it since: no
// later run of the same path is known, a settled run's log was last written
// by its end, and a vanished run's log still holds the last line read while it
// ran. Otherwise the run keeps the tail read while it ran, or, never read, no
// log (LogOmitted): what is in the file now is not its output.
func (c *Controller) keepFinalLogs(ids []string) {
	for _, id := range ids {
		c.backgroundMu.Lock()
		entry, ok := c.backgroundCommands[id]
		superseded := ok && c.laterRunWritesTo(entry.task)
		c.backgroundMu.Unlock()
		if !ok || !entry.over || entry.frozen {
			continue
		}
		var logged ports.ChatBackgroundTaskOutput
		has := false
		if !superseded {
			logged, has = backgroundtasks.RedirectedOutputUntil(entry.task.Command, c.workspacePath, entry.task.StartedAt, entry.task.EndedAt)
			if has && entry.task.EndedAt == nil && entry.hasLive && !continuesLog(logged.Text, entry.liveLog.Text) {
				logged, has = ports.ChatBackgroundTaskOutput{LogOmitted: true}, false
			}
		} else {
			logged.LogOmitted = true
		}
		if !has && entry.hasLive {
			logged, has = entry.liveLog, true
		}
		c.backgroundMu.Lock()
		if current, ok := c.backgroundCommands[id]; ok && current.over && !current.frozen && current.run == entry.run {
			current.frozen, current.finalLog, current.hasLog = true, logged, has
			c.backgroundCommands[id] = current
		}
		c.backgroundMu.Unlock()
	}
}

// continuesLog reports whether a log read now still holds the last line read
// while the run was live: a restart that truncated the file has lost it.
func continuesLog(now, seen string) bool {
	seen = strings.TrimRight(seen, "\n")
	last := seen[strings.LastIndexByte(seen, '\n')+1:]
	return last == "" || strings.Contains(now, last)
}

// laterRunWritesTo reports a remembered task that started after task and
// sends its output to the same log. Caller holds backgroundMu.
func (c *Controller) laterRunWritesTo(task ports.ChatBackgroundTask) bool {
	if task.StartedAt == nil {
		return false
	}
	path := backgroundtasks.RedirectTarget(task.Command, c.workspacePath)
	if path == "" {
		return false
	}
	for id, other := range c.backgroundCommands {
		if id == task.ID || other.task.StartedAt == nil || !other.task.StartedAt.After(*task.StartedAt) {
			continue
		}
		if backgroundtasks.RedirectTarget(other.task.Command, c.workspacePath) == path {
			return true
		}
	}
	return false
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
