package capability

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Recorder persists harness capability evidence durably so a restart can answer
// from what was actually observed instead of probing again or guessing.
//
// It is a ports.CapabilityRecorder, which means the resolver can use it as its
// recorded-evidence source with no special case. What it deliberately does NOT do
// is grade anything: a snapshot arrives with its own Source, and the recorder
// stores that value verbatim. Upgrading evidence inside a storage adapter would
// make every gate downstream trust a grade nobody observed.

// Store is the durable half of the recorder. It is declared here rather
// than taken from the SQLite store package so the capability layer depends on the
// four operations it uses and nothing else — the store can move a column without
// this package noticing.
type Store interface {
	UpsertHarnessCapabilitySnapshot(ctx context.Context, snapshot domain.HarnessCapabilitySnapshot) error
	GetHarnessCapabilitySnapshot(ctx context.Context, harness domain.AgentHarness) (domain.HarnessCapabilitySnapshot, bool, error)
	UpsertSessionCapabilitySnapshot(ctx context.Context, snapshot domain.HarnessCapabilitySnapshot) error
	GetSessionCapabilitySnapshot(ctx context.Context, sessionID domain.SessionID) (domain.HarnessCapabilitySnapshot, bool, error)
}

// Recorder is a ports.CapabilityRecorder backed by the durable store.
type Recorder struct {
	store Store
	now   func() time.Time
	ttl   time.Duration
}

var _ ports.CapabilityRecorder = (*Recorder)(nil)

// RecorderOption configures a Recorder. It is a separate type from Option
// because the resolver's options configure a Resolver; sharing one type would let
// a caller pass the wrong option to the wrong constructor and only find out at
// runtime.
type RecorderOption func(*Recorder)

// WithRecorderClock injects the time source. Tests must use this instead of
// sleeping.
func WithRecorderClock(now func() time.Time) RecorderOption {
	return func(r *Recorder) {
		if now != nil {
			r.now = now
		}
	}
}

// WithRecorderTTL overrides how long newly recorded evidence stays usable. A
// non-positive value is ignored: evidence with no stated lifetime cannot gate
// anything, so accepting it would silently disable every confirmed observation.
func WithRecorderTTL(ttl time.Duration) RecorderOption {
	return func(r *Recorder) {
		if ttl > 0 {
			r.ttl = ttl
		}
	}
}

// NewRecorder builds a recorder over a durable store.
func NewRecorder(store Store, opts ...RecorderOption) *Recorder {
	recorder := &Recorder{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
		ttl:   DefaultTTL,
	}
	for _, opt := range opts {
		opt(recorder)
	}
	return recorder
}

// RecordCapabilities implements ports.CapabilityRecorder. A session-scoped
// snapshot goes to its session row; everything else goes to the harness row.
func (r *Recorder) RecordCapabilities(ctx context.Context, snapshot domain.HarnessCapabilitySnapshot) error {
	// 持久表按 Harness/Session 索引，不能保存需当前秘密身份才能复用的 profile 认证。
	if snapshot.Profile != "" {
		return nil
	}
	if r == nil || r.store == nil {
		return ErrRecorderUnavailable
	}
	if snapshot.SessionScoped() {
		return r.store.UpsertSessionCapabilitySnapshot(ctx, snapshot)
	}
	return r.store.UpsertHarnessCapabilitySnapshot(ctx, snapshot)
}

// LoadCapabilities implements ports.CapabilityRecorder.
func (r *Recorder) LoadCapabilities(
	ctx context.Context,
	harness domain.AgentHarness,
) (domain.HarnessCapabilitySnapshot, bool, error) {
	if r == nil || r.store == nil {
		return domain.HarnessCapabilitySnapshot{}, false, ErrRecorderUnavailable
	}
	return r.store.GetHarnessCapabilitySnapshot(ctx, harness)
}

// LoadSessionCapabilities implements ports.CapabilityRecorder.
func (r *Recorder) LoadSessionCapabilities(
	ctx context.Context,
	sessionID domain.SessionID,
) (domain.HarnessCapabilitySnapshot, bool, error) {
	if r == nil || r.store == nil {
		return domain.HarnessCapabilitySnapshot{}, false, ErrRecorderUnavailable
	}
	return r.store.GetSessionCapabilitySnapshot(ctx, sessionID)
}

// ErrRecorderUnavailable reports a recorder with no store. It is an error rather
// than a silent no-op because a caller that asked to record evidence and got
// "fine" back would later gate on nothing.
var ErrRecorderUnavailable = errors.New("capability recorder has no durable store")

