// Typed contracts for the cloud control plane's renderer-facing v0 surface
// (`/api/cloud/v1/*`, WorkOS Bearer auth). Every shape below is derived from
// the Go handler request/response structs in `cloud/internal/httpapi` — do not
// add fields the control plane does not serve. Go `time.Time` fields arrive as
// RFC 3339 strings and are typed as `string` here.

/** Error envelope the control plane writes for every non-2xx response (`transport.go` errorEnvelope). */
export interface CloudCpErrorEnvelope {
	error: string;
	code: string;
	message: string;
	requestId: string;
	details?: Record<string, unknown>;
}

// ---------------------------------------------------------------------------
// Account (`auth_handlers.go`)
// ---------------------------------------------------------------------------

export interface CloudCpUser {
	id: string;
	email: string;
	displayName: string;
	authProvider: string;
}

export interface CloudCpOrganization {
	id: string;
	slug: string;
	displayName: string;
	role: string;
}

/** Sandbox providers a control plane offers (`/me` sandboxProviders). */
export interface CloudCpSandboxProviders {
	/** Every provider a session may select on this control plane. */
	available: string[];
	/** The provider used when a session does not specify one. */
	default: string;
}

/** GET /me */
export interface CloudCpMeResponse {
	user: CloudCpUser;
	organizations: CloudCpOrganization[];
	/**
	 * Present when the control plane reports its providers. A single-provider
	 * deployment lists exactly one available provider (the default).
	 */
	sandboxProviders?: CloudCpSandboxProviders;
}

// ---------------------------------------------------------------------------
// Organizations and invitations (`org_handlers.go`)
// ---------------------------------------------------------------------------

/** POST /orgs */
export interface CloudCpCreateOrganizationRequest {
	/** Workspace name, 1-80 characters. */
	displayName: string;
}

export interface CloudCpCreateOrganizationResponse {
	organization: CloudCpOrganization;
}

export interface CloudCpInvitation {
	id: string;
	orgId: string;
	email: string;
	invitedByEmail?: string;
	invitedByName?: string;
	role: string;
	status: string;
	expiresAt: string;
	acceptedAt?: string;
	declinedAt?: string;
	revokedAt?: string;
	createdAt: string;
	updatedAt: string;
}

/** GET /invitations */
export interface CloudCpInvitationsResponse {
	invitations: CloudCpInvitation[];
}

// ---------------------------------------------------------------------------
// Pagination (`resource_handlers.go` pageInfo / transport.go cursors)
// ---------------------------------------------------------------------------

export interface CloudCpPageInfo {
	hasMore: boolean;
	/** Opaque cursor for the next page; present only when `hasMore` is true. */
	nextCursor?: string;
}

// ---------------------------------------------------------------------------
// Cloud notifications (`notification_handlers.go`)
// ---------------------------------------------------------------------------

export interface CloudCpNotification {
	id: string;
	source: "cloud";
	eventId?: string;
	orgId: string;
	projectId?: string;
	sessionId?: string;
	type: string;
	title: string;
	body: string;
	status: "unread" | "read";
	resolvedAt?: string;
	createdAt: string;
	updatedAt: string;
}

export interface CloudCpNotificationEvent {
	sequence: number;
	orgId: string;
	recipientUserId: string;
	kind: "notification_created" | "notification_updated" | "notification_resolved";
	notification: CloudCpNotification;
	createdAt: string;
}

export interface CloudCpNotificationListQuery extends CloudCpListQuery {
	status?: "unread" | "read" | "all";
}

export interface CloudCpNotificationListResponse {
	items: CloudCpNotification[];
	page: CloudCpPageInfo;
	unreadCount: number;
	latestSequence: number;
}

export interface CloudCpNotificationEventsResponse {
	items: CloudCpNotificationEvent[];
	hasMore: boolean;
}

export interface CloudCpListQuery {
	/** Page size, 1-100 (control-plane default: 50). */
	limit?: number;
	/** Opaque cursor from a previous page's `page.nextCursor`. */
	cursor?: string;
}

// ---------------------------------------------------------------------------
// Projects (`resource_handlers.go`)
// ---------------------------------------------------------------------------

import type { ProjectConfig, ProjectAgentConfig, ProjectSettingsInput, ProjectRoleConfig, ProjectReviewer } from "../../../../../packages/cloud-client/src/types";

