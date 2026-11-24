package report

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ReportInterruptWindow is the durable per-worker urgent interruption limit.
const ReportInterruptWindow = 3 * time.Minute

// OrchestratorBusyRetry is how long a batch refused with ErrOrchestratorBusy
// waits before its next delivery attempt.
const OrchestratorBusyRetry = 30 * time.Second

// NoOrchestratorRetry is how long a due batch waits before its next attempt
// when its project has no active orchestrator to receive it.
const NoOrchestratorRetry = 30 * time.Second

// DefaultMaxDeliveryAttempts is the upper bound on delivery attempts for a
// report batch before transitioning it to a terminal failed state.
const DefaultMaxDeliveryAttempts = 10

// DefaultReportExpiry is the time-based backstop after which an undelivered
// report batch transitions to a terminal failed state.
const DefaultReportExpiry = 24 * time.Hour

// ErrReportDeliveryModeUnsupported reports a target without semantic delivery.
var ErrReportDeliveryModeUnsupported = errors.New("report delivery requires a semantic conversation")

// ErrOrchestratorBusy marks a Submit refused because the orchestrator cannot
// take the batch right now — its composer holds the operator's unsent draft —
// and should be retried later rather than immediately.
var ErrOrchestratorBusy = errors.New("orchestrator cannot take reports right now")

// ErrNoActiveOrchestrator marks a due batch whose project has no orchestrator
// to deliver it to. The batch is deferred, not dropped.
var ErrNoActiveOrchestrator = errors.New("no active orchestrator")

// ErrUnacknowledgedDelivery reports a delivery where the prompt was submitted to
// the agent runtime, but acceptance confirmation was not observed.
var ErrUnacknowledgedDelivery = errors.New("prompt submission was not observed")

func isUnacknowledged(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrUnacknowledgedDelivery) || strings.Contains(err.Error(), "prompt submission was not observed")
}

// DeliveryStore is the durable report scheduling and routing boundary.
type DeliveryStore interface {
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListPendingReportSchedule(context.Context, int64) ([]domain.ReportRecord, error)
	ListPendingReportsOfDoneProjects(context.Context) ([]domain.ReportRecord, error)
	ClaimPendingReportBatch(context.Context, domain.ProjectID, string, time.Time) ([]domain.ReportRecord, error)
	AcknowledgeReportBatch(context.Context, domain.ProjectID, string, time.Time) (int, error)
	ReleaseReportBatch(context.Context, domain.ProjectID, string, time.Time, string) (int, error)
	DeferReportBatch(context.Context, domain.ProjectID, string, time.Time, string) (int, error)
	FailReportBatch(context.Context, domain.ProjectID, string, time.Time, string) (int, error)
	RetryUnacknowledgedReportBatch(context.Context, domain.ProjectID, string, time.Time, int, string) (int, error)
	AcquireReportInterrupt(context.Context, domain.SessionID, time.Time, time.Duration) (bool, time.Time, error)
}

// Delivery submits accepted semantic context and interrupts active work.
type Delivery interface {
	Submit(context.Context, domain.SessionID, string, string) error
	Interrupt(context.Context, domain.SessionID) error
}

// Coordinator delivers durable report batches to active orchestrators.
type Coordinator struct {
	store               DeliveryStore
	delivery            Delivery
	now                 func() time.Time
	newToken            func() string
	retryDelay          time.Duration
	maxDeliveryAttempts int
	expiry              time.Duration
	onBatchFailed       func(context.Context, PreparedBatch, error)
	failureSeq          map[domain.ProjectID]int
	failureAckedSeq     map[domain.ProjectID]int
	wake                chan struct{}
	mu                  sync.Mutex
	// earlyHold pauses early flush for a project whose last attempt, early or
	// due, left its batch pending (busy orchestrator, failed submit, or coalesced
	// stuck deferral). Those deferrals cannot move a done row's later settlement
	// deadline, and early flush does not wait for a retry time, so without the
	// hold every wake would retry at once. Guarded by mu.
	earlyHold map[domain.ProjectID]time.Time
	// started prevents immediate retries of batches recovered after restart.
	started time.Time
}

