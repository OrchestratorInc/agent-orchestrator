package controllers

import (
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/devimport"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/legacyimport"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	agentsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/agent"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentaccounts"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentauth"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/computeruse"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systemcheck"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systeminstall"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

type ConversationFindParam struct {
	ConversationID string `path:"conversationId" required:"true"`
}

type ConversationFindQuery struct {
	Query              string `query:"query" description:"Case-insensitive literal substring in displayed message text (maximum 512 characters)."`
	Cursor             string `query:"cursor,omitempty" description:"Opaque continuation bound to query and transcript revision."`
	AnchorMessageID    string `query:"anchorMessageId,omitempty" description:"Start at or after this visible message when cursor is absent."`
	AnchorBlock        int    `query:"anchorBlock,omitempty" minimum:"0"`
	AnchorOffset       int    `query:"anchorOffset,omitempty" minimum:"0"`
	AnchorSequence     int64  `query:"anchorSequence,omitempty" minimum:"0" description:"Fallback position when the anchor message was removed."`
	FallbackToPrevious bool   `query:"fallbackToPrevious,omitempty" description:"Choose the preceding match if a removed anchor has no following match."`
}

type ConversationFindWindowQuery struct {
	Sequence int64 `query:"sequence" required:"true" minimum:"1"`
}

type ConversationFindResponse chatsvc.FindResult

type CreateReportRequest struct {
	SessionID string                `json:"sessionId"`
	State     string                `json:"state,omitempty" enum:"checkpoint,needs_input,stuck,done"`
	Note      string                `json:"note,omitempty" maxLength:"1000"`
	Message   string                `json:"message,omitempty" maxLength:"1000"`
	Outputs   []ReportOutputRequest `json:"outputs,omitempty"`
}

type ReportOutputRequest struct {
	Kind      string `json:"kind" enum:"artifact,pr_created,pr_reviewed"`
	Reference string `json:"reference" minLength:"1"`
	Label     string `json:"label,omitempty"`
}

type CreateReportResponse struct {
	ID string `json:"id"`
}

type ReportOutputResponse struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	Label     string `json:"label,omitempty"`
}

type ReportResponse struct {
	ID          string                 `json:"id"`
	SessionID   string                 `json:"sessionId"`
	ProjectID   string                 `json:"projectId"`
	State       string                 `json:"state,omitempty"`
	Note        string                 `json:"note,omitempty"`
	Message     string                 `json:"message,omitempty"`
	Outputs     []ReportOutputResponse `json:"outputs,omitempty"`
	CreatedAt   time.Time              `json:"createdAt"`
	RepeatCount int64                  `json:"repeatCount"`
}

type ListReportsResponse struct {
	Reports []ReportResponse `json:"reports"`
}

type ListReportsQuery struct {
	ProjectID string `query:"projectId" required:"true" description:"Stable project identifier."`
}

type ProjectIDParam struct {
	ID string `path:"id" description:"Project identifier (registry key)."`
}

type TaskPreparationTokenParam struct {
	Token string `path:"token" description:"Opaque speculative task-worktree token."`
}

type AgentIDParam struct {
	Agent string `path:"agent" description:"Agent adapter identifier."`
}

type UnavailableChatAgentsQuery struct {
	Agents string `query:"agents" description:"Comma-separated agent adapter identifiers."`
}

type UnavailableChatAgentsResponse struct {
	Agents []UnavailableChatAgent `json:"agents"`
}

type UnavailableChatAgent struct {
	Agent  string `json:"agent"`
	Reason string `json:"reason"`
}

type ChatPermissionModesResponse struct {
	Agents []ChatAgentPermissionModes `json:"agents"`
}

type ChatAgentPermissionModes struct {
	Agent string   `json:"agent"`
	Modes []string `json:"modes"`
}

type PrewarmChatAgentResponse struct {
	Agent string `json:"agent" description:"Agent adapter identifier whose Chat check was started."`
}

type ListAgentAuthPlansResponse struct {
	Plans []agentauth.Plan `json:"plans"`
}

type StartAgentAuthResponse struct {
	AgentID       string                `json:"agentId"`
	Action        agentauth.Action      `json:"action"`
	Guidance      string                `json:"guidance,omitempty"`
	TerminalInput string                `json:"terminalInput,omitempty"`
	Terminal      ShellTerminalResponse `json:"terminal"`
}

type StartAgentUILoginRequest struct {
	Method   string `json:"method"`
	Secret   string `json:"secret,omitempty"`
	Model    string `json:"model,omitempty"`
	Device   *bool  `json:"device,omitempty"`
	Provider string `json:"provider,omitempty"`
}

type CatalogProviderParam struct {
	Provider string `path:"provider" description:"Provider ID from the harness's catalog."`
}

type RecordAgentSignInResponse struct {
	ReleasedKey string `json:"releasedKey,omitempty"`
}

type ListCatalogProvidersResponse struct {
	Providers []agentauth.CatalogProvider `json:"providers"`
}

type ListCatalogModelsResponse struct {
	Models []string `json:"models"`
}

type ProviderKeyNameParam struct {
	Name string `path:"name" description:"Provider environment variable, such as ANTHROPIC_API_KEY."`
}

type ListProviderKeysResponse struct {
	Keys []agentauth.ProviderKey `json:"keys"`
}

type SaveProviderKeyRequest struct {
	Secret string `json:"secret,omitempty"`
	Device bool   `json:"device"`
}

type CheckProviderKeyRequest struct {
	Secret string `json:"secret,omitempty"`
}

type AgentLoginActionRequest struct {
	Action string `json:"action" enum:"previous,next,select,1,2,3,4,5,6,7,8,9"`
	Option string `json:"option,omitempty" maxLength:"256"`
}

type SubmitAgentUILoginCodeRequest struct {
	Code string `json:"code"`
}

type AgentUILoginIDParam struct {
	OperationID string `path:"operationId"`
}

type CodexAccountIDParam struct {
	AccountID string `path:"accountId" description:"AO Codex account identifier."`
}

type CodexAccountLoginIDParam struct {
	OperationID string `path:"operationId" description:"In-memory Codex account login operation identifier."`
}

type CodexAccountSwitchIDParam struct {
	SwitchID string `path:"switchId" description:"Durable Codex account switch identifier."`
}

type ListProjectsResponse struct {
	Projects []projectsvc.Summary `json:"projects"`
}

type ProjectResponse struct {
	Project projectsvc.Project `json:"project"`
}

type ClonePrepareStreamEvent struct {
	Progress *projectsvc.CloneProgress          `json:"progress,omitempty"`
	Result   *projectsvc.ClonePreparationResult `json:"result,omitempty"`
	Error    *envelope.APIError                 `json:"error,omitempty"`
}

type GetProjectResponse struct {
	Status  string            `json:"status" enum:"ok,degraded"`
	Project ProjectOrDegraded `json:"project"`
}

type ProjectOrDegraded struct {
	Project  *projectsvc.Project
	Degraded *projectsvc.Degraded
}

func (p ProjectOrDegraded) MarshalJSON() ([]byte, error) {
	switch {
	case p.Degraded != nil:
		return json.Marshal(p.Degraded)
	case p.Project != nil:
		return json.Marshal(p.Project)
	default:
		return nil, errEmptyProjectOrDegraded
	}
}

var errEmptyProjectOrDegraded = errors.New("controllers: GetResult has neither Project nor Degraded set")

func (ProjectOrDegraded) JSONSchemaOneOf() []interface{} {
	return []interface{}{projectsvc.Project{}, projectsvc.Degraded{}}
}

func newGetProjectResponse(res projectsvc.GetResult) (GetProjectResponse, error) {
	if res.Project == nil && res.Degraded == nil {
		return GetProjectResponse{}, errEmptyProjectOrDegraded
	}
	return GetProjectResponse{
		Status:  res.Status,
		Project: ProjectOrDegraded{Project: res.Project, Degraded: res.Degraded},
	}, nil
}

type SessionIDParam struct {
	SessionID string `path:"sessionId" description:"Session identifier, e.g. project-1."`
}

type PreviewFileQuery struct {
	Source          string `query:"source,omitempty" enum:"workspace,artifact" description:"File root to serve from. Defaults to the session workspace; artifact selects the session artifact directory."`
	Raw             bool   `query:"raw,omitempty" description:"When true, serve Markdown files as raw source instead of rendering them to HTML for Browser preview."`
	ArtifactVersion int64  `query:"artifactVersion,omitempty" description:"With source=artifact, serve this saved version of the file instead of its current content." minimum:"1"`
}

type PRNumberParam struct {
	PRNumber int `path:"prNumber" description:"Associated pull request number." minimum:"1"`
}

type SessionInterfaceTransitionIDParam struct {
	TransitionID string `path:"transitionId" description:"Durable interface-transition identifier."`
}

type ListSessionsQuery struct {
	Project          string `query:"project,omitempty" description:"Project id filter."`
	Active           *bool  `query:"active,omitempty" description:"When true, return non-terminated sessions; when false, return terminated sessions."`
	OrchestratorOnly *bool  `query:"orchestratorOnly,omitempty" description:"When true, return only orchestrator sessions."`
	Fresh            *bool  `query:"fresh,omitempty" description:"When true, return only fresh non-terminated sessions."`
	Kind             string `query:"kind,omitempty" enum:"worker,orchestrator" description:"Session kind filter. worker keeps every session that is not an orchestrator."`
	Limit            int    `query:"limit,omitempty" minimum:"1" maximum:"200" description:"Page size. When set, sessions are ordered pinned first, then most recently updated, and nextCursor resumes the list. When omitted, every match is returned."`
	Cursor           string `query:"cursor,omitempty" description:"Opaque cursor returned by the previous page. Requires limit."`
}

type CleanupSessionsQuery struct {
	Project string `query:"project,omitempty" description:"Project id filter. When omitted, clean terminated sessions across all projects."`
}

type ArtifactFileQuery struct {
	Path string `query:"path" description:"Artifact-directory-relative file path, as listed in the session's artifactFiles."`
}

type UpdateArtifactFileRequest struct {
	Path                    string `json:"path" description:"Artifact-directory-relative file path, as listed in the session's artifactFiles."`
	Content                 string `json:"content" description:"The file's new UTF-8 text content."`
	ExpectedFileFingerprint string `json:"expectedFileFingerprint" description:"Hex SHA-256 of the file content the edit started from."`
}

type ArtifactFileResponse struct {
	Path            string `json:"path"`
	Size            int64  `json:"size"`
	FileFingerprint string `json:"fileFingerprint" description:"Hex SHA-256 of the file's content."`
}

type WorkspaceFileQuery struct {
	Snapshot        bool   `query:"snapshot,omitempty" description:"Read the immutable review snapshot instead of the live worktree."`
	Branch          string `query:"branch,omitempty" description:"Local or remote-tracking branch to read without checkout."`
	PRNumber        int    `query:"prNumber,omitempty" description:"Associated pull request number."`
	SourceURL       string `query:"sourceUrl,omitempty" description:"Associated pull request URL."`
	ExpectedTipSHA  string `query:"expectedTipSha,omitempty" description:"Pinned source tip for snapshot reads. Must remain reachable from the selected source."`
	ExpectedBaseSHA string `query:"expectedBaseSha,omitempty" description:"Pinned comparison base for snapshot reads."`
	CommitSHAs      string `query:"commitShas,omitempty" description:"Comma-separated selected commit SHAs."`

	Path      string `query:"path" description:"Session-worktree-relative file path."`
	Section   string `query:"section,omitempty" enum:"uncommitted,committed,staged,unstaged,untracked" description:"Git-state section the file was opened from (see WorkspaceFileSections). uncommitted diffs the worktree against HEAD; staged diffs the index against HEAD; unstaged diffs the worktree against the index; omitted/committed/untracked diff the worktree against the compare base."`
	CommitSHA string `query:"commitSha,omitempty" description:"Exact commit SHA to read as an immutable committed-scope snapshot."`
}

type UpdateWorkspaceFileRequest struct {
	Path                    string `json:"path"`
	Content                 string `json:"content"`
	ExpectedFileFingerprint string `json:"expectedFileFingerprint"`
}

type WorkspaceFileBlobQuery struct {
	Snapshot        bool   `query:"snapshot,omitempty" description:"Read the immutable review snapshot instead of the live worktree."`
	Branch          string `query:"branch,omitempty" description:"Local or remote-tracking branch to read without checkout."`
	PRNumber        int    `query:"prNumber,omitempty" description:"Associated pull request number."`
	SourceURL       string `query:"sourceUrl,omitempty" description:"Associated pull request URL."`
	ExpectedTipSHA  string `query:"expectedTipSha,omitempty" description:"Pinned source tip for snapshot reads. Must remain reachable from the selected source."`
	ExpectedBaseSHA string `query:"expectedBaseSha,omitempty" description:"Pinned comparison base for snapshot reads."`
	CommitSHAs      string `query:"commitShas,omitempty" description:"Comma-separated selected commit SHAs."`

	CommitSHA string `query:"commitSha,omitempty" description:"Selected commit SHA."`
	Path      string `query:"path" required:"true" description:"Session-worktree-relative file path."`
	Side      string `query:"side,omitempty" enum:"before,after" description:"Which revision to read: the compare base (before) or the session worktree (after). Defaults to after."`
	V         string `query:"v,omitempty" description:"Cache-busting token. Ignored by the server; the response is never cached."`
}

type WorkspaceFileRevisionQuery struct {
	Snapshot         bool   `query:"snapshot,omitempty" description:"Read the selected immutable comparison, including an empty commit range."`
	Path             string `query:"path" required:"true" description:"Session-worktree-relative file path."`
	Scope            string `query:"scope,omitempty" enum:"combined,uncommitted,committed,staged,unstaged,untracked" description:"Comparison scope. Defaults to combined."`
	Side             string `query:"side,omitempty" enum:"before,after" description:"Comparison side. Defaults to after."`
	WorkspaceVersion string `query:"workspaceVersion,omitempty" description:"Opaque workspace snapshot token used for consistency checks."`
	ExpectedRevision string `query:"expectedRevision,omitempty" description:"Opaque revision token used for optimistic consistency checks."`
	CommitSHA        string `query:"commitSha,omitempty" description:"Exact commit SHA for a committed-scope comparison. Cannot be combined with commitShas."`
	CommitSHAs       string `query:"commitShas,omitempty" description:"Comma-separated full SHAs (max 250) whose combined changes are compared. Requires the committed scope."`
	Branch           string `query:"branch,omitempty" description:"Local branch to read instead of the checked-out worktree. Requires the committed scope."`
	PRNumber         int    `query:"prNumber,omitempty" minimum:"1" description:"Associated pull request to read instead of the checked-out worktree. Requires the committed scope."`
	SourceURL        string `query:"sourceUrl,omitempty" description:"Stable URL of the pull request, for PRs that share a number. Requires prNumber."`
	ExpectedTipSHA   string `query:"expectedTipSha,omitempty" description:"The branch or pull request view's tipSha from its history. A moved tip fails with WORKSPACE_REF_MOVED."`
	ExpectedBaseSHA  string `query:"expectedBaseSha,omitempty" description:"The branch or pull request view's baseSha from its history. A moved base fails with WORKSPACE_REF_MOVED."`
}

type WorkspaceHistoryQuery struct {
	Branch    string `query:"branch,omitempty" description:"Local branch to read instead of the checked-out worktree. Read from revisions only; nothing is checked out."`
	PRNumber  int    `query:"prNumber,omitempty" minimum:"1" description:"Associated pull request to read instead of the checked-out worktree."`
	SourceURL string `query:"sourceUrl,omitempty" description:"Stable URL of the pull request, for PRs that share a number. Requires prNumber."`
}

type PRFilesQuery struct {
	SourceURL string `query:"sourceUrl,omitempty" description:"Stable URL of the selected associated pull request."`
}

type PRFileQuery struct {
	Path         string `query:"path" required:"true" description:"Repository-relative file path."`
	PreviousPath string `query:"previousPath,omitempty" description:"Previous repository-relative path supplied by the selected PR file summary for rename detection."`
	SourceURL    string `query:"sourceUrl,omitempty" description:"Stable URL of the selected associated pull request."`
	CommitSHA    string `query:"commitSha,omitempty" description:"Exact SHA of one of the pull request's commits; reads that commit's change instead of the whole pull request."`
}

type PRFileRevisionQuery struct {
	Path      string `query:"path" required:"true" description:"Repository-relative file path."`
	Side      string `query:"side,omitempty" enum:"before,after" description:"Comparison side. Defaults to after."`
	SourceURL string `query:"sourceUrl,omitempty" description:"Stable URL of the selected associated pull request."`
	CommitSHA string `query:"commitSha,omitempty" description:"Exact SHA of one of the pull request's commits; before is its first parent, after is the commit."`
}

type WorkspaceSearchQuery struct {
	Query  string `query:"query" required:"true" description:"Case-insensitive path substring."`
	Cursor string `query:"cursor,omitempty" description:"Opaque pagination cursor returned by the previous page."`
	Limit  int    `query:"limit,omitempty" minimum:"1" maximum:"100" description:"Maximum results. Defaults to 50."`
}

type WorkspaceContentSearchQuery struct {
	Query string `query:"query" required:"true" description:"Words to find in file contents, case-insensitive and in any order."`
}

type ConversationSearchQuery struct {
	Query string `query:"query" required:"true" description:"Words to find in chat messages, matched by word form."`
}

type WorkspaceResolveQuery struct {
	Path string `query:"path" required:"true" description:"Absolute or workspace-relative file path to classify."`
}

type WorkspaceTreeQuery struct {
	Filter          string `query:"filter,omitempty" description:"Case-insensitive path search across the selected snapshot."`
	CommitSHA       string `query:"commitSha,omitempty" description:"Selected commit SHA."`
	Snapshot        bool   `query:"snapshot,omitempty" description:"Read the immutable review snapshot instead of the live worktree."`
	Branch          string `query:"branch,omitempty" description:"Local or remote-tracking branch to read without checkout."`
	PRNumber        int    `query:"prNumber,omitempty" description:"Associated pull request number."`
	SourceURL       string `query:"sourceUrl,omitempty" description:"Associated pull request URL."`
	ExpectedTipSHA  string `query:"expectedTipSha,omitempty" description:"Pinned source tip for snapshot reads. Must remain reachable from the selected source."`
	ExpectedBaseSHA string `query:"expectedBaseSha,omitempty" description:"Pinned comparison base for snapshot reads."`
	CommitSHAs      string `query:"commitShas,omitempty" description:"Comma-separated selected commit SHAs."`

	Path string `query:"path,omitempty" description:"Directory path relative to the session workspace root. Empty or omitted lists the root."`
}

type ListWorkspaceTreeResponse struct {
	Snapshot  bool                 `json:"snapshot,omitempty" description:"True only when the tree was read from the requested immutable snapshot."`
	SessionID domain.SessionID     `json:"sessionId"`
	Path      string               `json:"path"`
	Entries   []WorkspaceTreeEntry `json:"entries"`
	Truncated bool                 `json:"truncated"`
}

type WorkspaceTreeEntry struct {
	Name       string                            `json:"name"`
	Path       string                            `json:"path"`
	Type       sessionsvc.WorkspaceTreeEntryType `json:"type" enum:"file,dir"`
	Status     sessionsvc.WorkspaceFileStatus    `json:"status,omitempty" enum:"unmodified,modified,added,deleted,renamed"`
	HasChanges bool                              `json:"hasChanges,omitempty"`
	Size       int64                             `json:"size,omitempty"`
	Binary     bool                              `json:"binary,omitempty"`
}

