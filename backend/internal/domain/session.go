package domain

import (
	"errors"
	"strings"
	"time"
)

type (
	SessionID string
	ProjectID string
	IssueID   string
)

type SessionKind string

const (
	KindWorker       SessionKind = "worker"
	KindOrchestrator SessionKind = "orchestrator"
)

type ConversationCheckpointState string

const (
	ConversationCheckpointLegacy       ConversationCheckpointState = "legacy"
	ConversationCheckpointEmpty        ConversationCheckpointState = "empty"
	ConversationCheckpointCoordination ConversationCheckpointState = "coordination"
	ConversationCheckpointPrompt       ConversationCheckpointState = "prompt"
	ConversationCheckpointComplete     ConversationCheckpointState = "complete"
)

func (s ConversationCheckpointState) Trusted() bool {
	return s == ConversationCheckpointPrompt || s == ConversationCheckpointComplete
}

type ConversationCheckpointOrigin string

const (
	ConversationCheckpointOriginUnknown      ConversationCheckpointOrigin = ""
	ConversationCheckpointOriginHuman        ConversationCheckpointOrigin = "human"
	ConversationCheckpointOriginCoordination ConversationCheckpointOrigin = "coordination"
)

func (o ConversationCheckpointOrigin) Valid() bool {
	return o == ConversationCheckpointOriginUnknown ||
		o == ConversationCheckpointOriginHuman ||
		o == ConversationCheckpointOriginCoordination
}

type SessionOutputType string

const (
	SessionOutputNone          SessionOutputType = "none"
	SessionOutputPR            SessionOutputType = "pr"
	SessionOutputArtifact      SessionOutputType = "artifact"
	SessionOutputPRAndArtifact SessionOutputType = "pr_artifact"
)

type RequestedOutput string

const RequestedOutputArtifact RequestedOutput = "artifact"

func (k RequestedOutput) Valid() bool {
	return k == "" || k == RequestedOutputArtifact
}

func (t SessionOutputType) HasPR() bool {
	return t == SessionOutputPR || t == SessionOutputPRAndArtifact
}

func (t SessionOutputType) HasArtifact() bool {
	return t == SessionOutputArtifact || t == SessionOutputPRAndArtifact
}

type SessionArtifactKind string

const (
	SessionArtifactHTML     SessionArtifactKind = "html"
	SessionArtifactMarkdown SessionArtifactKind = "markdown"
	SessionArtifactGeneric  SessionArtifactKind = "file"
)

const MaxArtifactRegistrationPaths = 100

type SessionArtifactFile struct {
	ID            string              `json:"id,omitempty"`
	Path          string              `json:"path"`
	Name          string              `json:"name"`
	Kind          SessionArtifactKind `json:"kind" enum:"html,markdown,file"`
	Size          int64               `json:"size"`
	UpdatedAt     time.Time           `json:"updatedAt"`
	ShareURL      string              `json:"shareUrl,omitempty"`
	ShareAccess   string              `json:"shareAccess,omitempty"`
	Registered    bool                `json:"registered,omitempty"`
	PreviousPaths []string            `json:"previousPaths,omitempty"`
}

type FileIdentity struct {
	Dev        uint64
	Ino        uint64
	Birth      int64
	Generation uint64
	ModTime    int64
}

type SessionArtifactRegistration struct {
	Path string
	File FileIdentity
}

func (f FileIdentity) SameFile(other FileIdentity) bool {
	return f.Ino != 0 && f.Birth != 0 && f == other
}

type SessionArtifactRecord struct {
	ID            string
	SessionID     SessionID
	Path          string
	RegisteredAt  *time.Time
	File          FileIdentity
	PreviousPaths []string
	CreatedAt     time.Time
	HasVersions   bool
}

var ErrSessionArtifactsChanged = errors.New("session artifacts changed since they were read")

type SessionArtifactChanges struct {
	Insert  []SessionArtifactRecord
	Move    []SessionArtifactMove
	SetFile []SessionArtifactFileUpdate
	Drop    []SessionArtifactFileUpdate
	Forget  []SessionArtifactFileUpdate
}

func (c SessionArtifactChanges) Empty() bool {
	return len(c.Insert) == 0 && len(c.Move) == 0 && len(c.SetFile) == 0 && len(c.Drop) == 0 && len(c.Forget) == 0
}

type SessionArtifactMove struct {
	ID            string
	From          string
	To            string
	File          FileIdentity
	PreviousPaths []string
}

type SessionArtifactFileUpdate struct {
	ID   string
	Path string
	File FileIdentity
}

type SessionArtifactVersion struct {
	SessionID    SessionID
	Path         string
	Version      int64
	SHA256       string
	Size         int64
	ContentType  string
	ModTime      time.Time
	CreatedAt    time.Time
	Source       ArtifactVersionSource
	Harness      AgentHarness
	RestoredFrom int64
}