// CoordinatorDeps configures durable scheduling and delivery.
type CoordinatorDeps struct {
	Store               DeliveryStore
	Delivery            Delivery
	Now                 func() time.Time
	NewToken            func() string
	RetryDelay          time.Duration
	MaxDeliveryAttempts int
	Expiry              time.Duration
	// OnBatchFailed is notified when a report batch transitions to the terminal
	// failed state. It executes under the coordinator mutex; implementations
	// must not call Coordinator methods or perform blocking operations.
	OnBatchFailed func(context.Context, PreparedBatch, error)
}

// NewCoordinator constructs an idle report delivery coordinator.
func NewCoordinator(d CoordinatorDeps) *Coordinator {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewToken == nil {
		d.NewToken = uuid.NewString
	}
	if d.RetryDelay <= 0 {
		d.RetryDelay = 5 * time.Second
	}
	if d.MaxDeliveryAttempts <= 0 {
		d.MaxDeliveryAttempts = DefaultMaxDeliveryAttempts
	}
	if d.Expiry == 0 {
		d.Expiry = DefaultReportExpiry
	} else if d.Expiry < 0 {
		d.Expiry = 0
	}
	return &Coordinator{
		store:               d.Store,
		delivery:            d.Delivery,
		now:                 d.Now,
		newToken:            d.NewToken,
		retryDelay:          d.RetryDelay,
		maxDeliveryAttempts: d.MaxDeliveryAttempts,
		expiry:              d.Expiry,
		onBatchFailed:       d.OnBatchFailed,
		failureSeq:          make(map[domain.ProjectID]int),
		failureAckedSeq:     make(map[domain.ProjectID]int),
		wake:                make(chan struct{}, 1),
		earlyHold:           make(map[domain.ProjectID]time.Time),
		started:             d.Now().UTC(),
	}
}