type SessionView struct {
	domain.Session
	Branch             string                `json:"branch,omitempty"`
	TerminalGeneration string                `json:"terminalGeneration,omitempty"`
	PreviewURL         string                `json:"previewUrl,omitempty"`
	PreviewRevision    int64                 `json:"previewRevision,omitempty"`
	Model              string                `json:"model,omitempty"`
	LastUserMessageAt  *time.Time            `json:"lastUserMessageAt,omitempty"`
	LastInteractionAt  *time.Time            `json:"lastInteractionAt,omitempty"`
	LastEventAt        time.Time             `json:"lastEventAt"`
	PRs                []SessionPRFacts      `json:"prs"`
	ArtifactFiles      []SessionArtifactView `json:"artifactFiles,omitempty"`
}

type SessionArtifactView struct {
	ID            string                     `json:"id,omitempty"`
	Path          string                     `json:"path"`
	Name          string                     `json:"name"`
	Kind          domain.SessionArtifactKind `json:"kind" enum:"html,markdown,file"`
	Size          int64                      `json:"size"`
	UpdatedAt     time.Time                  `json:"updatedAt"`
	Registered    bool                       `json:"registered,omitempty"`
	PreviousPaths []string                   `json:"previousPaths,omitempty"`
	PreviewURL    string                     `json:"previewUrl,omitempty"`
	RawURL        string                     `json:"rawUrl,omitempty"`
	InlineURL     string                     `json:"inlineUrl,omitempty"`
	ShareURL      string                     `json:"shareUrl,omitempty"`
	ShareAccess   string                     `json:"shareAccess,omitempty" enum:"restricted,anyone"`
}

type RegisterArtifactsRequest struct {
	Paths []string `json:"paths" description:"Files or bundle directories in the session's artifact directory: absolute paths, or paths relative to it."`
}

type RegisterArtifactsResponse struct {
	Registered     []string `json:"registered" description:"The requested paths, relative to the artifact directory."`
	OtherFiles     int      `json:"otherFiles" description:"How many of the session's files are not registered."`
	OtherRootFiles []string `json:"otherRootFiles" description:"The unregistered files at the root of the artifact directory."`
}

type ListSessionsResponse struct {
	Sessions   []SessionView          `json:"sessions"`
	NextCursor string                 `json:"nextCursor,omitempty"`
	Archived   *SessionArchiveSummary `json:"archived,omitempty"`
}

type SessionArchiveSummary struct {
	Total     int            `json:"total"`
	ByProject map[string]int `json:"byProject"`
}

type SpawnSessionRequest struct {
	ClientRequestID string                 `json:"clientRequestId,omitempty" maxLength:"128"`
	ProjectID       domain.ProjectID       `json:"projectId,omitempty"`
	IssueID         domain.IssueID         `json:"issueId,omitempty"`
	ParentSessionID domain.SessionID       `json:"parentSessionId,omitempty"`
	TrackerProvider domain.TrackerProvider `json:"trackerProvider,omitempty" enum:"github,gitlab"`
	Kind            domain.SessionKind     `json:"kind,omitempty" enum:"worker,orchestrator"`
	Harness         domain.AgentHarness    `json:"harness,omitempty" enum:"claude-code,codex,aider,opencode,opencode-v2,grok,droid,amp,agy,crush,cursor,qwen,gemini,copilot,goose,auggie,continue,devin,cline,kimi,muse,kiro,kilocode,vibe,pi,kimchi,omp,fx,prime-agent,autohand,unreal-agent,codewhale,mimo-code,minimax-code,deepseek-harness,openhands,command-code,docker-agent,memcode,hermes,stakpak"`
	Branch          string                 `json:"branch,omitempty"`
	BaseBranch      string                 `json:"baseBranch,omitempty" maxLength:"255"`
	Mode            domain.SessionMode     `json:"mode,omitempty" enum:"chat,tui"`
	ApprovalMode    domain.PermissionMode  `json:"approvalMode,omitempty" enum:"default,accept-edits,auto,bypass-permissions"`
	Prompt          string                 `json:"prompt,omitempty" maxLength:"102400"`
	Model           string                 `json:"model,omitempty" maxLength:"256"`
	Effort          string                 `json:"effort,omitempty" maxLength:"32"`
	AccountID       string                 `json:"accountId,omitempty" maxLength:"128"`

	RequestedOutput domain.RequestedOutput `json:"requestedOutput,omitempty" enum:"artifact"`

	DisplayName         string            `json:"displayName,omitempty" maxLength:"100"`
	DisplayNameExplicit bool              `json:"displayNameExplicit,omitempty"`
	Attachments         []AttachmentInput `json:"attachments,omitempty"`
	Async               bool              `json:"async,omitempty"`
}

type AttachmentInput struct {
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data"`
}

type SessionResponse struct {
	Session SessionView `json:"session"`
}

type SpawnSessionResponse struct {
	Session           SessionView `json:"session"`
	PromptBytes       int         `json:"promptBytes"`
	SystemPromptBytes int         `json:"systemPromptBytes"`
}

type StageSessionAttachmentsRequest struct {
	Attachments []AttachmentInput `json:"attachments"`
}

type StageSessionAttachmentsResponse struct {
	SessionID domain.SessionID `json:"sessionId"`
	Paths     []string         `json:"paths"`
}

type ListWorkspaceFilesResponse struct {
	SessionID        domain.SessionID                `json:"sessionId"`
	WorkspaceVersion string                          `json:"workspaceVersion"`
	CompareBaseSHA   string                          `json:"compareBaseSha,omitempty"`
	CompareBaseRef   string                          `json:"compareBaseRef,omitempty"`
	CompareMode      sessionsvc.WorkspaceCompareMode `json:"compareMode,omitempty" enum:"base,head_fallback"`
	Files            []WorkspaceFileSummary          `json:"files"`
	Truncated        bool                            `json:"truncated"`
	Sections         WorkspaceFileSections           `json:"sections"`
	Commits          []WorkspaceCommitSummary        `json:"commits"`
	CommitsTruncated bool                            `json:"commitsTruncated,omitempty" description:"True when older commits were left out of commits: the list keeps the newest 250, and stops at the last commit whose changes fit the daemon's size cap."`
	Summary          WorkspaceSummary                `json:"summary"`
	Degraded         bool                            `json:"degraded"`
	DegradedCode     string                          `json:"degradedCode,omitempty"`
	Ahead            *int                            `json:"ahead,omitempty"`
	Behind           *int                            `json:"behind,omitempty"`
}

type WorkspaceManifestResponse struct {
	SessionID        domain.SessionID                `json:"sessionId"`
	WorkspaceVersion string                          `json:"workspaceVersion"`
	CompareBaseSHA   string                          `json:"compareBaseSha,omitempty"`
	CompareBaseRef   string                          `json:"compareBaseRef,omitempty"`
	CompareMode      sessionsvc.WorkspaceCompareMode `json:"compareMode,omitempty" enum:"base,head_fallback"`
	Files            []WorkspaceFileSummary          `json:"files"`
	Sections         WorkspaceFileSections           `json:"sections"`
	Summary          WorkspaceSummary                `json:"summary"`
	Truncated        bool                            `json:"truncated"`
	Stale            bool                            `json:"stale"`
	Refreshing       bool                            `json:"refreshing"`
	Degraded         bool                            `json:"degraded"`
	DegradedCode     string                          `json:"degradedCode,omitempty"`
}

type WorkspaceHistoryResponse struct {
	SessionID        domain.SessionID         `json:"sessionId"`
	View             WorkspaceViewResponse    `json:"view"`
	Commits          []WorkspaceCommitSummary `json:"commits"`
	CommitsTruncated bool                     `json:"commitsTruncated,omitempty"`
	RangeSummary     *WorkspaceSummary        `json:"rangeSummary,omitempty" description:"Totals for All commits. Views only."`
	Ahead            *int                     `json:"ahead,omitempty" description:"Checked-out view only."`
	Behind           *int                     `json:"behind,omitempty" description:"Checked-out view only."`
}

type WorkspaceViewResponse struct {
	Kind      sessionsvc.WorkspaceViewKind  `json:"kind" enum:"checked_out,branch,pull_request"`
	Name      string                        `json:"name,omitempty" description:"Branch name, or the pull request's source branch."`
	TipSHA    string                        `json:"tipSha,omitempty" description:"Fence a later view read with it as expectedTipSha."`
	BaseSHA   string                        `json:"baseSha,omitempty" description:"Set only in since_base mode. Fence a later view read with it as expectedBaseSha."`
	BaseRef   string                        `json:"baseRef,omitempty"`
	RangeMode sessionsvc.WorkspaceRangeMode `json:"rangeMode" enum:"since_base,recent" description:"since_base lists the commits since baseSha; recent lists the newest 50 first-parent commits of a default branch, or of a branch with no default branch to measure from."`
}

type WorkspaceBranchesResponse struct {
	SessionID     domain.SessionID          `json:"sessionId"`
	Supported     bool                      `json:"supported" description:"False for scratch sessions, which have no Git history."`
	DefaultBranch string                    `json:"defaultBranch,omitempty"`
	Branches      []WorkspaceBranchResponse `json:"branches" description:"The checked-out row first, then the rest newest first; at most 200 rows."`
	Truncated     bool                      `json:"truncated"`
}

type WorkspaceBranchResponse struct {
	Kind       sessionsvc.WorkspaceBranchKind `json:"kind" enum:"local,remote,pull_request" description:"local reads as a branch view (or the checked-out worktree); pull_request reads as a pull request view."`
	Name       string                         `json:"name" description:"Branch name, the pull request's source branch, or empty for a detached HEAD."`
	TipSHA     string                         `json:"tipSha,omitempty"`
	UpdatedAt  *time.Time                     `json:"updatedAt,omitempty" description:"The tip's committer time, or the pull request's last update."`
	CheckedOut bool                           `json:"checkedOut"`
	Detached   bool                           `json:"detached,omitempty"`
	IsDefault  bool                           `json:"isDefault"`
	PRNumber   int                            `json:"prNumber,omitempty"`
	PRURL      string                         `json:"prUrl,omitempty" description:"Pass as sourceUrl with prNumber to read a pull_request row."`
	PRTitle    string                         `json:"prTitle,omitempty"`
}

type ListPRFilesResponse struct {
	SessionID        domain.SessionID         `json:"sessionId"`
	Files            []WorkspaceFileSummary   `json:"files"`
	Commits          []WorkspaceCommitSummary `json:"commits"`
	CommitsTruncated bool                     `json:"commitsTruncated,omitempty" description:"True when older commits were left out of commits: the list keeps the newest 250, and stops at the last commit whose changes fit the daemon's size cap."`
	Truncated        bool                     `json:"truncated"`
	Summary          WorkspaceSummary         `json:"summary"`
}

type WorkspaceFileSections struct {
	Uncommitted []WorkspaceFileSummary `json:"uncommitted" description:"Worktree vs HEAD, untracked files included. Always present; a response without it comes from an older daemon."`
	Staged      []WorkspaceFileSummary `json:"staged"`
	Unstaged    []WorkspaceFileSummary `json:"unstaged"`
	Untracked   []WorkspaceFileSummary `json:"untracked"`
	Committed   []WorkspaceFileSummary `json:"committed"`
}

type WorkspaceCommitSummary struct {
	SHA       string                 `json:"sha"`
	Subject   string                 `json:"subject"`
	Author    string                 `json:"author"`
	Timestamp time.Time              `json:"timestamp"`
	Files     []WorkspaceFileSummary `json:"files"`
}

type WorkspaceSummary struct {
	Files     int `json:"files"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

type WorkspaceFileSummary struct {
	Path            string                         `json:"path"`
	PreviousPath    string                         `json:"previousPath,omitempty"`
	Status          sessionsvc.WorkspaceFileStatus `json:"status" enum:"unmodified,modified,added,deleted,renamed"`
	Additions       int                            `json:"additions"`
	Deletions       int                            `json:"deletions"`
	Size            int64                          `json:"size"`
	Binary          bool                           `json:"binary"`
	Editable        bool                           `json:"editable"`
	FileFingerprint string                         `json:"fileFingerprint"`
}

type WorkspaceFileResponse struct {
	Historical       bool                            `json:"historical,omitempty" description:"True only for a read-only revision, never a live worktree file."`
	SessionID        domain.SessionID                `json:"sessionId"`
	Path             string                          `json:"path"`
	PreviousPath     string                          `json:"previousPath,omitempty"`
	Status           sessionsvc.WorkspaceFileStatus  `json:"status" enum:"unmodified,modified,added,deleted,renamed"`
	Additions        int                             `json:"additions"`
	Deletions        int                             `json:"deletions"`
	Size             int64                           `json:"size"`
	Binary           bool                            `json:"binary"`
	Deleted          bool                            `json:"deleted"`
	Editable         bool                            `json:"editable"`
	ImageMediaType   string                          `json:"imageMediaType,omitempty"`
	Content          string                          `json:"content"`
	ContentTruncated bool                            `json:"contentTruncated"`
	Diff             string                          `json:"diff"`
	DiffTruncated    bool                            `json:"diffTruncated"`
	CompareBaseSHA   string                          `json:"compareBaseSha,omitempty"`
	CompareBaseRef   string                          `json:"compareBaseRef,omitempty"`
	CompareMode      sessionsvc.WorkspaceCompareMode `json:"compareMode,omitempty" enum:"base,head_fallback"`
	WorkspaceVersion string                          `json:"workspaceVersion"`
	FileFingerprint  string                          `json:"fileFingerprint"`
}

type WorkspaceDiffRequest struct {
	Scope            string   `json:"scope" enum:"combined,uncommitted,committed,staged,unstaged,untracked"`
	Paths            []string `json:"paths" minItems:"1" maxItems:"100"`
	ContextLines     int      `json:"contextLines" minimum:"0" maximum:"20"`
	IgnoreWhitespace bool     `json:"ignoreWhitespace"`
	WorkspaceVersion string   `json:"workspaceVersion,omitempty"`
	CommitSHA        string   `json:"commitSha,omitempty" description:"Exact commit SHA for a committed-scope comparison. Cannot be combined with commitShas."`
	CommitSHAs       []string `json:"commitShas,omitempty" maxItems:"250" description:"Full SHAs whose combined changes are compared. Requires the committed scope."`
	Branch           string   `json:"branch,omitempty" description:"Local branch to read instead of the checked-out worktree. Requires the committed scope."`
	PRNumber         int      `json:"prNumber,omitempty" minimum:"1" description:"Associated pull request to read instead of the checked-out worktree. Requires the committed scope."`
	SourceURL        string   `json:"sourceUrl,omitempty" description:"Stable URL of the pull request, for PRs that share a number. Requires prNumber."`
	ExpectedTipSHA   string   `json:"expectedTipSha,omitempty" description:"The view's tipSha from its history. A moved tip fails with WORKSPACE_REF_MOVED."`
	ExpectedBaseSHA  string   `json:"expectedBaseSha,omitempty" description:"The view's baseSha from its history. A moved base fails with WORKSPACE_REF_MOVED."`
}

type WorkspaceCommitSelectionRequest struct {
	Branch           string   `json:"branch,omitempty" description:"Local branch to read instead of the checked-out worktree."`
	PRNumber         int      `json:"prNumber,omitempty" minimum:"1" description:"Associated pull request to read instead of the checked-out worktree."`
	SourceURL        string   `json:"sourceUrl,omitempty" description:"Stable URL of the pull request, for PRs that share a number. Requires prNumber."`
	ExpectedTipSHA   string   `json:"expectedTipSha,omitempty" description:"The view's tipSha from its history. A moved tip fails with WORKSPACE_REF_MOVED."`
	ExpectedBaseSHA  string   `json:"expectedBaseSha,omitempty" description:"The view's baseSha from its history. A moved base fails with WORKSPACE_REF_MOVED."`
	CommitSHAs       []string `json:"commitShas" maxItems:"250" description:"Full SHAs. Empty = the whole range (All commits)."`
	WorkspaceVersion string   `json:"workspaceVersion,omitempty" description:"Fence for checked-out selections."`
}

type WorkspaceCommitSelectionResponse struct {
	SessionID  domain.SessionID                 `json:"sessionId"`
	TipSHA     string                           `json:"tipSha"`
	BaseSHA    string                           `json:"baseSha,omitempty"`
	Contiguous bool                             `json:"contiguous" description:"True when the commits form one unbroken range."`
	Combinable bool                             `json:"combinable"`
	Conflict   *WorkspaceCommitConflictResponse `json:"conflict,omitempty"`
	Files      []WorkspaceFileSummary           `json:"files"`
	Summary    WorkspaceSummary                 `json:"summary"`
	Truncated  bool                             `json:"truncated"`
}

type WorkspaceCommitConflictResponse struct {
	CommitSHA string                                   `json:"commitSha" description:"The ticked commit whose changes did not apply."`
	Paths     []string                                 `json:"paths" description:"Up to 20 paths that did not apply."`
	Reason    sessionsvc.WorkspaceCommitConflictReason `json:"reason" enum:"conflict,unsupported_history"`
}

type WorkspaceDiffDeferredResponse struct {
	Path   string `json:"path"`
	Reason string `json:"reason" enum:"binary,oversized,generated,long_line,budget_exceeded"`
}

type WorkspaceDiffGroupResponse struct {
	Repository    string                          `json:"repository,omitempty"`
	Patch         string                          `json:"patch"`
	Truncated     bool                            `json:"truncated"`
	IncludedPaths []string                        `json:"includedPaths"`
	Deferred      []WorkspaceDiffDeferredResponse `json:"deferred"`
	Errors        []WorkspaceDiffErrorResponse    `json:"errors"`
}

type WorkspaceDiffErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type WorkspaceDiffsResponse struct {
	SessionID        domain.SessionID             `json:"sessionId"`
	WorkspaceVersion string                       `json:"workspaceVersion"`
	Groups           []WorkspaceDiffGroupResponse `json:"groups"`
}

type WorkspaceFileRevisionResponse struct {
	SessionID        domain.SessionID                 `json:"sessionId"`
	Path             string                           `json:"path"`
	Side             sessionsvc.WorkspaceFileBlobSide `json:"side" enum:"before,after"`
	Revision         string                           `json:"revision,omitempty"`
	WorkspaceVersion string                           `json:"workspaceVersion"`
	MediaType        string                           `json:"mediaType,omitempty"`
	Encoding         string                           `json:"encoding,omitempty"`
	Size             int64                            `json:"size"`
	Exists           bool                             `json:"exists"`
	Binary           bool                             `json:"binary"`
	Truncated        bool                             `json:"truncated"`
	Content          string                           `json:"content"`
}

type WorkspaceFileSearchResultResponse struct {
	Path            string                         `json:"path"`
	Status          sessionsvc.WorkspaceFileStatus `json:"status" enum:"unmodified,modified,added,deleted,renamed"`
	Size            int64                          `json:"size"`
	Binary          bool                           `json:"binary"`
	FileFingerprint string                         `json:"fileFingerprint"`
}

type WorkspaceFileSearchResponse struct {
	SessionID  domain.SessionID                    `json:"sessionId"`
	Query      string                              `json:"query"`
	Results    []WorkspaceFileSearchResultResponse `json:"results"`
	NextCursor string                              `json:"nextCursor,omitempty"`
	Truncated  bool                                `json:"truncated"`
}

type WorkspaceContentSearchHitResponse struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text" description:"The line, with each match wrapped in U+0002 and U+0003."`
}

type WorkspaceContentSearchResponse struct {
	Results []WorkspaceContentSearchHitResponse `json:"results"`
}

type ConversationSearchHitResponse struct {
	MessageID string `json:"messageId"`
	SessionID string `json:"sessionId"`
	ProjectID string `json:"projectId"`
	ReviewID  string `json:"reviewId,omitempty"`
	Title     string `json:"title"`
	Text      string `json:"text" description:"A passage of the message, with each match wrapped in U+0002 and U+0003."`
}

type ConversationSearchResponse struct {
	Results []ConversationSearchHitResponse `json:"results"`
}

type WorkspacePathResolutionResponse struct {
	SessionID domain.SessionID             `json:"sessionId"`
	Kind      sessionsvc.WorkspacePathKind `json:"kind" enum:"workspace,artifact,outside"`
	Path      string                       `json:"path"`
}

