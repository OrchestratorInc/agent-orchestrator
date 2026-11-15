package ports

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type PRWriter interface {
	WritePR(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, comments []domain.PullRequestComment) error
}

type ReviewWriteMode int

const (
	ReviewWritePreserve ReviewWriteMode = iota
	ReviewWriteReplace
	ReviewWriteMerge
)

type SCMWriter interface {
	WriteSCMObservation(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ReviewWriteMode) error
}

type PRClaimer interface {
	ClaimPR(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ReviewWriteMode, allowActiveTakeover bool) (ClaimOutcome, error)
}

var ErrPRClaimedByActiveSession = errors.New("pr claimed by active session")

type PRClaimedByActiveSessionError struct {
	Owner domain.SessionID
}

func (e PRClaimedByActiveSessionError) Error() string {
	return fmt.Sprintf("%s: %s", ErrPRClaimedByActiveSession, e.Owner)
}

func (e PRClaimedByActiveSessionError) Unwrap() error { return ErrPRClaimedByActiveSession }

type ClaimOutcome struct {
	PreviousOwner   domain.SessionID
	OwnerTerminated bool
}

type AgentMessenger interface {
	Send(ctx context.Context, id domain.SessionID, message string) error
}

type Runtime interface {
	Create(ctx context.Context, cfg RuntimeConfig) (RuntimeHandle, error)
	Destroy(ctx context.Context, handle RuntimeHandle) error
	GetOutput(ctx context.Context, handle RuntimeHandle, lines int) (string, error)
	IsAlive(ctx context.Context, handle RuntimeHandle) (bool, error)
}

type RuntimeChildInspector interface {
	IsChildAlive(ctx context.Context, handle RuntimeHandle) (bool, error)
}

type FencedLiveness string

const (
	FencedAlive   FencedLiveness = "alive"
	FencedDead    FencedLiveness = "dead"
	FencedUnknown FencedLiveness = "unknown"
)

type FencedProbeReason string

const (
	FencedReasonExactMatch         FencedProbeReason = "exact_match"
	FencedReasonExactAbsent        FencedProbeReason = "exact_absent"
	FencedReasonIdentityMissing    FencedProbeReason = "identity_missing"
	FencedReasonRegistryUnreadable FencedProbeReason = "registry_unreadable"
	FencedReasonRegistryMalformed  FencedProbeReason = "registry_malformed"
	FencedReasonProbeFailed        FencedProbeReason = "probe_failed"
	FencedReasonGenerationMismatch FencedProbeReason = "generation_mismatch"
)

type FencedRuntimeRef struct {
	Handle         RuntimeHandle
	SessionID      domain.SessionID
	Generation     string
	NativeIdentity string
}

type FencedProbeResult struct {
	Liveness FencedLiveness
	Reason   FencedProbeReason
}

type FencedRuntimeProber interface {
	ProbeFencedRuntime(context.Context, FencedRuntimeRef) FencedProbeResult
}

type RuntimeEffectOutcome string

const (
	RuntimeEffectNone     RuntimeEffectOutcome = "none"
	RuntimeEffectPossible RuntimeEffectOutcome = "possible"
	RuntimeEffectApplied  RuntimeEffectOutcome = "applied"
)

type RuntimeCleanupOutcome string

const (
	RuntimeCleanupNotAttempted RuntimeCleanupOutcome = "not_attempted"
	RuntimeCleanupSucceeded    RuntimeCleanupOutcome = "succeeded"
	RuntimeCleanupFailed       RuntimeCleanupOutcome = "failed"
)

type RuntimeEffectError interface {
	error
	PossibleHandle() RuntimeHandle
	EffectOutcome() RuntimeEffectOutcome
	CleanupOutcome() RuntimeCleanupOutcome
}

type StyledTerminalOutputReader interface {
	GetStyledOutput(ctx context.Context, handle RuntimeHandle, lines int) (string, error)
}

var ErrStyledTerminalOutputUnavailable = errors.New("runtime: styled terminal output unavailable for handle")

var ErrRuntimeProcessExited = errors.New("runtime: process exited")

