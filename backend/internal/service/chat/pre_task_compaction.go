package chat

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// defaultPreTaskCompactionDeadline bounds how long a queued task waits for a
// compaction whose provider turn never starts. Real compactions start within
// seconds; this only catches a hung /compact or a lost event.
const defaultPreTaskCompactionDeadline = 5 * time.Minute

// PreTaskCompactionPolicy decides whether a turn sent by sender compacts the
// conversation first. It returns the context size, in tokens, above which the
// conversation compacts, and false when the rule does not apply to this sender
// and conversation. The service builds it per session: the rule is an
// orchestrator handing a worker a new task (see Service.preTaskCompactionPolicy).
type PreTaskCompactionPolicy func(ctx context.Context, sender domain.SessionID) (int64, bool)

// preTaskCompaction is the controller's volatile compact-before-task state.
//
// A worker reused for follow-up work can carry hundreds of thousands of tokens
// of history, and every response to the new task re-reads all of it. Compacting
// once before the task is dispatched pays that cost once instead.
type preTaskCompaction struct {
	// attemptedFor is the queued turn a compaction was already tried for. A
	// compaction that leaves the context above the threshold, or fails, must
	// not start again before the same turn.
	attemptedFor string
	// running is set from the moment the provider accepts the compaction until
	// its turn settles. While set, the queued task waits.
	running bool
	// seq identifies the running compaction, so a deadline armed for an earlier
	// one does nothing.
	seq uint64
	// deadline fires if the compaction's turn has not started in time. Nil when
	// no compaction is running.
	deadline *time.Timer
}

// SetPreTaskCompactionPolicy installs the compact-before-task rule. Nil turns
// it off.
func (c *Controller) SetPreTaskCompactionPolicy(policy PreTaskCompactionPolicy) {
	c.mu.Lock()
	c.preTaskPolicy = policy
	c.mu.Unlock()
}

// wantsPreTaskCompaction reports whether the turn turnID, sent by sender, should
// wait for a compaction. It has no side effects. Callers hold sendMu.
func (c *Controller) wantsPreTaskCompaction(ctx context.Context, turnID, sender string) bool {
	if sender == "" || turnID == "" || c.reviewID != "" {
		return false
	}
	c.mu.Lock()
	policy := c.preTaskPolicy
	used := c.usage.ContextUsed
	attempted := c.preTask.attemptedFor == turnID
	c.mu.Unlock()
	// No project threshold is below the minimum, so a smaller context skips the
	// policy's session and project reads.
	if policy == nil || attempted || used <= domain.MinWorkerCompactionThreshold {
		return false
	}
	if _, ok := c.compactor(); !ok {
		return false
	}
	threshold, ok := policy(ctx, domain.SessionID(sender))
	return ok && used > threshold
}

// startPreTaskCompaction starts the compaction for turnID and reports whether
// the provider accepted it. On false the caller dispatches the turn: a refused
// compaction must never hold the task back. Callers hold sendMu.
func (c *Controller) startPreTaskCompaction(ctx context.Context, turnID string) bool {
	c.mu.Lock()
	c.preTask.attemptedFor = turnID
	used := c.usage.ContextUsed
	c.mu.Unlock()
	if _, err := c.compactLocked(ctx); err != nil {
		c.log.Info("compact before task refused; delivering the task as is",
			"session", c.sessionID, "turn", turnID, "contextUsed", used, "error", err)
		return false
	}
	c.mu.Lock()
	c.preTask.running = true
	c.preTask.seq++
	seq := c.preTask.seq
	wait := c.preTaskDeadline
	if wait <= 0 {
		wait = defaultPreTaskCompactionDeadline
	}
	c.preTask.deadline = time.AfterFunc(wait, func() { c.expirePreTaskCompaction(seq) })
	c.mu.Unlock()
	c.log.Info("compacting before an orchestrator task",
		"session", c.sessionID, "turn", turnID, "contextUsed", used)
	return true
}

// compactBeforeQueuedBatch starts a compaction ahead of the next queued batch
// when any turn in it is an orchestrator's task over the threshold. The drain
// sends consecutive non-human turns as one, so a CI or review notice ahead of
// the task must not hide it. The attempt is keyed on the batch's head turn. It
// reports true when the drain must stop: the compaction's settlement drains the
// batch. Callers hold sendMu.
func (c *Controller) compactBeforeQueuedBatch(ctx context.Context, batch []domain.QueuedTurn) bool {
	if len(batch) == 0 {
		return false
	}
	head := batch[0].TurnID
	checked := make(map[string]bool, len(batch))
	for _, turn := range batch {
		sender := turn.SenderSessionID
		if sender == "" || checked[sender] {
			continue
		}
		checked[sender] = true
		if c.wantsPreTaskCompaction(ctx, head, sender) {
			return c.startPreTaskCompaction(ctx, head)
		}
	}
	return false
}

// finishPreTaskCompaction clears the running compaction and reports whether
// there was one. Its settlement releases the queue whatever the outcome: the
// task would have been dispatched without the compaction, so a failed
// compaction must not strand it behind the hold a failed turn otherwise
// places on queued work.
func (c *Controller) finishPreTaskCompaction() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	running := c.preTask.running
	c.preTask.running = false
	if c.preTask.deadline != nil {
		c.preTask.deadline.Stop()
		c.preTask.deadline = nil
	}
	return running
}

// expirePreTaskCompaction releases a task whose compaction the provider
// accepted but never started: a hung /compact or a lost event would otherwise
// hold it, and every later send behind it, with nobody watching. The task then
// runs on the uncompacted context, as after a refused compaction. A compaction
// whose turn did start is left to the turn path, which shows Stop and releases
// the task when the turn settles, including when it fails.
func (c *Controller) expirePreTaskCompaction(seq uint64) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	select {
	case <-c.stopped:
		return
	default:
	}
	c.mu.Lock()
	if !c.preTask.running || c.preTask.seq != seq || c.pendingTurnID != "" {
		c.mu.Unlock()
		return
	}
	c.preTask.running = false
	c.preTask.deadline = nil
	c.compactionPending = false
	turnID := c.preTask.attemptedFor
	c.mu.Unlock()
	c.log.Warn("compact before task never started; delivering the task as is",
		"session", c.sessionID, "turn", turnID)
	_ = c.drainLocked(context.Background(), true) // drain logs failures.
}

// preTaskCompactionPolicy builds the compact-before-task rule for the Chat
// session target: a turn compacts first only when target is a worker and the
// sender is an orchestrator session, over the target project's threshold. Any
// read that fails leaves the turn alone; a lookup error must never hold a task.
func (s *Service) preTaskCompactionPolicy(target domain.SessionID) PreTaskCompactionPolicy {
	if s.projects == nil || s.sessions == nil {
		return nil
	}
	return func(ctx context.Context, sender domain.SessionID) (int64, bool) {
		worker, found, err := s.sessions.GetSession(ctx, target)
		if err != nil || !found || worker.Kind != domain.KindWorker {
			return 0, false
		}
		from, found, err := s.sessions.GetSession(ctx, sender)
		if err != nil || !found || from.Kind != domain.KindOrchestrator {
			return 0, false
		}
		project, found, err := s.projects.GetProject(ctx, string(worker.ProjectID))
		if err != nil || !found {
			return 0, false
		}
		return project.Config.WorkerCompaction.Threshold()
	}
}