type DesktopWorkspaceLocationResponse struct {
	SessionID     domain.SessionID `json:"sessionId"`
	WorkspacePath string           `json:"workspacePath"`
}

type DesktopArtifactLocationResponse struct {
	SessionID   domain.SessionID `json:"sessionId"`
	ArtifactDir string           `json:"artifactDir"`
}

type SessionPreviewResponse struct {
	SessionID  domain.SessionID `json:"sessionId"`
	PreviewURL string           `json:"previewUrl,omitempty"`
	Entry      string           `json:"entry,omitempty"`
	Source     string           `json:"source,omitempty" enum:"workspace,artifact" description:"Root containing the selected static entry; omitted for external app URLs."`
}

type RenameSessionRequest struct {
	DisplayName string `json:"displayName" minLength:"1" maxLength:"100"`
}

type SetSessionReviewerRequest struct {
	Harness     domain.ReviewerHarness `json:"harness,omitempty" enum:"claude-code,codex,copilot,cursor,kilocode,opencode,opencode-v2,kiro,pi,agy,devin,droid,kimi,kimchi,muse,amp,aider,grok,crush,auggie,cline,autohand"`
	AgentConfig domain.AgentConfig     `json:"agentConfig,omitempty"`
}

type SetSessionAutoReviewRequest struct {
	Enabled        bool `json:"enabled"`
	enabledPresent bool
}

func (r *SetSessionAutoReviewRequest) UnmarshalJSON(data []byte) error {
	var wire struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	r.enabledPresent = wire.Enabled != nil
	if wire.Enabled != nil {
		r.Enabled = *wire.Enabled
	}
	return nil
}

type SetSessionPreviewRequest struct {
	URL                  string `json:"url,omitempty" description:"Preview target URL. When empty, the daemon autodetects a static entry point in the session workspace."`
	RequireWorkspaceFile bool   `json:"requireWorkspaceFile,omitempty" description:"Reject the target unless it resolves to an existing file in the session workspace."`
}

type StartPreviewServerRequest struct {
	Configuration string `json:"configuration,omitempty" description:"Named preview configuration. Optional when exactly one configuration exists."`
}

type PreviewServerStatusResponse struct {
	SessionID     domain.SessionID `json:"sessionId"`
	State         string           `json:"state" enum:"stopped,starting,ready,stopping,failed"`
	Configuration string           `json:"configuration,omitempty"`
	TargetKind    string           `json:"targetKind,omitempty" enum:"app,api"`
	URL           string           `json:"url,omitempty"`
	Port          int              `json:"port,omitempty"`
	StartedAt     time.Time        `json:"startedAt,omitempty"`
	Error         string           `json:"error,omitempty"`
	Logs          []string         `json:"logs"`
}

type BrowserStatusQuery struct {
	SessionID domain.SessionID `query:"sessionId" description:"AO session identifier."`
}

type BrowserCapabilityHeader struct {
	Capability string `header:"X-AO-Browser-Capability" description:"Opaque browser capability injected into the owning AO worker."`
}

type PreviewCapabilityHeader struct {
	Capability string `header:"X-AO-Preview-Capability" description:"Opaque preview-only capability injected into a session-scoped user shell."`
}

type BrowserStatusResponse struct {
	SessionID     domain.SessionID `json:"sessionId"`
	Connected     bool             `json:"connected"`
	ConnectedAt   time.Time        `json:"connectedAt,omitempty"`
	Transport     string           `json:"transport" enum:"electron-webcontents-debugger,chrome-extension"`
	Target        string           `json:"target" enum:"panel,chrome"`
	Client        string           `json:"client,omitempty"`
	ClientVersion string           `json:"clientVersion,omitempty"`
}

type BrowserChromeChannel struct {
	Name         string `json:"name"`
	Available    bool   `json:"available"`
	Registered   bool   `json:"registered"`
	ManifestPath string `json:"manifestPath"`
}

type BrowserChromeStatusResponse struct {
	Enabled          bool                   `json:"enabled"`
	Connected        bool                   `json:"connected"`
	ExtensionID      string                 `json:"extensionId"`
	ExtensionPath    string                 `json:"extensionPath"`
	ExtensionBundled bool                   `json:"extensionBundled"`
	ExtensionVersion string                 `json:"extensionVersion,omitempty"`
	ClientVersion    string                 `json:"clientVersion,omitempty"`
	HostPath         string                 `json:"hostPath"`
	Channels         []BrowserChromeChannel `json:"channels"`
}

type BrowserCommandRequest struct {
	SessionID domain.SessionID       `json:"sessionId"`
	Action    string                 `json:"action"`
	Args      map[string]interface{} `json:"args,omitempty"`
}

type BrowserCommandResponse struct {
	RequestID string           `json:"requestId"`
	SessionID domain.SessionID `json:"sessionId"`
	Action    string           `json:"action"`
	Result    interface{}      `json:"result"`
}

type SetSessionMergePolicyRequest struct {
	TerminateOnPRMerge bool `json:"terminateOnPrMerge"`
}

type RenameSessionResponse struct {
	OK          bool             `json:"ok"`
	SessionID   domain.SessionID `json:"sessionId"`
	DisplayName string           `json:"displayName"`
}

type SetSessionMergePolicyResponse struct {
	OK                 bool             `json:"ok"`
	SessionID          domain.SessionID `json:"sessionId"`
	TerminateOnPRMerge bool             `json:"terminateOnPrMerge"`
	Session            SessionView      `json:"session"`
}

type SetSessionAutoInjectReviewRequest struct {
	AutoInjectReview bool `json:"autoInjectReview"`
}

type SetSessionAutoInjectReviewResponse struct {
	OK               bool             `json:"ok"`
	SessionID        domain.SessionID `json:"sessionId"`
	AutoInjectReview bool             `json:"autoInjectReview"`
	Session          SessionView      `json:"session"`
}

type SetSessionAutoInjectCIRequest struct {
	AutoInjectCI bool `json:"autoInjectCI"`
}

type SetSessionAutoInjectCIResponse struct {
	OK           bool             `json:"ok"`
	SessionID    domain.SessionID `json:"sessionId"`
	AutoInjectCI bool             `json:"autoInjectCI"`
	Session      SessionView      `json:"session"`
}

type RestoreSessionResponse struct {
	OK          bool                       `json:"ok"`
	SessionID   domain.SessionID           `json:"sessionId"`
	RestoreMode sessionsvc.RestoreModeView `json:"restoreMode" enum:"native,saved_prompt,fresh"`
	Session     SessionView                `json:"session"`
}

type ExitAgentResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	Session   SessionView      `json:"session"`
}

type ResumeAgentResponse struct {
	OK         bool                       `json:"ok"`
	SessionID  domain.SessionID           `json:"sessionId"`
	ResumeMode sessionsvc.RestoreModeView `json:"resumeMode" enum:"native,saved_prompt,fresh"`
	Session    SessionView                `json:"session"`
}

type StartSessionInterfaceTransitionRequest struct {
	TargetMode    domain.SessionMode                             `json:"targetMode" enum:"chat,tui"`
	Policy        domain.SessionInterfaceTransitionPolicy        `json:"policy" enum:"drain,interrupt"`
	HistoryPolicy domain.SessionInterfaceTransitionHistoryPolicy `json:"historyPolicy,omitempty" enum:"strict,provider_history"`
}

type SessionInterfaceTransitionView struct {
	ID                   string                                         `json:"id"`
	SessionID            domain.SessionID                               `json:"sessionId"`
	SourceMode           domain.SessionMode                             `json:"sourceMode" enum:"chat,tui"`
	TargetMode           domain.SessionMode                             `json:"targetMode" enum:"chat,tui"`
	Policy               domain.SessionInterfaceTransitionPolicy        `json:"policy" enum:"drain,interrupt"`
	HistoryPolicy        domain.SessionInterfaceTransitionHistoryPolicy `json:"historyPolicy" enum:"strict,provider_history"`
	Phase                domain.SessionInterfaceTransitionPhase         `json:"phase" enum:"requested,preflighting,draining,source_stopping,source_stopped,target_starting,activating,completed,failed,cancelled,recovery_required"`
	SourceAccountID      string                                         `json:"sourceAccountId,omitempty"`
	TargetAccountID      string                                         `json:"targetAccountId,omitempty"`
	ErrorCode            string                                         `json:"errorCode,omitempty"`
	ErrorDetail          string                                         `json:"errorDetail,omitempty"`
	CreatedAt            time.Time                                      `json:"createdAt"`
	UpdatedAt            time.Time                                      `json:"updatedAt"`
	CompletedAt          *time.Time                                     `json:"completedAt,omitempty"`
	NoticeAcknowledgedAt *time.Time                                     `json:"noticeAcknowledgedAt,omitempty"`
}

type SessionInterfaceTransitionStatusResponse struct {
	Supported  bool                            `json:"supported"`
	TargetMode domain.SessionMode              `json:"targetMode" enum:"chat,tui"`
	ReasonCode string                          `json:"reasonCode,omitempty"`
	Reason     string                          `json:"reason,omitempty"`
	Transition *SessionInterfaceTransitionView `json:"transition,omitempty"`
}

type StartSessionInterfaceTransitionResponse struct {
	OK         bool                           `json:"ok"`
	SessionID  domain.SessionID               `json:"sessionId"`
	Transition SessionInterfaceTransitionView `json:"transition"`
}

type CancelSessionInterfaceTransitionResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
}

type InterfaceTransitionNoticeAckResponse struct {
	OK         bool                           `json:"ok"`
	SessionID  domain.SessionID               `json:"sessionId"`
	Transition SessionInterfaceTransitionView `json:"transition"`
}

type KillSessionRequest struct {
	DeleteArtifacts bool             `json:"deleteArtifacts,omitempty"`
	CallerSessionID domain.SessionID `json:"callerSessionId,omitempty"`
}

type KillSessionResponse struct {
	OK               bool             `json:"ok"`
	SessionID        domain.SessionID `json:"sessionId"`
	Freed            bool             `json:"freed,omitempty"`
	CleanupPending   bool             `json:"cleanupPending,omitempty"`
	ArtifactsDeleted bool             `json:"artifactsDeleted,omitempty"`
}

type RollbackSessionResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	Deleted   bool             `json:"deleted,omitempty"`
	Killed    bool             `json:"killed,omitempty"`
}

type DeleteSessionQuery struct {
	Cleanup bool `query:"cleanup,omitempty" description:"When true, a worktree that is still on disk is cleaned up first, the way session cleanup does it; a dirty or locked worktree is never forced and refuses the delete."`
}

type DeleteSessionResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
}

type CleanupSkippedSession struct {
	SessionID domain.SessionID `json:"sessionId"`
	Code      string           `json:"code" enum:"dirty,locked_worktree,missing_repo,archived_project_unresolvable,permission_error,cleanup_script_failed,worktree_busy,in_use_by_live_session,shell_terminal_open,cleanup_running,attachment_preservation_failed,outside_managed_root,teardown_failed"`
	Reason    string           `json:"reason"`
}

type CleanupSessionsResponse struct {
	OK          bool                    `json:"ok"`
	Cleaned     []domain.SessionID      `json:"cleaned"`
	AlreadyGone []domain.SessionID      `json:"alreadyGone"`
	Skipped     []CleanupSkippedSession `json:"skipped"`
}

type SendSessionMessageRequest struct {
	SenderSessionID string           `json:"senderSessionId,omitempty"`
	Message         string           `json:"message" minLength:"1" maxLength:"4096"`
	UserAuthored    bool             `json:"userAuthored,omitempty"`
	Attachment      *AttachmentInput `json:"attachment,omitempty"`
}

type SendSessionMessageResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	Message   string           `json:"message"`
}

type DelegateTaskRequest struct {
	ClientRequestID string                `json:"clientRequestId,omitempty" maxLength:"128"`
	ProjectID       domain.ProjectID      `json:"projectId"`
	Brief           string                `json:"brief" maxLength:"102400"`
	Branch          string                `json:"branch,omitempty" maxLength:"255"`
	BaseBranch      string                `json:"baseBranch,omitempty" maxLength:"255"`
	Agent           domain.AgentHarness   `json:"agent,omitempty" enum:"claude-code,codex,aider,opencode,opencode-v2,grok,droid,amp,agy,crush,cursor,qwen,gemini,copilot,goose,auggie,continue,devin,cline,kimi,muse,kiro,kilocode,vibe,pi,kimchi,omp,fx,prime-agent,autohand,unreal-agent,codewhale,mimo-code,minimax-code,deepseek-harness,openhands,command-code,docker-agent,memcode,hermes,stakpak,fake"`
	Model           string                `json:"model,omitempty" maxLength:"256"`
	Effort          *string               `json:"effort,omitempty" maxLength:"64"`
	ApprovalMode    domain.PermissionMode `json:"approvalMode,omitempty" enum:"default,accept-edits,auto,bypass-permissions"`
	Mode            domain.SessionMode    `json:"mode,omitempty" enum:"tui,chat"`
	AccountID       string                `json:"accountId,omitempty" maxLength:"128"`
	Attachments     []AttachmentInput     `json:"attachments,omitempty"`
	TaskPreparation string                `json:"taskPreparation,omitempty"`
}

type PrepareTaskResponse struct {
	OK              bool   `json:"ok"`
	TaskPreparation string `json:"taskPreparation,omitempty"`
}

type DelegateTaskResponse struct {
	OK             bool             `json:"ok"`
	WorkerID       domain.SessionID `json:"workerId"`
	OrchestratorID domain.SessionID `json:"orchestratorId,omitempty"`
}

type SessionPRFacts struct {
	URL            string                `json:"url"`
	Number         int                   `json:"number"`
	State          string                `json:"state" enum:"draft,open,merged,closed"`
	CI             domain.CIState        `json:"ci" enum:"unknown,pending,passing,failing"`
	Review         domain.ReviewDecision `json:"review" enum:"none,approved,changes_requested,review_required"`
	Mergeability   domain.Mergeability   `json:"mergeability" enum:"unknown,mergeable,conflicting,blocked,unstable"`
	ReviewComments bool                  `json:"reviewComments"`
	UpdatedAt      time.Time             `json:"updatedAt"`
}

type SessionPRSummary struct {
	URL              string                       `json:"url"`
	HTMLURL          string                       `json:"htmlUrl,omitempty"`
	Number           int                          `json:"number"`
	Title            string                       `json:"title"`
	State            domain.PRState               `json:"state" enum:"draft,open,merged,closed"`
	Provider         string                       `json:"provider" enum:"github,gitlab"`
	Repo             string                       `json:"repo"`
	Author           string                       `json:"author"`
	AuthorAvatarURL  string                       `json:"authorAvatarUrl,omitempty"`
	SourceBranch     string                       `json:"sourceBranch"`
	TargetBranch     string                       `json:"targetBranch"`
	HeadSHA          string                       `json:"headSha"`
	Additions        int                          `json:"additions"`
	Deletions        int                          `json:"deletions"`
	ChangedFiles     int                          `json:"changedFiles"`
	CI               SessionPRCISummary           `json:"ci"`
	Review           SessionPRReviewSummary       `json:"review"`
	Mergeability     SessionPRMergeabilitySummary `json:"mergeability"`
	StateChangedAt   *time.Time                   `json:"stateChangedAt,omitempty"`
	CreatedAt        *time.Time                   `json:"createdAt,omitempty"`
	UpdatedAt        time.Time                    `json:"updatedAt"`
	ObservedAt       time.Time                    `json:"observedAt,omitempty"`
	CIObservedAt     time.Time                    `json:"ciObservedAt,omitempty"`
	ReviewObservedAt time.Time                    `json:"reviewObservedAt,omitempty"`

	Description          string                       `json:"description,omitempty"`
	DescriptionTruncated bool                         `json:"descriptionTruncated,omitempty"`
	Labels               []SessionPRLabel             `json:"labels"`
	RequestedReviewers   []SessionPRRequestedReviewer `json:"requestedReviewers"`
	Commits              []SessionPRCommit            `json:"commits"`
	CommitCount          int                          `json:"commitCount"`
	DetailsObservedAt    *time.Time                   `json:"detailsObservedAt,omitempty"`
	Threads              []SessionPRThread            `json:"threads,omitempty"`
	Conversation         []SessionPRComment           `json:"conversation,omitempty"`
}

type SessionPRThread struct {
	ID       string             `json:"id"`
	Path     string             `json:"path,omitempty"`
	Line     int                `json:"line,omitempty"`
	Resolved bool               `json:"resolved"`
	Comments []SessionPRComment `json:"comments"`
}

type SessionPRComment struct {
	ID        string     `json:"id"`
	Author    string     `json:"author"`
	IsBot     bool       `json:"isBot"`
	Body      string     `json:"body"`
	URL       string     `json:"url,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

type SessionPRLabel struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type SessionPRRequestedReviewer struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl,omitempty"`
	IsTeam    bool   `json:"isTeam,omitempty"`
}

type SessionPRCommit struct {
	SHA         string     `json:"sha"`
	Headline    string     `json:"headline"`
	Author      string     `json:"author,omitempty"`
	CommittedAt *time.Time `json:"committedAt,omitempty"`
}

type SessionPRCISummary struct {
	State         domain.CIState          `json:"state" enum:"unknown,pending,passing,failing"`
	FailingChecks []SessionPRFailingCheck `json:"failingChecks"`
	Checks        []SessionPRCheck        `json:"checks"`
	AutoInjectCI  bool                    `json:"autoInjectCI"`
}

type SessionPRCheck struct {
	Name   string               `json:"name"`
	Status domain.PRCheckStatus `json:"status" enum:"unknown,queued,in_progress,passed,failed,skipped,cancelled"`
	URL    string               `json:"url,omitempty"`
}

type SessionPRFailingCheck struct {
	Name       string               `json:"name"`
	Status     domain.PRCheckStatus `json:"status" enum:"failed,cancelled"`
	Conclusion string               `json:"conclusion"`
	URL        string               `json:"url,omitempty"`
}

type SessionPRReviewSummary struct {
	Decision                   domain.ReviewDecision         `json:"decision" enum:"none,approved,changes_requested,review_required"`
	HasUnresolvedHumanComments bool                          `json:"hasUnresolvedHumanComments"`
	UnresolvedThreadCount      *int                          `json:"unresolvedThreadCount,omitempty"`
	UnresolvedBy               []SessionPRUnresolvedReviewer `json:"unresolvedBy"`
	ResolvedBy                 []SessionPRUnresolvedReviewer `json:"resolvedBy,omitempty"`
	Reviews                    []SessionPRReviewEntry        `json:"reviews,omitempty"`
}

type SessionPRReviewEntry struct {
	ReviewerID       string                `json:"reviewerId"`
	Verdict          domain.ReviewDecision `json:"verdict" enum:"none,approved,changes_requested,review_required"`
	Body             string                `json:"body,omitempty"`
	ReviewURL        string                `json:"reviewUrl,omitempty"`
	SubmittedAt      time.Time             `json:"submittedAt"`
	IsBot            bool                  `json:"isBot,omitempty"`
	AutoInjectReview bool                  `json:"autoInjectReview"`
}

type SessionPRUnresolvedReviewer struct {
	ReviewerID string                       `json:"reviewerId"`
	Count      int                          `json:"count"`
	Links      []SessionPRReviewCommentLink `json:"links"`
	ReviewURL  string                       `json:"reviewUrl,omitempty"`
	IsBot      bool                         `json:"isBot,omitempty"`
}

type SessionPRReviewCommentLink struct {
	URL              string `json:"url,omitempty"`
	ReviewID         string `json:"reviewId,omitempty"`
	File             string `json:"file,omitempty"`
	Line             int    `json:"line,omitempty"`
	Body             string `json:"body,omitempty"`
	AutoInjectReview bool   `json:"autoInjectReview"`
}