// Wake asks the coordinator to inspect durable scheduling state.
func (c *Coordinator) Wake() {
	if c == nil {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// SessionActivityChanged wakes the coordinator when a session stops working:
// its activity left active or it was terminated. That is the moment a pending
// done batch may become eligible for an early flush. The wake is non-blocking.
func (c *Coordinator) SessionActivityChanged(state domain.ActivityState, terminated bool) {
	if terminated || state != domain.ActivityActive {
		c.Wake()
	}
}

// Start runs delivery until ctx is cancelled and returns its completion signal.
func (c *Coordinator) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() { defer close(done); c.run(ctx) }()
	return done
}

func (c *Coordinator) run(ctx context.Context) {
	for {
		delay, err := c.RunDue(ctx)
		if err != nil {
			delay = c.retryDelay
		}
		if delay < 0 {
			delay = time.Hour
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-c.wake:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

// RunDue performs bounded due delivery and returns the delay until the next
// durable deadline. A pending done batch is flushed before its settlement
// deadline once no other worker of its project is still working (see
// nextEarlyFlush). Tests call it directly with fake time.
func (c *Coordinator) RunDue(ctx context.Context) (time.Duration, error) {
	if c == nil || c.store == nil || c.delivery == nil {
		return -1, errors.New("report coordinator dependencies are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		pending, err := c.store.ListPendingReportSchedule(ctx, 1)
		if err != nil {
			return c.retryDelay, err
		}
		if len(pending) == 0 {
			clear(c.earlyHold)
			return -1, nil
		}
		now := c.now().UTC()
		projectID := pending[0].ProjectID
		if pending[0].AvailableAt.After(now) {
			delay := pending[0].AvailableAt.Sub(now)
			early, wait, err := c.nextEarlyFlush(ctx, now)
			if err != nil {
				return c.retryDelay, err
			}
			if early == "" {
				if wait > 0 && wait < delay {
					delay = wait
				}
				return delay, nil
			}
			projectID = early
		}
		// Held until a batch of the project is delivered (see deliverProject).
		c.earlyHold[projectID] = now.Add(OrchestratorBusyRetry)
		if err := c.deliverProject(ctx, projectID); err != nil {
			return c.retryDelay, err
		}
	}
}

// nextEarlyFlush returns a project whose pending done batch may be delivered
// before its settlement deadline, or the shortest remaining debounce or hold
// among projects that only wait for time. Projects whose other workers are
// still working wait for an activity wake or the deadline.
func (c *Coordinator) nextEarlyFlush(ctx context.Context, now time.Time) (domain.ProjectID, time.Duration, error) {
	pending, err := c.store.ListPendingReportsOfDoneProjects(ctx)
	if err != nil {
		return "", 0, err
	}
	var projects []domain.ProjectID
	byProject := map[domain.ProjectID][]domain.ReportRecord{}
	for _, report := range pending {
		if _, seen := byProject[report.ProjectID]; !seen {
			projects = append(projects, report.ProjectID)
		}
		byProject[report.ProjectID] = append(byProject[report.ProjectID], report)
	}
	for projectID := range c.earlyHold {
		if _, pending := byProject[projectID]; !pending {
			delete(c.earlyHold, projectID)
		}
	}
	var wait time.Duration
	for _, projectID := range projects {
		first := byProject[projectID][0]
		if !claimsDone(byProject[projectID]) {
			// The next claim would take another batch first, for example one
			// deferred until a retry time. It goes out when it is due.
			continue
		}
		flushAt, ok, err := c.earlyFlushAt(ctx, projectID, byProject[projectID])
		if err != nil {
			return "", 0, err
		}
		if !ok {
			continue
		}
		if hold := c.earlyHold[projectID]; hold.After(flushAt) {
			flushAt = hold
		}
		// Only the original done settlement deadline may be shortened. An
		// attempted batch's earlier urgent row can hold a longer retry time.
		if first.DeliveryBatchID != "" && (first.State != domain.ReportDone || first.AvailableAt.After(first.SettlementDeadline)) && first.AvailableAt.After(flushAt) {
			flushAt = first.AvailableAt
		}
		// A batch that was already attempted may wait for a retry time that a
		// done row cannot store, so after a restart it waits out a full retry.
		if retry := c.started.Add(OrchestratorBusyRetry); first.DeliveryBatchID != "" && retry.After(flushAt) {
			flushAt = retry
		}
		if !flushAt.After(now) {
			return projectID, 0, nil
		}
		if d := flushAt.Sub(now); wait == 0 || d < wait {
			wait = d
		}
	}
	return "", wait, nil
}

// claimsDone reports whether the next claim for a project takes a done report.
// pending holds the project's pending reports in claim order: the first one
// names the batch a claim takes, and unbatched reports join a new batch.
func claimsDone(pending []domain.ReportRecord) bool {
	for _, report := range pending {
		if report.DeliveryBatchID == pending[0].DeliveryBatchID && report.State == domain.ReportDone {
			return true
		}
	}
	return false
}

// earlyFlushAt reports when a project's pending done batch may flush early.
// Live workers that are active or provisioning hold the batch unless they have
// already reported done. Without an active orchestrator the batch keeps its
// deadline, so no retry loop starts early.
func (c *Coordinator) earlyFlushAt(ctx context.Context, projectID domain.ProjectID, pending []domain.ReportRecord) (time.Time, bool, error) {
	recs, err := c.store.ListSessions(ctx, projectID)
	if err != nil {
		return time.Time{}, false, err
	}
	if _, ok := newestOrchestrator(recs); !ok {
		return time.Time{}, false, nil
	}
	reporters := map[domain.SessionID]bool{}
	var settled time.Time
	for _, report := range pending {
		if report.State != domain.ReportDone {
			continue
		}
		reporters[report.SessionID] = true
		if report.CreatedAt.After(settled) {
			settled = report.CreatedAt
		}
	}
	for _, rec := range recs {
		if rec.Kind != domain.KindWorker || rec.IsTaskPreparation {
			continue
		}
		if !rec.IsTerminated && (rec.Activity.State == domain.ActivityActive || rec.ProvisionState.IsProvisioning()) {
			if !reporters[rec.ID] {
				return time.Time{}, false, nil
			}
			continue
		}
		if rec.Activity.LastActivityAt.After(settled) {
			settled = rec.Activity.LastActivityAt
		}
	}
	return settled.Add(domain.ReportEarlyFlushDebounce), true, nil
}

func (c *Coordinator) deliverProject(ctx context.Context, projectID domain.ProjectID) error {
	target, ok, err := c.activeOrchestrator(ctx, projectID)
	if err != nil {
		return err
	}
	batch, err := c.claim(ctx, projectID)
	if err != nil || len(batch.Reports) == 0 {
		return err
	}
	if !ok {
		// Left pending and due, the batch would stay the earliest report in
		// the schedule, and every pass would stop on it before any other
		// project's reports. Back off like a busy orchestrator, so other
		// projects keep receiving reports and the expiry backstop applies.
		cause := fmt.Errorf("%w: project %s", ErrNoActiveOrchestrator, projectID)
		if c.isExpired(batch) {
			return c.fail(ctx, batch, cause)
		}
		_, deferErr := c.store.DeferReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC().Add(NoOrchestratorRetry), cause.Error())
		return deferErr
	}
	// Terminal orchestrators queue typed input during a turn, and a Ctrl-C
	// there cancels the running turn or a prompt that was just submitted, so
	// stuck reports are delivered to them like any other report.
	if batch.hasState(domain.ReportStuck) && domain.NormalizeSessionMode(target.Mode) == domain.SessionModeChat {
		interrupted := false
		var nextInterrupt time.Time
		for _, worker := range batch.stuckWorkers() {
			acquired, next, acquireErr := c.store.AcquireReportInterrupt(ctx, worker, c.now().UTC(), ReportInterruptWindow)
			if acquireErr != nil {
				return c.release(ctx, batch, acquireErr)
			}
			if acquired {
				interrupted = true
				if interruptErr := c.delivery.Interrupt(ctx, target.ID); interruptErr != nil {
					return c.release(ctx, batch, interruptErr)
				}
			} else if nextInterrupt.IsZero() || next.Before(nextInterrupt) {
				nextInterrupt = next
			}
		}
		if !interrupted && !batch.hasState(domain.ReportNeedsInput) {
			if c.isExpired(batch) {
				return c.fail(ctx, batch, errors.New("repeated stuck report expired"))
			}
			_, deferErr := c.store.DeferReportBatch(ctx, batch.ProjectID, batch.Token, nextInterrupt, "repeated stuck report coalesced inside interrupt window")
			return deferErr
		}
	}
	if err := c.delivery.Submit(ctx, target.ID, batch.Render(), batch.IdempotencyKey()); err != nil {
		if errors.Is(err, ErrOrchestratorBusy) {
			if c.isExpired(batch) {
				return c.fail(ctx, batch, err)
			}
			// Released, the batch would stay the earliest due and every retry would
			// stop RunDue before any other project's reports. Back off instead.
			_, deferErr := c.store.DeferReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC().Add(OrchestratorBusyRetry), err.Error())
			return deferErr
		}
		return c.retryLater(ctx, batch, err)
	}
	nAcked, err := c.store.AcknowledgeReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC())
	if err != nil {
		return err
	}
	if nAcked > 0 && batch.UndeliveredReports > 0 && batch.FailureCheckpoint > c.failureAckedSeq[batch.ProjectID] {
		c.failureAckedSeq[batch.ProjectID] = batch.FailureCheckpoint
	}
	// Accepted, so a separate pending batch need not wait out the hold.
	delete(c.earlyHold, projectID)
	return nil
}

func (c *Coordinator) activeOrchestrator(ctx context.Context, projectID domain.ProjectID) (domain.SessionRecord, bool, error) {
	recs, err := c.store.ListSessions(ctx, projectID)
	if err != nil {
		return domain.SessionRecord{}, false, err
	}
	selected, ok := newestOrchestrator(recs)
	return selected, ok, nil
}

func newestOrchestrator(recs []domain.SessionRecord) (domain.SessionRecord, bool) {
	var selected domain.SessionRecord
	for _, rec := range recs {
		if rec.Kind != domain.KindOrchestrator || rec.IsTerminated || rec.Activity.State == domain.ActivityExited {
			continue
		}
		if selected.ID == "" || rec.CreatedAt.After(selected.CreatedAt) {
			selected = rec
		}
	}
	return selected, selected.ID != ""
}

// PreparedBatch is a token-fenced durable batch awaiting acceptance.
type PreparedBatch struct {
	ProjectID          domain.ProjectID
	Token              string
	ID                 string
	Reports            []domain.ReportRecord
	UndeliveredReports int
	FailureCheckpoint  int
}

func (c *Coordinator) claim(ctx context.Context, projectID domain.ProjectID) (PreparedBatch, error) {
	token := c.newToken()
	reports, err := c.store.ClaimPendingReportBatch(ctx, projectID, token, c.now().UTC())
	for i := range reports {
		session, found, lookupErr := c.store.GetSession(ctx, reports[i].SessionID)
		if lookupErr == nil && found {
			reports[i].SessionDisplayName = strings.TrimSpace(session.DisplayName)
		}
	}
	batchID := ""
	if len(reports) > 0 {
		batchID = reports[0].DeliveryBatchID
	}
	undelivered := c.undeliveredReportsLocked(projectID)
	checkpoint := c.failureSeq[projectID]
	return PreparedBatch{
		ProjectID:          projectID,
		Token:              token,
		ID:                 batchID,
		Reports:            reports,
		UndeliveredReports: undelivered,
		FailureCheckpoint:  checkpoint,
	}, err
}

// UndeliveredCount reports the number of failed reports awaiting notification for a project.
func (c *Coordinator) UndeliveredCount(projectID domain.ProjectID) int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.undeliveredReportsLocked(projectID)
}

func (c *Coordinator) undeliveredReportsLocked(projectID domain.ProjectID) int {
	diff := c.failureSeq[projectID] - c.failureAckedSeq[projectID]
	if diff <= 0 {
		return 0
	}
	return diff
}

// PreparePiggyback claims pending reports for the active Chat orchestrator.
// The caller must acknowledge only after its user message is durably accepted.
func (c *Coordinator) PreparePiggyback(ctx context.Context, target domain.SessionID) (PreparedBatch, error) {
	if c == nil || c.store == nil {
		return PreparedBatch{}, nil
	}
	rec, ok, err := c.store.GetSession(ctx, target)
	if err != nil || !ok {
		return PreparedBatch{}, err
	}
	if rec.Kind != domain.KindOrchestrator || rec.IsTerminated || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return PreparedBatch{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.claim(ctx, rec.ProjectID)
}

// AcceptPiggyback acknowledges reports after the combined user turn is accepted.
func (c *Coordinator) AcceptPiggyback(ctx context.Context, batch PreparedBatch) error {
	if len(batch.Reports) == 0 {
		return nil
	}
	nAcked, err := c.store.AcknowledgeReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC())
	if err == nil {
		c.mu.Lock()
		if nAcked > 0 && batch.UndeliveredReports > 0 && batch.FailureCheckpoint > c.failureAckedSeq[batch.ProjectID] {
			c.failureAckedSeq[batch.ProjectID] = batch.FailureCheckpoint
		}
		c.mu.Unlock()
	}
	return err
}

// ReleasePiggyback makes a batch retryable when the combined turn was refused.
func (c *Coordinator) ReleasePiggyback(ctx context.Context, batch PreparedBatch, cause error) error {
	if len(batch.Reports) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.release(ctx, batch, cause)
}

func (c *Coordinator) release(ctx context.Context, batch PreparedBatch, cause error) error {
	if c.isExpired(batch) {
		return c.fail(ctx, batch, cause)
	}
	_, releaseErr := c.store.ReleaseReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC(), cause.Error())
	if releaseErr != nil {
		return errors.Join(cause, releaseErr)
	}
	return cause
}

