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

const ReportInterruptWindow = 3 * time.Minute

const OrchestratorBusyRetry = 30 * time.Second

var ErrReportDeliveryModeUnsupported = errors.New("report delivery requires a semantic conversation")

var ErrOrchestratorBusy = errors.New("orchestrator cannot take reports right now")

type DeliveryStore interface {
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListPendingReportSchedule(context.Context, int64) ([]domain.ReportRecord, error)
	ClaimPendingReportBatch(context.Context, domain.ProjectID, string, time.Time) ([]domain.ReportRecord, error)
	AcknowledgeReportBatch(context.Context, domain.ProjectID, string, time.Time) (int, error)
	ReleaseReportBatch(context.Context, domain.ProjectID, string, time.Time, string) (int, error)
	DeferReportBatch(context.Context, domain.ProjectID, string, time.Time, string) (int, error)
	AcquireReportInterrupt(context.Context, domain.SessionID, time.Time, time.Duration) (bool, time.Time, error)
}

type Delivery interface {
	Submit(context.Context, domain.SessionID, string, string) error
	Interrupt(context.Context, domain.SessionID) error
}

type Coordinator struct {
	store      DeliveryStore
	delivery   Delivery
	now        func() time.Time
	newToken   func() string
	retryDelay time.Duration
	wake       chan struct{}
	mu         sync.Mutex
}

type CoordinatorDeps struct {
	Store      DeliveryStore
	Delivery   Delivery
	Now        func() time.Time
	NewToken   func() string
	RetryDelay time.Duration
}

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
	return &Coordinator{store: d.Store, delivery: d.Delivery, now: d.Now, newToken: d.NewToken, retryDelay: d.RetryDelay, wake: make(chan struct{}, 1)}
}

func (c *Coordinator) Wake() {
	if c == nil {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

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
			return -1, nil
		}
		now := c.now().UTC()
		if pending[0].AvailableAt.After(now) {
			return pending[0].AvailableAt.Sub(now), nil
		}
		if err := c.deliverProject(ctx, pending[0].ProjectID); err != nil {
			return c.retryDelay, err
		}
	}
}

func (c *Coordinator) deliverProject(ctx context.Context, projectID domain.ProjectID) error {
	target, ok, err := c.activeOrchestrator(ctx, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("project %s has no active orchestrator", projectID)
	}
	batch, err := c.claim(ctx, projectID)
	if err != nil || len(batch.Reports) == 0 {
		return err
	}
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
			_, deferErr := c.store.DeferReportBatch(ctx, batch.ProjectID, batch.Token, nextInterrupt, "repeated stuck report coalesced inside interrupt window")
			return deferErr
		}
	}
	if err := c.delivery.Submit(ctx, target.ID, batch.Render(), batch.IdempotencyKey()); err != nil {
		if errors.Is(err, ErrOrchestratorBusy) {
			_, deferErr := c.store.DeferReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC().Add(OrchestratorBusyRetry), err.Error())
			return deferErr
		}
		return c.retryLater(ctx, batch, err)
	}
	_, err = c.store.AcknowledgeReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC())
	return err
}

func (c *Coordinator) activeOrchestrator(ctx context.Context, projectID domain.ProjectID) (domain.SessionRecord, bool, error) {
	recs, err := c.store.ListSessions(ctx, projectID)
	if err != nil {
		return domain.SessionRecord{}, false, err
	}
	var selected domain.SessionRecord
	for _, rec := range recs {
		if rec.Kind != domain.KindOrchestrator || rec.IsTerminated || rec.Activity.State == domain.ActivityExited {
			continue
		}
		if selected.ID == "" || rec.CreatedAt.After(selected.CreatedAt) {
			selected = rec
		}
	}
	return selected, selected.ID != "", nil
}

type PreparedBatch struct {
	ProjectID domain.ProjectID
	Token     string
	ID        string
	Reports   []domain.ReportRecord
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
	return PreparedBatch{ProjectID: projectID, Token: token, ID: batchID, Reports: reports}, err
}

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
	return c.claim(ctx, rec.ProjectID)
}

func (c *Coordinator) AcceptPiggyback(ctx context.Context, batch PreparedBatch) error {
	if len(batch.Reports) == 0 {
		return nil
	}
	_, err := c.store.AcknowledgeReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC())
	return err
}

func (c *Coordinator) ReleasePiggyback(ctx context.Context, batch PreparedBatch, cause error) error {
	if len(batch.Reports) == 0 {
		return nil
	}
	return c.release(ctx, batch, cause)
}

func (c *Coordinator) release(ctx context.Context, batch PreparedBatch, cause error) error {
	_, releaseErr := c.store.ReleaseReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC(), cause.Error())
	if releaseErr != nil {
		return errors.Join(cause, releaseErr)
	}
	return cause
}

func (c *Coordinator) retryLater(ctx context.Context, batch PreparedBatch, cause error) error {
	_, deferErr := c.store.DeferReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC().Add(c.retryDelay), cause.Error())
	if deferErr != nil {
		return errors.Join(cause, deferErr)
	}
	return cause
}

const (
	chatReportOpen  = "<ao-worker-reports>"
	chatReportClose = "</ao-worker-reports>"
)

func (b PreparedBatch) AppendToUserMessage(message string) string {
	if len(b.Reports) == 0 {
		return message
	}
	return message + "\n\n" + chatReportOpen + "\n" + b.Render() + "\n" + chatReportClose
}

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
	return out.String()
}

func reportDisplayNameOrID(r domain.ReportRecord) string {
	if name := strings.TrimSpace(r.SessionDisplayName); name != "" {
		return name
	}
	return string(r.SessionID)
}

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