type SessionPRMergeabilitySummary struct {
	State         domain.Mergeability     `json:"state" enum:"unknown,mergeable,conflicting,blocked,unstable"`
	Reasons       []string                `json:"reasons"`
	PRURL         string                  `json:"prUrl"`
	ConflictFiles []SessionPRConflictFile `json:"conflictFiles,omitempty"`
}

type SessionPRConflictFile struct {
	Path string `json:"path"`
	URL  string `json:"url,omitempty"`
}

type SessionPRReference struct {
	URL      string `json:"url"`
	Provider string `json:"provider" enum:"github,gitlab"`
	Host     string `json:"host"`
	Repo     string `json:"repo"`
	Number   int    `json:"number"`
}

type ListSessionPRsResponse struct {
	SessionID domain.SessionID     `json:"sessionId"`
	PRs       []SessionPRSummary   `json:"prs"`
	LinkedPRs []SessionPRReference `json:"linkedPrs"`
}

func NewSessionPRSummary(in sessionsvc.PRSummary) SessionPRSummary {
	return SessionPRSummary{
		URL:              in.URL,
		HTMLURL:          in.HTMLURL,
		Number:           in.Number,
		Title:            in.Title,
		State:            in.State,
		Provider:         in.Provider,
		Repo:             in.Repo,
		Author:           in.Author,
		AuthorAvatarURL:  in.AuthorAvatarURL,
		SourceBranch:     in.SourceBranch,
		TargetBranch:     in.TargetBranch,
		HeadSHA:          in.HeadSHA,
		Additions:        in.Additions,
		Deletions:        in.Deletions,
		ChangedFiles:     in.ChangedFiles,
		CI:               newSessionPRCISummary(in.CI),
		Review:           newSessionPRReviewSummary(in.Review),
		Mergeability:     newSessionPRMergeabilitySummary(in.Mergeability),
		StateChangedAt:   optionalTime(in.StateChangedAt),
		CreatedAt:        optionalTime(in.CreatedAt),
		UpdatedAt:        in.UpdatedAt,
		ObservedAt:       in.ObservedAt,
		CIObservedAt:     in.CIObservedAt,
		ReviewObservedAt: in.ReviewObservedAt,

		Description:          in.Description,
		DescriptionTruncated: in.DescriptionTruncated,
		Labels:               newSessionPRLabels(in.Labels),
		RequestedReviewers:   newSessionPRRequestedReviewers(in.RequestedReviewers),
		Commits:              newSessionPRCommits(in.Commits),
		CommitCount:          in.CommitCount,
		DetailsObservedAt:    optionalTime(in.DetailsObservedAt),
		Threads:              newSessionPRThreads(in.Threads),
		Conversation:         newSessionPRComments(in.Conversation),
	}
}

func newSessionPRThreads(in []contract.PullRequestThread) []SessionPRThread {
	if len(in) == 0 {
		return nil
	}
	out := make([]SessionPRThread, 0, len(in))
	for _, th := range in {
		comments := newSessionPRComments(th.Comments)
		if comments == nil {
			comments = []SessionPRComment{}
		}
		out = append(out, SessionPRThread{ID: th.ID, Path: th.Path, Line: th.Line, Resolved: th.Resolved, Comments: comments})
	}
	return out
}

func newSessionPRComments(in []contract.PullRequestCommentEntry) []SessionPRComment {
	if len(in) == 0 {
		return nil
	}
	out := make([]SessionPRComment, 0, len(in))
	for _, c := range in {
		out = append(out, SessionPRComment{ID: c.ID, Author: c.Author, IsBot: c.IsBot, Body: c.Body, URL: c.URL, CreatedAt: optionalTime(c.CreatedAt)})
	}
	return out
}

func newSessionPRLabels(in []contract.PullRequestLabel) []SessionPRLabel {
	out := make([]SessionPRLabel, 0, len(in))
	for _, label := range in {
		out = append(out, SessionPRLabel{Name: label.Name, Color: label.Color})
	}
	return out
}

func newSessionPRRequestedReviewers(in []contract.PullRequestRequestedReviewer) []SessionPRRequestedReviewer {
	out := make([]SessionPRRequestedReviewer, 0, len(in))
	for _, reviewer := range in {
		out = append(out, SessionPRRequestedReviewer{Login: reviewer.Login, AvatarURL: reviewer.AvatarURL, IsTeam: reviewer.IsTeam})
	}
	return out
}

func newSessionPRCommits(in []contract.PullRequestCommit) []SessionPRCommit {
	out := make([]SessionPRCommit, 0, len(in))
	for _, commit := range in {
		out = append(out, SessionPRCommit{SHA: commit.SHA, Headline: commit.Headline, Author: commit.Author, CommittedAt: optionalTime(commit.CommittedAt)})
	}
	return out
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func newSessionPRCISummary(in sessionsvc.PRCISummary) SessionPRCISummary {
	checks := make([]SessionPRFailingCheck, 0, len(in.FailingChecks))
	for _, ch := range in.FailingChecks {
		checks = append(checks, SessionPRFailingCheck{Name: ch.Name, Status: ch.Status, Conclusion: ch.Conclusion, URL: ch.URL})
	}
	all := make([]SessionPRCheck, 0, len(in.Checks))
	for _, ch := range in.Checks {
		all = append(all, SessionPRCheck{Name: ch.Name, Status: ch.Status, URL: ch.URL})
	}
	return SessionPRCISummary{State: in.State, FailingChecks: checks, Checks: all, AutoInjectCI: in.AutoInjectCI}
}

func newSessionPRReviewSummary(in sessionsvc.PRReviewSummary) SessionPRReviewSummary {
	reviewers := newSessionPRCommentReviewers(in.UnresolvedBy)
	resolvedReviewers := newSessionPRCommentReviewers(in.ResolvedBy)
	entries := make([]SessionPRReviewEntry, 0, len(in.Reviews))
	for _, review := range in.Reviews {
		entries = append(entries, SessionPRReviewEntry{
			ReviewerID:       review.Reviewer,
			Verdict:          review.Verdict,
			Body:             review.Body,
			ReviewURL:        review.URL,
			SubmittedAt:      review.SubmittedAt,
			IsBot:            review.IsBot,
			AutoInjectReview: review.AutoInjectReview,
		})
	}
	return SessionPRReviewSummary{
		Decision:                   in.Decision,
		HasUnresolvedHumanComments: in.HasUnresolvedHumanComments,
		UnresolvedThreadCount:      in.UnresolvedThreadCount,
		UnresolvedBy:               reviewers,
		ResolvedBy:                 resolvedReviewers,
		Reviews:                    entries,
	}
}

func newSessionPRCommentReviewers(in []sessionsvc.PRUnresolvedReviewer) []SessionPRUnresolvedReviewer {
	reviewers := make([]SessionPRUnresolvedReviewer, 0, len(in))
	for _, reviewer := range in {
		links := make([]SessionPRReviewCommentLink, 0, len(reviewer.Links))
		for _, link := range reviewer.Links {
			links = append(links, SessionPRReviewCommentLink{URL: link.URL, ReviewID: link.ReviewID, File: link.File, Line: link.Line, Body: link.Body, AutoInjectReview: link.AutoInjectReview})
		}
		reviewers = append(reviewers, SessionPRUnresolvedReviewer{ReviewerID: reviewer.ReviewerID, Count: reviewer.Count, Links: links, ReviewURL: reviewer.ReviewURL, IsBot: reviewer.IsBot})
	}
	return reviewers
}

func newSessionPRMergeabilitySummary(in sessionsvc.PRMergeabilitySummary) SessionPRMergeabilitySummary {
	files := make([]SessionPRConflictFile, 0, len(in.ConflictFiles))
	for _, file := range in.ConflictFiles {
		files = append(files, SessionPRConflictFile{Path: file.Path, URL: file.URL})
	}
	reasons := append([]string{}, in.Reasons...)
	return SessionPRMergeabilitySummary{State: in.State, Reasons: reasons, PRURL: in.PRURL, ConflictFiles: files}
}

type ClaimPRRequest struct {
	PR            string `json:"pr" minLength:"1"`
	AllowTakeover *bool  `json:"allowTakeover,omitempty"`
}

type ClaimPRResponse struct {
	OK            bool               `json:"ok"`
	SessionID     domain.SessionID   `json:"sessionId"`
	PRs           []SessionPRFacts   `json:"prs"`
	BranchChanged bool               `json:"branchChanged"`
	TakenOverFrom []domain.SessionID `json:"takenOverFrom"`
}

type SetActivityRequest struct {
	ObservedAt                   time.Time                           `json:"observedAt,omitempty" description:"Time the local hook process observed the event, before delivery to the daemon."`
	State                        string                              `json:"state,omitempty" enum:"active,idle,waiting_input,blocked,exited" description:"Agent activity state reported by an agent hook. Optional for metadata-only hooks."`
	Event                        string                              `json:"event,omitempty" description:"AO hook sub-command that produced this state (e.g. post-tool-use)."`
	ToolName                     string                              `json:"toolName,omitempty" description:"Native tool name, for tool-use hook events."`
	ToolUseID                    string                              `json:"toolUseId,omitempty" description:"Native tool-use id, for tool-use hook events."`
	SubagentID                   string                              `json:"subagentId,omitempty" description:"Native child agent id for this hook event."`
	RunningSubagentIDs           *[]string                           `json:"runningSubagentIds,omitempty" description:"Running Claude subagent ids observed in a parent Stop hook; empty means none, absent means no snapshot."`
	AgentSessionID               string                              `json:"agentSessionId,omitempty" description:"Native agent session identifier used to resume its transcript."`
	LatestUserPrompt             string                              `json:"latestUserPrompt,omitempty" maxLength:"16384" description:"Latest real user prompt exposed by the provider hook."`
	LatestAssistantUpdate        string                              `json:"latestAssistantUpdate,omitempty" maxLength:"16384" description:"Latest assistant update exposed by the provider hook."`
	FinalAssistantMessage        string                              `json:"finalAssistantMessage,omitempty" maxLength:"16384" description:"The agent's final message of the turn, kept for terminal turn history only."`
	TurnUserRequest              string                              `json:"turnUserRequest,omitempty" maxLength:"16384" description:"The user's request for the turn, kept for terminal turn history only."`
	Harness                      string                              `json:"harness,omitempty" maxLength:"64" description:"The agent whose hook sent this signal. A signal from an agent the session no longer runs is ignored."`
	ConversationCheckpointOrigin domain.ConversationCheckpointOrigin `json:"conversationCheckpointOrigin,omitempty" enum:"human,coordination" description:"Whether the main-turn boundary came from a human or AO coordination."`
	CoordinationID               string                              `json:"coordinationId,omitempty" description:"Opaque identity of an AO-authored semantic prompt accepted by the native agent."`
	ProviderTurnID               string                              `json:"providerTurnId,omitempty" description:"Native main-turn identity reported by the hook, when supported."`
	SubmissionID                 string                              `json:"submissionId,omitempty" maxLength:"36" description:"AO prompt-hook context correlation UUID, when supported."`
	TranscriptPath               string                              `json:"transcriptPath,omitempty" maxLength:"4096" description:"Read-only provider-native transcript path exposed by the hook."`
	LaunchID                     string                              `json:"launchId,omitempty" description:"AO process generation that produced the signal."`
	Entrypoint                   string                              `json:"entrypoint,omitempty" description:"Provider process-kind marker (Claude's CLAUDE_CODE_ENTRYPOINT) of the process that fired the hook."`
	ProcessID                    int                                 `json:"processId,omitempty" minimum:"0" description:"Process id of the provider process that fired the hook (Claude's CLAUDE_PID)."`
	Usage                        *UsageHookMetadata                  `json:"usage,omitempty" description:"Provider transcript metadata used by the local usage pipeline."`
	PRMayHaveChanged             bool                                `json:"prMayHaveChanged,omitempty" description:"The finished tool call probably changed the session's pull request (gh pr merge, git push), so AO re-reads it early."`
}

type UsageHookMetadata struct {
	Harness                domain.AgentHarness `json:"harness" enum:"claude-code,codex"`
	ProviderID             string              `json:"providerId,omitempty" description:"Canonical provider routing hint derived by the trusted local Claude hook."`
	TranscriptPath         string              `json:"transcriptPath,omitempty"`
	ModelID                string              `json:"modelId,omitempty"`
	SubagentID             string              `json:"subagentId,omitempty"`
	SubagentTranscriptPath string              `json:"subagentTranscriptPath,omitempty"`
}

type SetActivityResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	State     string           `json:"state"`
}

type CodewhaleLifecycleWebhookRequest struct {
	At    time.Time               `json:"at,omitempty"`
	Event CodewhaleLifecycleEvent `json:"event"`
}

type CodewhaleLifecycleEvent struct {
	SchemaVersion int       `json:"schema_version"`
	Sequence      uint64    `json:"seq"`
	Event         string    `json:"event"`
	Kind          string    `json:"kind" enum:"session.started,turn.started,turn.completed,turn.failed,turn.interrupted,turn.stalled,session.ended,subagent.spawned,subagent.completed"`
	ThreadID      string    `json:"thread_id"`
	TurnID        string    `json:"turn_id,omitempty"`
	ItemID        *string   `json:"item_id,omitempty"`
	Timestamp     time.Time `json:"timestamp,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	Payload       any       `json:"payload,omitempty"`
}

type SetReviewActivityRequest struct {
	State          string `json:"state,omitempty" enum:"active,idle,waiting_input,blocked,exited" description:"Reviewer activity state reported by a hook. Used for reviewer-pane live status, not worker session state."`
	Event          string `json:"event,omitempty" description:"AO hook sub-command that produced this signal."`
	AgentSessionID string `json:"agentSessionId,omitempty" description:"Native reviewer session identifier used to resume its transcript."`
	LaunchID       string `json:"launchId,omitempty" description:"AO process generation that produced the signal."`
}

type SetReviewActivityResponse struct {
	OK              bool   `json:"ok"`
	ReviewSessionID string `json:"reviewSessionId"`
}

type OrchestratorIDParam struct {
	ID string `path:"id" description:"Orchestrator session identifier, e.g. project-orchestrator."`
}

type ReviewSessionIDParam struct {
	ID string `path:"reviewSessionID" description:"Reviewer session identifier, currently the per-harness review row id."`
}

type ReviewIDParam struct {
	ReviewID string `path:"reviewId" description:"Reviewer conversation identifier."`
}

type SpawnOrchestratorRequest struct {
	ProjectID    domain.ProjectID      `json:"projectId"`
	Clean        bool                  `json:"clean,omitempty"`
	Mode         domain.SessionMode    `json:"mode,omitempty" enum:"chat,tui"`
	ApprovalMode domain.PermissionMode `json:"approvalMode,omitempty" enum:"default,accept-edits,auto,bypass-permissions"`
}

type SpawnOrchestratorResponse struct {
	Orchestrator OrchestratorResponse `json:"orchestrator"`
}

type OrchestratorResponse struct {
	ID          domain.SessionID `json:"id"`
	ProjectID   domain.ProjectID `json:"projectId"`
	ProjectName string           `json:"projectName,omitempty"`
}

type ListAgentsResponse = agentsvc.Inventory

type RefreshAgentsResponse = agentsvc.Inventory

type ProbeAgentResponse = agentsvc.ProbeResult

type AgentReadinessResponse = agentsvc.Readiness

type EnsureAgentReadinessRequest struct {
	AgentIDs []string                     `json:"agentIds,omitempty"`
	Purpose  domain.AgentReadinessPurpose `json:"purpose" enum:"display,launch"`
}

type CodexAccountsResponse struct {
	ActiveAccountID      string                            `json:"activeAccountId,omitempty"`
	AccountRevision      int64                             `json:"accountRevision"`
	Accounts             []CodexAccountResponse            `json:"accounts"`
	Capabilities         CodexAccountCapabilitiesResponse  `json:"capabilities"`
	DeviceReconciliation CodexDeviceReconciliationResponse `json:"deviceReconciliation"`
	ActiveLogin          *CodexActiveLoginResponse         `json:"activeLogin,omitempty"`
	CurrentSwitch        *CodexAccountSwitchResponse       `json:"currentSwitch,omitempty"`
}

type CodexDeviceReconciliationResponse struct {
	Status                string     `json:"status" enum:"not_checked,checking,verified,temporarily_unavailable,blocked"`
	ActiveAccountVerified bool       `json:"activeAccountVerified"`
	ReasonCode            string     `json:"reasonCode"`
	Retryable             bool       `json:"retryable"`
	AttemptedAt           *time.Time `json:"attemptedAt,omitempty"`
	VerifiedAt            *time.Time `json:"verifiedAt,omitempty"`
	NextRetryAt           *time.Time `json:"nextRetryAt,omitempty"`
}

type CodexAccountResponse struct {
	ID             string                            `json:"id"`
	Label          string                            `json:"label"`
	Status         string                            `json:"status" enum:"valid,signed_out,broken"`
	ReasonCode     string                            `json:"reasonCode"`
	Reason         string                            `json:"reason"`
	Active         bool                              `json:"active"`
	Authentication CodexAuthenticationResponse       `json:"authentication"`
	AuthMethod     string                            `json:"authMethod" enum:"chatgpt,api_key,other,unknown"`
	AccountEmail   *string                           `json:"accountEmail,omitempty"`
	Capacity       CodexAccountCapacityResponse      `json:"capacity"`
	UsageSummary   *CodexAccountUsageSummaryResponse `json:"usageSummary,omitempty"`
	CreatedAt      time.Time                         `json:"createdAt"`
}

type CodexAuthenticationResponse struct {
	State       string     `json:"state" enum:"authorized,unauthorized,unknown,not_applicable"`
	Freshness   string     `json:"freshness" enum:"fresh,stale,checking"`
	CheckedAt   *time.Time `json:"checkedAt"`
	AttemptedAt *time.Time `json:"attemptedAt"`
	ReasonCode  string     `json:"reasonCode"`
	Reason      string     `json:"reason"`
}

type CodexAccountCapacityResponse struct {
	State             string                            `json:"state" enum:"available,near_limit,exhausted,unknown,unsupported"`
	Freshness         string                            `json:"freshness" enum:"fresh,stale,checking"`
	Plan              *string                           `json:"plan,omitempty"`
	UsedPercent       *float64                          `json:"usedPercent,omitempty" minimum:"0" maximum:"100"`
	RemainingPercent  *float64                          `json:"remainingPercent,omitempty" minimum:"0" maximum:"100"`
	ResetsAt          *time.Time                        `json:"resetsAt,omitempty"`
	ObservedAt        *time.Time                        `json:"observedAt,omitempty"`
	CheckedAt         *time.Time                        `json:"checkedAt,omitempty"`
	AttemptedAt       *time.Time                        `json:"attemptedAt,omitempty"`
	ReasonCode        string                            `json:"reasonCode"`
	Reason            string                            `json:"reason"`
	Overall           *CodexCapacityBucketResponse      `json:"overall,omitempty"`
	AdditionalBuckets []CodexCapacityBucketResponse     `json:"additionalBuckets"`
	ResetCredits      *CodexResetCreditsSummaryResponse `json:"resetCredits,omitempty"`
}

type CodexCapacityBucketResponse struct {
	DisplayName *string                      `json:"displayName,omitempty"`
	Primary     *CodexCapacityWindowResponse `json:"primary,omitempty"`
	Secondary   *CodexCapacityWindowResponse `json:"secondary,omitempty"`
	Reached     string                       `json:"reached" enum:"not_reached,reached,unknown"`
}

type CodexCapacityWindowResponse struct {
	UsedPercent           float64    `json:"usedPercent" minimum:"0" maximum:"100"`
	WindowDurationMinutes *int64     `json:"windowDurationMinutes,omitempty"`
	ResetsAt              *time.Time `json:"resetsAt,omitempty"`
}

type CodexResetCreditsSummaryResponse struct {
	AvailableCount   int64      `json:"availableCount" minimum:"0"`
	NearestExpiresAt *time.Time `json:"nearestExpiresAt,omitempty"`
}

type CodexAccountUsageSummaryResponse struct {
	LatestDayTokens           *int64    `json:"latestDayTokens,omitempty"`
	LatestDayStartDate        *string   `json:"latestDayStartDate,omitempty"`
	LifetimeTokens            *int64    `json:"lifetimeTokens,omitempty"`
	PeakDailyTokens           *int64    `json:"peakDailyTokens,omitempty"`
	LongestRunningTurnSeconds *int64    `json:"longestRunningTurnSeconds,omitempty"`
	CurrentStreakDays         *int64    `json:"currentStreakDays,omitempty"`
	LongestStreakDays         *int64    `json:"longestStreakDays,omitempty"`
	ObservedAt                time.Time `json:"observedAt"`
}

type CodexCapabilityObservationResponse struct {
	State      string `json:"state" enum:"supported,unsupported,unknown"`
	ReasonCode string `json:"reasonCode"`
	Reason     string `json:"reason"`
}

type CodexAccountCapabilitiesResponse struct {
	NativeLogin        CodexCapabilityObservationResponse `json:"nativeLogin"`
	ResetCreditConsume CodexCapabilityObservationResponse `json:"resetCreditConsume"`
	GlobalSwitch       CodexCapabilityObservationResponse `json:"globalSwitch"`
}

type EnsureCodexAccountsRequest struct {
	AccountIDs                []string `json:"accountIds,omitempty"`
	IncludeUsage              bool     `json:"includeUsage,omitempty"`
	ForceAuthentication       bool     `json:"forceAuthentication,omitempty"`
	ForceDeviceReconciliation bool     `json:"forceDeviceReconciliation,omitempty"`
}

type ConsumeCodexAccountResetCreditRequest struct {
	IdempotencyKey string `json:"idempotencyKey" minLength:"1" maxLength:"200"`
}

type OpenCodexAccountLoginTerminalResponse struct {
	Operation     CodexAccountLoginResponse         `json:"operation"`
	ShellTerminal CodexAccountLoginTerminalResponse `json:"shellTerminal"`
}

type CodexAccountLoginResponse struct {
	OperationID string                `json:"operationId"`
	AccountID   string                `json:"accountId,omitempty"`
	Status      string                `json:"status" enum:"pending,verifying,unauthorized,retryable,completed,cancelled,failed,expired"`
	ReasonCode  string                `json:"reasonCode"`
	Reason      string                `json:"reason"`
	Account     *CodexAccountResponse `json:"account,omitempty"`
	ExpiresAt   time.Time             `json:"expiresAt"`
}

type CodexActiveLoginResponse struct {
	OperationID   string                            `json:"operationId"`
	AccountID     string                            `json:"accountId,omitempty"`
	Status        string                            `json:"status" enum:"pending,verifying,unauthorized,retryable,completed,cancelled,failed,expired"`
	ReasonCode    string                            `json:"reasonCode"`
	Reason        string                            `json:"reason"`
	ExpiresAt     time.Time                         `json:"expiresAt"`
	ShellTerminal CodexAccountLoginTerminalResponse `json:"shellTerminal"`
}

type CodexAccountLoginTerminalResponse struct {
	HandleID  string    `json:"handleId"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
}

