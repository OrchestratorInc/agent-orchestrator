package ports

import (
	"context"
	"io"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestingTargetEnvironment owns target lifecycle and target-only observations.
// The daemon selects its adapter and launch recipe. Workers never supply a
// target, PID, endpoint or executable. Every operation must honor ctx and refuse
// a missing or changed launch identity without falling back to another target.
type TestingTargetEnvironment interface {
	Start(ctx context.Context, spec TestingTargetSpec) (domain.TestTargetIdentity, error)
	Probe(ctx context.Context, target domain.TestTargetIdentity) error
	Stop(ctx context.Context, target domain.TestTargetIdentity) (TestingCleanupResult, error)
	ReadLogs(ctx context.Context, target domain.TestTargetIdentity, request domain.TestReadLogsRequest) (domain.TestLogResult, error)
	QueryDaemon(ctx context.Context, target domain.TestTargetIdentity, request domain.TestDaemonQueryRequest) (domain.TestDaemonQueryResult, error)
}

// TestingTargetSpec is daemon-resolved launch input. StateRoot must be beneath
// ~/.ao/dev/agentic-target, and CheckoutPath must be an isolated test checkout.
// Adapters strip all inherited AO_* variables before applying their own env.
type TestingTargetSpec struct {
	AttemptID      domain.TestAttemptID
	Generation     int64
	CheckoutPath   string
	CommitSHA      string
	RecipeSnapshot string
	StateRoot      string
	Deadline       time.Time
}

// TestingCleanupResult retains leftover identities even when Stop returns an
// error. A failed probe alone must never be reported as successful cleanup.
type TestingCleanupResult struct {
	State     domain.TestCleanupState
	Leftovers []string
}

// TestingDesktopControl captures and operates only an explicitly bound window.
// BindWindow returns the supplied launch identity with its WindowID filled in.
// Input validates that frame and request refer to that same live window, maps
// screenshot pixels to native points, and refuses input outside its bounds.
// It must never fall back to full-desktop input or expose a provider MCP server.
type TestingDesktopControl interface {
	BindWindow(ctx context.Context, target domain.TestTargetIdentity) (domain.TestTargetIdentity, error)
	Screenshot(ctx context.Context, target domain.TestTargetIdentity) (domain.TestScreenshot, error)
	Click(ctx context.Context, target domain.TestTargetIdentity, frame domain.TestDesktopFrame, request domain.TestClickRequest) (domain.TestActionResult, error)
	Type(ctx context.Context, target domain.TestTargetIdentity, frame domain.TestDesktopFrame, request domain.TestTypeRequest) (domain.TestActionResult, error)
	Key(ctx context.Context, target domain.TestTargetIdentity, frame domain.TestDesktopFrame, request domain.TestKeyRequest) (domain.TestActionResult, error)
}

// TestingDesktopPolicy declares the configured policy and the mode actually
// requested for each input tool. The service journals both before dispatch.
type TestingDesktopPolicy interface {
	DeliveryMode() string
	InputDeliveryMode(tool string) string
}

// TestingDesktopRecorder is an optional window-only recording extension.
// Output must stay in the supervising daemon's supplied evidenceDir. Gap
// declares unavailable or incomplete recording without claiming video evidence.
type TestingDesktopRecorder interface {
	StartRecording(ctx context.Context, target domain.TestTargetIdentity, evidenceDir string) (TestingRecordingResult, error)
	StopRecording(ctx context.Context, target domain.TestTargetIdentity) (TestingRecordingResult, error)
}

// TestingRecordingResult is provider-neutral recording metadata. Path is private
// daemon staging input, never a worker argument or a durable evidence path.
type TestingRecordingResult struct {
	Path        string
	MIMEType    string
	Width       int
	Height      int
	Duration    time.Duration
	StartedAt   time.Time
	StoppedAt   time.Time
	RecorderPID int
	Gap         string
}

// TestingDesktopReleaser revokes one attempt's desktop session at cleanup.
type TestingDesktopReleaser interface {
	Release(ctx context.Context, target domain.TestTargetIdentity) error
}

// TestingEvidenceStore writes attempt-owned artifacts and a durable journal
// outside the target, under ~/.ao. It returns receipts only after saving data.
// Writes must be bounded, reject path traversal and exclude capability secrets.
// An evidence failure prevents dispatch or a successful tool result.
type TestingEvidenceStore interface {
	Write(ctx context.Context, attemptID domain.TestAttemptID, artifact TestingEvidenceArtifact, data io.Reader) (domain.TestEvidenceReceipt, error)
	AppendAction(ctx context.Context, action domain.TestActionRecord) error
	List(ctx context.Context, attemptID domain.TestAttemptID) ([]domain.TestEvidenceReceipt, error)
}

// TestingEvidenceArtifact describes daemon-selected content, never a worker
// path. The store chooses the filename and keeps screenshot frame metadata.
type TestingEvidenceArtifact struct {
	Kind     string
	MIMEType string
	Frame    *domain.TestDesktopFrame
}