// retryLater queues a refused submission behind every report that is already
// due. Schedule selection takes the earliest pending report across projects,
// so an orchestrator that keeps refusing must not hold that place.
func (c *Coordinator) retryLater(ctx context.Context, batch PreparedBatch, cause error) error {
	if c.isExpired(batch) {
		return c.fail(ctx, batch, cause)
	}
	retryAt := c.now().UTC().Add(c.backoffDelay(batch))
	if isUnacknowledged(cause) {
		n, err := c.store.RetryUnacknowledgedReportBatch(ctx, batch.ProjectID, batch.Token, retryAt, c.maxDeliveryAttempts, cause.Error())
		if err != nil {
			return errors.Join(cause, err)
		}
		c.recordFailedBatch(ctx, batch, cause, n)
		if n > 0 {
			return cause
		}
	} else if _, deferErr := c.store.DeferReportBatch(ctx, batch.ProjectID, batch.Token, retryAt, cause.Error()); deferErr != nil {
		return errors.Join(cause, deferErr)
	}
	if retryAt.After(c.earlyHold[batch.ProjectID]) && batch.hasState(domain.ReportDone) {
		c.earlyHold[batch.ProjectID] = retryAt
	}
	return cause
}

// backoffDelay calculates exponential backoff for transient delivery failures,
// growing through 1x, 2x, 4x, 8x, 16x, 32x and capped at 64x of RetryDelay.
func (c *Coordinator) backoffDelay(batch PreparedBatch) time.Duration {
	attempts := batch.maxAttempts()
	if attempts <= 1 {
		return c.retryDelay
	}
	shift := attempts - 1
	if shift > 6 {
		shift = 6
	}
	factor := time.Duration(1 << shift)
	return c.retryDelay * factor
}

