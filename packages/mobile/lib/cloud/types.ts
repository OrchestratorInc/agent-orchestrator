// Wire shapes for the Orchestrator Cloud control plane's user routes (`/api/cloud/v1`).
//
// Mirrors the desktop's renderer client (frontend/src/renderer/lib/cloud-cp/
// types.ts), which is derived from the control plane's handler structs, trimmed
// to what the phone reads. Fields the phone does not use are left out rather
// than guessed; the control plane may send more, and nothing here validates
// responses strictly (its contract lags the server in places).

export type CloudErrorEnvelope = {
	error: string;
	code: string;
	message: string;
	requestId: string;
	details?: Record<string, unknown>;
};

export type CloudPageInfo = { hasMore: boolean; nextCursor?: string };
export type CloudPage<T> = { items: T[]; page: CloudPageInfo };

// ---- Account ----------------------------------------------------------------

export type CloudUser = { id: string; email: string; displayName: string; authProvider?: string };

export type CloudOffering = {
	providers: string[];
	defaultProvider: string;
	billingMode: "self_serve" | "contract" | "off";
	kind: "b2b" | "b2c" | "both" | "none";
	features: string[];
	customCoder?: boolean;
	artifacts?: boolean;
	/** The largest file a session attachment may be. Present only where the control plane takes attachments. */
	maxAttachmentBytes?: number;
};

export type CloudOrganization = {
	id: string;
	slug: string;
	displayName: string;
	role: string;
	offering?: CloudOffering;
};

export type CloudMe = {
	user: CloudUser;
	organizations: CloudOrganization[];
	sandboxProviders?: { available: string[]; default: string };
	/** Harnesses that can start in Chat. Absent on an older control plane: everything starts in the terminal. */
	chatHarnesses?: string[];
	/** The organization the sign-in token maps to. Absent on an older control plane. */
	activeOrganizationId?: string;
	offering?: CloudOffering;
};

// ---- Projects ---------------------------------------------------------------

export type CloudRoleConfig = { agent?: string; agentConfig?: { model?: string; reasoningEffort?: string } };

export type CloudProjectConfig = {
	worker?: CloudRoleConfig;
	orchestrator?: CloudRoleConfig;
	sandboxProvider?: string;
	[key: string]: unknown;
};

export type CloudProject = {
	id: string;
	orgId: string;
	displayName: string;
	repositoryUrl: string;
	defaultBranch: string;
	githubRepositoryId?: string;
	standalone?: boolean;
	config: CloudProjectConfig;
	createdAt: string;
	updatedAt: string;
};

export type CloudGitHubInstallation = {
	id: string;
	githubInstallationId: string;
	accountLogin: string;
	accountType: string;
	status: string;
	repositorySelection: string;
	syncStatus: string;
	lastSyncedAt?: string;
	lastError?: string;
	createdAt: string;
	updatedAt: string;
};

export type CloudGitHubRepository = {
	githubRepositoryId: string;
	name: string;
	fullName: string;
	htmlUrl: string;
	defaultBranch: string;
	visibility: string;
	isPrivate: boolean;
	isArchived: boolean;
	access: string;
	grantedAt: string;
	revokedAt?: string;
};

// ---- Sessions ---------------------------------------------------------------

export type CloudSessionKind = "worker" | "orchestrator";
export type CloudInterfaceMode = "chat" | "tui";

export type CloudSessionPullRequest = {
	url: string;
	number: number;
	state: "draft" | "open" | "merged" | "closed";
	ci: string;
	review: string;
	mergeability: string;
	sourceBranch?: string;
	targetBranch?: string;
	updatedAt: string;
};

export type CloudStartupError = { code: string; message: string; at: string; detail?: string };

export type CloudSession = {
	id: string;
	orgId: string;
	projectId: string;
	kind: string;
	harness: string;
	displayName: string;
	branch: string;
	/** Trust level (read-only | standard | trusted), not the interface. */
	mode: string;
	model?: string;
	interfaceMode: CloudInterfaceMode;
	activityState: string;
	status: string;
	runtimeConnected: boolean;
	sandboxProvider?: string;
	desiredState?: string;
	observedState?: string;
	runtimeState?: string;
	runtimeError?: string;
	startupError?: CloudStartupError;
	isTerminated: boolean;
	prs?: CloudSessionPullRequest[];
	/** The session's own reviewer agent, when it overrides the project's. */
	reviewerHarness?: string;
	autoReviewEnabled?: boolean;
	/** Whether review comments and CI failures go to the agent by themselves. */
	autoInjectReview?: boolean;
	autoInjectCI?: boolean;
	workerEpoch?: number;
	/** Whether the signed-in person pinned it. A pin is one person's. */
	pinned?: boolean;
	pinnedAt?: string;
	/** Who started it (for a spawned worker, its orchestrator's starter). */
	createdByUserId?: string;
	/** How many artifact files it holds, and when they last changed; absent on an older control plane. */
	artifactCount?: number;
	artifactsUpdatedAt?: string;
	createdAt: string;
	updatedAt: string;
};