type ArtifactVersionSource string

const (
	ArtifactVersionSourceAgent ArtifactVersionSource = "agent"
	ArtifactVersionSourceUser  ArtifactVersionSource = "user"
)

type ImportedMessage struct {
	Role MessageRole
	Text string
	At   time.Time
}

type SessionImportSource struct {
	NativeID             string `json:"nativeId"`
	ConfigDir            string `json:"configDir"`
	CWD                  string `json:"cwd"`
	Adopted              bool   `json:"adopted"`
	Transferred          bool   `json:"transferred"`
	Prepared             bool   `json:"prepared"`
	WorkingSubdir        string `json:"workingSubdir,omitempty"`
	ResumeCwd            string `json:"resumeCwd,omitempty"`
	ResumeNativeID       string `json:"resumeNativeId,omitempty"`
	ResumeConfigDir      string `json:"resumeConfigDir,omitempty"`
	ResumeTranscriptPath string `json:"resumeTranscriptPath,omitempty"`
	Fingerprint          string `json:"fingerprint,omitempty"`
	HistoryThrough       int64  `json:"historyThrough,omitempty"`
}

type SessionMetadata struct {
	ImportSource *SessionImportSource `json:"-"`

	Permissions PermissionMode `json:"permissions,omitempty"`

	Branch                           string                      `json:"branch,omitempty"`
	WorkspacePath                    string                      `json:"workspacePath,omitempty"`
	WorkspaceRepoPath                string                      `json:"workspaceRepoPath,omitempty"`
	DiffBaseSHA                      string                      `json:"diffBaseSha,omitempty"`
	DiffBaseRef                      string                      `json:"diffBaseRef,omitempty"`
	RuntimeHandleID                  string                      `json:"runtimeHandleId,omitempty"`
	RuntimeLaunchID                  string                      `json:"runtimeLaunchId,omitempty"`
	RuntimeReleasedLaunchID          string                      `json:"-"`
	AgentSessionID                   string                      `json:"agentSessionId,omitempty"`
	AgentSessionIDLaunchID           string                      `json:"-"`
	NativeIdentityObservedAt         time.Time                   `json:"-"`
	Prompt                           string                      `json:"prompt,omitempty"`
	LatestUserPrompt                 string                      `json:"latestUserPrompt,omitempty"`
	LatestUserPromptAt               time.Time                   `json:"-"`
	LatestInteractionAt              time.Time                   `json:"-"`
	LatestAssistantUpdate            string                      `json:"latestAssistantUpdate,omitempty"`
	LatestAssistantUpdateAt          time.Time                   `json:"-"`
	ConversationCheckpointState      ConversationCheckpointState `json:"-"`
	ConversationCheckpointGeneration string                      `json:"-"`
	ConversationCheckpointNativeID   string                      `json:"-"`
	ConversationCheckpointTurnID     string                      `json:"-"`
	NativeCheckpointEvidence         string                      `json:"-"`
	ConversationCheckpointUnsettled  bool                        `json:"-"`
	NativeTranscriptPath             string                      `json:"nativeTranscriptPath,omitempty"`
	ClaudeActivityFacts              string                      `json:"-"`
	CodexActivityFacts               string                      `json:"-"`
	ProviderConversationID           string                      `json:"providerConversationId,omitempty"`
	ControllerGeneration             string                      `json:"controllerGeneration,omitempty"`
	PreviewURL                       string                      `json:"previewUrl,omitempty"`
	PreviewRevision                  int64                       `json:"previewRevision,omitempty"`
	Model                            string                      `json:"model,omitempty"`
	Effort                           string                      `json:"effort,omitempty"`
	ArtifactDir                      string                      `json:"-"`
	BrowserCapabilityVerifier        string                      `json:"-"`
}