export type CloudCpProjectAgentConfig = ProjectAgentConfig;
export type CloudCpProjectRoleConfig = ProjectRoleConfig;
export type CloudCpProjectReviewer = ProjectReviewer;
/**
 * PATCH /orgs/{orgId}/projects/{projectId}/settings. `config.coder` accepts
 * only `workspaceNamePrefix` (an empty string returns to the default `ao`);
 * the rest of a project's coder config is fixed at creation.
 */
export type CloudCpProjectSettingsRequest = ProjectSettingsInput & {
	config?: NonNullable<ProjectSettingsInput["config"]> & {
		coder?: { workspaceNamePrefix: string };
	};
};

export interface CloudCpProject {
	id: string;
	orgId: string;
	displayName: string;
	repositoryUrl: string;
	defaultBranch: string;
	githubRepositoryId?: string;
	config: ProjectConfig;
	createdAt: string;
	updatedAt: string;
}

/** POST /orgs/{orgId}/projects (requires an Idempotency-Key header). */
export interface CloudCpCreateProjectRequest {
	/** 1-120 characters. */
	displayName: string;
	/** Must be an https URL. */
	repositoryUrl: string;
	/** 1-255 characters. */
	defaultBranch: string;
	config?: Record<string, unknown>;
	/**
	 * Optional coder dev-kit config chosen at project setup (template picker +
	 * size/startup + extra repos). Stored on the project; every coder session of
	 * the project inherits it. Absent = default template, single repo.
	 */
	coder?: CloudCpProjectCoderConfig;
}

export interface CloudCpProjectCoderConfig {
	/** Coder template UUID from GET /orgs/{orgId}/sandbox/coder/templates. Omit for the default template. */
	templateId?: string;
	/** t-shirt size the template maps to a VM SKU. Only with a non-default template. */
	size?: "small" | "medium" | "large";
	/** Optional shell snippet the template runs after checkout. Only with a non-default template. */
	startupScript?: string;
	/** Additional repositories every session of the project clones alongside the primary repo. */
	extraRepos?: CloudCpSessionRepo[];
	/**
	 * Prefix for the project's new Coder workspace names (`<prefix>-<short id>`).
	 * Empty/absent keeps the default `ao`. Must match `^[a-z][a-z0-9-]{0,19}$`
	 * without a trailing `-` or a `--` run.
	 */
	workspaceNamePrefix?: string;
}

/** PATCH /orgs/{orgId}/projects/{projectId} */
export interface CloudCpUpdateProjectRequest {
	displayName: string;
	defaultBranch: string;
}

export interface CloudCpProjectResponse {
	project: CloudCpProject;
}

export interface CloudCpProjectListResponse {
	items: CloudCpProject[];
	page: CloudCpPageInfo;
}

/** DELETE /orgs/{orgId}/projects/{projectId} responds 202: archive is asynchronous. */
export interface CloudCpProjectDeletedResponse {
	project: {
		id: string;
		deleted: boolean;
	};
}

// ---------------------------------------------------------------------------
// Sessions (`resource_handlers.go`)
// ---------------------------------------------------------------------------

export type CloudCpSessionKind = "worker" | "orchestrator";

export type CloudCpSessionMode = "read-only" | "standard" | "trusted";
export type CloudCpInterfaceMode = "tui" | "chat";

/** POST /orgs/{orgId}/sessions (requires an Idempotency-Key header). */
export interface CloudCpCreateSessionRequest {
	projectId: string;
	kind: CloudCpSessionKind;
	/** Coding-agent harness identifier (e.g. "claude-code"), 1-120 characters. */
	harness: string;
	/** 1-80 characters. */
	displayName: string;
	/** Up to 65536 bytes. */
	prompt: string;
	/** Defaults to "trusted" on the control plane when omitted. */
	mode?: CloudCpSessionMode;
	/**
	 * Coding-agent model the session launches with (harness-native id). Optional:
	 * omitted uses the harness default.
	 */
	model?: string;
	reasoningEffort?: string;
	deniedCommands?: string[];
	sandboxProviderConnectionId?: string;
	/**
	 * Sandbox provider for this session. Optional: omitted uses the control
	 * plane default. When set it must be one of `sandboxProviders.available`
	 * from `/me`.
	 */
	provider?: string;
}