// ---- Pull requests and AO reviews (the control plane's pull_request_handlers.go)

type CloudReviewCommentLink = { url?: string; reviewId?: string; file?: string; line?: number; body?: string; autoInjectReview: boolean };
type CloudReviewerComments = { reviewerId: string; count: number; links: CloudReviewCommentLink[]; reviewUrl?: string; isBot?: boolean };

/** A session's pull request as GitHub last showed it: the PR page's detail. */
export type CloudPullRequestSummary = {
	url: string;
	htmlUrl?: string;
	number: number;
	title: string;
	state: "draft" | "open" | "merged" | "closed";
	provider: string;
	/** owner/name */
	repository: string;
	author: string;
	sourceBranch: string;
	targetBranch: string;
	headSha: string;
	additions: number;
	deletions: number;
	changedFiles: number;
	ci: { state: "unknown" | "pending" | "passing" | "failing"; failingChecks: { name: string; status: string; conclusion: string; url?: string }[] };
	review: {
		decision: "none" | "approved" | "changes_requested" | "review_required";
		hasUnresolvedHumanComments: boolean;
		unresolvedBy: CloudReviewerComments[];
		resolvedBy?: CloudReviewerComments[];
		reviews?: { reviewerId: string; verdict: "none" | "approved" | "changes_requested" | "review_required"; body?: string; reviewUrl?: string; submittedAt: string; isBot?: boolean; autoInjectReview: boolean }[];
	};
	mergeability: { state: "unknown" | "mergeable" | "conflicting" | "blocked" | "unstable"; reasons: string[]; pullRequestUrl?: string; conflictFiles?: { path: string; url?: string }[] };
	updatedAt?: string;
};

export type CloudSessionPullRequests = { sessionId: string; pullRequests: CloudPullRequestSummary[] };

export type CloudReviewRun = {
	id: string;
	reviewId: string;
	sessionId: string;
	batchId: string;
	harness: string;
	triggerSource: "manual" | "auto";
	pullRequestUrl: string;
	targetSha: string;
	status: "running" | "complete" | "delivered" | "failed" | "cancelled";
	verdict: "" | "approved" | "changes_requested";
	body: string;
	providerReviewId: string;
	reviewerTerminalId?: string;
	/** "chat" for a review that runs as a Chat conversation. */
	interfaceMode?: "chat";
	createdAt: string;
	deliveredAt?: string;
	autoInjectReview: boolean;
};

export type CloudPRReviewState = {
	pullRequestUrl: string;
	pullRequestNumber: number;
	title: string;
	targetSha: string;
	status: "needs_review" | "running" | "up_to_date" | "changes_requested" | "ineligible";
	latestRun?: CloudReviewRun;
	previousRun?: CloudReviewRun;
};

export type CloudSessionReviewState = {
	sessionId: string;
	reviewerHandleId?: string;
	reviewerHarness?: string;
	availableReviewerHarnesses?: string[];
	reviews: CloudPRReviewState[];
	runs: CloudReviewRun[];
	/** The reviewer conversation: the newest review that runs in Chat. */
	reviewerSurface?: { mode: "chat"; reviewId: string; harness: string };
};

/** The session settings PATCH /preferences changes; only the fields sent change. */
export type CloudSessionPreferences = {
	reviewerHarness?: string;
	autoReviewEnabled?: boolean;
	autoInjectCI?: boolean;
	autoInjectReview?: boolean;
};

// ---- The session's workspace (the control plane's workspace_review_handlers.go)

export type CloudWorkspaceStatus = "unmodified" | "modified" | "added" | "deleted" | "renamed" | "copied" | "untracked";
export type CloudWorkspaceScope = "combined" | "committed" | "staged" | "unstaged" | "untracked";

export type CloudWorkspaceFileSummary = {
	path: string;
	previousPath?: string;
	status: CloudWorkspaceStatus;
	additions: number;
	deletions: number;
	size: number;
	binary: boolean;
	editable?: boolean;
	fileFingerprint?: string;
};

export type CloudWorkspaceReview = {
	workspaceVersion: string;
	compareBaseRef?: string;
	compareMode?: "base" | "head_fallback";
	files: CloudWorkspaceFileSummary[];
	truncated: boolean;
	sections?: Partial<Record<"committed" | "staged" | "unstaged" | "untracked", CloudWorkspaceFileSummary[]>>;
	summary: { files: number; additions: number; deletions: number };
	ahead?: number;
	behind?: number;
};