type SessionRecord struct {
	ID                        SessionID              `json:"id"`
	ProjectID                 ProjectID              `json:"projectId,omitempty"`
	AutomationRunID           *AutomationRunID       `json:"-"`
	AutomationLaunchCompleted bool                   `json:"-"`
	ClientRequestID           string                 `json:"-"`
	ClientRequestHash         string                 `json:"-"`
	ClientRequestCommitted    bool                   `json:"-"`
	IssueID                   IssueID                `json:"issueId,omitempty"`
	Kind                      SessionKind            `json:"kind"`
	Harness                   AgentHarness           `json:"harness,omitempty"`
	ReviewerHarness           ReviewerHarness        `json:"reviewerHarness,omitempty" enum:"claude-code,codex,copilot,cursor,kilocode,opencode,opencode-v2,kiro,pi,agy,devin,droid,kimi,kimchi,muse,amp,aider,grok,crush,auggie,cline,autohand"`
	ReviewerConfig            AgentConfig            `json:"reviewerConfig,omitempty"`
	AutoReviewEnabled         bool                   `json:"autoReviewEnabled"`
	DisplayName               string                 `json:"displayName,omitempty"`
	DisplayNameExplicit       bool                   `json:"-"`
	Mode                      SessionMode            `json:"mode" enum:"chat,tui"`
	Activity                  Activity               `json:"activity"`
	FirstSignalAt             time.Time              `json:"-"`
	IsTerminated              bool                   `json:"isTerminated"`
	HibernatedAt              *time.Time             `json:"hibernatedAt,omitempty"`
	StaleBinary               *SessionStaleBinary    `json:"staleBinary,omitempty"`
	TerminateOnPRMerge        bool                   `json:"terminateOnPrMerge"`
	AutoInjectReview          bool                   `json:"autoInjectReview"`
	AutoInjectCI              bool                   `json:"autoInjectCI"`
	OutputType                SessionOutputType      `json:"outputType" enum:"none,pr,artifact,pr_artifact"`
	RequestedOutput           RequestedOutput        `json:"requestedOutput,omitempty" enum:"artifact"`
	Metadata                  SessionMetadata        `json:"-"`
	CleanupGeneration         int64                  `json:"-"`
	Revision                  int64                  `json:"-"`
	CreatedAt                 time.Time              `json:"createdAt"`
	UpdatedAt                 time.Time              `json:"updatedAt"`
	IsPinned                  bool                   `json:"isPinned"`
	PinnedAt                  *time.Time             `json:"pinnedAt,omitempty"`
	IsTaskPreparation         bool                   `json:"-"`
	ProvisionState            SessionProvisionState  `json:"provisionState,omitempty" enum:"provisioning,ready,failed"`
	ProvisionError            string                 `json:"provisionError,omitempty"`
	ProvisionSteps            []SessionProvisionStep `json:"provisionSteps,omitempty"`
	BranchState               *SessionBranchState    `json:"branchState,omitempty"`
	BranchNaming              bool                   `json:"branchNaming,omitempty"`
	TemporaryBranch           string                 `json:"-"`
}

type SessionStaleBinary struct {
	Version        string     `json:"version" description:"Harness release now installed; the session moves to it on its next launch."`
	Since          time.Time  `json:"since"`
	SwitchFailedAt *time.Time `json:"switchFailedAt,omitempty"`
}

type SessionWaitReason string

const (
	SessionWaitWorking  SessionWaitReason = "working"
	SessionWaitCommand  SessionWaitReason = "command"
	SessionWaitApproval SessionWaitReason = "approval"
	SessionWaitStarting SessionWaitReason = "starting"
	SessionWaitReviewer SessionWaitReason = "reviewer"
	SessionWaitResume   SessionWaitReason = "resume"
)

type SessionWait struct {
	SessionID SessionID         `json:"sessionId"`
	Reason    SessionWaitReason `json:"reason" enum:"working,command,approval,starting,reviewer,resume"`
}

type SessionBranchState struct {
	Branch       string `json:"branch,omitempty"`
	Commits      int    `json:"commits"`
	RemoteBranch string `json:"remoteBranch,omitempty"`
	RemoteHead   string `json:"remoteHead,omitempty"`
	Unpushed     int    `json:"unpushed"`
	Base         string `json:"base,omitempty"`
	BaseRef      string `json:"baseRef,omitempty"`
	Behind       int    `json:"behind"`
	Head         string `json:"head,omitempty"`
	Conflicts    bool   `json:"conflicts"`
}

type SessionProvisionStepID string

const (
	SessionProvisionStepFetch    SessionProvisionStepID = "fetch"
	SessionProvisionStepWorktree SessionProvisionStepID = "worktree"
	SessionProvisionStepSetup    SessionProvisionStepID = "setup"
	SessionProvisionStepAgent    SessionProvisionStepID = "agent"
)

type SessionProvisionStepStatus string

const (
	SessionProvisionStepPending SessionProvisionStepStatus = "pending"
	SessionProvisionStepRunning SessionProvisionStepStatus = "running"
	SessionProvisionStepDone    SessionProvisionStepStatus = "done"
)

type SessionProvisionStep struct {
	ID        SessionProvisionStepID     `json:"id" enum:"fetch,worktree,setup,agent"`
	Status    SessionProvisionStepStatus `json:"status" enum:"pending,running,done"`
	StartedAt *time.Time                 `json:"startedAt,omitempty"`
	EndedAt   *time.Time                 `json:"endedAt,omitempty"`
}