type StartCodexAccountSwitchRequest struct {
	TargetAccountID         string `json:"targetAccountId" minLength:"1"`
	ExpectedAccountRevision *int64 `json:"expectedAccountRevision,omitempty" minimum:"0" deprecated:"true"`
	IdempotencyKey          string `json:"idempotencyKey" minLength:"1"`
}

type CodexAccountSwitchPhase string

type CodexAccountSwitchResponse struct {
	ID                     string                  `json:"id"`
	SourceKind             string                  `json:"sourceKind" enum:"managed,device,none"`
	SourceAccountID        string                  `json:"sourceAccountId,omitempty"`
	TargetAccountID        string                  `json:"targetAccountId"`
	Phase                  CodexAccountSwitchPhase `json:"phase" enum:"requested,checkpointing_source,activating_target,recovery_required,completed,failed"`
	FailureCode            string                  `json:"failureCode,omitempty"`
	CredentialsCommittedAt *time.Time              `json:"credentialsCommittedAt,omitempty"`
	CreatedAt              time.Time               `json:"createdAt"`
	UpdatedAt              time.Time               `json:"updatedAt"`
	CompletedAt            *time.Time              `json:"completedAt,omitempty"`
}

type AgentReadinessSnapshot = domain.AgentReadinessSnapshot

type AgentInstallationObservation = domain.AgentInstallationObservation

type AgentAuthenticationObservation = domain.AgentAuthenticationObservation

type AgentModelsQuery struct {
	CatalogOnly bool   `query:"catalogOnly,omitempty" description:"Return catalog facts without reading session history for model recency. Cached catalogs are served while discovery refreshes in the background."`
	ProjectID   string `query:"projectId,omitempty" description:"Optional cloud credential scope. Any other value, including a project identifier, is ignored: model catalogs are cached per device, not per project."`
}

type AgentModelsRefreshQuery struct {
	CatalogOnly bool   `query:"catalogOnly,omitempty" description:"Refresh the Harness Settings browsing catalog without session-history enrichment; FX includes all connections in a separate cache."`
	ProjectID   string `query:"projectId,omitempty" description:"Optional cloud credential scope. Any other value, including a project identifier, is ignored: model catalogs are cached per device, not per project."`
	Revalidate  bool   `query:"revalidate,omitempty" description:"When true, compare executable and config metadata before running discovery."`
}

type AgentModelsResponse = ports.AgentModelCatalog

type AgentModelInfo = ports.AgentModelInfo

type AgentInfo = agentsvc.Info

type ListUsageSessionsQuery struct {
	ProjectID domain.ProjectID `query:"projectId,omitempty" description:"Optional project id filter for dashboard cards."`
}

type UsageReportQuery struct {
	Days      int              `query:"days,omitempty" minimum:"1" maximum:"90" description:"Days to read, today included. 1 reads the last 24 hours by hour. Defaults to 7."`
	Offset    int              `query:"offset,omitempty" minimum:"0" description:"Whole windows to step back: 1 with days=7 reads the week before this one. Defaults to 0."`
	TimeZone  string           `query:"tz,omitempty" description:"IANA time zone that days and hours are bucketed in. Defaults to UTC."`
	ProjectID domain.ProjectID `query:"projectId,omitempty" description:"Read one project only."`
}

type UsagePriceOverride struct {
	ModelID    string `json:"modelId" required:"true" maxLength:"256" description:"Model ID as usage rows store it."`
	Input      string `json:"inputUsdPerMillion,omitempty" description:"Decimal USD per million uncached input tokens. Set with output."`
	Output     string `json:"outputUsdPerMillion,omitempty" description:"Decimal USD per million output tokens. Set with input."`
	CacheRead  string `json:"cacheReadUsdPerMillion,omitempty" description:"Decimal USD per million cache-read tokens. Defaults to the input price."`
	CacheWrite string `json:"cacheWriteUsdPerMillion,omitempty" description:"Decimal USD per million cache-write tokens. Defaults to the input price."`
	PriceAs    string `json:"priceAs,omitempty" maxLength:"256" description:"Catalog model to price as, provider/model, in place of rates."`
}

type UsagePricesResponse struct {
	Overrides []UsagePriceOverride `json:"overrides" required:"true"`
	Models    []string             `json:"models" required:"true" description:"Catalog models a model can price as, provider/model."`
}

type UsagePricesRequest struct {
	Overrides []UsagePriceOverride `json:"overrides" required:"true"`
}

type UsageLimitHistoryRequest struct {
	Enabled *bool `json:"enabled"`
}

type UsageLimitHistoryResponse struct {
	Enabled bool `json:"enabled"`
}

type UsageLimitWindowsQuery struct {
	Days int `query:"days,omitempty" minimum:"1" maximum:"14" description:"Days of readings to read; they are kept for 14. Defaults to 7."`
}

type UsageLimitWindowsResponse struct {
	Since          time.Time                `json:"since"`
	Until          time.Time                `json:"until"`
	FirstReadingAt *time.Time               `json:"firstReadingAt,omitempty"`
	Logins         []domain.UsageLimitLogin `json:"logins"`
}

type UsageReportResponse struct {
	Days       int                          `json:"days"`
	TimeZone   string                       `json:"timeZone"`
	Resolution string                       `json:"resolution" enum:"day,hour"`
	Since      time.Time                    `json:"since"`
	Until      time.Time                    `json:"until"`
	Periods    []string                     `json:"periods" description:"Every bucket in the window, oldest first: 2006-01-02 by day, 2006-01-02T15:00 by hour."`
	Rows       []UsageReportRowResponse     `json:"rows"`
	Sessions   []UsageReportSessionResponse `json:"sessions"`
}

type UsageReportRowResponse struct {
	Period              string `json:"period"`
	Harness             string `json:"harness"`
	Model               string `json:"model"`
	Speed               string `json:"speed" description:"standard, priority, fast, flex, ultrafast, or unknown."`
	Events              int64  `json:"events"`
	PricedEvents        int64  `json:"pricedEvents"`
	InputTokens         int64  `json:"inputTokens" description:"Uncached input that was not written to the cache."`
	CacheReadTokens     int64  `json:"cacheReadTokens"`
	CacheWriteTokens    int64  `json:"cacheWriteTokens"`
	OutputTokens        int64  `json:"outputTokens"`
	InputCostNanos      int64  `json:"inputCostNanos"`
	CacheReadCostNanos  int64  `json:"cacheReadCostNanos"`
	CacheWriteCostNanos int64  `json:"cacheWriteCostNanos"`
	OutputCostNanos     int64  `json:"outputCostNanos"`
	CostNanos           int64  `json:"costNanos"`
	PremiumNanos        int64  `json:"premiumNanos" description:"Part of costNanos above standard rates, from priority, fast or ultrafast processing."`
	CacheSavingsNanos   int64  `json:"cacheSavingsNanos" description:"What cache reads saved against paying input rates for those tokens."`
}

type UsageReportSessionResponse struct {
	SessionID    string    `json:"sessionId"`
	ProjectID    string    `json:"projectId,omitempty"`
	Harness      string    `json:"harness"`
	Title        string    `json:"title"`
	Events       int64     `json:"events"`
	PricedEvents int64     `json:"pricedEvents"`
	Tokens       int64     `json:"tokens"`
	CostNanos    int64     `json:"costNanos"`
	LastUsedAt   time.Time `json:"lastUsedAt"`
}

type EstimatedCostResponse struct {
	TotalNanos          int64  `json:"totalNanos" minimum:"0" format:"int64"`
	InputNanos          *int64 `json:"inputNanos" minimum:"0" format:"int64" description:"Every non-cache-read input charge, cache writes included."`
	CachedInputNanos    *int64 `json:"cachedInputNanos" minimum:"0" format:"int64"`
	OutputNanos         *int64 `json:"outputNanos" minimum:"0" format:"int64"`
	Coverage            string `json:"coverage" enum:"complete,partial"`
	ProviderAttribution string `json:"providerAttribution" enum:"observed,inferred,mixed" description:"Whether contributing billing providers were detected, inferred from model ownership, or both."`
}

type CompactSessionUsageResponse struct {
	SessionID       domain.SessionID       `json:"sessionId"`
	ProcessedTokens *int64                 `json:"processedTokens" minimum:"0" description:"Canonical input plus output. Null when either component is unknown."`
	TotalTokens     int64                  `json:"totalTokens" minimum:"0" description:"Deprecated compatibility alias for processedTokens."`
	Incomplete      bool                   `json:"incomplete"`
	EstimatedCost   *EstimatedCostResponse `json:"estimatedCost"`
}

type ListCompactSessionUsageResponse struct {
	Sessions []CompactSessionUsageResponse `json:"sessions"`
}

type SessionMemoryProcessResponse struct {
	PID        int     `json:"pid"`
	PPID       int     `json:"ppid"`
	RSSBytes   uint64  `json:"rssBytes" minimum:"0"`
	CPUPercent float64 `json:"cpuPercent" minimum:"0" description:"Share of one core used since the previous sample; zero on the first."`
	Command    string  `json:"command"`
}

type SessionMemoryResponse struct {
	SessionID    domain.SessionID               `json:"sessionId"`
	RSSBytes     uint64                         `json:"rssBytes" minimum:"0" description:"Resident set size summed over the runtime process tree."`
	ProcessCount int                            `json:"processCount" minimum:"0"`
	CPUPercent   float64                        `json:"cpuPercent" minimum:"0" description:"Share of one core the whole tree used since the previous sample; zero on the first."`
	SampledAt    time.Time                      `json:"sampledAt"`
	Processes    []SessionMemoryProcessResponse `json:"processes"`
	Activity     *SessionActivityResponse       `json:"activity,omitempty"`
}

type SessionStepResponse struct {
	Tool      string     `json:"tool"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty" description:"Absent while the tool is still running."`
	Failed    bool       `json:"failed"`
}

type SessionActivityResponse struct {
	Current *SessionStepResponse  `json:"current,omitempty"`
	Recent  []SessionStepResponse `json:"recent"`
}

type ListSessionMemoryResponse struct {
	Sessions []SessionMemoryResponse `json:"sessions"`
	System   *SystemMemoryResponse   `json:"system,omitempty"`
	App      *AppMemoryResponse      `json:"app,omitempty"`
}

type AppMemoryResponse struct {
	RSSBytes     uint64                   `json:"rssBytes" minimum:"0"`
	ProcessCount int                      `json:"processCount" minimum:"0"`
	CPUPercent   float64                  `json:"cpuPercent" minimum:"0"`
	CPUMeasured  bool                     `json:"cpuMeasured" description:"False when there was no earlier sample to measure against: cpuPercent is unknown, not zero."`
	Own          *SessionMemoryResponse   `json:"own,omitempty"`
	Reviewers    []ReviewerMemoryResponse `json:"reviewers,omitempty"`
}

type ReviewerMemoryResponse struct {
	ReviewID  string                `json:"reviewId"`
	SessionID domain.SessionID      `json:"sessionId" description:"The worker session this reviewer reviews, not the reviewer's own identity."`
	Harness   string                `json:"harness"`
	Memory    SessionMemoryResponse `json:"memory"`
}

type SystemMemoryResponse struct {
	TotalBytes      uint64  `json:"totalBytes" minimum:"0"`
	AvailableBytes  uint64  `json:"availableBytes" minimum:"0" description:"What the kernel would hand out without swapping (MemAvailable)."`
	SwapTotalBytes  uint64  `json:"swapTotalBytes" minimum:"0"`
	SwapUsedBytes   uint64  `json:"swapUsedBytes" minimum:"0"`
	SwapBytesPerSec float64 `json:"swapBytesPerSec" minimum:"0"`
	CPUCount        int     `json:"cpuCount" minimum:"0"`
	Load1           float64 `json:"load1"`
	CPUPercent      float64 `json:"cpuPercent" minimum:"0"`
	CPUMeasured     bool    `json:"cpuMeasured"`
	PressureRaw     float64 `json:"pressureRaw" minimum:"0"`
	PressureSource  string  `json:"pressureSource" enum:"psi,available_pct,memorystatus"`
}

type MemoryPressureResponse struct {
	PressureRaw    float64 `json:"pressureRaw" minimum:"0"`
	PressureSource string  `json:"pressureSource" enum:"psi,available_pct,memorystatus"`
}

type SystemRequirementsResponse = systemcheck.Report

type GitHubAuthRequirementResponse = systemcheck.Requirement

type InstallTargetParam struct {
	Target string `path:"target" enum:"tmux,gh,claude,codex,opencode,opencode-v2,copilot,cloudflared,cua-driver" description:"Install target identifier: tmux, gh, claude, codex, opencode, opencode-v2, copilot, cloudflared, or cua-driver."`
}

type ComputerUseStatusResponse = computeruse.Status

type ComputerUseCallRequest = computeruse.CallRequest

type ComputerUseCallResponse = computeruse.CallResult

type ComputerUseDoctorResponse struct {
	Report string `json:"report" description:"Cua Driver's own environment report (cua-driver doctor)."`
}

type ComputerUseAgentToolsResponse struct {
	Tools []computeruse.AgentTool `json:"tools"`
}

type StartInstallResponse = systeminstall.Job

type InstallStatusResponse = systeminstall.Job

type AutomationIDParam struct {
	AutomationID string `path:"automationId" description:"Automation identifier."`
}

type ListAutomationsQuery struct {
	ProjectID string `query:"projectId,omitempty"`
	Enabled   *bool  `query:"enabled,omitempty"`
	Limit     int    `query:"limit,omitempty" minimum:"1" maximum:"100"`
	Cursor    string `query:"cursor,omitempty"`
}

type ListAutomationRunsQuery struct {
	Limit  int    `query:"limit,omitempty" minimum:"1" maximum:"100"`
	Cursor string `query:"cursor,omitempty"`
}

type CreateAutomationRequest struct {
	ProjectID   string `json:"projectId"`
	DisplayName string `json:"displayName"`
	Prompt      string `json:"prompt"`
	UseCase     string `json:"useCase,omitempty" enum:"pr_review,issue_triage,repo_digest,ci_triage,release_notes,security_audit,dependency_maintenance,docs_maintenance,other,unknown"`
	TemplateID  string `json:"templateId,omitempty" enum:"custom,daily_pr_review_v1,daily_issue_triage_v1,weekly_release_notes_v1"`
	Kind        string `json:"kind" enum:"worker,orchestrator"`
	Harness     string `json:"harness,omitempty"`
	RRule       string `json:"rrule,omitempty"`
	Cron        string `json:"cron,omitempty"`
	Timezone    string `json:"timezone"`
	Enabled     *bool  `json:"enabled,omitempty"`
	Model       string `json:"model,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Effort      string `json:"effort,omitempty"`
}

type UpdateAutomationRequest struct {
	DisplayName *string `json:"displayName,omitempty"`
	Prompt      *string `json:"prompt,omitempty"`
	UseCase     *string `json:"useCase,omitempty" enum:"pr_review,issue_triage,repo_digest,ci_triage,release_notes,security_audit,dependency_maintenance,docs_maintenance,other,unknown"`
	Kind        *string `json:"kind,omitempty" enum:"worker,orchestrator"`
	Harness     *string `json:"harness,omitempty"`
	RRule       *string `json:"rrule,omitempty"`
	Cron        *string `json:"cron,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
	Model       *string `json:"model,omitempty"`
	Mode        *string `json:"mode,omitempty"`
	Effort      *string `json:"effort,omitempty"`
}

