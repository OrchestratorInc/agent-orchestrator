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
		return out, nil //nolint:nilerr // An unattached controller is an unavailable reading, not a provider read failure.
	}
	inspector, ok := controller.conv.(ports.ChatBackgroundTaskInspector)
	if !ok {
		return out, nil
	}
	// Runs that ended in this reading keep their final log. Read once every
	// return has released backgroundMu: reading files must not hold up every
	// other reading and output poll waiting on it.
	var over []string
	defer func() { controller.keepFinalLogs(over) }()
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

		func() {
			// Always release followers, including when the provider panics and the
			// HTTP recovery boundary handles it. A panic must publish no sample.
			defer func() {
				controller.backgroundMu.Lock()
				controller.backgroundInFlight = nil
				close(flight)
				controller.backgroundMu.Unlock()
			}()
			readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			sample, readErr := inspector.BackgroundTasks(readCtx)
			controller.backgroundMu.Lock()
			defer controller.backgroundMu.Unlock()
			// A stop invalidates samples already in flight, as well as cached samples.
			// The canceled leader must not cache its cancellation for other readers.
			if ctx.Err() == nil && version == controller.backgroundVersion {
				controller.backgroundPartial = errors.Is(readErr, ports.ErrBackgroundTasksPartial)
				if controller.backgroundPartial {
					readErr = nil
				}
				controller.backgroundErr = readErr
				controller.backgroundTasks = append([]ports.ChatBackgroundTask(nil), sample...)
				if readErr == nil {
					over = controller.rememberBackgroundCommands(sample, controller.backgroundPartial)
				}
				controller.backgroundSampledAt = s.now()
			}
		}()
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
	controller.backgroundVersion++
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
		// The log leads; the provider's own text and its Stale and Truncated
		// flags still describe what it printed.
		truncated := logged.Truncated || output.Truncated
		if own := strings.Trim(output.Text, "\n"); own != "" {
			merged := backgroundtasks.TailOutput(logged.Text+"\n"+own, truncated)
			output.Text, truncated = merged.Text, merged.Truncated
		} else {
			output.Text = logged.Text
		}
		output.Truncated = truncated
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
	output.RunID = controller.backgroundRunID(taskID)
	return output, nil
}