export interface CloudCpSessionRepo {
	url: string;
	branch?: string;
}

/** GET /orgs/{orgId}/sandbox/coder/templates */
export interface CloudCpCoderTemplate {
	id: string;
	name: string;
	displayName: string;
	description: string;
	icon: string;
	// The per-workspace coder_parameter names this template declares (e.g.
	// "size", "startup_script"). The picker only offers a control when its
	// parameter is present, so a template that declares none shows no form.
	parameters: string[];
}

export interface CloudCpCoderTemplatesResponse {
	templates: CloudCpCoderTemplate[];
}

/**
 * GET /orgs/{orgId}/coder-config — an org's bring-your-own-Coder connection.
 * Non-secret fields only: the stored API token is never echoed back, surfaced
 * here solely as `tokenSet`.
 */
export interface CloudCpOrgCoderConfig {
	/** Coder deployment base URL or IP (http or https). */
	baseUrl: string;
	/**
	 * Coder owner/username new workspaces are created under. The control plane
	 * derives it from the API token on save, so it is always present once stored.
	 */
	owner?: string;
	/**
	 * Default Coder template id (a UUID) new workspaces use. Optional: a
	 * bring-your-own org leaves it empty and picks the template per project.
	 */
	defaultTemplateId?: string;
	/** Optional agent name the sandbox connects through. */
	agentName?: string;
	/**
	 * Optional PrivateLink VPC endpoint service name (the
	 * `coder_endpoint_service_name` Terraform output), set only when the Coder lives
	 * in a private VPC. AO ops provisions the VPC endpoint from it.
	 */
	endpointServiceName?: string;
	/** Optional AWS region for the PrivateLink endpoint (e.g. eu-north-1). */
	region?: string;
	/** True when an API token is stored. The token itself is never returned. */
	tokenSet: boolean;
}

export interface CloudCpOrgCoderConfigResponse {
	/** The stored config, or null when the org has none configured yet. */
	coderConfig: CloudCpOrgCoderConfig | null;
}

/**
 * PUT /orgs/{orgId}/coder-config. The slimmed form sends only `baseUrl` and
 * `token`; the control plane derives the workspace owner from the token and the
 * template is chosen per project, so `owner` and `defaultTemplateId` are optional.
 */
export interface CloudCpPutOrgCoderConfigRequest {
	baseUrl: string;
	/** Raw Coder API token; stored encrypted and never echoed back. Omit to keep the existing token. */
	token?: string;
	/** Optional: derived from the token when omitted. */
	owner?: string;
	/** Optional: the template is chosen per project, not at the org level. */
	defaultTemplateId?: string;
	agentName?: string;
	/** Optional PrivateLink VPC endpoint service name; omit for a directly reachable Coder. */
	endpointServiceName?: string;
	/** Optional AWS region for the PrivateLink endpoint. */
	region?: string;
}

export interface CloudCpSession {
	id: string;
	orgId: string;
	projectId: string;
	kind: string;
	harness: string;
	reviewerHarness?: string;
	autoReviewEnabled?: boolean;
	displayName: string;
	branch: string;
	mode: string;
	interfaceMode: CloudCpInterfaceMode;
	deniedCommands: string[];
	activityState: string;
	status: string;
	runtimeConnected: boolean;
	sandboxProvider?: string;
	desiredState?: string;
	observedState?: string;
	runtimeState?: string;
	runtimeError?: string;
	/**
	 * Why the session's worker has not started yet. Set while AO is retrying
	 * startup and kept once it gives up (`runtimeState === "terminated"`);
	 * cleared once the worker connects. `message` is user-facing text.
	 */
	startupError?: CloudCpSessionStartupError;
	isTerminated: boolean;
	autoInjectCI?: boolean;
	autoInjectReview?: boolean;
	terminateOnPrMerge?: boolean;
	prs: CloudCpSessionPullRequest[];
	/**
	 * Highest worker epoch the session has minted for its agent terminal. It
	 * advances on every fresh worker connection (resume from idle-pause,
	 * restore, re-provision), so the terminal can key on it and re-attach to the
	 * live agent instead of the dead epoch's exited terminal. Absent/0 when no
	 * worker has connected yet.
	 */
	workerEpoch?: number;
	createdAt: string;
	updatedAt: string;
}