// RecordConfirmed marks the named features confirmed for one session generation.
// It is the only writer allowed to produce CapabilitySourceConfirmedRuntime.
//
// Why this exists at all: the resolver's gate refuses to authorize a per-session
// feature without evidence that the provider actually performed it. A handshake is
// a promise, and a static profile is a guess, so neither can prove steer works.
// The one place that fact is ever established is the moment a steer lands, which
// is why the chat service holds the only recorder that calls this.
//
// The snapshot is built from whatever is already stored for the session (so a
// second confirmation does not erase the first) and inherits auth state and
// readiness from the harness-scoped observation when one exists: those are
// harness facts, and a session-scoped confirmation says nothing about them on its
// own. Without that carry-over the gate would reject the evidence as unauthorized
// even though the provider just proved it was not.
func (r *Recorder) RecordConfirmed(
	ctx context.Context,
	harness domain.AgentHarness,
	sessionID domain.SessionID,
	generation string,
	features ...domain.CapabilityFeature,
) error {
	if r == nil || r.store == nil {
		return ErrRecorderUnavailable
	}
	if sessionID == "" {
		return fmt.Errorf("record confirmed capabilities: %s", ports.CapabilityReasonNoSession)
	}
	if generation == "" {
		return fmt.Errorf("record confirmed capabilities for %s: %w",
			sessionID, domain.ErrInvalidCapabilityGeneration)
	}
	for _, feature := range features {
		if !feature.Valid() {
			return fmt.Errorf("record confirmed capabilities for %s: %w",
				sessionID, domain.ErrInvalidCapabilityState)
		}
	}

	now := r.now().UTC()
	snapshot := r.confirmedBase(ctx, harness, sessionID)
	snapshot.Harness = harness
	snapshot.ObservedSessionID = sessionID
	snapshot.Generation = generation
	snapshot.Source = domain.CapabilitySourceConfirmedRuntime
	snapshot.ObservedAt = now
	snapshot.ExpiresAt = now.Add(r.ttl)
	// A confirmed-per-session snapshot must never inherit an old reason code: the
	// code explains why the evidence was weak, and this observation replaced it.
	snapshot.ReasonCode = ports.CapabilityReasonSupported

	harnessSnapshot, harnessFound, err := r.store.GetHarnessCapabilitySnapshot(ctx, harness)
	if err != nil {
		return fmt.Errorf("read harness capabilities for %s: %w", harness, err)
	}
	if harnessFound {
		snapshot.AuthState = harnessSnapshot.AuthState
		snapshot.Readiness = harnessSnapshot.Readiness
		snapshot.Surfaces = harnessSnapshot.Surfaces
		if snapshot.ApplyAt == domain.CapabilityApplyAtUnknown {
			snapshot.ApplyAt = harnessSnapshot.ApplyAt
		}
		if len(snapshot.Models) == 0 {
			snapshot.Models = harnessSnapshot.Models
		}
	}
	for _, feature := range features {
		setCapabilityState(&snapshot, feature, domain.CapabilitySupported)
	}
	if err := r.store.UpsertSessionCapabilitySnapshot(ctx, snapshot); err != nil {
		return fmt.Errorf("record confirmed capabilities for %s: %w", sessionID, err)
	}
	return nil
}

// confirmedBase seeds the snapshot with the session's current evidence, so one
// confirmation does not erase another. An unreadable or absent row falls back to
// an all-unknown snapshot, which is the truthful starting point: nothing about
// this session has been established yet.
func (r *Recorder) confirmedBase(
	ctx context.Context,
	harness domain.AgentHarness,
	sessionID domain.SessionID,
) domain.HarnessCapabilitySnapshot {
	if existing, found, err := r.store.GetSessionCapabilitySnapshot(ctx, sessionID); err == nil && found {
		return existing
	}
	return domain.NewHarnessCapabilitySnapshot(harness, domain.CapabilitySourceConfirmedRuntime, r.now(), r.ttl)
}

// setCapabilityState writes one feature's observed state.
//
// The three enum-shaped features collapse back to their cheapest affirmative
// value, and only because the caller is asserting the provider actually performed
// the operation: a resumed conversation was observed to carry its history, an
// approval policy was observed to bite, and usage was observed to be reported as
// actual counts. A feature with no column here is left untouched rather than
// dropped into a neighbouring one.
func setCapabilityState(
	snapshot *domain.HarnessCapabilitySnapshot,
	feature domain.CapabilityFeature,
	state domain.CapabilityState,
) {
	switch feature {
	case domain.CapabilityFeatureStreaming:
		snapshot.Streaming = state
	case domain.CapabilityFeatureTools:
		snapshot.Tools = state
	case domain.CapabilityFeatureInterrupt:
		snapshot.Interrupt = state
	case domain.CapabilityFeatureSteer:
		snapshot.Steer = state
	case domain.CapabilityFeatureQueue:
		snapshot.Queue = state
	case domain.CapabilityFeatureResume:
		if state.Supported() {
			snapshot.Resume = domain.CapabilityResumeWithHistory
		}
	case domain.CapabilityFeatureApproval:
		if state.Supported() {
			snapshot.Approval = domain.CapabilityApprovalEnforced
		}
	case domain.CapabilityFeatureUsage:
		if state.Supported() {
			snapshot.Usage = domain.CapabilityUsageExact
		}
	}
}