func (c *Coordinator) fail(ctx context.Context, batch PreparedBatch, cause error) error {
	n, failErr := c.store.FailReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC(), cause.Error())
	if failErr != nil {
		return errors.Join(cause, failErr)
	}
	c.recordFailedBatch(ctx, batch, cause, n)
	return cause
}

func (c *Coordinator) recordFailedBatch(ctx context.Context, batch PreparedBatch, cause error, n int) {
	if n > 0 {
		c.failureSeq[batch.ProjectID] += n
		if c.onBatchFailed != nil {
			c.onBatchFailed(ctx, batch, cause)
		}
	}
}

func (c *Coordinator) isExpired(batch PreparedBatch) bool {
	if c == nil || c.expiry <= 0 {
		return false
	}
	now := c.now().UTC()
	for _, report := range batch.Reports {
		if !report.CreatedAt.IsZero() && now.Sub(report.CreatedAt) >= c.expiry {
			return true
		}
	}
	return false
}

func (b PreparedBatch) maxAttempts() int64 {
	var maxAttempts int64
	for _, report := range b.Reports {
		if report.DeliveryAttempts > maxAttempts {
			maxAttempts = report.DeliveryAttempts
		}
	}
	return maxAttempts
}

const (
	chatReportOpen  = "<ao-worker-reports>"
	chatReportClose = "</ao-worker-reports>"
)