/**
 * A cloud session's startup failure. Known codes: workspace_not_ready,
 * terminal_unavailable, unsupported_architecture, durable_root_unavailable,
 * worker_never_started, bootstrap_failed; treat any other code generically.
 */
export interface CloudCpSessionStartupError {
	code: string;
	message: string;
	/** RFC 3339 time the failure was recorded. */
	at: string;
}

/** POST /orgs/{orgId}/sessions/{sessionId}/startup-retry responds 202. */
export interface CloudCpRetrySessionStartupResponse {
	session: CloudCpSession;
}

export interface CloudCpInterfaceTransition {
	id: string;
	/** Mirrors the durable Cloud coordinator state machine. */
	phase:
		| "requested"
		| "preflighting"
		| "draining"
		| "source_stopping"
		| "source_stopped"
		| "target_starting"
		| "activating"
		| "completed"
		| "failed"
		| "cancelled"
		| "recovery_required";
	policy: "drain" | "interrupt";
	sessionId: string;
	sourceMode: CloudCpInterfaceMode;
	targetMode: CloudCpInterfaceMode;
	nativeConversationId?: string;
	errorCode?: string;
	errorDetail?: string;
	noticeAcknowledgedAt?: string;
	createdAt: string;
	updatedAt: string;
	completedAt?: string;
}

export interface CloudCpInterfaceTransitionStatusResponse {
	supported: boolean;
	targetMode: CloudCpInterfaceMode;
	reasonCode?: string;
	reason?: string;
	transition?: CloudCpInterfaceTransition;
}

export interface CloudCpStartInterfaceTransitionRequest {
	targetMode: CloudCpInterfaceMode;
	policy: "drain" | "interrupt";
	model?: string;
	reasoningEffort?: string;
}

export interface CloudCpStartInterfaceTransitionResponse {
	transition: CloudCpInterfaceTransition;
}

/** DELETE /orgs/{orgId}/sessions/{sessionId}/interface-transition */
export interface CloudCpCancelInterfaceTransitionResponse {
	ok: boolean;
}

/** PUT /orgs/{orgId}/sessions/{sessionId}/interface-transition/{transitionId}/notice-acknowledgement */
export interface CloudCpAcknowledgeInterfaceTransitionNoticeResponse {
	ok: boolean;
}

export interface CloudCpSessionResponse {
	session: CloudCpSession;
}

export interface CloudCpUpdateSessionPreferencesRequest {
	reviewerHarness?: string;
	autoReviewEnabled?: boolean;
	autoInjectCI?: boolean;
	autoInjectReview?: boolean;
	terminateOnPrMerge?: boolean;
}

export interface CloudCpSessionListResponse {
	items: CloudCpSession[];
	page: CloudCpPageInfo;
}

// ---------------------------------------------------------------------------
// Docker workspace review (`workspace_handlers.go`)
// ---------------------------------------------------------------------------

/** One changed file in a cloud workspace. */
export interface CloudCpWorkspaceDiffFile {
	path: string;
	status: "unmodified" | "modified" | "added" | "deleted" | "renamed" | "untracked" | "copied" | "changed";
	additions: number;
	deletions: number;
	binary: boolean;
}

/** Changed-file summary, compared with the session's HEAD. */
export interface CloudCpWorkspaceDiff {
	files: CloudCpWorkspaceDiffFile[];
	diffBaseRef: string;
	diffBaseSha?: string;
	truncated: { combined: boolean; stats: boolean };
}

/** Selected-file review details. */
export interface CloudCpWorkspaceDiffFileDetail extends CloudCpWorkspaceDiffFile {
	size: number;
	deleted: boolean;
	content: string;
	contentTruncated: boolean;
	diff: string;
	diffTruncated: boolean;
}

// ---------------------------------------------------------------------------
// Provider-neutral workspace review (`workspace_review_handlers.go`)
// ---------------------------------------------------------------------------

export type CloudCpWorkspaceReviewScope = "combined" | "committed" | "staged" | "unstaged" | "untracked";
export type CloudCpWorkspaceReviewStatus = "unmodified" | "modified" | "added" | "deleted" | "renamed" | "copied" | "untracked";
export type CloudCpWorkspaceReviewSide = "before" | "after";

