package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// ErrNotFound indicates that a provider environment does not exist. It is the
// only error the reconciler accepts as proof that an environment is gone; every
// other failure leaves observed reality untouched.
var ErrNotFound = errors.New("sandbox environment not found")

// ErrAtCapacity indicates that a provider has no capacity for a new sandbox.
var ErrAtCapacity = errors.New("sandbox provider at capacity")

// ErrSnapshotNotFound indicates that Create was asked to boot from a snapshot
// (root filesystem) the provider no longer has.
var ErrSnapshotNotFound = errors.New("sandbox snapshot not found")

// ErrAlreadyExists indicates that Create found the session's sandbox already
// present: the provider enforces one sandbox per session name. The caller
// adopts that sandbox through FindBySession instead of creating another.
var ErrAlreadyExists = errors.New("sandbox environment already exists")

// WorkspaceNameTakenError reports that Create was refused because another
// workspace, not the session's own, already holds the session's stored
// workspace name, and that the session has one retry name left to use. The
// caller records Retry as the session's workspace name and provisions again;
// it never adopts or deletes the workspace that holds Name.
type WorkspaceNameTakenError struct {
	Name  string
	Retry string
	Err   error
}

func (e *WorkspaceNameTakenError) Error() string {
	return fmt.Sprintf("workspace name %q is taken by another workspace (retrying as %q): %v", e.Name, e.Retry, e.Err)
}

func (e *WorkspaceNameTakenError) Unwrap() error { return e.Err }

// DuplicateRejectingCreator marks a provider whose Create fails with
// ErrAlreadyExists rather than making a second sandbox for the same session.
// For such a provider the reconciler creates first and looks up only on that
// conflict, saving the lookup round trip on every normal provision.
type DuplicateRejectingCreator interface {
	CreateRejectsDuplicates()
}

// ID uniquely identifies a provider sandbox.
type ID string

// Spec describes a sandbox to create.
type Spec struct {
	Name            string
	SessionID       string
	OrgID           string
	ResourceProfile domain.ResourceProfile
	Shape           string
	RootFS          string
	// FallbackRootFS is booted instead when RootFS no longer exists at the
	// provider, e.g. a Freestyle project snapshot collected or replaced after
	// the session was planned on it. It is the harness base snapshot the
	// project snapshot was built on; empty means no fallback.
	FallbackRootFS string
	Ingress        string
	Environment    map[string]string
	Labels         map[string]string
	// DurableRoot is provider-specific persisted workspace storage. It remains
	// empty for providers whose established contract already owns persistence.
	DurableRoot       string
	AutoDeleteMinutes int
	// AutoPauseSeconds is disabled when zero.
	AutoPauseSeconds int
}

// Environment is the provider-neutral view of a sandbox. State is always one of
// the AO vocabulary values the reconciler switches on, never a provider string.
type Environment struct {
	ID        ID
	Name      string
	State     string
	Target    string
	Resource  domain.ResourceProfile
	Deadline  *time.Time
	StopCause string
	// Failure explains a StateFailed environment whose failed operation was
	// bringing compute up. It is nil for every other state, and for a failed
	// operation that was stopping or deleting compute.
	Failure *Failure
}

// Failure is a provider operation that failed and will not converge on its
// own, such as a Coder workspace build that failed or was canceled.
type Failure struct {
	// ID identifies the failed operation (the Coder build id), so the
	// reconciler acts on each failure once, without comparing clocks.
	ID string
	// Canceled is set when the operation was canceled rather than failing.
	Canceled bool
	// Err is a generic user-facing reason, code StartupErrorWorkspaceBuildFailed.
	// A FailureDescriber gives a specific one when the failure is reported.
	Err *StartupError
}

// FailureDescriber is an optional provider capability that explains a failed
// operation the reconciler is about to report. It is called once per failure,
// so it may read provider logs. customerOwned is set for an organization's own
// compute, whose provider errors the user may see; for AO-operated compute the
// message must not reveal AO's infrastructure, and the provider's own words go
// only in Err, for operators.
type FailureDescriber interface {
	DescribeFailure(ctx context.Context, id ID, failure Failure, customerOwned bool) *StartupError
}

// DeadlineExtender is an optional provider capability. The reconciler uses it
// only while durable AO work or a recent interactive gesture says the sandbox
// is active. Providers without native deadlines keep their existing behavior.
type DeadlineExtender interface {
	ExtendDeadline(context.Context, ID, time.Time) error
}

// ErrWorkspaceNotReady reports that a sandbox's compute exists but cannot take
// a worker bootstrap yet (for example a Coder agent whose startup script is
// still running, or a workspace terminal that closed before the bootstrap
// began). It is retry-later, never
// evidence that a repair attempt failed.
var ErrWorkspaceNotReady = errors.New("sandbox workspace is not ready for worker bootstrap")