// AppendToUserMessage places reports after the user's text in one semantic turn.
// The envelope lets AO's Chat UI hide daemon-authored context while preserving
// the exact prompt accepted by the provider for retry and recovery.
func (b PreparedBatch) AppendToUserMessage(message string) string {
	if len(b.Reports) == 0 {
		return message
	}
	return message + "\n\n" + chatReportOpen + "\n" + b.Render() + "\n" + chatReportClose
}

// IdempotencyKey returns the stable durable delivery identity.
func (b PreparedBatch) IdempotencyKey() string {
	if b.ID != "" {
		return b.ID
	}
	ids := make([]string, len(b.Reports))
	for i := range b.Reports {
		ids[i] = b.Reports[i].ID
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\x00")))
	return "report-batch:" + hex.EncodeToString(sum[:16])
}

// Render formats compact attributed report context for an orchestrator.
func (b PreparedBatch) Render() string {
	var out strings.Builder
	out.WriteString("Reports since your previous turn:")
	for _, report := range coalesceReports(b.Reports) {
		state := string(report.State)
		if state == "" {
			state = "information"
		}
		fmt.Fprintf(&out, "\n\n[%s] %s", state, reportSessionLink(report))
		if report.RepeatCount > 1 {
			fmt.Fprintf(&out, " (repeated %d times)", report.RepeatCount)
		}
		text := report.Note
		if text == "" {
			text = report.Message
		}
		if text != "" {
			out.WriteString("\n")
			out.WriteString(text)
		}
		for _, output := range report.Outputs {
			fmt.Fprintf(&out, "\n%s: %s", output.Kind, output.Reference)
		}
	}
	if b.UndeliveredReports > 0 {
		if b.UndeliveredReports == 1 {
			out.WriteString("\n\nNote: 1 report could not be delivered.")
		} else {
			fmt.Fprintf(&out, "\n\nNote: %d reports could not be delivered.", b.UndeliveredReports)
		}
	}
	return out.String()
}

func reportDisplayNameOrID(r domain.ReportRecord) string {
	if name := strings.TrimSpace(r.SessionDisplayName); name != "" {
		return name
	}
	return string(r.SessionID)
}

// reportSessionLink is the markdown attribution a standalone report shows.
// Piggybacked copies stay inside the hidden user-message envelope.
func reportSessionLink(r domain.ReportRecord) string {
	label := strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "\n", " ").Replace(reportDisplayNameOrID(r))
	href := "ao://sessions/" + url.PathEscape(string(r.ProjectID)) + "/" + url.PathEscape(string(r.SessionID))
	return "[" + label + "](" + href + ")"
}

