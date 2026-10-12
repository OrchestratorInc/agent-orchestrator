// Package activity records the per-organization support trail
// (ao_activity_events) and keeps it bounded. Recording is fire-and-forget: a
// slow or failing database never delays or fails the request or reconcile pass
// that produced the event.
package activity

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/logctx"
	"github.com/google/uuid"
)

// Store persists activity events.
type Store interface {
	RecordActivityEvent(ctx context.Context, event domain.ActivityEvent) error
}

// Bounds applied to every event before it is queued.
const (
	MaxAttrs          = 24
	MaxAttrKeyLength  = 48
	MaxAttrValueBytes = 2048
	// DefaultQueueSize is how many events may wait for the writer. A full
	// queue drops new events rather than blocking the caller.
	DefaultQueueSize = 1024
	// DefaultWriteTimeout bounds one insert.
	DefaultWriteTimeout = 5 * time.Second
)

// deniedAttrKey matches attribute names that must never be recorded, whatever
// the value: credentials and user content.
var deniedAttrKey = regexp.MustCompile(`(?i)(token|secret|password|passwd|api[_-]?key|authorization|cookie|credential|prompt|message_body|transcript)`)

var kindPattern = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`)

// Options configures a Recorder.
type Options struct {
	QueueSize    int
	WriteTimeout time.Duration
}

// Recorder queues sanitized events for a single background writer. The zero
// value is not usable; a nil *Recorder is, and records nothing.
type Recorder struct {
	store        Store
	log          *slog.Logger
	queue        chan domain.ActivityEvent
	writeTimeout time.Duration
	dropped      atomic.Int64
	written      atomic.Int64
}

// NewRecorder creates a Recorder. Call Run to start writing.
func NewRecorder(store Store, log *slog.Logger, options Options) *Recorder {
	if log == nil {
		log = slog.Default()
	}
	if options.QueueSize <= 0 {
		options.QueueSize = DefaultQueueSize
	}
	if options.WriteTimeout <= 0 {
		options.WriteTimeout = DefaultWriteTimeout
	}
	return &Recorder{
		store:        store,
		log:          log,
		queue:        make(chan domain.ActivityEvent, options.QueueSize),
		writeTimeout: options.WriteTimeout,
	}
}

// Record logs the event and queues it for the database. It never blocks and
// never fails: an invalid event is logged and discarded, and a full queue
// drops the event (counted in Dropped). Identifiers from the event are also
// annotated onto ctx's log scope so the surrounding request log carries them.
func (r *Recorder) Record(ctx context.Context, event domain.ActivityEvent) {
	if r == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	event, ok := r.logEvent(ctx, event)
	if !ok {
		return
	}

	select {
	case r.queue <- event:
	default:
		if dropped := r.dropped.Add(1); dropped == 1 || dropped%100 == 0 {
			r.log.WarnContext(ctx, "activity queue full; dropping events",
				"activity_kind", event.Kind, "dropped_total", dropped)
		}
	}
}

// Log emits the structured log line for an event that was already written to
// the database elsewhere (inside the store's own transaction).
func (r *Recorder) Log(ctx context.Context, event domain.ActivityEvent) {
	if r == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.logEvent(ctx, event)
}

func (r *Recorder) logEvent(ctx context.Context, event domain.ActivityEvent) (domain.ActivityEvent, bool) {
	event, ok := Sanitize(event)
	if !ok {
		r.log.WarnContext(ctx, "activity event discarded", "activity_kind", event.Kind)
		return event, false
	}
	logctx.Annotate(ctx, logctx.Fields{
		OrgID:     event.OrgID,
		UserID:    event.UserID,
		SessionID: event.SessionID,
		Provider:  event.Provider,
	})
	// The same event as a structured log line: CloudWatch metric filters and
	// Logs Insights queries key on activity_kind (see support-runbook.md).
	args := []any{
		"activity_kind", event.Kind,
		logctx.KeyOrgID, event.OrgID,
	}
	if event.UserID != "" {
		args = append(args, logctx.KeyUserID, event.UserID)
	}
	if event.SessionID != "" {
		args = append(args, logctx.KeySessionID, event.SessionID)
	}
	if event.Provider != "" {
		args = append(args, logctx.KeyProvider, event.Provider)
	}
	if len(event.Attrs) > 0 {
		keys := make([]string, 0, len(event.Attrs))
		for key := range event.Attrs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		attrs := make([]any, 0, len(keys))
		for _, key := range keys {
			attrs = append(attrs, slog.Any(key, event.Attrs[key]))
		}
		args = append(args, slog.Group("activity", attrs...))
	}
	r.log.InfoContext(ctx, "activity event", args...)
	return event, true
}

// Run writes queued events until ctx is canceled, then drains what is already
// queued with a short deadline.
func (r *Recorder) Run(ctx context.Context) {
	if r == nil {
		return
	}
	for {
		select {
		case event := <-r.queue:
			r.write(ctx, event)
		case <-ctx.Done():
			drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			for {
				select {
				case event := <-r.queue:
					r.write(drainCtx, event)
				default:
					return
				}
			}
		}
	}
}

func (r *Recorder) write(ctx context.Context, event domain.ActivityEvent) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.writeTimeout)
	defer cancel()
	if err := r.store.RecordActivityEvent(writeCtx, event); err != nil {
		r.log.Warn("record activity event",
			"activity_kind", event.Kind, logctx.KeyOrgID, event.OrgID, "error", err)
		return
	}
	r.written.Add(1)
}

// Dropped reports how many events a full queue discarded.
func (r *Recorder) Dropped() int64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

// Written reports how many events reached the store.
func (r *Recorder) Written() int64 {
	if r == nil {
		return 0
	}
	return r.written.Load()
}

// Sanitize validates and bounds an event: the organization must be a UUID and
// the kind a lowercase dotted name; optional identifiers that are not UUIDs are
// dropped; attributes are flattened to scalars, credential- and content-named
// keys are removed, and every string is redacted and truncated.
func Sanitize(event domain.ActivityEvent) (domain.ActivityEvent, bool) {
	event.Kind = strings.TrimSpace(event.Kind)
	if !kindPattern.MatchString(event.Kind) {
		return event, false
	}
	if _, err := uuid.Parse(event.OrgID); err != nil {
		return event, false
	}
	if _, err := uuid.Parse(event.UserID); err != nil {
		event.UserID = ""
	}
	if _, err := uuid.Parse(event.SessionID); err != nil {
		event.SessionID = ""
	}
	event.Provider = strings.ToLower(strings.TrimSpace(event.Provider))
	if len(event.Provider) > 32 {
		event.Provider = event.Provider[:32]
	}
	event.Attrs = SanitizeAttrs(event.Attrs)
	return event, true
}

// SanitizeAttrs applies the attribute bounds used by Sanitize.
func SanitizeAttrs(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	clean := make(map[string]any, min(len(keys), MaxAttrs))
	for _, key := range keys {
		if len(clean) >= MaxAttrs {
			break
		}
		name := strings.TrimSpace(key)
		if name == "" || len(name) > MaxAttrKeyLength || deniedAttrKey.MatchString(name) {
			continue
		}
		if value, ok := scalar(attrs[key]); ok {
			clean[name] = value
		}
	}
	if len(clean) == 0 {
		return nil
	}
	return clean
}

func scalar(value any) (any, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, false
	case string:
		return Excerpt(typed, MaxAttrValueBytes), true
	case bool:
		return typed, true
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint32:
		return int64(typed), true
	case uint64:
		if typed > math.MaxInt64 {
			return nil, false
		}
		return int64(typed), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, false
		}
		return typed, true
	case float32:
		return scalar(float64(typed))
	case time.Duration:
		return typed.Milliseconds(), true
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano), true
	case error:
		return Excerpt(typed.Error(), MaxAttrValueBytes), true
	case fmt.Stringer:
		return Excerpt(typed.String(), MaxAttrValueBytes), true
	default:
		// Maps, slices and structs are not recorded: attrs stay flat and small.
		return nil, false
	}
}