export interface CloudCpWorkspaceReviewFileSummary {
	path: string;
	previousPath?: string;
	status: CloudCpWorkspaceReviewStatus;
	additions: number;
	deletions: number;
	size: number;
	binary: boolean;
	editable: boolean;
	fileFingerprint: string;
}

export interface CloudCpWorkspaceReviewSections {
	staged: CloudCpWorkspaceReviewFileSummary[];
	unstaged: CloudCpWorkspaceReviewFileSummary[];
	untracked: CloudCpWorkspaceReviewFileSummary[];
	committed: CloudCpWorkspaceReviewFileSummary[];
}

export interface CloudCpWorkspaceReviewCommit {
	sha: string;
	subject: string;
	author: string;
	timestamp: string;
	files: CloudCpWorkspaceReviewFileSummary[];
}

export interface CloudCpWorkspaceReviewResponse {
	workspaceVersion: string;
	compareBaseSha?: string;
	compareBaseRef?: string;
	compareMode?: "base" | "head_fallback";
	files: CloudCpWorkspaceReviewFileSummary[];
	truncated: boolean;
	sections: CloudCpWorkspaceReviewSections;
	commits: CloudCpWorkspaceReviewCommit[];
	summary: { files: number; additions: number; deletions: number };
	ahead?: number;
	behind?: number;
}

export interface CloudCpWorkspaceReviewFileQuery {
	path: string;
	scope?: CloudCpWorkspaceReviewScope;
	commitSha?: string;
}

export interface CloudCpWorkspaceReviewFileResponse extends CloudCpWorkspaceReviewFileSummary {
	deleted: boolean;
	imageMediaType?: string;
	content: string;
	contentTruncated: boolean;
	diff: string;
	diffTruncated: boolean;
	compareBaseSha?: string;
	compareBaseRef?: string;
	compareMode?: "base" | "head_fallback";
	workspaceVersion: string;
	historical?: boolean;
}

export interface CloudCpWorkspaceReviewDiffsRequest {
	scope: CloudCpWorkspaceReviewScope;
	paths: string[];
	contextLines: number;
	ignoreWhitespace: boolean;
	workspaceVersion?: string;
	commitSha?: string;
}

export interface CloudCpWorkspaceReviewDiffsResponse {
	workspaceVersion: string;
	groups: Array<{
		repository?: string;
		patch: string;
		truncated: boolean;
		includedPaths: string[];
		deferred: Array<{ path: string; reason: "binary" | "oversized" | "generated" | "long_line" | "budget_exceeded" }>;
		errors: Array<{ code: string; message: string }>;
	}>;
}

export interface CloudCpWorkspaceReviewRevisionQuery {
	path: string;
	scope?: CloudCpWorkspaceReviewScope;
	side?: CloudCpWorkspaceReviewSide;
	workspaceVersion?: string;
	expectedRevision?: string;
	commitSha?: string;
}

export interface CloudCpWorkspaceReviewRevisionResponse {
	path: string;
	side: CloudCpWorkspaceReviewSide;
	revision?: string;
	workspaceVersion: string;
	mediaType?: string;
	encoding?: string;
	size: number;
	exists: boolean;
	binary: boolean;
	truncated: boolean;
	content: string;
}

export interface CloudCpWorkspaceReviewTreeResponse {
	path: string;
	entries: Array<{
		name: string;
		path: string;
		type: "file" | "dir";
		status?: CloudCpWorkspaceReviewStatus;
		hasChanges?: boolean;
		size?: number;
		binary?: boolean;
	}>;
	truncated: boolean;
}

export interface CloudCpWorkspaceReviewSearchQuery {
	query: string;
	cursor?: string;
	limit?: number;
}

export interface CloudCpWorkspaceReviewSearchResponse {
	query: string;
	results: Array<Pick<CloudCpWorkspaceReviewFileSummary, "path" | "status" | "size" | "binary" | "fileFingerprint">>;
	nextCursor?: string;
	truncated: boolean;
}

export interface CloudCpWorkspaceReviewWriteRequest {
	path: string;
	content: string;
	expectedFileFingerprint: string;
}

export interface CloudCpWorkspaceReviewWriteResponse {
	path: string;
	content: string;
	size: number;
	fileFingerprint: string;
	workspaceVersion: string;
}