func (b PreparedBatch) hasState(state domain.ReportState) bool {
	for _, report := range b.Reports {
		if report.State == state {
			return true
		}
	}
	return false
}

func (b PreparedBatch) stuckWorkers() []domain.SessionID {
	seen := map[domain.SessionID]bool{}
	var out []domain.SessionID
	for _, report := range b.Reports {
		if report.State == domain.ReportStuck && !seen[report.SessionID] {
			seen[report.SessionID] = true
			out = append(out, report.SessionID)
		}
	}
	return out
}

func coalesceReports(in []domain.ReportRecord) []domain.ReportRecord {
	out := make([]domain.ReportRecord, 0, len(in))
	indexes := map[string]int{}
	for _, report := range in {
		key := reportKey(report)
		if report.State == domain.ReportStuck {
			if i, ok := indexes[key]; ok {
				out[i].RepeatCount += report.RepeatCount
				continue
			}
			indexes[key] = len(out)
		}
		out = append(out, report)
	}
	return out
}

func reportKey(report domain.ReportRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\x00%s\x00%s\x00%s", report.SessionID, report.State, report.Note, report.Message)
	for _, output := range report.Outputs {
		fmt.Fprintf(&b, "\x00%s\x00%s\x00%s", output.Kind, output.Reference, output.Label)
	}
	return b.String()
}