func (s SessionRecord) EligibleForChatHibernation() bool {
	return NormalizeSessionMode(s.Mode) == SessionModeChat &&
		s.Kind != KindOrchestrator &&
		!s.IsTerminated && !s.IsTaskPreparation && s.ProvisionState.WithDefault() == SessionProvisionReady &&
		s.HibernatedAt == nil && s.Activity.State == ActivityIdle &&
		!s.Activity.LastActivityAt.IsZero() &&
		strings.TrimSpace(s.Metadata.ProviderConversationID) != ""
}

type SessionProvisionState string

type TaskPreparationToken string

const (
	SessionProvisionProvisioning SessionProvisionState = "provisioning"
	SessionProvisionReady        SessionProvisionState = "ready"
	SessionProvisionFailed       SessionProvisionState = "failed"
)

func (s SessionProvisionState) WithDefault() SessionProvisionState {
	if s == "" {
		return SessionProvisionReady
	}
	return s
}

func (s SessionProvisionState) IsProvisioning() bool {
	return s == SessionProvisionProvisioning
}

func (s SessionRecord) IsStandalone() bool { return s.ProjectID == "" }

func (s SessionRecord) RuntimeReleased() bool {
	launch := s.Metadata.RuntimeLaunchID
	return s.Activity.State == ActivityExited && launch != "" && s.Metadata.RuntimeReleasedLaunchID == launch
}

type SessionControllerOwner struct {
	Harness                AgentHarness
	Mode                   SessionMode
	IsTerminated           bool
	RuntimeLaunchID        string
	AgentSessionID         string
	AgentSessionIDLaunchID string
	ProviderConversationID string
	ControllerGeneration   string
}

func (r SessionRecord) ControllerOwner() SessionControllerOwner {
	return SessionControllerOwner{
		Harness:                r.Harness,
		Mode:                   NormalizeSessionMode(r.Mode),
		IsTerminated:           r.IsTerminated,
		RuntimeLaunchID:        r.Metadata.RuntimeLaunchID,
		AgentSessionID:         r.Metadata.AgentSessionID,
		AgentSessionIDLaunchID: r.Metadata.AgentSessionIDLaunchID,
		ProviderConversationID: r.Metadata.ProviderConversationID,
		ControllerGeneration:   r.Metadata.ControllerGeneration,
	}
}

type Session struct {
	NeedsResume bool `json:"needsResume"`
	SessionRecord
	WorkspaceCleanup           WorkspaceDisposition  `json:"workspaceCleanup,omitempty" enum:"pending,removed,preserved_dirty,failed,not_applicable"`
	WorkspaceCleanupReportOnly bool                  `json:"workspaceCleanupReportOnly,omitempty"`
	WorkspacePublished         bool                  `json:"workspacePublished" description:"Whether the session has a recorded workspace that workspace reads can use."`
	StatusReadiness            string                `json:"statusReadiness" enum:"checking,ready,unavailable"`
	ChatProviderPreserved      bool                  `json:"chatProviderPreserved"`
	Status                     SessionStatus         `json:"status" enum:"working,pr_open,draft,ci_failed,review_pending,changes_requested,commented,approved,mergeable,merged,needs_input,exited,idle,terminated,no_signal"`
	SCMStatus                  SessionStatus         `json:"scmStatus,omitempty" enum:"pr_open,draft,ci_failed,review_pending,changes_requested,commented,approved,mergeable,merged"`
	KanbanColumn               KanbanColumn          `json:"kanbanColumn" enum:"building,validating,needs_review,ready,archive"`
	DisplayStatus              DisplayStatus         `json:"displayStatus" enum:"Working,Blocked,Needs input,Exited,No signal,Interrupted,Idle,Fixing CI failures,Addressing comments,Review scheduled,Reviewing,Retrying review,Draft,Awaiting review,Review failed,Review cancelled,CI failing,Merge conflicts,Commented,Changes requested,Mergeable,Approved,Merged,Closed without merge,Terminated"`
	TerminalHandleID           string                `json:"terminalHandleId,omitempty"`
	TerminalTitle              string                `json:"terminalTitle,omitempty"`
	ArtifactFiles              []SessionArtifactFile `json:"-"`
	PRs                        []PRFacts             `json:"-"`
}

func (s Session) LastEventAt() time.Time {
	latest := s.CreatedAt
	consider := func(t time.Time) {
		if t.After(latest) {
			latest = t
		}
	}
	consider(s.Activity.LastActivityAt)
	for _, pr := range s.PRs {
		consider(pr.StateChangedAt)
		consider(pr.CIChangedAt)
		consider(pr.LastReviewAt)
	}
	return latest
}

func (s SessionRecord) NeedsImportResume() bool {
	return s.Metadata.ImportSource != nil && !s.Metadata.ImportSource.Adopted
}