// backgroundRunID returns the provider's run ID for the latest run the
// readings report under taskID, or "" when there is none.
func (c *Controller) backgroundRunID(taskID string) string {
	c.backgroundMu.Lock()
	defer c.backgroundMu.Unlock()
	return c.backgroundCommands[taskID].task.RunID
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
// task leaves the inventory. A run that is over shows the log as it was when it
// ended, never the file as it is now: until that final log is kept it shows
// the tail read while it ran, or none. A log a later run also writes to is no
// longer this run's. A log outside the workspace is not read, and the result
// says so.
func (c *Controller) redirectedBackgroundOutput(taskID string) (ports.ChatBackgroundTaskOutput, bool) {
	c.backgroundMu.Lock()
	entry, ok := c.backgroundCommands[taskID]
	c.backgroundMu.Unlock()
	if !ok {
		return ports.ChatBackgroundTaskOutput{}, false
	}
	if entry.over && !entry.frozen {
		// A reading saw the run end but its final log isn't kept yet: keep it
		// now, bounded by the run's end, rather than read the live file.
		c.keepFinalLogs([]string{taskID})
		c.backgroundMu.Lock()
		entry, ok = c.backgroundCommands[taskID]
		c.backgroundMu.Unlock()
		switch {
		case !ok:
			return ports.ChatBackgroundTaskOutput{}, false
		case entry.over && !entry.frozen && entry.hasLive:
			// The final read failed for now and a later poll tries again.
			return entry.liveLog, true
		case entry.over && !entry.frozen:
			return ports.ChatBackgroundTaskOutput{LogOmitted: true}, false
		}
	}
	if entry.frozen {
		return entry.finalLog, entry.hasLog
	}
	if c.laterRunSharesLog(entry.task) {
		// Listed live again after a later run took over its log: what is in
		// the file now is that run's.
		return ports.ChatBackgroundTaskOutput{LogOmitted: true}, false
	}
	logged, ok := backgroundtasks.RedirectedOutput(entry.task.Command, c.workspacePath, entry.task.StartedAt)
	if ok {
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
	// run is a token from Controller.backgroundRuns, replaced whenever the
	// entry is marked over or starts a new run, so a log read for an earlier
	// state is never stored under a later one, even after eviction.
	run uint64
	// liveLog: the last tail read while the run was live.
	liveLog ports.ChatBackgroundTaskOutput
	hasLive bool
	// finalTries counts final log reads that failed in a way a later read can
	// get past, such as a file still locked by its writer.
	finalTries int
	// frozen: finalLog and hasLog hold the log as the run left it.
	frozen   bool
	finalLog ports.ChatBackgroundTaskOutput
	hasLog   bool
}

// backgroundCommandsKept bounds how many tasks no longer listed keep their
// command; every listed task keeps its own.
const backgroundCommandsKept = 32

// finalLogTries bounds how often a run's final log read is tried again after
// a failure that may pass; the run then keeps what it has.
const finalLogTries = 3

// nextBackgroundRun returns a run token no entry has had. Caller holds backgroundMu.
func (c *Controller) nextBackgroundRun() uint64 {
	c.backgroundRuns++
	return c.backgroundRuns
}

// rememberBackgroundCommands keeps each read task's command after the task
// leaves the 30-second inventory, so an open tab keeps showing its log file.
// Listed tasks always keep theirs; of the others, the oldest goes first. It
// returns the tasks whose run is now over, whose final log keepFinalLogs reads
// once backgroundMu is released. A task that leaves the readings while live is
// over too: Codex drops a vanished terminal, and a task can settle and age out
// between two readings. A partial reading can leave out a task that still
// runs, so only a complete one ends the tasks it doesn't list. Caller holds
// backgroundMu.
func (c *Controller) rememberBackgroundCommands(tasks []ports.ChatBackgroundTask, partial bool) []string {
	if c.backgroundCommands == nil {
		c.backgroundCommands = make(map[string]backgroundCommand)
	}
	var over []string
	listed := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		listed[task.ID] = true
		entry, ok := c.backgroundCommands[task.ID]
		if task.Command == "" {
			// A sparse update of the remembered run keeps its command. A new run
			// under the same ID, whose command isn't known yet, drops the last
			// run's: its log is not this run's.
			if ok && !sameRun(entry.task, task) {
				c.backgroundCommands[task.ID] = backgroundCommand{task: task, run: c.nextBackgroundRun()}
			}
			continue
		}
		switch {
		case !ok:
			c.backgroundCommandOrder = append(c.backgroundCommandOrder, task.ID)
			entry = backgroundCommand{run: c.nextBackgroundRun()}
		case !sameRun(entry.task, task):
			// The provider reused the ID for a new run: nothing of the last
			// run's log is this one's.
			entry = backgroundCommand{run: c.nextBackgroundRun()}
		}
		entry.task = task
		live := task.State == "running" || task.State == "paused"
		switch {
		case entry.vanished && live:
			// Listed live again: its log is its own to follow again.
			entry = backgroundCommand{task: task, run: c.nextBackgroundRun(), liveLog: entry.liveLog, hasLive: entry.hasLive}
		case !entry.over && !live:
			entry.over, entry.run = true, c.nextBackgroundRun()
			over = append(over, task.ID)
		}
		c.backgroundCommands[task.ID] = entry
	}
	if !partial {
		for id, entry := range c.backgroundCommands {
			if listed[id] || entry.over {
				continue
			}
			entry.over, entry.vanished, entry.run = true, true, c.nextBackgroundRun()
			c.backgroundCommands[id] = entry
			over = append(over, id)
		}
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
// later run writes to the same file, and a settled run's log was last written
// by its end. A run that left the readings without an end time has no such
// bound, so once its log was read while it ran it keeps that tail: matching
// text in the file now would not prove the bytes are its own. Otherwise the
// run keeps the tail read while it ran, or, never read, no log (LogOmitted).
// A read that failed in a way that may pass is tried again by a later output
// poll, up to finalLogTries times.
func (c *Controller) keepFinalLogs(ids []string) {
	for _, id := range ids {
		c.backgroundMu.Lock()
		entry, ok := c.backgroundCommands[id]
		c.backgroundMu.Unlock()
		if !ok || !entry.over || entry.frozen {
			continue
		}
		var logged ports.ChatBackgroundTaskOutput
		has, retry := false, false
		switch {
		case c.laterRunSharesLog(entry.task):
			logged.LogOmitted = true
		case entry.task.EndedAt == nil && entry.hasLive:
			// Keeps the live tail, below.
		default:
			logged, has, retry = backgroundtasks.FinalRedirectedOutput(entry.task.Command, c.workspacePath, entry.task.StartedAt, entry.task.EndedAt)
		}
		if !has && entry.hasLive {
			logged, has, retry = entry.liveLog, true, false
		}
		c.backgroundMu.Lock()
		if current, ok := c.backgroundCommands[id]; ok && current.over && !current.frozen && current.run == entry.run {
			if retry && current.finalTries+1 < finalLogTries {
				current.finalTries++
			} else {
				current.frozen, current.finalLog, current.hasLog = true, logged, has
			}
			c.backgroundCommands[id] = current
		}
		c.backgroundMu.Unlock()
	}
}

// laterRunSharesLog reports a remembered task that started after task and
// writes its output to the same log file, however its command spells the
// path. It stats files, so the caller must not hold backgroundMu.
func (c *Controller) laterRunSharesLog(task ports.ChatBackgroundTask) bool {
	if task.StartedAt == nil {
		return false
	}
	var later []string
	c.backgroundMu.Lock()
	for id, other := range c.backgroundCommands {
		if id != task.ID && other.task.StartedAt != nil && other.task.StartedAt.After(*task.StartedAt) {
			later = append(later, other.task.Command)
		}
	}
	c.backgroundMu.Unlock()
	return backgroundtasks.SharesLog(task.Command, later, c.workspacePath)
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