/** One pull request on a children listing (GET .../sessions/{id}/children). */
export interface CloudCpSessionPullRequest {
	url: string;
	number: number;
	state: "draft" | "open" | "merged" | "closed";
	ci: string;
	review: string;
	mergeability: string;
	failingChecks?: Array<{
		name: string;
		status: "failed" | "cancelled";
		conclusion: string;
		url?: string;
	}>;
	/** Always false today: the control plane does not track unresolved comments yet. */
	reviewComments: boolean;
	sourceBranch?: string;
	targetBranch?: string;
	updatedAt: string;
}

/** A child session as listed under its orchestrator, with its pull requests. */
export interface CloudCpSessionChild extends CloudCpSession {
	prs: CloudCpSessionPullRequest[];
}

export interface CloudCpSessionChildrenResponse {
	items: CloudCpSessionChild[];
	page: CloudCpPageInfo;
}

// ---------------------------------------------------------------------------
// Pull requests and AO reviews (`pull_request_handlers.go`)
// ---------------------------------------------------------------------------

export interface CloudCpPullRequestSummary {
	url: string;
	htmlUrl?: string;
	number: number;
	title: string;
	state: "draft" | "open" | "merged" | "closed";
	provider: string;
	repository: string;
	author: string;
	authorAvatarUrl?: string;
	sourceBranch: string;
	targetBranch: string;
	headSha: string;
	additions: number;
	deletions: number;
	changedFiles: number;
	ci: {
		state: "unknown" | "pending" | "passing" | "failing";
		failingChecks: Array<{
			name: string;
			status: "failed" | "cancelled";
			conclusion: string;
			url?: string;
		}>;
	};
	review: {
		decision: "none" | "approved" | "changes_requested" | "review_required";
		hasUnresolvedHumanComments: boolean;
		unresolvedBy: Array<{
			reviewerId: string;
			count: number;
			links: Array<{ url?: string; reviewId?: string; file?: string; line?: number; body?: string; autoInjectReview: boolean }>;
			reviewUrl?: string;
			isBot?: boolean;
		}>;
		resolvedBy: Array<{
			reviewerId: string;
			count: number;
			links: Array<{ url?: string; reviewId?: string; file?: string; line?: number; body?: string; autoInjectReview: boolean }>;
			reviewUrl?: string;
			isBot?: boolean;
		}>;
		reviews: Array<{
			reviewerId: string;
			verdict: "none" | "approved" | "changes_requested" | "review_required";
			body?: string;
			reviewUrl?: string;
			submittedAt: string;
			isBot?: boolean;
			autoInjectReview: boolean;
		}>;
	};
	mergeability: {
		state: "unknown" | "mergeable" | "conflicting" | "blocked" | "unstable";
		reasons: string[];
		pullRequestUrl: string;
		conflictFiles: Array<{ path: string; url?: string }>;
	};
	stateChangedAt?: string;
	createdAt?: string;
	updatedAt: string;
	observedAt: string;
	ciObservedAt: string;
	reviewObservedAt: string;
}

export interface CloudCpSessionPullRequestsResponse {
	sessionId: string;
	pullRequests: CloudCpPullRequestSummary[];
}

export type CloudCpAOReviewRunStatus = "running" | "complete" | "delivered" | "failed" | "cancelled";
export type CloudCpAOReviewVerdict = "" | "approved" | "changes_requested";
export type CloudCpAOReviewState = "needs_review" | "running" | "up_to_date" | "changes_requested" | "ineligible";

export interface CloudCpAOReviewRun {
	id: string;
	reviewId: string;
	sessionId: string;
	batchId: string;
	harness: string;
	triggerSource: "manual" | "auto";
	pullRequestUrl: string;
	targetSha: string;
	status: CloudCpAOReviewRunStatus;
	verdict: CloudCpAOReviewVerdict;
	body: string;
	providerReviewId: string;
	reviewerTerminalId?: string;
	createdAt: string;
	deliveredAt?: string;
	autoInjectReview: boolean;
}

export interface CloudCpPRReviewState {
	pullRequestUrl: string;
	pullRequestNumber: number;
	title: string;
	targetSha: string;
	status: CloudCpAOReviewState;
	latestRun?: CloudCpAOReviewRun;
	previousRun?: CloudCpAOReviewRun;
}