type AutomationRunSummaryResponse struct {
	ID           string     `json:"id"`
	Status       string     `json:"status" enum:"pending,spawning,running,completed,failed"`
	ScheduledFor time.Time  `json:"scheduledFor"`
	SessionID    string     `json:"sessionId,omitempty"`
	ErrorMessage string     `json:"errorMessage,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
}

type AutomationResponse struct {
	ID          string                        `json:"id"`
	ProjectID   string                        `json:"projectId"`
	DisplayName string                        `json:"displayName"`
	Prompt      string                        `json:"prompt"`
	UseCase     string                        `json:"useCase" enum:"pr_review,issue_triage,repo_digest,ci_triage,release_notes,security_audit,dependency_maintenance,docs_maintenance,other,unknown"`
	TemplateID  string                        `json:"templateId" enum:"custom,daily_pr_review_v1,daily_issue_triage_v1,weekly_release_notes_v1"`
	Kind        string                        `json:"kind" enum:"worker,orchestrator"`
	Harness     string                        `json:"harness,omitempty"`
	RRule       string                        `json:"rrule"`
	Timezone    string                        `json:"timezone"`
	Enabled     bool                          `json:"enabled"`
	NextRunAt   time.Time                     `json:"nextRunAt"`
	LastRunAt   *time.Time                    `json:"lastRunAt,omitempty"`
	CreatedAt   time.Time                     `json:"createdAt"`
	UpdatedAt   time.Time                     `json:"updatedAt"`
	LatestRun   *AutomationRunSummaryResponse `json:"latestRun,omitempty"`
	Model       string                        `json:"model,omitempty"`
	Mode        string                        `json:"mode,omitempty"`
	Effort      string                        `json:"effort,omitempty"`
}

type AutomationEnvelope struct {
	Automation AutomationResponse `json:"automation"`
}

type ListAutomationsResponse struct {
	Automations []AutomationResponse `json:"automations"`
	NextCursor  string               `json:"nextCursor,omitempty"`
}

type AutomationRunResponse struct {
	ID             string     `json:"id"`
	AutomationID   string     `json:"automationId"`
	ScheduledFor   time.Time  `json:"scheduledFor"`
	SessionID      string     `json:"sessionId,omitempty"`
	Status         string     `json:"status" enum:"pending,spawning,running,completed,failed"`
	AttemptCount   int64      `json:"attemptCount"`
	ClaimedAt      *time.Time `json:"claimedAt,omitempty"`
	LeaseExpiresAt *time.Time `json:"leaseExpiresAt,omitempty"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	ErrorMessage   string     `json:"errorMessage,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type ListAutomationRunsResponse struct {
	Runs       []AutomationRunResponse `json:"runs"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}

type AgentInstallResponse = systeminstall.Job

type AgentUpdateAdvisoryQuery struct {
	Refresh bool `query:"refresh,omitempty" description:"Re-probe the installed binary, ownership and latest release instead of reusing cached advisory evidence."`
}

type StartAgentInstallRequest struct {
	Method          string                       `json:"method,omitempty" description:"Server-issued installation method id. Omit to use the recommended viable method for an install or reinstall, and the method that owns the installed copy for an update."`
	Operation       systeminstall.AgentOperation `json:"operation,omitempty" enum:"install,reinstall,update" description:"Requested operation. Defaults to install for older clients."`
	ExpectedVersion string                       `json:"expectedVersion,omitempty" description:"For updates, the available version explicitly approved by the user. The update must reach it. It is installed only when it lies between the installed version and the latest release AO found; otherwise that latest release is installed. A version at or below the installed one, or one sent with another operation, is rejected with INVALID_UPDATE_VERSION."`
}

type AgentInstallJobsResponse struct {
	Jobs []systeminstall.Job `json:"jobs"`
}

type ListNotificationsQuery struct {
	Status string `query:"status,omitempty" enum:"unread,all,unresolved" description:"Notification filter. Defaults to unread (unseen); unresolved returns notifications whose underlying issue is still open; all includes read history."`
	Limit  int    `query:"limit,omitempty" minimum:"1" maximum:"100" description:"Maximum notifications to return. Defaults to 100."`
	Cursor string `query:"cursor,omitempty" description:"Opaque cursor returned by the previous page."`
}

type NotificationStreamQuery struct {
	ProjectID string `query:"projectId,omitempty" description:"Optional project id filter for live notifications."`
}

type NotificationIDParam struct {
	ID string `path:"id" description:"Notification identifier."`
}

type NotificationTarget struct {
	Kind      string `json:"kind" enum:"session,pr"`
	SessionID string `json:"sessionId"`
	PRURL     string `json:"prUrl,omitempty"`
}

type NotificationResponse struct {
	ID         string             `json:"id"`
	SessionID  string             `json:"sessionId"`
	ProjectID  string             `json:"projectId"`
	PRURL      string             `json:"prUrl"`
	Type       string             `json:"type" enum:"needs_input,ready_to_merge,pr_merged,pr_closed_unmerged,review_completed,review_changes_requested"`
	Title      string             `json:"title"`
	Body       string             `json:"body"`
	Status     string             `json:"status" enum:"unread,read" description:"Seen state. unread means the user has not opened the notification panel since it arrived."`
	CreatedAt  time.Time          `json:"createdAt"`
	ResolvedAt *time.Time         `json:"resolvedAt,omitempty"`
	Target     NotificationTarget `json:"target"`
}

type ListNotificationsResponse struct {
	Notifications   []NotificationResponse `json:"notifications"`
	NextCursor      string                 `json:"nextCursor,omitempty"`
	UnreadCount     int                    `json:"unreadCount"`
	UnresolvedCount int                    `json:"unresolvedCount"`
}

type MarkNotificationReadRequest struct {
	Status string `json:"status" enum:"read" description:"V1 supports only marking an unread notification read."`
}

type NotificationEnvelope struct {
	Notification NotificationResponse `json:"notification"`
}

type ShellTerminalHandleIDParam struct {
	HandleID string `path:"handleId" description:"Shell terminal runtime handle identifier."`
}

type OpenShellTerminalRequest struct {
	ProjectID     string `json:"projectId,omitempty" description:"Project whose root the shell starts in. Omitted opens the shell in the daemon data dir."`
	SessionID     string `json:"sessionId,omitempty" description:"Agent session the shell is scoped to, so it appears only in that session's tab strip. Omitted makes it a standalone shell."`
	Shell         string `json:"shell,omitempty" description:"Shell to launch: auto (the user's default shell), an executable path, or on Windows a selector (git-bash, pwsh, powershell, cmd, wsl)."`
	StartOnAttach bool   `json:"startOnAttach,omitempty" description:"Start the shell when the requesting client attaches with its terminal grid instead of immediately, so the shell starts at the size that client shows. Only for clients that attach as a sized viewer; omitted starts the shell immediately at the default grid."`
	Title         string `json:"title,omitempty" description:"Tab title for the new shell, for a client that already shows its tab. Trimmed; omitted or empty numbers it after the existing shells (Terminal N)."`
	Cols          int    `json:"cols,omitempty" description:"Initial terminal columns. With rows, the shell starts immediately at this grid instead of waiting for the client to attach (startOnAttach is then ignored)."`
	Rows          int    `json:"rows,omitempty" description:"Initial terminal rows. See cols."`
}

type UpdateShellTerminalRequest struct {
	Title string `json:"title" description:"New tab title for the shell terminal. Trimmed; must be non-empty."`
}

type ShellTerminalResponse struct {
	HandleID      string    `json:"handleId"`
	ProjectID     string    `json:"projectId,omitempty"`
	SessionID     string    `json:"sessionId,omitempty"`
	WorkingDir    string    `json:"workingDir"`
	Title         string    `json:"title"`
	TerminalTitle string    `json:"terminalTitle,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type ListShellTerminalsResponse struct {
	ShellTerminals []ShellTerminalResponse `json:"shellTerminals"`
}

type ShellTerminalEnvelope struct {
	ShellTerminal ShellTerminalResponse `json:"shellTerminal"`
}

type TerminalShellResponse struct {
	ID   string `json:"id" description:"Value to send as a terminal's shell: auto, a Windows selector, or the executable path."`
	Name string `json:"name" description:"Display name, such as zsh or PowerShell."`
	Path string `json:"path,omitempty" description:"Executable path. Empty when the default shell cannot be resolved."`
}

type TerminalShellsResponse struct {
	Default TerminalShellResponse   `json:"default" description:"The shell auto resolves to."`
	Shells  []TerminalShellResponse `json:"shells" description:"Installed shells, one per real binary."`
}

type TerminalShellCheckResponse struct {
	Executable bool   `json:"executable"`
	Path       string `json:"path,omitempty" description:"Resolved executable path when executable."`
	Message    string `json:"message,omitempty" description:"Why the path cannot launch."`
}

type CueIDParam struct {
	CueID string `path:"cueId" description:"Cue identifier."`
}

type CueProjectIDParam struct {
	ProjectID string `path:"projectId" description:"Project whose cues are listed or extended."`
}

type CueDefinitionRequest struct {
	Name    string `json:"name" maxLength:"64" description:"Short cue name, unique within the project. Trimmed; must be non-empty and at most 64 bytes."`
	Type    string `json:"type" description:"Cue kind: command sends to a project- or session-scoped shell terminal; agent sends an authored prompt. Definition body limit: 128 KiB."`
	Command string `json:"command,omitempty" maxLength:"4096" description:"Shell command for a command cue. At most 4096 bytes; cleared when saving agent cues."`
	Prompt  string `json:"prompt,omitempty" maxLength:"16384" description:"Agent instruction for an agent cue. At most 16384 bytes; cleared when saving command cues."`
}

type CueResponse struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Command   string    `json:"command,omitempty"`
	Prompt    string    `json:"prompt,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ListCuesResponse struct {
	Cues []CueResponse `json:"cues"`
}

type InvokeCueRequest struct {
	SessionID string `json:"sessionId,omitempty" description:"Optional exact session target. Agent cues message it; command cues use its worktree. Omit it to spawn an agent worker or run a command in the project root. A supplied id must be non-blank and compatible, and never falls back to a replacement worker. Invocation body limit: 4 KiB."`
	Shell     string `json:"shell,omitempty" description:"Desktop shell selection used only for command cues."`
}

type InvokeCueResponse struct {
	Kind          string                 `json:"kind" enum:"agent,command" description:"Invocation kind."`
	SessionID     string                 `json:"sessionId,omitempty" description:"For agent cues, the session that received the prompt or newly spawned worker."`
	ShellTerminal *ShellTerminalResponse `json:"shellTerminal,omitempty" description:"For command cues, the normal shell terminal that received the command."`
}

type CueEnvelope struct {
	Cue CueResponse `json:"cue"`
}

type MarkAllNotificationsReadRequest struct {
	IDs []string `json:"ids,omitempty" description:"Acknowledge exactly these notifications. Omit to acknowledge every unread notification; paginating clients should send the ids they actually rendered so later pages stay unread."`
}

type MarkAllNotificationsReadResponse struct {
	Notifications []NotificationResponse `json:"notifications" description:"Deprecated compatibility field. Always empty so mark-all responses stay bounded."`
	UpdatedCount  int64                  `json:"updatedCount" description:"Number of notifications changed from unread to read."`
}

type ClearNotificationsResponse struct {
	ClearedCount  int64  `json:"clearedCount" description:"Number of notifications deleted."`
	ClearID       string `json:"clearId" description:"Identifier shared with the ordered notification_cleared stream event."`
	ClearEpoch    string `json:"clearEpoch" description:"Daemon epoch for ordering notification clears across one daemon lifetime."`
	ClearSequence int64  `json:"clearSequence" description:"Monotonic notification-clear sequence within clearEpoch."`
}

type ImportStatusResponse struct {
	Available  bool   `json:"available"`
	LegacyRoot string `json:"legacyRoot"`
}

type ImportRunResponse struct {
	Report legacyimport.Report `json:"report"`
}

type ListDirsQuery struct {
	Path string `query:"path,omitempty" description:"Absolute directory on the daemon host to list. When omitted, the daemon user's home directory."`
}

type FSEntry struct {
	Name    string `json:"name" description:"Directory name."`
	Path    string `json:"path" description:"Absolute path of the directory on the daemon host."`
	GitRepo bool   `json:"gitRepo" description:"True when the directory carries a .git entry (clone or worktree checkout)."`
}

type ListDirsResponse struct {
	Path      string    `json:"path" description:"Absolute path that was listed."`
	Parent    string    `json:"parent" description:"Absolute path of the listed directory's parent; equals path at the filesystem root."`
	Entries   []FSEntry `json:"entries" description:"Subdirectories, excluding dotted names."`
	Truncated bool      `json:"truncated,omitempty" description:"True when the listing hit the entry cap and more subdirectories exist."`
}

type DevImportProjectsRequest struct {
	SourceDataDir string `json:"sourceDataDir" minLength:"1"`
	DryRun        bool   `json:"dryRun"`
}

type DevImportProjectsResponse struct {
	Report devimport.Report `json:"report"`
}

type PRIDParam struct {
	ID string `path:"id" description:"PR number."`
}

type MergePRRequest struct {
	PRURL           string `json:"prUrl" minLength:"1"`
	ExpectedHeadSHA string `json:"expectedHeadSha" minLength:"40"`
	Method          string `json:"method,omitempty" enum:"squash,merge,rebase"`
	AllowNoChecks   bool   `json:"allowNoChecks,omitempty"`
}

type MergePRResponse struct {
	OK             bool           `json:"ok"`
	PRNumber       int            `json:"prNumber"`
	Method         string         `json:"method"`
	State          domain.PRState `json:"state" enum:"merged"`
	StateChangedAt time.Time      `json:"stateChangedAt"`
}

type ChangePRStateRequest struct {
	PRURL  string `json:"prUrl" minLength:"1"`
	Action string `json:"action" enum:"close,reopen,ready,draft"`
}

type ChangePRStateResponse struct {
	OK             bool           `json:"ok"`
	PRNumber       int            `json:"prNumber"`
	State          domain.PRState `json:"state" enum:"open,draft,closed"`
	StateChangedAt time.Time      `json:"stateChangedAt"`
}

type PRStackLayerResponse struct {
	Number         int              `json:"number"`
	Title          string           `json:"title"`
	URL            string           `json:"url"`
	State          domain.PRState   `json:"state" enum:"draft,open,merged,closed"`
	HeadBranch     string           `json:"headBranch"`
	HeadSHA        string           `json:"headSha"`
	CI             string           `json:"ci,omitempty"`
	ReviewDecision string           `json:"reviewDecision,omitempty"`
	Mergeability   string           `json:"mergeability,omitempty"`
	NoChecks       bool             `json:"noChecks,omitempty"`
	SessionID      domain.SessionID `json:"sessionId,omitempty"`
}

type LoadPRDetailsRequest struct {
	PRURL string `json:"prUrl"`
}

type PRStackResponse struct {
	Number     int                    `json:"number"`
	URL        string                 `json:"url"`
	BaseBranch string                 `json:"baseBranch"`
	Open       bool                   `json:"open"`
	Layers     []PRStackLayerResponse `json:"layers"`
}

type PRStackParentResponse struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

type PRStackQuery struct {
	PRURL string `query:"prUrl" required:"true" description:"The pull request's URL."`
}

type GetPRStackResponse struct {
	Stack  *PRStackResponse       `json:"stack,omitempty"`
	Parent *PRStackParentResponse `json:"parent,omitempty"`
}

type AddPRToStackRequest struct {
	PRURL string `json:"prUrl" minLength:"1"`
}

type PRStackHead struct {
	Number  int    `json:"number"`
	HeadSHA string `json:"headSha" minLength:"40"`
}

type MergePRStackRequest struct {
	PRURL         string        `json:"prUrl" minLength:"1"`
	ExpectedHeads []PRStackHead `json:"expectedHeads" minItems:"1"`
	Method        string        `json:"method,omitempty" enum:"squash,merge,rebase"`
	AllowNoChecks bool          `json:"allowNoChecks,omitempty"`
}

type MergePRStackResponse struct {
	OK             bool      `json:"ok"`
	PRNumber       int       `json:"prNumber"`
	Numbers        []int     `json:"numbers"`
	Method         string    `json:"method"`
	Status         string    `json:"status" enum:"merged,enqueued,pending"`
	StateChangedAt time.Time `json:"stateChangedAt"`
}

type PostPRCommentRequest struct {
	PRURL       string                `json:"prUrl" minLength:"1"`
	Body        string                `json:"body" minLength:"1"`
	ThreadID    string                `json:"threadId,omitempty"`
	Attachments []PRCommentAttachment `json:"attachments,omitempty" maxItems:"10"`
}

type PRCommentAttachment struct {
	Name        string `json:"name" minLength:"1"`
	ContentType string `json:"contentType"`
	Data        string `json:"data" minLength:"1"`
}

type PostPRCommentResponse struct {
	OK        bool      `json:"ok"`
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"createdAt"`
}

type PRAttachmentAccessQuery struct {
	PRURL string `query:"prUrl" required:"true" description:"The pull request's URL."`
}

type PRAttachmentAccessResponse struct {
	Allowed  bool   `json:"allowed"`
	Location string `json:"location,omitempty"`
	Reason   string `json:"reason,omitempty" enum:"no_write_access,unsupported"`
}

type ResolveCommentsRequest struct {
	CommentIDs []string `json:"commentIds,omitempty"`
}

type ResolveCommentsResponse struct {
	OK       bool `json:"ok"`
	Resolved int  `json:"resolved"`
}

type EndpointsResponse struct {
	Endpoints []mobilebridge.Endpoint `json:"endpoints"`
}

type IdentityResponse struct {
	HostID     string `json:"hostId"`
	APIVersion int    `json:"apiVersion"`
}

type TelemetryIdentityResponse struct {
	DistinctID       string `json:"distinctId"`
	InstallID        string `json:"installId,omitempty"`
	CloudUserID      string `json:"cloudUserId,omitempty"`
	IsSignedIn       *bool  `json:"isSignedIn,omitempty"`
	GitHubLogin      string `json:"githubLogin,omitempty"`
	OptedOut         bool   `json:"optedOut"`
	LifetimeSessions *int64 `json:"lifetimeSessions,omitempty"`
}

type LinkPreviewQuery struct {
	URL string `query:"url" description:"Absolute http(s) URL of the page to preview."`
}

type LinkPreviewResponse struct {
	URL            string `json:"url"`
	Title          string `json:"title,omitempty"`
	Description    string `json:"description,omitempty"`
	ImageURL       string `json:"imageUrl,omitempty"`
	SiteName       string `json:"siteName,omitempty"`
	FaviconURL     string `json:"faviconUrl,omitempty"`
	FaviconDarkURL string `json:"faviconDarkUrl,omitempty"`
}

type MobileStatusResponse struct {
	Enabled                  bool                      `json:"enabled"`
	AccountLinked            bool                      `json:"accountLinked"`
	AccountLinkRefused       bool                      `json:"accountLinkRefused"`
	BrowserRemoteAccess      bool                      `json:"browserRemoteAccess"`
	LoopbackOnly             bool                      `json:"loopbackOnly"`
	NoPublicTunnel           bool                      `json:"noPublicTunnel"`
	Endpoints                []mobilebridge.Endpoint   `json:"endpoints"`
	HostID                   string                    `json:"hostId"`
	Tunnel                   mobilebridge.TunnelStatus `json:"tunnel"`
	Host                     string                    `json:"host"`
	TailscaleHost            string                    `json:"tailscaleHost"`
	Port                     int                       `json:"port"`
	Password                 string                    `json:"password"`
	Warning                  string                    `json:"warning"`
	SecurePairing            SecurePairingStatus       `json:"securePairing"`
	KeepAwake                KeepAwakeStatus           `json:"keepAwake"`
	RequireDeviceCredentials bool                      `json:"requireDeviceCredentials"`
	PasswordLastUsedAt       *time.Time                `json:"passwordLastUsedAt,omitempty"`
}

type PairingCodeResponse struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type PairDeviceRequest struct {
	Name     string `json:"name,omitempty" description:"Name shown in the host's device list."`
	Platform string `json:"platform,omitempty" description:"The device's platform, such as ios, android, darwin, win32 or linux."`
}

type PairDeviceResponse struct {
	HostID     string `json:"hostId"`
	DeviceID   string `json:"deviceId"`
	Token      string `json:"token"`
	Credential string `json:"credential" enum:"pairing-code,password"`
}

type SetRequireDeviceCredentialsRequest struct {
	Enabled bool `json:"enabled"`
}

type KeepAwakeStatus struct {
	Supported    bool   `json:"supported"`
	Enabled      bool   `json:"enabled"`
	Active       bool   `json:"active"`
	HasBattery   bool   `json:"hasBattery"`
	LidClosed    bool   `json:"lidClosed,omitempty"`
	LidSupported bool   `json:"lidSupported,omitempty"`
	LidActive    bool   `json:"lidActive,omitempty"`
	LidReason    string `json:"lidReason,omitempty"`
}

type SetKeepAwakeRequest struct {
	Enabled   bool  `json:"enabled"`
	LidClosed *bool `json:"lidClosed,omitempty"`
}

type SetBrowserRemoteAccessRequest struct {
	Enabled bool `json:"enabled"`
}

type SecurePairingStatus struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Active    bool   `json:"active"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Reason    string `json:"reason"`
}