// User-facing startup error codes. They are stable API values surfaced on the
// session so a client can explain why a sandbox never started.
const (
	StartupErrorWorkspaceNotReady       = "workspace_not_ready"
	StartupErrorTerminalUnavailable     = "terminal_unavailable"
	StartupErrorUnsupportedArchitecture = "unsupported_architecture"
	StartupErrorDurableRootUnavailable  = "durable_root_unavailable"
	StartupErrorWorkerNeverStarted      = "worker_never_started"
	StartupErrorBootstrapFailed         = "bootstrap_failed"
	// StartupErrorWorkspaceSetupNotReady means a bring-your-own Coder
	// connection's workspace ready check never passed within its timeout: the
	// template's own setup did not finish, so AO did not start its worker.
	StartupErrorWorkspaceSetupNotReady = "workspace_setup_not_ready"
	// StartupErrorWorkspaceBuildFailed means the provider could not build the
	// session's compute (a Coder workspace build failed or was canceled, for
	// example on a cloud quota). It does not recover until a new build runs.
	StartupErrorWorkspaceBuildFailed = "workspace_build_failed"
	// StartupErrorWorkspaceCreateFailed means AO could not create the
	// session's compute at the provider within the startup budget.
	StartupErrorWorkspaceCreateFailed = "workspace_create_failed"
	// StartupErrorWorkingDirectoryNotDurable means the project's working
	// folder is outside the Coder connection's durable root, so the agent's
	// uncommitted work would not survive a workspace restart. It does not
	// recover until the project's working folder changes.
	StartupErrorWorkingDirectoryNotDurable = domain.CoderWorkingFolderNotDurableCode
	// StartupErrorWorkingDirectoryUnusable means AO could not prepare the
	// project's working folder in the workspace (the same code a worker
	// reports, domain.WorkspaceStartupErrorWorkingDirectory). It does not
	// recover until the folder or the setting changes.
	StartupErrorWorkingDirectoryUnusable = domain.WorkspaceStartupErrorWorkingDirectory
)

// StartupError attaches a stable code and a human message to a provider
// failure. Message is shown to users verbatim; Err keeps the operator detail.
type StartupError struct {
	Code    string
	Message string
	Err     error
}

func (e *StartupError) Error() string {
	if e.Err == nil {
		return e.Message
	}
	return e.Message + ": " + e.Err.Error()
}

func (e *StartupError) Unwrap() error { return e.Err }

// WorkerBuild is one CPU architecture's worker and helper executables.
type WorkerBuild struct {
	Binary       []byte
	HelperBinary []byte
}

// Worker CPU architectures, spelled as Go's GOARCH.
const (
	ArchAMD64 = "amd64"
	ArchARM64 = "arm64"
)

// WorkerBootstrap contains the worker executable and launch environment.
type WorkerBootstrap struct {
	// Binary and HelperBinary are the linux/amd64 build every provider without
	// architecture detection installs.
	Binary            []byte
	Destination       string
	HelperBinary      []byte
	HelperDestination string
	// Builds holds the worker for every architecture the control plane ships,
	// keyed by GOARCH. A provider that can detect the sandbox CPU picks the
	// matching build and advertises its hashes instead of the amd64 ones.
	Builds      map[string]WorkerBuild
	User        string
	Environment map[string]string
	// DurableRoot and DurableIdentity are used by providers whose compute is
	// replaced on stop/start while a template-backed filesystem is retained.
	// RequireDurableIdentity makes a restore fail closed if the original volume
	// marker is missing or belongs to another session.
	DurableRoot            string
	DurableIdentity        string
	RequireDurableIdentity bool
	// RequireMountedDurableRoot makes bootstrap fail unless DurableRoot is a
	// mount point. AO-operated templates mount a dedicated volume there; a
	// bring-your-own template may keep home on the root filesystem, in which
	// case the root is created if missing and the identity marker alone guards
	// against reusing another session's state.
	RequireMountedDurableRoot bool
	// RunAsWorkspaceUser (Coder only) runs the worker as the workspace's own
	// login user instead of User: no account is created and nothing is
	// launched through sudo, so the worker and its agent keep that user's
	// HOME, groups and environment. The Coder provider sets it from the
	// session profile; callers leave it false.
	RunAsWorkspaceUser bool
	// CheckoutDirectory (Coder only, dedicated worker user) is the absolute
	// folder the session's main repository is checked out in instead of the
	// durable root's repository folder, and CheckoutSiblings the folder names
	// of its additional repositories beside it. The bootstrap creates any of
	// them that is missing, owned by User, and never touches one that exists.
	// The Coder provider sets them from the session profile; callers leave
	// them empty.
	CheckoutDirectory string
	CheckoutSiblings  []string
	// CheckoutHome (with CheckoutDirectory) is the workspace login user's
	// home: the bootstrap gives traverse permission only to it and the
	// folders below it, and creates missing parents only below it or the
	// durable root.
	CheckoutHome string
}

// Bootstrapper installs and starts an AO worker in an existing sandbox.
// Providers implement it when they expose an authenticated exec/file API,
// which lets the reconciler repair a live sandbox instead of replacing it.
type Bootstrapper interface {
	BootstrapWorker(context.Context, ID, WorkerBootstrap) error
}

// Recreator re-establishes compute with a fresh worker launch.
type Recreator interface {
	Recreate(context.Context, ID, Spec) (Environment, error)
}

// Provider manages the lifecycle of cloud sandbox environments.
type Provider interface {
	Create(context.Context, Spec) (Environment, error)
	Get(context.Context, ID) (Environment, error)
	FindBySession(context.Context, string) (Environment, bool, error)
	Start(context.Context, ID) error
	Stop(context.Context, ID) error
	Pause(context.Context, ID) error
	Resume(context.Context, ID) error
	Delete(context.Context, ID) error
}

// Provider-neutral environment states. Every provider maps its own vocabulary
// onto these; anything unrecognized must become StateProvisioning, never
// StateRunning, because reporting a sandbox as running before its worker has
// checked in suppresses the startup deadline.
const (
	StateProvisioning = "provisioning"
	StateRunning      = "running"
	StateStopped      = "stopped"
	StatePaused       = "paused"
	// StateFailed is compute whose last provider operation failed or was
	// canceled and will not converge on its own (a failed Coder workspace
	// build). Only a new operation recovers it.
	StateFailed   = "failed"
	StateDeleting = "deleting"
	StateDeleted  = "deleted"
)

// Provider-neutral stop causes. An empty cause is deliberately ambiguous and
// preserves the existing restore behavior. StopCauseExternalIdle is positive
// evidence that the provider applied its own idle/autostop policy.
const StopCauseExternalIdle = "external_idle"
