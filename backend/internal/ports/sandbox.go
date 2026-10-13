package ports

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrSandboxUnavailable reports that Docker is missing or not running. A
// sandboxed spawn fails instead of falling back to the host.
var ErrSandboxUnavailable = errors.New("sandbox unavailable")

// ErrSandboxMisconfigured reports a sandbox that Docker can run but AO cannot
// use: the image lacks the harness, the workspace is outside the mounted root,
// or the session shape is not supported yet.
var ErrSandboxMisconfigured = errors.New("sandbox misconfigured")

// SandboxRequest asks for a running container that can host one session.
type SandboxRequest struct {
	SessionID       domain.SessionID
	ProjectID       domain.ProjectID
	Config          domain.SandboxConfig
	WorkspacePath   string
	RequireBinaries []string
}

// SandboxTarget is a ready container plus the host directories mounted into
// it at identical paths.
type SandboxTarget struct {
	Container  string
	Docker     string // absolute path of the docker CLI on the host
	BinDir     string // relay `ao` and `ao-enter`
	ProjectDir string // per-project sandbox state (home, harness config)
	SessionDir string // per-session env file and relay spool
	RelayDir   string
}

// Sandbox runs agent processes inside local containers (#6663).
type Sandbox interface {
	Ensure(ctx context.Context, req SandboxRequest) (SandboxTarget, error)
	// Exec writes the session env file and returns the argv that runs argv
	// inside the container. Secrets never appear in the returned argv.
	Exec(target SandboxTarget, workspacePath string, env map[string]string, argv []string) ([]string, error)
	// Release removes the project's shared containers when inUse is false.
	Release(ctx context.Context, project domain.ProjectID, inUse bool) error
}

// AgentSandboxSupport is implemented by harnesses that can run sandboxed.
type AgentSandboxSupport interface {
	// SandboxBinary is the harness executable name on the image's PATH.
	SandboxBinary() string
	// PrepareSandbox seeds harness state under projectDir and sets the env the
	// harness needs inside the container (credentials, config dir).
	PrepareSandbox(env map[string]string, dataDir, projectDir, workspacePath string) error
}