type SetSecurePairingRequest struct {
	Enabled bool `json:"enabled"`
}

type PushPairingIDParam struct {
	ID string `path:"id" description:"The phone's install id, or its push token for older builds."`
}

type PushDeviceTokenParam struct {
	Token string `path:"token" description:"Expo push token (URL-encoded) identifying the device."`
}

type RegisterPushDeviceRequest struct {
	InstallID  string `json:"installId,omitempty" description:"Stable per-install device id, keying the registry so a rotated push token updates the same row. Optional: older app builds omit it and the daemon synthesizes one."`
	Token      string `json:"token,omitempty" description:"Expo push token, e.g. ExponentPushToken[...]. Optional: omitted when the phone has no push token yet."`
	Platform   string `json:"platform,omitempty" enum:"ios,android" description:"Device platform."`
	DeviceName string `json:"deviceName,omitempty" description:"Human-friendly device label."`
	HostName   string `json:"hostName,omitempty" description:"This phone's label for the host; used in OS push notification titles."`
}

type PushDeviceResponse struct {
	Token      string    `json:"token,omitempty"`
	Platform   string    `json:"platform,omitempty"`
	DeviceName string    `json:"deviceName,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type PushDeviceEnvelope struct {
	Device PushDeviceResponse `json:"device"`
}

type UnregisterPushDeviceResponse struct {
	Token   string `json:"token"`
	Deleted bool   `json:"deleted"`
}

type ConversationPromptSuggestionRequest struct {
	TurnID string `json:"turnId"`
}

type ConversationPromptSuggestionResponse struct {
	TurnID string `json:"turnId"`
	Prompt string `json:"prompt"`
}

type SetChatViewRequest struct {
	ViewID        string `json:"viewId"`
	Active        bool   `json:"active"`
	activePresent bool
}

func (r *SetChatViewRequest) UnmarshalJSON(data []byte) error {
	var wire struct {
		ViewID string `json:"viewId"`
		Active *bool  `json:"active"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	r.ViewID = wire.ViewID
	r.Active = wire.Active != nil && *wire.Active
	r.activePresent = wire.Active != nil
	return nil
}

type SendConversationMessageRequest struct {
	Text            string                                `json:"text"`
	ClientMessageID string                                `json:"clientMessageId,omitempty"`
	Attachments     []ConversationImageContentRequest     `json:"attachments,omitempty"`
	Resources       []ConversationResourceContentRequest  `json:"resources,omitempty"`
	Excerpts        []ConversationExcerptReferenceRequest `json:"excerpts,omitempty"`
	FileReferences  []ConversationFileReferenceRequest    `json:"fileReferences,omitempty"`
	Continuation    bool                                  `json:"continuation,omitempty"`
}

type ResolveFileReferenceResponse sessionsvc.FileReferenceResolution

type ConversationFileReferenceRequest ports.ChatFileReference

type ConversationExcerptReferenceRequest struct {
	Snapshot       *domain.ChatExcerptSnapshot `json:"snapshot,omitempty"`
	ID             string                      `json:"id,omitempty"`
	Order          int64                       `json:"order,omitempty"`
	ConversationID string                      `json:"conversationId"`
	MessageID      string                      `json:"messageId"`
	Revision       int64                       `json:"revision"`
	Text           string                      `json:"text"`
	Comment        string                      `json:"comment,omitempty" maxLength:"4096"`
}

type ProviderSwitchRequest struct {
	TargetHarness     string                          `json:"targetHarness"`
	IdempotencyKey    string                          `json:"idempotencyKey,omitempty"`
	Model             string                          `json:"model,omitempty" maxLength:"200"`
	Effort            string                          `json:"effort,omitempty" maxLength:"64"`
	FastMode          bool                            `json:"fastMode,omitempty"`
	Ultracode         bool                            `json:"ultracode,omitempty"`
	AutoCompactWindow string                          `json:"autoCompactWindow,omitempty" enum:"200k,1m"`
	PermissionMode    string                          `json:"permissionMode,omitempty" enum:"default,accept-edits,auto,bypass-permissions"`
	Message           *SendConversationMessageRequest `json:"message,omitempty"`
}

type ProviderSwitchResponse struct {
	Status  string                           `json:"status"`
	Message *SendConversationMessageResponse `json:"message,omitempty"`
}

type SessionHistoryQuery struct {
	Offset int `query:"offset,omitempty" minimum:"0" description:"Number of newest turns to skip."`
	Limit  int `query:"limit,omitempty" minimum:"1" maximum:"50" description:"Maximum turns to return. Defaults to 10."`
}

type SessionHistoryResponse struct {
	Turns  []domain.HandoffTurn `json:"turns"`
	Total  int                  `json:"total"`
	Offset int                  `json:"offset"`
}

type ConversationImageContentRequest struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type EditQueuedConversationMessageRequest struct {
	ClientMessageID  string                            `json:"clientMessageId,omitempty"`
	Text             string                            `json:"text"`
	Attachments      []ConversationImageContentRequest `json:"attachments,omitempty"`
	RetainedContent  *[]int                            `json:"retainedContent,omitempty"`
	ExpectedRevision *int64                            `json:"expectedRevision,omitempty"`
}

type ConversationResourceContentRequest struct {
	URI      string  `json:"uri"`
	Name     string  `json:"name"`
	MIMEType string  `json:"mimeType,omitempty"`
	Text     *string `json:"text,omitempty"`
}

type SendConversationMessageResponse struct {
	TurnID         string           `json:"turnId,omitempty"`
	ProviderTurnID string           `json:"providerTurnId,omitempty"`
	State          domain.TurnState `json:"state,omitempty" enum:"queued,running,completed,recovered,interrupted,failed"`
	Duplicate      bool             `json:"duplicate"`
}

type SteerConversationRequest struct {
	Text            string                            `json:"text"`
	SenderSessionID string                            `json:"senderSessionId,omitempty"`
	Attachments     []ConversationImageContentRequest `json:"attachments,omitempty"`
	ClientMessageID string                            `json:"clientMessageId,omitempty"`
	RecoverOnly     bool                              `json:"recoverOnly,omitempty"`
}

type SteerConversationResponse struct {
	ProviderTurnID string `json:"providerTurnId"`
	ActivityID     string `json:"activityId,omitempty"`
}

type SteerOrSendConversationResponse struct {
	Outcome        string           `json:"outcome" enum:"steered,sent"`
	TurnID         string           `json:"turnId,omitempty"`
	ProviderTurnID string           `json:"providerTurnId,omitempty"`
	ActivityID     string           `json:"activityId,omitempty"`
	State          domain.TurnState `json:"state,omitempty" enum:"queued,running,completed,recovered,interrupted,failed"`
	Duplicate      bool             `json:"duplicate"`
}

type EditConversationMessageRequest struct {
	Text            string `json:"text"`
	ClientMessageID string `json:"clientMessageId,omitempty"`
}

type ConversationContentSummaryResponse struct {
	Snapshot             *domain.ChatExcerptSnapshot       `json:"snapshot,omitempty"`
	FileReference        *ConversationFileReferenceRequest `json:"fileReference,omitempty"`
	AnnotationID         string                            `json:"annotationId,omitempty"`
	AnnotationOrder      int64                             `json:"annotationOrder,omitempty"`
	Type                 string                            `json:"type"`
	MIMEType             string                            `json:"mimeType,omitempty"`
	URI                  string                            `json:"uri,omitempty"`
	Name                 string                            `json:"name,omitempty"`
	Text                 string                            `json:"text,omitempty"`
	Comment              string                            `json:"comment,omitempty"`
	SourceConversationID string                            `json:"sourceConversationId,omitempty"`
	SourceMessageID      string                            `json:"sourceMessageId,omitempty"`
	SourceRevision       *int64                            `json:"sourceRevision,omitempty"`
}

type EditConversationMessageResponse struct {
	SourceBranchID string           `json:"sourceBranchId"`
	ActiveBranchID string           `json:"activeBranchId"`
	TurnID         string           `json:"turnId,omitempty"`
	ProviderTurnID string           `json:"providerTurnId,omitempty"`
	State          domain.TurnState `json:"state,omitempty" enum:"queued,running,completed,recovered,interrupted,failed"`
}

type RetryTurnResponse struct {
	TurnID         string           `json:"turnId,omitempty"`
	ProviderTurnID string           `json:"providerTurnId,omitempty"`
	State          domain.TurnState `json:"state,omitempty" enum:"queued,running,completed,recovered,interrupted,failed"`
}

type SetTurnAutoResumeRequest struct {
	Enabled *bool `json:"enabled"`
}

type ActivateConversationBranchResponse struct {
	ActiveBranchID string `json:"activeBranchId"`
}

type ConversationModelsResponse struct {
	Models   []ConversationModelResponse     `json:"models"`
	Selected ConversationTurnSettingsPayload `json:"selected"`
}

type ConversationConfigOptionsResponse struct {
	Options []ConversationConfigOptionResponse `json:"options"`
}

type ConversationConfigOptionResponse struct {
	ID             string                             `json:"id"`
	Name           string                             `json:"name"`
	Description    string                             `json:"description,omitempty"`
	Category       string                             `json:"category,omitempty"`
	Type           string                             `json:"type" enum:"select,boolean"`
	CurrentValue   string                             `json:"currentValue,omitempty"`
	CurrentBoolean *bool                              `json:"currentBoolean,omitempty"`
	Choices        []ConversationConfigChoiceResponse `json:"choices,omitempty"`
}

type ConversationConfigChoiceResponse struct {
	PermissionMode domain.PermissionMode `json:"permissionMode,omitempty" enum:"default,accept-edits,auto,bypass-permissions"`
	Value          string                `json:"value"`
	Name           string                `json:"name"`
	Description    string                `json:"description,omitempty"`
	Group          string                `json:"group,omitempty"`
	GroupName      string                `json:"groupName,omitempty"`
}

type SetConversationConfigOptionRequest struct {
	Value   string `json:"value,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type ConversationModelResponse struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"displayName"`
	Description   string   `json:"description,omitempty"`
	Default       bool     `json:"default"`
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
	SupportsFast  bool     `json:"supportsFast,omitempty"`
}

type ConversationSkillsResponse struct {
	Skills []ConversationSkillResponse `json:"skills"`
}

type ConversationSkillResponse struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
	InputHint   string `json:"inputHint,omitempty"`
	Source      string `json:"source,omitempty"`
}

type ConversationTurnSettingsPayload struct {
	Model             string `json:"model,omitempty"`
	ReasoningEffort   string `json:"reasoningEffort,omitempty"`
	ApprovalMode      string `json:"approvalMode,omitempty" enum:"default,accept-edits,auto,bypass-permissions"`
	FastMode          bool   `json:"fastMode,omitempty"`
	PlanMode          bool   `json:"planMode,omitempty"`
	AutoCompactWindow string `json:"autoCompactWindow,omitempty" enum:"200k,1m"`
}

type ResolveConversationApprovalRequest struct {
	DecisionID string `json:"decisionId"`
}

type ResolveConversationInputRequest struct {
	Action  string         `json:"action" enum:"accept,decline,cancel"`
	Content map[string]any `json:"content,omitempty"`
}

type CompactConversationResponse struct {
	TokensBefore int64 `json:"tokensBefore,omitempty"`
	TokensAfter  int64 `json:"tokensAfter,omitempty"`
}

type ConversationTurnResponse struct {
	ID              string                             `json:"id"`
	State           string                             `json:"state" enum:"queued,running,completed,recovered,interrupted,failed,cancelled"`
	ProviderTurnID  string                             `json:"providerTurnId,omitempty"`
	RetryOfTurnID   string                             `json:"retryOfTurnId,omitempty"`
	HasRetryAttempt bool                               `json:"hasRetryAttempt,omitempty"`
	HasHumanPrompt  bool                               `json:"hasHumanPrompt,omitempty"`
	ErrorMessage    string                             `json:"errorMessage,omitempty"`
	FailureCode     string                             `json:"failureCode,omitempty" enum:"provider_key_missing,auth_required"`
	UsageLimit      *ConversationUsageLimitResponse    `json:"usageLimit,omitempty"`
	RequestedAt     string                             `json:"requestedAt"`
	StartedAt       *string                            `json:"startedAt,omitempty"`
	CompletedAt     *string                            `json:"completedAt,omitempty"`
	RolledBack      bool                               `json:"rolledBack,omitempty"`
	Diff            *ConversationTurnDiffResponse      `json:"diff,omitempty"`
	Artifacts       *ConversationTurnArtifactsResponse `json:"artifacts,omitempty"`
	Plan            *ConversationPlanResponse          `json:"plan,omitempty"`
}

type ConversationUsageLimitResponse struct {
	ResetsAt    *string `json:"resetsAt,omitempty"`
	AutoResume  bool    `json:"autoResume"`
	ResumeError string  `json:"resumeError,omitempty"`
}

type ConversationPlanResponse struct {
	Explanation string                         `json:"explanation,omitempty"`
	Steps       []ConversationPlanStepResponse `json:"steps"`
	SpansTurns  bool                           `json:"spansTurns,omitempty"`
	Superseded  bool                           `json:"superseded,omitempty"`
}

type ConversationPlanStepResponse struct {
	Text   string `json:"text"`
	Status string `json:"status" enum:"pending,in_progress,completed"`
}

type ConversationTurnArtifactsResponse struct {
	Files []ConversationTurnArtifactResponse `json:"files"`
}

type ConversationTurnArtifactResponse struct {
	Path   string `json:"path"`
	Status string `json:"status" enum:"added,modified"`
}

type ConversationTurnDiffResponse struct {
	Files     []ConversationDiffFileResponse `json:"files"`
	Truncated bool                           `json:"truncated,omitempty"`
}

