package sandbox

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// ErrNotFound indicates that a provider environment does not exist. It is the
// only error the reconciler accepts as proof that an environment is gone; every
// other failure leaves observed reality untouched.
var ErrNotFound = errors.New("sandbox environment not found")

// ErrAtCapacity indicates that a provider has no capacity for a new sandbox.
var ErrAtCapacity = errors.New("sandbox provider at capacity")

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
	Ingress         string
	Environment     map[string]string
	Labels          map[string]string
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
	StateDeleting     = "deleting"
	StateDeleted      = "deleted"
)

// Provider-neutral stop causes. An empty cause is deliberately ambiguous and
// preserves the existing restore behavior. StopCauseExternalIdle is positive
// evidence that the provider applied its own idle/autostop policy.
const StopCauseExternalIdle = "external_idle"