export type CloudWorkspaceFile = CloudWorkspaceFileSummary & {
	deleted: boolean;
	imageMediaType?: string;
	content: string;
	contentTruncated: boolean;
	diff: string;
	diffTruncated: boolean;
	workspaceVersion: string;
};

export type CloudWorkspaceTree = {
	path: string;
	entries: { name: string; path: string; type: "file" | "dir"; status?: CloudWorkspaceStatus; hasChanges?: boolean; size?: number; binary?: boolean }[];
	truncated: boolean;
};

export type CloudWorkspaceSearch = {
	query: string;
	results: { path: string; status: CloudWorkspaceStatus; size: number; binary: boolean }[];
	nextCursor?: string;
	truncated: boolean;
};

/** One side of a file at a version: its text, or base64 for a binary file. */
export type CloudWorkspaceRevision = {
	path: string;
	side: "before" | "after";
	workspaceVersion: string;
	mediaType?: string;
	encoding?: string;
	size: number;
	exists: boolean;
	binary: boolean;
	truncated: boolean;
	content: string;
};

// ---- Artifacts (the control plane's artifact_handlers.go)

export type CloudArtifact = { path: string; name: string; kind: "html" | "markdown" | "file"; contentType?: string; size: number; sha256?: string; updatedAt: string };

/** A signed prefix the session's artifact files load from with no header, for ten minutes. */
export type CloudArtifactAccess = { filesPrefix: string; expiresAt: string };

/** A file's kept version, as lib/artifactVersions.ts reads it. */
export type CloudArtifactVersionSummary = { version: number; createdAt: string; size: number; source?: string; harness?: string; restoredFrom?: number };

/** A file uploaded to a session for a message to carry. */
export type CloudAttachment = { id: string; fileName: string; contentType: string; size: number; sha256?: string; createdAt?: string };

export type CloudSessionPin = { sessionId: string; pinned: boolean; pinnedAt?: string };

export type CreateCloudSessionInput = {
	projectId: string;
	kind: CloudSessionKind;
	harness: string;
	/** 1-80 characters (the contract's limit; the server allows 100). */
	displayName: string;
	prompt: string;
	model?: string;
	reasoningEffort?: string;
	approvalMode?: CloudApprovalMode;
	interfaceMode?: CloudInterfaceMode;
};

// ---- Chat -------------------------------------------------------------------

export type CloudClientEvent = {
	sessionId: string;
	sequence: number;
	type: string;
	/** Raw payload; its shape depends on `type`. */
	payload: unknown;
	createdAt: string;
};

export type CloudChatEventsPage = { events: CloudClientEvent[]; hasMore: boolean; nextAfter: number };

export type CloudApprovalMode = "default" | "accept-edits" | "auto" | "bypass-permissions";

export type CloudSendMessageInput = {
	/** May be empty when the message carries files. */
	text: string;
	/** Files uploaded to the session (at most ten); the worker places them in the workspace first. */
	attachmentIds?: string[];
	model?: string;
	reasoningEffort?: string;
	mode?: "read-only" | "standard" | "trusted";
	approvalMode?: CloudApprovalMode;
};

export type CloudChatModel = {
	id: string;
	displayName: string;
	description?: string;
	default: boolean;
	efforts?: string[];
	defaultEffort?: string;
	approvalModes?: CloudApprovalMode[];
};

export type CloudChatModels = {
	models: CloudChatModel[];
	modes?: string[];
	model?: string;
	reasoningEffort?: string;
	source?: string;
	cliVersion?: string;
};

// ---- Terminal ---------------------------------------------------------------

export type CloudTerminalKind = "agent" | "workspace";
export type CloudTerminalTicket = { ticket: string; expiresIn: number; scopes: string[] };

// ---- Credentials ------------------------------------------------------------

export type CloudProviderConnection = {
	id: string;
	provider: string;
	label: string;
	config: Record<string, unknown>;
	validationState: string;
	validatedAt?: string;
	createdAt: string;
	updatedAt: string;
};

// ---- Notifications ----------------------------------------------------------

export type CloudNotification = {
	id: string;
	source: "cloud";
	eventId?: string;
	orgId: string;
	projectId?: string;
	sessionId?: string;
	metadata?: Record<string, unknown>;
	type: string;
	title: string;
	body: string;
	status: "unread" | "read";
	resolvedAt?: string;
	createdAt: string;
	updatedAt: string;
};

export type CloudNotificationPage = CloudPage<CloudNotification> & { unreadCount: number; latestSequence: number };