export interface CloudCpSessionReviewState {
	sessionId: string;
	reviewerHandleId?: string;
	reviewerHarness?: string;
	availableReviewerHarnesses: string[];
	reviews: CloudCpPRReviewState[];
	runs: CloudCpAOReviewRun[];
}

export interface CloudCpHarnessStatus {
	harness: "claude-code" | "codex" | "cursor";
	status: "missing" | "ready" | "failed";
	version?: string;
	error?: string;
}

export interface CloudCpHarnessInspectResponse {
	harnesses: CloudCpHarnessStatus[];
}
export interface CloudCpListSessionsQuery extends CloudCpListQuery {
	/** Restrict the listing to one project. */
	projectId?: string;
}

/** DELETE /orgs/{orgId}/sessions/{sessionId} responds 202: teardown is reconciler-owned. */
export interface CloudCpSessionDeletedResponse {
	session: {
		id: string;
		desiredState: string;
	};
}

/** POST /orgs/{orgId}/sessions/{sessionId}/resume accepts user resume intent. */
export interface CloudCpResumeSessionResponse {
	session: {
		id: string;
		sandboxProvider: string;
		desiredState: string;
		observedState: string;
	};
}

/**
 * POST /orgs/{orgId}/sessions/{sessionId}/restore responds 202: a deleted
 * session is re-provisioned with its conversation and work intact, and the
 * reconciler owns bringing it back — the response only echoes the new intent.
 */
export interface CloudCpRestoreSessionResponse {
	session: {
		id: string;
		desiredState: string;
	};
}

// ---------------------------------------------------------------------------
// Chat events (`event_handlers.go`)
// ---------------------------------------------------------------------------

/** POST /orgs/{orgId}/sessions/{sessionId}/messages (requires an Idempotency-Key header). */
export interface CloudCpSendMessageRequest {
	/** 1-65536 bytes. */
	text: string;
	model?: string;
	reasoningEffort?: string;
	/** Per-turn permission mode, capped by the session and share grant. */
	mode?: "read-only" | "standard" | "trusted";
	approvalMode?: "default" | "accept-edits" | "auto" | "bypass-permissions";
}

export interface CloudCpChatModelsResponse {
	modes?: string[];
	model?: string;
	reasoningEffort?: string;
	models: Array<{
		id: string;
		displayName: string;
		description?: string;
		default: boolean;
		efforts?: string[];
		defaultEffort?: string;
	}>;
}

export interface CloudCpClientEvent {
	sessionId: string;
	sequence: number;
	type: string;
	/** Raw event payload (Go `json.RawMessage`); shape depends on `type`. */
	payload: unknown;
	createdAt: string;
}

export interface CloudCpSendMessageResponse {
	event: CloudCpClientEvent;
}

/** POST /orgs/{orgId}/sessions/{sessionId}/turns/{turnId}/cancel responds 202. */
export interface CloudCpCancelTurnResponse {
	ok: boolean;
}

/** POST steering response; guidance for the active turn was accepted. */
export interface CloudCpSteerTurnResponse {
	event: CloudCpClientEvent;
}

export interface CloudCpChatEventsQuery {
	/** Replay events with sequence strictly greater than this (0-9007199254740991). */
	after?: number;
	/** Page size, 1-500 (control-plane default: 100). */
	limit?: number;
}

/** GET /orgs/{orgId}/sessions/{sessionId}/chat-events */
export interface CloudCpChatEventsResponse {
	events: CloudCpClientEvent[];
	hasMore: boolean;
	nextAfter: number;
}

// ---------------------------------------------------------------------------
// Terminal tickets (`terminal_handlers.go`)
// ---------------------------------------------------------------------------

export type CloudCpTerminalKind = "workspace" | "agent";

/** POST /orgs/{orgId}/sessions/{sessionId}/terminal-ticket */
export interface CloudCpTerminalTicketRequest {
	kind: CloudCpTerminalKind;
	/** Optional exact terminal surface, used for a dedicated reviewer terminal. */
	terminalId?: string;
}

export interface CloudCpTerminalTicketResponse {
	/** Single-use ticket redeemed by the `/api/cloud/v1/terminal` WebSocket upgrade. */
	ticket: string;
	/** Seconds until the ticket expires. */
	expiresIn: number;
	scopes: string[];
}