type ConversationDiffFileResponse struct {
	Path       string `json:"path"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	Status     string `json:"status" enum:"added,modified,deleted,renamed"`
	OldPath    string `json:"oldPath,omitempty"`
	RolledBack bool   `json:"rolledBack,omitempty"`
}

type ConversationMessageResponse struct {
	Kind              string                               `json:"kind" enum:"message"`
	ID                string                               `json:"id"`
	TurnID            string                               `json:"turnId,omitempty"`
	Sequence          int64                                `json:"sequence"`
	Revision          int64                                `json:"revision"`
	Role              string                               `json:"role" enum:"user,assistant"`
	Origin            string                               `json:"origin" enum:"human,automation,daemon,provider"`
	Text              string                               `json:"text"`
	Content           []ConversationContentSummaryResponse `json:"content,omitempty"`
	SenderSessionID   string                               `json:"senderSessionId,omitempty"`
	SenderProjectID   string                               `json:"senderProjectId,omitempty"`
	SenderDisplayName string                               `json:"senderDisplayName,omitempty"`
	ClientMessageID   string                               `json:"clientMessageId,omitempty"`
	EditAvailable     bool                                 `json:"editAvailable"`
	Streaming         bool                                 `json:"streaming"`
	Continuation      bool                                 `json:"continuation,omitempty"`
	AuthoredByUser    bool                                 `json:"authoredByUser,omitempty"`
	CreatedAt         string                               `json:"createdAt"`
}

type ConversationActivityResponse struct {
	Kind           string         `json:"kind" enum:"activity"`
	ID             string         `json:"id"`
	TurnID         string         `json:"turnId,omitempty"`
	Sequence       int64          `json:"sequence"`
	Revision       int64          `json:"revision"`
	ActivityKind   string         `json:"activityKind" enum:"command,file_change,plan,reasoning,approval,usage,error,system,mcp_tool,auto_review,user_input,ao_action"`
	Status         string         `json:"status" enum:"running,completed,recovered,failed,cancelled,pending,resolved"`
	Summary        string         `json:"summary"`
	Detail         map[string]any `json:"detail,omitempty"`
	RequestID      string         `json:"requestId,omitempty"`
	ProviderItemID string         `json:"providerItemId,omitempty"`
	CreatedAt      string         `json:"createdAt"`
}

type ConversationSnapshotResponse struct {
	ContinueTurnID                   string                                     `json:"continueTurnId"`
	SupportsExcerpts                 bool                                       `json:"supportsExcerpts,omitempty"`
	SupportsFileReferences           bool                                       `json:"supportsFileReferences,omitempty"`
	ConversationID                   string                                     `json:"conversationId"`
	ActiveBranchID                   string                                     `json:"activeBranchId,omitempty"`
	BranchedFromEarlierMessage       bool                                       `json:"branchedFromEarlierMessage"`
	BatchedDeliveries                []domain.ConversationBatchedDelivery       `json:"batchedDeliveries,omitempty"`
	SessionID                        string                                     `json:"sessionId"`
	Harness                          string                                     `json:"harness,omitempty"`
	Mode                             string                                     `json:"mode" enum:"chat,tui"`
	Controller                       string                                     `json:"controller" enum:"connecting,ready,busy,recovering,hibernated,stopped"`
	LatestSequence                   int64                                      `json:"latestSequence"`
	OldestSequence                   int64                                      `json:"oldestSequence,omitempty"`
	HasMoreBefore                    bool                                       `json:"hasMoreBefore"`
	AfterSequence                    int64                                      `json:"afterSequence,omitempty"`
	NativeForkAvailableAfterSequence int64                                      `json:"nativeForkAvailableAfterSequence"`
	Turns                            []ConversationTurnResponse                 `json:"turns"`
	Messages                         []ConversationMessageResponse              `json:"messages"`
	Activities                       []ConversationActivityResponse             `json:"activities"`
	BranchPoints                     []ConversationBranchPointResponse          `json:"branchPoints,omitempty"`
	BranchMaterialization            *ConversationBranchMaterializationResponse `json:"branchMaterialization,omitempty"`
	Settings                         ConversationTurnSettingsPayload            `json:"settings"`
	Title                            string                                     `json:"title,omitempty"`
	Usage                            *ConversationUsagePayload                  `json:"usage,omitempty"`
	RateLimits                       *ConversationRateLimitsPayload             `json:"rateLimits,omitempty"`
	CompactedAt                      *string                                    `json:"compactedAt,omitempty"`
	ModelReroute                     *ConversationModelReroutePayload           `json:"modelReroute,omitempty"`
	Account                          *ConversationAccountPayload                `json:"account,omitempty"`
	ThreadState                      *ConversationThreadStatePayload            `json:"threadState,omitempty"`
	MCPServers                       []ConversationMCPServerPayload             `json:"mcpServers,omitempty"`
	Capabilities                     []string                                   `json:"capabilities,omitempty"`
}

type ConversationBranchMaterializationResponse struct {
	Strategy        string `json:"strategy" enum:"native,approximate_context"`
	ReplayTruncated bool   `json:"replayTruncated"`
}

type ConversationBranchPointResponse struct {
	TurnID           string `json:"turnId"`
	Position         int    `json:"position"`
	Total            int    `json:"total"`
	PreviousBranchID string `json:"previousBranchId,omitempty"`
	NextBranchID     string `json:"nextBranchId,omitempty"`
}

type ConversationModelReroutePayload struct {
	FromModel      string `json:"fromModel,omitempty"`
	ToModel        string `json:"toModel"`
	Reason         string `json:"reason,omitempty"`
	ProviderTurnID string `json:"providerTurnId,omitempty"`
	At             string `json:"at"`
}

type ConversationAccountPayload struct {
	AuthenticationState   string  `json:"authenticationState,omitempty" enum:"unknown,required,authenticated"`
	AuthVerifiedAt        *string `json:"authVerifiedAt,omitempty"`
	LastAuthFailureAt     *string `json:"lastAuthFailureAt,omitempty"`
	LastAuthFailureReason string  `json:"lastAuthFailureReason,omitempty"`
	AuthFailureID         string  `json:"authFailureId,omitempty"`
	AuthMode              string  `json:"authMode,omitempty"`
	PlanLabel             string  `json:"planLabel,omitempty"`
	ReauthRequiredAt      *string `json:"reauthRequiredAt,omitempty"`
	ReauthReason          string  `json:"reauthReason,omitempty"`
}

type ConversationThreadStatePayload struct {
	Status     string   `json:"status,omitempty" enum:"active,idle,not_loaded,system_error,closed"`
	WaitingOn  []string `json:"waitingOn,omitempty"`
	ArchivedAt *string  `json:"archivedAt,omitempty"`
	ClosedAt   *string  `json:"closedAt,omitempty"`
}

type ConversationMCPServerPayload struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	FailureReason string `json:"failureReason,omitempty"`
}

type ReloadConversationMCPServersResponse struct {
	Servers []ConversationMCPServerPayload `json:"servers"`
}

type ConversationUsagePayload struct {
	ContextUsed   int64    `json:"contextUsed"`
	ContextWindow int64    `json:"contextWindow"`
	InputTokens   int64    `json:"inputTokens"`
	OutputTokens  int64    `json:"outputTokens"`
	CachedTokens  int64    `json:"cachedTokens"`
	TotalTokens   int64    `json:"totalTokens"`
	Cost          *float64 `json:"cost,omitempty"`
	Currency      string   `json:"currency,omitempty"`
}

type ConversationRateLimitsPayload struct {
	PrimaryUsedPercent       float64 `json:"primaryUsedPercent"`
	SecondaryUsedPercent     float64 `json:"secondaryUsedPercent"`
	PrimaryResetsInSeconds   int64   `json:"primaryResetsInSeconds,omitempty"`
	SecondaryResetsInSeconds int64   `json:"secondaryResetsInSeconds,omitempty"`
	PlanLabel                string  `json:"planLabel,omitempty"`
	Title                    string  `json:"title,omitempty"`
}

type ConversationRequestIDParam struct {
	RequestID string `path:"requestId" description:"Provider approval request identifier. Zero is a legitimate value."`
}

type ConversationConfigIDParam struct {
	ConfigID string `path:"configId" description:"Provider session configuration option identifier."`
}

type ConversationTurnIDParam struct {
	TurnID string `path:"turnId" description:"AO conversation turn identifier, from the snapshot's turns array."`
}

type PublishRenderRequest struct {
	HTML     string `json:"html" description:"A complete, self-contained HTML document, at most 25 MiB."`
	Title    string `json:"title" description:"Short name for the page."`
	Height   int    `json:"height,omitempty" description:"First-paint frame height in CSS pixels, clamped to 80-2000; the frame then fits the page."`
	Artifact bool   `json:"artifact,omitempty" description:"Also keep the page as a session artifact, a deliverable the user keeps."`
}

type PublishRenderResponse struct {
	RenderID      string `json:"renderId"`
	ActivityID    string `json:"activityId"`
	Path          string `json:"path"`
	ArtifactPath  string `json:"artifactPath,omitempty" description:"With artifact: the absolute path of the kept page."`
	ArtifactError string `json:"artifactError,omitempty" description:"With artifact: why the page was not kept. The page is still published."`
}

type SaveRenderArtifactRequest struct {
	Title string `json:"title" minLength:"1" maxLength:"200" description:"Name for the file. Characters a file system refuses are replaced."`
}

type SaveRenderArtifactResponse struct {
	Path string `json:"path" description:"The file, relative to the session's artifact directory."`
	Name string `json:"name"`
}

type RenderCheckRequest struct {
	HTML  string `json:"html" description:"A complete, self-contained HTML document, at most 25 MiB."`
	Width int    `json:"width,omitempty" description:"Viewport width in CSS pixels, 240-1600. Defaults to 720."`
}

type RenderCheckScreenshot struct {
	MimeType    string `json:"mimeType"`
	Data        string `json:"data" description:"Base64 PNG."`
	Width       int    `json:"width" description:"Page width the screenshot shows, in CSS pixels."`
	Height      int    `json:"height" description:"Page height the screenshot shows, in CSS pixels."`
	ImageWidth  int    `json:"imageWidth,omitempty" description:"PNG width in pixels, when it differs from the page width."`
	ImageHeight int    `json:"imageHeight,omitempty" description:"PNG height in pixels, when it differs from the page height."`
}

type RenderConsoleMessage struct {
	Level string `json:"level" enum:"debug,log,warning,error"`
	Text  string `json:"text"`
}

type RenderCheckResponse struct {
	Screenshot      RenderCheckScreenshot  `json:"screenshot"`
	ContentHeight   int                    `json:"contentHeight" description:"Height the page needs at this width, in CSS pixels."`
	ConsoleMessages []RenderConsoleMessage `json:"consoleMessages"`
	Network         string                 `json:"network" enum:"public,none" description:"Network the page could use. none when the agent's own sandbox has no network, so remote resources did not load."`
}

type RenderIDParam struct {
	RenderID string `path:"renderId" description:"Render identifier returned when the page was published."`
}

type ConversationBranchIDParam struct {
	BranchID string `path:"branchId" description:"Conversation branch identifier, from a snapshot branch navigation point."`
}

type RollbackConversationRequest struct {
	RestoreFiles bool   `json:"restoreFiles,omitempty"`
	PlanID       string `json:"planId,omitempty"`
}

type RollbackPreviewResponse struct {
	FilesRestorable bool                 `json:"filesRestorable"`
	Reason          string               `json:"reason,omitempty"`
	Files           []RollbackFileChange `json:"files"`
	TotalFiles      int                  `json:"totalFiles"`
	CommitsSince    int                  `json:"commitsSince"`
	PlanID          string               `json:"planId,omitempty"`
}

type RollbackFileChange struct {
	Path   string `json:"path"`
	Change string `json:"change" enum:"restored,reverted,removed"`
}

type RollbackConversationResponse struct {
	TurnsDiscarded int `json:"turnsDiscarded"`
}

type SetConversationTitleRequest struct {
	Title string `json:"title"`
}

type SetConversationTitleResponse struct {
	Title string `json:"title"`
}

type SettingsResponse struct {
	HarnessDefaults        map[string]domain.HarnessDefault `json:"harnessDefaults,omitempty"`
	DefaultSessionMode     string                           `json:"defaultSessionMode" enum:"chat,tui"`
	ChatHarnesses          []string                         `json:"chatHarnesses"`
	Client                 string                           `json:"client"`
	LocalEnabled           bool                             `json:"localEnabled"`
	ChatHibernationEnabled bool                             `json:"chatHibernationEnabled"`
	VersionRefresh         bool                             `json:"versionRefresh" description:"After a harness update, idle open sessions restart on the new release instead of running the old one until they happen to restart."`
	ComputerUse            bool                             `json:"computerUse" description:"New chat sessions get tools to drive desktop apps through Cua Driver (Settings, Computer use agents)."`
	AccountHandoff         bool                             `json:"accountHandoff" description:"A Chat turn refused for a usage limit moves its session to another signed-in account of the same agent with room, and the prompt is retried there once (Settings, Agent accounts)."`
	SessionRetentionDays   int                              `json:"sessionRetentionDays" description:"Days an archived session whose worktree is released is kept before the daemon deletes it permanently; 0 keeps it."`
	CloudEnabled           bool                             `json:"cloudEnabled"`
	CloudControlPlaneURL   string                           `json:"cloudControlPlaneUrl"`
	TrackerIntakeEnabled   bool                             `json:"trackerIntakeEnabled"`
}

type AgentInstallerCatalogResponse struct {
	Agents []systeminstall.AgentPlan `json:"agents"`
}

type UpdateSessionInterfaceRequest struct {
	DefaultSessionMode string `json:"defaultSessionMode" enum:"chat,tui"`
}

type UpdateHarnessDefaultRequest struct {
	DisabledModels *[]string `json:"disabledModels" required:"true" maxItems:"20000"`
}

type UpdateVersionRefreshRequest struct {
	Enabled *bool `json:"enabled"`
}

type UpdateSessionRetentionRequest struct {
	Days *int `json:"days" description:"Days to keep an archived session whose worktree is released; 0 keeps it." minimum:"0" maximum:"3650"`
}

type UpdateComputerUseRequest struct {
	Enabled *bool `json:"enabled"`
}

type UpdateAccountHandoffRequest struct {
	Enabled *bool `json:"enabled"`
}

type UpdateChatHibernationRequest struct {
	Enabled *bool `json:"enabled"`
}

func capabilityNames(caps ports.ChatCapabilities) []string {
	if len(caps) == 0 {
		return nil
	}
	names := make([]string, 0, len(caps))
	for name, has := range caps {
		if has {
			names = append(names, string(name))
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	return names
}

type TriggerReviewRequest struct {
	Harness            domain.ReviewerHarness       `json:"harness,omitempty" enum:"claude-code,codex,copilot,cursor,kilocode,opencode,opencode-v2,kiro,pi,agy,devin,droid,kimi,kimchi,muse,amp,aider,grok,crush,auggie,cline,autohand"`
	AgentConfig        domain.AgentConfig           `json:"agentConfig,omitempty"`
	InterfaceMode      domain.ReviewerInterfaceMode `json:"interfaceMode,omitempty" enum:"chat,tui"`
	PRURL              string                       `json:"prUrl,omitempty" description:"Restrict the pass to this pull request, attaching it to the session first if AO does not track it yet (never from another active session). Omit to review every eligible PR on the session."`
	Source             string                       `json:"source,omitempty" enum:"manual,agent" description:"Who requested the pass: manual (a person, the default) or agent (an AO session through the CLI)."`
	RejectReviewedHead bool                         `json:"rejectReviewedHead,omitempty" description:"Return 409 instead of reusing when every open PR head is already being reviewed or already has a review."`
	Rerun              bool                         `json:"rerun,omitempty" description:"Start a fresh pass for already-reviewed current heads; a different reviewer may run alongside one that is still running. A person's rerun reuses an active pass from the same reviewer; an agent's rerun returns 409 REVIEW_ALREADY_RUNNING instead."`
	EnableAutoInject   bool                         `json:"enableAutoInject,omitempty" description:"Turn on the worker session's review auto-inject once a pass has started, so the reviewer's PR review comments reach the worker."`
}

type ResolveReviewCommentRequest struct {
	PullRequestURL string `json:"pullRequestUrl,omitempty" description:"Tracked pull request URL. Required when the session has multiple PRs."`
	CommentURL     string `json:"commentUrl" description:"Provider URL of the unresolved review comment to resolve."`
}

type ResolveReviewCommentResponse struct {
	OK bool `json:"ok"`
}

type RequestRereviewRequest struct {
	PullRequestURL string `json:"pullRequestUrl,omitempty" description:"Tracked pull request URL. Required when the session has multiple PRs."`
	ReviewerID     string `json:"reviewerId" description:"Provider login of the reviewer to ask for another review."`
}

type RequestRereviewResponse struct {
	OK bool `json:"ok"`
}

type MobileDeviceResponse struct {
	ID                   string    `json:"id" description:"What DELETE /api/v1/mobile/devices/{id} takes."`
	Kind                 string    `json:"kind" enum:"device,password,account" description:"device: holds its own token. password: a phone that still uses the pairing password, so removing it only stops its notifications. account: every device signed in to the linked account; they share one token."`
	InstallID            string    `json:"installId" description:"The phone's install id, or empty for a device that never registered for notifications."`
	Token                string    `json:"token,omitempty"`
	Platform             string    `json:"platform,omitempty"`
	DeviceName           string    `json:"deviceName,omitempty"`
	Live                 bool      `json:"live" description:"True when the device is connected, or the phone's app is open and polling."`
	NotificationsEnabled bool      `json:"notificationsEnabled" description:"True when this device has a push token registered."`
	CreatedAt            time.Time `json:"createdAt"`
	LastSeenAt           time.Time `json:"lastSeenAt"`
}

type MobileDevicesResponse struct {
	Devices []MobileDeviceResponse `json:"devices"`
}

type MobileDeviceIDParam struct {
	ID string `path:"id" description:"The row's id from the device list."`
}

type CreateConversationSideChatRequest struct {
	ForceNew       bool                                 `json:"forceNew,omitempty"`
	Label          string                               `json:"label,omitempty" maxLength:"80"`
	IdempotencyKey string                               `json:"idempotencyKey"`
	Reference      *ConversationExcerptReferenceRequest `json:"reference,omitempty"`
}

type CreateConversationSideChatResponse struct {
	Side domain.SideConversation `json:"side"`
}

type SendSideQuestionRequest struct {
	Text            string                                `json:"text"`
	ClientMessageID string                                `json:"clientMessageId"`
	References      []ConversationExcerptReferenceRequest `json:"references,omitempty"`
	Attachments     []ConversationImageContentRequest     `json:"attachments,omitempty"`
	Resources       []ConversationResourceContentRequest  `json:"resources,omitempty"`
}

type SideQuestionResponse struct {
	Turn domain.SideTurn `json:"turn"`
}

type SideChatSnapshotResponse struct {
	Snapshot domain.SideSnapshot `json:"snapshot"`
}

type SideChatListResponse struct {
	Sides []domain.SideConversation `json:"sides"`
}

type SideSettingsRequest struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

type SideDraftRequest struct {
	ContentJSON string `json:"contentJson"`
}

type SideDraftResponse struct {
	ContentJSON string `json:"contentJson"`
}

type SideChatIDParam struct {
	SideID string `path:"sideId" description:"Independent side chat identifier."`
}

type SideChatPageQuery struct {
	Before string `query:"before,omitempty" description:"Read side turns older than this RFC3339 timestamp."`
	Limit  *int   `query:"limit,omitempty" minimum:"1" maximum:"500"`
}

type SideChatLaunchClaimRequest struct {
	AppRunID string `json:"appRunId"`
}

type SideChatLaunchState struct {
	Numbers  map[domain.SessionID]int     `json:"numbers,omitempty"`
	AppRunID string                       `json:"appRunId"`
	Sides    []chatsvc.SideRecoveryRecord `json:"sides"`
}

type SideLabelRequest struct {
	Label string `json:"label"`
}

type AgentAccountIDParam struct {
	AccountID string `path:"accountId" description:"Agent account identifier; device-claude and device-codex are this computer's own logins."`
}

type AgentAccountLoginIDParam struct {
	OperationID string `path:"operationId" description:"Agent account sign-in operation identifier."`
}

type AgentAccountSessionIDParam struct {
	SessionID string `path:"sessionId" description:"AO session identifier."`
}

type AgentAccountListQuery struct {
	Refresh bool `query:"refresh" description:"Read identity and limits again even when the last reading is fresh."`
}

type AgentAccountUsageQuery struct {
	Days int `query:"days" minimum:"1" maximum:"90" description:"Days of usage to sum, today included. Defaults to 30."`
}

type AgentAccountResponse struct {
	ID          string                         `json:"id"`
	Provider    domain.AgentAccountProvider    `json:"provider" enum:"claude,codex"`
	Device      bool                           `json:"device"`
	Email       string                         `json:"email,omitempty"`
	Plan        string                         `json:"plan,omitempty"`
	Default     bool                           `json:"default"`
	SignedIn    bool                           `json:"signedIn"`
	LoggedIn    bool                           `json:"loggedIn"`
	CreatedAt   *time.Time                     `json:"createdAt,omitempty"`
	Problem     string                         `json:"problem,omitempty"`
	ProblemKind domain.AgentAccountProblemKind `json:"problemKind,omitempty" enum:"slow,rate_limited,unavailable,cli_missing,other_login,other_org"`
	Limits      *domain.AgentAccountLimits     `json:"limits,omitempty"`
	SameLoginAs string                         `json:"sameLoginAs,omitempty"`
	OrgID       string                         `json:"orgId,omitempty"`
	OrgName     string                         `json:"orgName,omitempty"`
}

type AgentAccountsResponse struct {
	Accounts []AgentAccountResponse `json:"accounts"`
}

type StartAgentAccountLoginRequest struct {
	Provider domain.AgentAccountProvider `json:"provider" enum:"claude,codex"`
}

type StartAgentAccountLoginResponse struct {
	OperationID   string                            `json:"operationId"`
	Provider      domain.AgentAccountProvider       `json:"provider" enum:"claude,codex"`
	ShellTerminal CodexAccountLoginTerminalResponse `json:"shellTerminal"`
}

type VerifyAgentAccountLoginResponse struct {
	Status  agentaccounts.LoginStatus `json:"status" enum:"waiting,complete,duplicate,mismatch,not_subscription"`
	Account *AgentAccountResponse     `json:"account,omitempty"`
}

type ConsumeAgentAccountResetCreditRequest struct {
	IdempotencyKey string `json:"idempotencyKey" minLength:"1" maxLength:"128"`
}

type ConsumeAgentAccountResetCreditResponse struct {
	Outcome domain.CodexResetCreditOutcome `json:"outcome" enum:"reset,already_redeemed,nothing_to_reset"`
}

type AgentAccountUsageResponse struct {
	Days  int                    `json:"days"`
	Usage []AgentAccountUsageRow `json:"usage"`
}

type AgentAccountUsageRow struct {
	AccountID    string                     `json:"accountId"`
	InputTokens  int64                      `json:"inputTokens"`
	OutputTokens int64                      `json:"outputTokens"`
	CostNanos    int64                      `json:"costNanos"`
	Daily        []AgentAccountUsageDay     `json:"daily"`
	Models       []AgentAccountUsageByModel `json:"models"`
}

type AgentAccountUsageDay struct {
	Day       string `json:"day"`
	Tokens    int64  `json:"tokens"`
	CostNanos int64  `json:"costNanos"`
}

type AgentAccountUsageByModel struct {
	Model     string `json:"model"`
	Tokens    int64  `json:"tokens"`
	CostNanos int64  `json:"costNanos"`
}

type SessionAgentAccountsResponse struct {
	Claude string `json:"claude,omitempty"`
	Codex  string `json:"codex,omitempty"`
}

type SwitchSessionAgentAccountRequest struct {
	AccountID string `json:"accountId" minLength:"1"`
	StopTurn  bool   `json:"stopTurn,omitempty"`
}

type SessionImportRequest struct {
	IDs []string `json:"ids" minItems:"1" maxItems:"500"`
}

type SessionImportResponse struct {
	Results []sessionimport.Result `json:"results"`
}

type SessionForkOptionsResponse struct {
	Allowed       bool                  `json:"allowed"`
	Refusal       *SessionForkRefusal   `json:"refusal,omitempty"`
	SuggestedName string                `json:"suggestedName,omitempty"`
	AnyTurn       bool                  `json:"anyTurn,omitempty"`
	ForkedFrom    *SessionForkOrigin    `json:"forkedFrom,omitempty"`
	Forks         []SessionForkSummary  `json:"forks,omitempty"`
	SourcePRs     []SessionForkSourcePR `json:"sourcePRs,omitempty"`
}

type SessionForkSummary struct {
	SessionID domain.SessionID `json:"sessionId"`
	Title     string           `json:"title"`
	Exists    bool             `json:"exists"`
}

type SessionForkSourcePR struct {
	URL    string `json:"url"`
	Number int    `json:"number"`
	Merged bool   `json:"merged"`
	Closed bool   `json:"closed"`
}

type SessionForkRefusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type SessionForkOrigin struct {
	SessionID    domain.SessionID `json:"sessionId"`
	Title        string           `json:"title"`
	TurnID       string           `json:"turnId,omitempty"`
	SourceExists bool             `json:"sourceExists"`
	TextHandoff  bool             `json:"textHandoff,omitempty"`
}

type ForkSessionRequest struct {
	ClientRequestID string `json:"clientRequestId" minLength:"1"`
	TurnID          string `json:"turnId,omitempty"`
}

type ForkSessionResponse struct {
	SessionID   domain.SessionID `json:"sessionId"`
	DisplayName string           `json:"displayName"`
}