type RuntimeRestarter interface {
	Restart(ctx context.Context, handle RuntimeHandle, cfg RuntimeConfig) (RuntimeHandle, error)
}

type RuntimeConfig struct {
	SessionID                domain.SessionID
	WorkspacePath            string
	Argv                     []string
	Env                      map[string]string
	ExitOnCommandCompletion  bool
	StartOnAttach            bool
	InitialCols, InitialRows uint16
	LazyStyledOutput         bool
}

type RuntimeHandle struct {
	ID string
}

type SupervisedProcessRef struct {
	SessionID domain.SessionID
	LaunchID  string
}

type SupervisedProcessInspector interface {
	IsSupervisedProcessAlive(ctx context.Context, handle RuntimeHandle, ref SupervisedProcessRef) (bool, error)
}

type RuntimeProcessRootInspector interface {
	ProcessRootPIDs(ctx context.Context, handle RuntimeHandle) ([]int, error)
}

type ExactSupervisedProcessInspector interface {
	IsExactSupervisedProcessAlive(ctx context.Context, handle RuntimeHandle, ref SupervisedProcessRef) (bool, error)
}

type WorkloadCommandInspector interface {
	RunningWorkloadCommands(ctx context.Context, handle RuntimeHandle, ref SupervisedProcessRef) (int, error)
}

type SupervisedProcessRecordInspector interface {
	HasSupervisedProcessRecord(ctx context.Context, handle RuntimeHandle) (bool, error)
}

type ContainerReaper interface {
	ReapSessionContainers(ctx context.Context, id domain.SessionID) (removed int, err error)
}

type Stream interface {
	io.ReadWriteCloser
	Resize(rows, cols uint16) error
}

type Attacher interface {
	Attach(ctx context.Context, handle RuntimeHandle, rows, cols uint16) (Stream, error)
}

type WorkspaceReclaim string

const (
	WorkspaceReclaimRemoved       WorkspaceReclaim = "removed"
	WorkspaceReclaimAlreadyAbsent WorkspaceReclaim = "already_absent"
)

type WorkspaceReclaimer interface {
	DestroyReclaim(ctx context.Context, info WorkspaceInfo) (WorkspaceReclaim, error)
}

type WorkspacePreparationBranchCleaner interface {
	DeletePreparedBranch(ctx context.Context, info WorkspaceInfo) error
}

type Workspace interface {
	Create(ctx context.Context, cfg WorkspaceConfig) (WorkspaceInfo, error)
	Destroy(ctx context.Context, info WorkspaceInfo) error
	Restore(ctx context.Context, cfg WorkspaceConfig) (WorkspaceInfo, error)
	ForceDestroy(ctx context.Context, info WorkspaceInfo) error
	StashUncommitted(ctx context.Context, info WorkspaceInfo) (ref string, err error)
	ApplyPreserved(ctx context.Context, info WorkspaceInfo, ref string) error
	AddExclude(ctx context.Context, info WorkspaceInfo, patterns ...string) error
}

type WorkspaceDefaultBranchRefresher interface {
	ResolveDefaultBranch(ctx context.Context, repoPath, configuredBranch string) (WorkspaceDefaultBranch, error)
	FetchDefaultBranch(ctx context.Context, repoPath string, target WorkspaceDefaultBranch) error
}

type WorkspaceDefaultBranch struct {
	Remote  string
	Branch  string
	BaseRef string
}

type WorkspaceObserver interface {
	ObserveWorkspace(ctx context.Context, info WorkspaceInfo) (WorkspaceObservation, error)
}

type WorkspaceObservation struct {
	Path      string
	Branch    string
	HeadSHA   string
	Dirty     bool
	Staged    bool
	Untracked bool
	Changes   []WorkspaceChange
	Commits   []WorkspaceCommit
}

type WorkspaceChange struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type WorkspaceCommit struct {
	SHA        string `json:"sha"`
	Subject    string `json:"subject"`
	AuthoredAt string `json:"authoredAt"`
}