// ---------------------------------------------------------------------------
// Provider connections (`provider_handlers.go`)
// ---------------------------------------------------------------------------

/** Coding-agent providers the control plane accepts (`validAgentProvider`). */
export type CloudCpAgentProvider = "claude-code" | "codex" | "cursor" | "opencode";

/**
 * Credential types by provider (`validAgentCredentialType`):
 * claude-code accepts "api_key" | "oauth_token"; codex accepts
 * "api_key" | "access_token" | "auth_json" (the opaque result of a
 * ChatGPT subscription login); cursor accepts "api_key"; opencode accepts
 * "auth_json" (its multi-provider auth document; no single api-key env var).
 */
export interface CloudCpPutAgentConnectionRequest {
	credentialType: string;
	/** Raw credential secret; validated then stored encrypted, never echoed back. */
	secret: string;
}

/** PUT /me/github-pat */
export interface CloudCpPutGitHubPATRequest {
	/** Raw GitHub personal access token; stored encrypted and never echoed. */
	secret: string;
}

/** POST /me/github-pat/validate-saved-repository */
export interface CloudCpValidateRepositoryAccessRequest {
	repositoryUrl: string;
}

export interface CloudCpValidateRepositoryAccessResponse {
	writeAccess: boolean;
}

export interface CloudCpProviderConnection {
	id: string;
	provider: string;
	label: string;
	config: Record<string, unknown>;
	validationState: string;
	validatedAt?: string;
	createdAt: string;
	updatedAt: string;
}

/** GET /me/providers */
export interface CloudCpProviderConnectionsResponse {
	providerConnections: CloudCpProviderConnection[];
}

/** PUT /me/providers/{agent}, PUT /me/github-pat */
export interface CloudCpProviderConnectionResponse {
	providerConnection: CloudCpProviderConnection;
}

/** GET /me/github/repos */
export interface CloudCpGitHubRepo {
	name: string;
	fullName: string;
	private: boolean;
	defaultBranch: string;
	cloneUrl: string;
}

export interface CloudCpGitHubReposResponse {
	repos: CloudCpGitHubRepo[];
}

// ---------------------------------------------------------------------------
// GitHub App connect flow (github_handlers.go)
//
// The secure, hosted GitHub connection: the control plane owns the GitHub App
// client id and secret, builds the install/authorize URL, catches the redirect
// on its own callback, and stores the installation. The desktop only opens the
// URL and polls for completion, then lists the App's repositories and creates a
// project from one. No GitHub secret ever reaches the desktop.
// ---------------------------------------------------------------------------

/** POST /orgs/{orgId}/github/installations/start */
export interface CloudCpStartGitHubInstallationResponse {
	installationUrl: string;
	expiresAt: string;
}

export interface CloudCpGitHubInstallation {
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
}

/** GET /orgs/{orgId}/github/installations */
export interface CloudCpGitHubInstallationsResponse {
	installations: CloudCpGitHubInstallation[];
}

/** POST /orgs/{orgId}/github/installations/{installationId}/sync */
export interface CloudCpSyncGitHubInstallationResponse {
	installation: CloudCpGitHubInstallation;
}

export interface CloudCpGitHubUserInstallation {
	githubInstallationId: string;
	accountLogin: string;
	accountType: string;
	repositorySelection: string;
	canCreateRepository: boolean;
	unavailableReason?: string;
}

/** GET /github/user */
export interface CloudCpGitHubUserConnection {
	connected: boolean;
	login?: string;
	avatarUrl?: string;
	installations: CloudCpGitHubUserInstallation[];
	lastSyncedAt?: string;
}

/** One repository an installation grants access to (GET /orgs/{orgId}/github/repositories). */
export interface CloudCpGitHubAppRepository {
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
}

export interface CloudCpGitHubRepositoriesPage {
	items: CloudCpGitHubAppRepository[];
	page: CloudCpPageInfo;
}

/**
 * POST /orgs/{orgId}/github/projects. `config` is stored verbatim on the
 * project; nest the coder dev-kit config under a `coder` key to attach a
 * template/size/startup/extra repos (the control plane reads `config.coder`).
 */
export interface CloudCpCreateGitHubProjectRequest {
	githubRepositoryId: string;
	displayName?: string;
	config?: Record<string, unknown>;
}