type WorkspaceProject interface {
	CreateWorkspaceProject(ctx context.Context, cfg WorkspaceProjectConfig) (WorkspaceProjectInfo, error)
	DestroyWorkspaceProject(ctx context.Context, info WorkspaceProjectInfo) error
}

var (
	ErrWorkspaceBranchCheckedOutElsewhere = errors.New("workspace: branch is already checked out in another worktree")
	ErrWorkspaceBranchAlreadyExists       = errors.New("workspace: branch name already exists")
	ErrWorkspaceBranchNotFetched          = errors.New("workspace: branch is not fetched")
	ErrWorkspaceDefaultBranchUnresolved   = errors.New("workspace: default branch is unresolved")
	ErrWorkspaceBranchInvalid             = errors.New("workspace: invalid branch name")
	ErrWorkspaceDirty                     = errors.New("workspace: uncommitted changes present")
	ErrWorkspaceRepoUnavailable           = errors.New("workspace: project repository is unavailable")
	ErrWorkspaceStale                     = errors.New("workspace: stale managed worktree")
	ErrWorkspaceLocked                    = errors.New("workspace: registered worktree is locked")
	ErrWorkspacePathOccupied              = errors.New("workspace: worktree path is occupied")
	ErrWorkspaceUnsafePath                = errors.New("workspace: unsafe workspace path")
	ErrWorkspaceDeferred                  = errors.New("workspace: removal deferred")
	ErrPreservedConflict                  = errors.New("workspace: preserved apply produced conflicts")
	ErrRuntimePrerequisite                = errors.New("runtime: prerequisite missing")
	ErrRuntimeCommandLineTooLong          = errors.New("runtime: command line too long")
	ErrRuntimeWorkspaceCwdMismatch        = errors.New("runtime: session working directory mismatch")
	ErrRuntimeUnavailable                 = errors.New("runtime: infrastructure unavailable")
	ErrRuntimeProbeInconclusive           = errors.New("runtime: liveness probe inconclusive")
)

type WorkspaceTaskNamer interface {
	RenameTaskBranch(ctx context.Context, info WorkspaceProjectInfo, branch string) (WorkspaceProjectInfo, error)
	RevertTaskBranch(ctx context.Context, renamed, original WorkspaceProjectInfo) (WorkspaceProjectInfo, error)
}

type WorkspaceConfig struct {
	ProjectID        domain.ProjectID
	SessionID        domain.SessionID
	Kind             domain.SessionKind
	SessionPrefix    string
	Branch           string
	LastBranch       string
	FreshBranch      bool
	RequireNewBranch bool
	BaseBranch       string
	BaseRef          string
	StartRef         string
	RepoPath         string
	Path             string
	StartPoint       string
}

type WorkspaceInfo struct {
	Path      string
	Branch    string
	BaseSHA   string
	BaseRef   string
	SessionID domain.SessionID
	ProjectID domain.ProjectID
	RepoPath  string
}

type WorkspaceProjectConfig struct {
	ProjectID        domain.ProjectID
	SessionID        domain.SessionID
	Kind             domain.SessionKind
	SessionPrefix    string
	Branch           string
	FreshBranch      bool
	RequireNewBranch bool
	RootRepoPath     string
	BaseBranch       string
	BaseRef          string
	StartRef         string
	Repos            []WorkspaceProjectRepoConfig
	Assets           []WorkspaceProjectAssetConfig
}

type WorkspaceProjectAssetConfig struct {
	RelativePath string
	SourcePath   string
}

type WorkspaceProjectRepoConfig struct {
	Name         string
	RelativePath string
	RepoPath     string
	BaseBranch   string
	BaseRef      string
}

type WorkspaceProjectInfo struct {
	Root      WorkspaceInfo
	Worktrees []WorkspaceRepoInfo
}

type WorkspaceRepoInfo struct {
	RepoName     string
	RepoPath     string
	Path         string
	Branch       string
	BaseSHA      string
	CreationSHA  string
	BaseRef      string
	SessionID    domain.SessionID
	ProjectID    domain.ProjectID
	RelativePath string
}
