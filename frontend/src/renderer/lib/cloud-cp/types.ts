export interface CloudCpErrorEnvelope {
	error: string;
	code: string;
	message: string;
	requestId: string;
	details?: Record<string, unknown>;
}

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
	offering?: CloudCpOffering;
	kind?: string;
}

export interface CloudCpSandboxProviders {
	available: string[];
	default: string;
}

export type CloudCpBillingMode = "self_serve" | "contract" | "off";

export type CloudCpOfferingKind = "b2b" | "b2c" | "both" | "none";

export interface CloudCpOffering {
	providers: string[];
	defaultProvider: string;
	billingMode: CloudCpBillingMode;
	kind: CloudCpOfferingKind;
	features: string[];
	maxAttachmentBytes?: number;
	customCoder?: boolean;
	artifacts?: boolean;
	coderWorkerWorkingFolder?: boolean;
}

export interface CloudCpMeResponse {
	user: CloudCpUser;
	organizations: CloudCpOrganization[];
	sandboxProviders?: CloudCpSandboxProviders;
	chatHarnesses?: string[];
	agentSwitchHarnesses?: string[];
	agentAccounts?: boolean;
	activeOrganizationId?: string;
	offering?: CloudCpOffering;
}

export interface CloudCpCreateOrganizationRequest {
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

export interface CloudCpInvitationsResponse {
	invitations: CloudCpInvitation[];
}

export interface CloudCpOrgMember {
	userId: string;
	email: string;
	displayName: string;
	role: string;
	createdAt: string;
}

export interface CloudCpOrgMembersResponse {
	members: CloudCpOrgMember[];
}

export interface CloudCpPageInfo {
	hasMore: boolean;
	nextCursor?: string;
}

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
	limit?: number;
	cursor?: string;
}

export interface CloudCpProjectListQuery extends CloudCpListQuery {
	standaloneAgents?: "exclude" | "only";
}

import type { AddAgentAccountInput, CloudAgentAccount, SessionAgentAccount } from "../../../../../packages/cloud-client/src/types";
import type { ProjectConfig, ProjectAgentConfig, ProjectSettingsInput, ProjectRoleConfig, ProjectReviewer, ProjectEnvironment, ProjectIntakeStatus, ProjectTrackerIntake, Cue, CueDefinitionInput, InvokeCueResult } from "../../../../../packages/cloud-client/src/types";

export type CloudCpProjectAgentConfig = ProjectAgentConfig;
export type CloudCpProjectEnvironment = ProjectEnvironment;
export type CloudCpProjectIntakeStatus = ProjectIntakeStatus;
export type CloudCpProjectTrackerIntake = ProjectTrackerIntake;
export type CloudCpCue = Cue;
export type CloudCpCueDefinition = CueDefinitionInput;
export type CloudCpInvokeCueResult = InvokeCueResult;
export type CloudCpProjectRoleConfig = ProjectRoleConfig;
export type CloudCpProjectReviewer = ProjectReviewer;
export type CloudCpProjectSettingsRequest = Omit<ProjectSettingsInput, "config"> & {
	config?: Omit<NonNullable<ProjectSettingsInput["config"]>, "coder" | "postCreate" | "preRemove"> & {
		coder?: { workspaceNamePrefix?: string; parameters?: Record<string, string> };
		postCreate?: CloudCpProjectScriptStep[];
		preRemove?: CloudCpProjectScriptStep[];
		repos?: CloudCpProjectRepo[];
		agentStartsIn?: CloudCpAgentStartsIn;
		workingDirectory?: string;
		agentRules?: string;
		orchestratorRules?: string;
	};
};

export interface CloudCpProjectRepo {
	url: string;
	branch?: string;
}

export type CloudCpProjectScriptStep = string | { command: string; repository?: string; timeoutMinutes?: number };

export type CloudCpAgentStartsIn = "repository" | "parent";

export interface CloudCpCreateStandaloneProjectRequest {
	displayName: string;
	config?: Record<string, unknown>;
}

export interface CloudCpProject {
	id: string;
	orgId: string;
	displayName: string;
	repositoryUrl: string;
	defaultBranch: string;
	githubRepositoryId?: string;
	config: ProjectConfig;
	standaloneAgent?: boolean;
	createdAt: string;
	updatedAt: string;
}

export type CloudCpCreateStandaloneAgentRequest = Omit<CloudCpCreateSessionRequest, "projectId" | "kind">;

export interface CloudCpStandaloneAgentResponse {
	project: CloudCpProject;
	session: CloudCpSession;
}

export interface CloudCpCreateProjectRequest {
	displayName: string;
	repositoryUrl: string;
	defaultBranch: string;
	config?: Record<string, unknown>;
	coder?: CloudCpProjectCoderConfig;
}

export interface CloudCpProjectCoderConfig {
	templateId?: string;
	size?: "small" | "medium" | "large";
	startupScript?: string;
	extraRepos?: CloudCpSessionRepo[];
	workspaceNamePrefix?: string;
	parameters?: Record<string, string>;
}

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

export interface CloudCpProjectDeletedResponse {
	project: {
		id: string;
		deleted: boolean;
	};
}

export type CloudCpSessionKind = "worker" | "orchestrator";

export type CloudCpSessionMode = "read-only" | "standard" | "trusted";
export type CloudCpInterfaceMode = "tui" | "chat";

export interface CloudCpCreateSessionRequest {
	projectId: string;
	kind: CloudCpSessionKind;
	harness: string;
	displayName: string;
	prompt: string;
	mode?: CloudCpSessionMode;
	model?: string;
	reasoningEffort?: string;
	approvalMode?: "default" | "accept-edits" | "auto" | "bypass-permissions";
	deniedCommands?: string[];
	sandboxProviderConnectionId?: string;
	provider?: string;
	requestedOutput?: "artifact";
	interfaceMode?: CloudCpInterfaceMode;
	defaultInterfaceMode?: CloudCpInterfaceMode;
}

export interface CloudCpSessionRepo {
	url: string;
	branch?: string;
}

export interface CloudCpCoderTemplate {
	id: string;
	name: string;
	displayName: string;
	description: string;
	icon: string;
	parameters: string[];
}

export interface CloudCpCoderTemplatesResponse {
	templates: CloudCpCoderTemplate[];
}

export type CloudCpCoderTemplateParameterType = "string" | "number" | "bool" | "list(string)";

export interface CloudCpCoderTemplateParameterOption {
	name: string;
	value: string;
	description?: string;
}

export interface CloudCpCoderTemplateParameterDiagnostic {
	severity: string;
	summary: string;
	detail?: string;
}

export interface CloudCpCoderTemplateParameter {
	name: string;
	displayName?: string;
	description?: string;
	type: CloudCpCoderTemplateParameterType;
	formType?: string;
	mutable: boolean;
	required: boolean;
	defaultValue?: string;
	options?: CloudCpCoderTemplateParameterOption[];
	order?: number;
	diagnostics?: CloudCpCoderTemplateParameterDiagnostic[];
}

export interface CloudCpCoderTemplateParametersResponse {
	parameters: CloudCpCoderTemplateParameter[];
}

export type CloudCpCoderWorkerUser = "ao-worker" | "workspace";

export interface CloudCpOrgCoderConfig {
	baseUrl: string;
	owner?: string;
	defaultTemplateId?: string;
	templateId?: string;
	agentName?: string;
	endpointServiceName?: string;
	region?: string;
	parameters?: Record<string, string>;
	durableRoot?: string;
	requireMountedDurableRoot?: boolean;
	startupTimeoutSeconds?: number;
	workerUser?: CloudCpCoderWorkerUser;
	readyProbe?: string;
	readyProbeTimeoutSeconds?: number;
	reuseExistingCheckout?: boolean;
	workingDirectory?: string;
	protectSecretFiles?: boolean;
	tokenSet: boolean;
	editable?: boolean;
}

export interface CloudCpOrgCoderConfigResponse {
	coderConfig: CloudCpOrgCoderConfig | null;
	editable?: boolean;
}

export interface CloudCpPutOrgCoderConfigRequest {
	baseUrl: string;
	token?: string;
	owner?: string;
	defaultTemplateId?: string;
	agentName?: string;
	endpointServiceName?: string;
	region?: string;
	templateId?: string;
	parameters?: Record<string, string>;
	durableRoot?: string;
	requireMountedDurableRoot?: boolean;
	startupTimeoutSeconds?: number;
	workerUser?: CloudCpCoderWorkerUser;
	readyProbe?: string;
	readyProbeTimeoutSeconds?: number;
	reuseExistingCheckout?: boolean;
	workingDirectory?: string;
	protectSecretFiles?: boolean;
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
	model?: string;
	currentModel?: string;
	currentReasoningEffort?: string;
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
	startupError?: CloudCpSessionStartupError;
	coderWorkspaceUrl?: string;
	isTerminated: boolean;
	autoInjectCI?: boolean;
	autoInjectReview?: boolean;
	terminateOnPrMerge?: boolean;
	prs: CloudCpSessionPullRequest[];
	workerEpoch?: number;
	requestedOutput?: "artifact";
	requestedBy?: CloudCpPersonRef;
	createdByUserId?: string;
	outputType?: "none" | "pr" | "artifact" | "pr_artifact";
	artifactCount?: number;
	artifactsUpdatedAt?: string;
	artifactFiles?: CloudCpSessionArtifact[];
	pinned?: boolean;
	pinnedAt?: string;
	createdAt: string;
	updatedAt: string;
}

export interface CloudCpSessionPinResponse {
	sessionId: string;
	pinned: boolean;
	pinnedAt?: string;
}

export interface CloudCpSessionArtifact {
	path: string;
	name: string;
	kind: "html" | "markdown" | "file";
	contentType: string;
	size: number;
	sha256: string;
	updatedAt: string;
	registered?: boolean;
	id?: string;
	previousPaths?: string[];
}

export interface CloudCpArtifactVersionSummary {
	version: number;
	createdAt: string;
	fileFingerprint: string;
	size: number;
	source: "agent" | "user";
	harness?: string;
	restoredFrom?: number;
}

export interface CloudCpArtifactVersion extends CloudCpArtifactVersionSummary {
	path: string;
	content: string;
	truncated?: boolean;
}

export interface CloudCpSaveArtifactRequest {
	path: string;
	content: string;
	expectedSha256: string;
}

export interface CloudCpChatSearchHit {
	sessionId: string;
	sessionTitle: string;
	projectId: string;
	sequence: number;
	role: "user" | "assistant";
	snippet: string;
	createdAt: string;
}

export interface CloudCpChatSearchResponse {
	results: CloudCpChatSearchHit[];
}

export interface CloudCpArtifactAccessResponse {
	filesPrefix: string;
	expiresAt: string;
}

export interface CloudCpSessionStartupError {
	code: string;
	message: string;
	at: string;
	detail?: string;
}

export interface CloudCpRetrySessionStartupResponse {
	session: CloudCpSession;
}

export interface CloudCpInterfaceTransition {
	id: string;
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
	sourceHarness?: string;
	targetHarness?: string;
	sourceModel?: string;
	targetModel?: string;
	targetReasoningEffort?: string;
	sourceAccountId?: string;
	targetAccountId?: string;
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
	targetHarness?: string;
}

export interface CloudCpStartInterfaceTransitionResponse {
	transition: CloudCpInterfaceTransition;
}

export interface CloudCpCancelInterfaceTransitionResponse {
	ok: boolean;
}

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

export interface CloudCpWorkspaceDiffFile {
	path: string;
	status: "unmodified" | "modified" | "added" | "deleted" | "renamed" | "untracked" | "copied" | "changed";
	additions: number;
	deletions: number;
	binary: boolean;
}

export interface CloudCpWorkspaceDiff {
	files: CloudCpWorkspaceDiffFile[];
	diffBaseRef: string;
	diffBaseSha?: string;
	truncated: { combined: boolean; stats: boolean };
}

export interface CloudCpWorkspaceDiffFileDetail extends CloudCpWorkspaceDiffFile {
	size: number;
	deleted: boolean;
	content: string;
	contentTruncated: boolean;
	diff: string;
	diffTruncated: boolean;
}

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
	repositories?: CloudCpWorkspaceRepository[];
}

export interface CloudCpWorkspaceRepository {
	name: string;
	label?: string;
	repository?: string;
	branch?: string;
	main?: boolean;
	present: boolean;
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
	mode?: "content";
}

export interface CloudCpWorkspaceReviewSearchResponse {
	query: string;
	results: Array<Pick<CloudCpWorkspaceReviewFileSummary, "path" | "status" | "size" | "binary" | "fileFingerprint"> & {
		matches?: Array<{ line: number; text: string }>;
	}>;
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
	reviewComments: boolean;
	sourceBranch?: string;
	targetBranch?: string;
	updatedAt: string;
}

export interface CloudCpSessionChild extends CloudCpSession {
	prs: CloudCpSessionPullRequest[];
}

export interface CloudCpSessionChildrenResponse {
	items: CloudCpSessionChild[];
	page: CloudCpPageInfo;
}

export interface CloudCpPullRequestOpener {
	name: string;
	githubLogin?: string;
	avatarUrl?: string;
}

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
	openedBy?: CloudCpPullRequestOpener;
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
	linkedPullRequests?: CloudCpLinkedPullRequest[];
}

export interface CloudCpLinkedPullRequest {
	url: string;
	repository: string;
	number: number;
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
	interfaceMode?: "chat";
	createdAt: string;
	deliveredAt?: string;
	autoInjectReview: boolean;
}

export interface CloudCpReviewerSurface {
	mode: "chat";
	reviewId: string;
	harness: string;
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
	reviewerSurface?: CloudCpReviewerSurface;
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

export type CloudCpManagedHarness = "claude-code" | "codex" | "cursor";

export interface CloudCpHarnessVersion {
	agentId: CloudCpManagedHarness;
	defaultVersion: string;
	selectedVersion?: string;
	supersededVersion?: string;
	plan: {
		agentId: string;
		automatic: boolean;
		available: boolean;
		documentationUrl: string;
		method: string;
		methods: Array<{
			id: string;
			label: string;
			available: boolean;
			recommended: boolean;
			reinstallAvailable: boolean;
			updateAvailable: boolean;
			updateReason?: string;
		}>;
	};
	advisory: {
		agentId: string;
		checkedAt: string;
		currentVersion?: string;
		latestVersion?: string;
		maintenanceMethod?: string;
		source?: string;
		reason?: "lookup_failed" | "build_unordered";
		status: "unknown" | "current" | "behind_latest" | "ahead_of_latest";
	};
	job?: {
		target: string;
		status: "succeeded" | "failed";
		method?: string;
		error?: string;
		output?: string;
		startedAt?: string;
		finishedAt?: string;
		updatedAt?: string;
	};
	components?: Array<{ tool: string; version: string; defaultVersion: string }>;
}

export interface CloudCpHarnessVersionsResponse {
	editable: boolean;
	agents: CloudCpHarnessVersion[];
}
export interface CloudCpListSessionsQuery extends CloudCpListQuery {
	projectId?: string;
}

export interface CloudCpSessionDeletedResponse {
	session: {
		id: string;
		desiredState: string;
	};
	artifactsDeleted?: boolean;
}

export interface CloudCpResumeSessionResponse {
	session: {
		id: string;
		sandboxProvider: string;
		desiredState: string;
		observedState: string;
	};
}

export interface CloudCpRestoreSessionResponse {
	session: {
		id: string;
		desiredState: string;
	};
}

export interface CloudCpSendMessageRequest {
	text: string;
	attachmentIds?: string[];
	model?: string;
	reasoningEffort?: string;
	mode?: "read-only" | "standard" | "trusted";
	approvalMode?: "default" | "accept-edits" | "auto" | "bypass-permissions";
	composerEpoch?: number;
	displayText?: string;
	content?: Array<{ type: string }>;
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
		approvalModes?: Array<"default" | "accept-edits" | "auto" | "bypass-permissions">;
		providerId?: string;
		sessionOnly?: boolean;
	}>;
	source?: "worker" | "release";
	cliVersion?: string;
	accountListed?: boolean;
	sessionOnlyMarked?: boolean;
}

export interface CloudCpClientEvent {
	sessionId: string;
	sequence: number;
	type: string;
	payload: unknown;
	createdAt: string;
}

export interface CloudCpSendMessageResponse {
	event: CloudCpClientEvent;
}

export interface CloudCpCancelTurnResponse {
	ok: boolean;
}

export interface CloudCpSteerTurnResponse {
	event: CloudCpClientEvent;
}

export interface CloudCpEditQueuedMessageRequest {
	text: string;
	attachmentIds?: string[];
}

export interface CloudCpEditQueuedMessageResponse {
	event: CloudCpClientEvent;
}

export interface CloudCpChatEventsQuery {
	after?: number;
	limit?: number;
	types?: readonly string[];
	before?: number;
	since?: string;
}

export interface CloudCpChatEventsResponse {
	events: CloudCpClientEvent[];
	hasMore: boolean;
	nextAfter: number;
	hasOlder?: boolean;
	nextBefore?: number;
}

export interface CloudCpPromptSuggestionRequest {
	turnId: string;
	messages: { role: "user" | "assistant"; text: string }[];
}

export interface CloudCpPromptSuggestionResponse {
	turnId: string;
	prompt: string;
}

export type CloudCpTerminalKind = "workspace" | "agent";

export interface CloudCpTerminalTicketRequest {
	kind: CloudCpTerminalKind;
	terminalId?: string;
}

export interface CloudCpTerminalTicketResponse {
	ticket: string;
	expiresIn: number;
	scopes: string[];
}

export type CloudCpAgentProvider = "claude-code" | "codex" | "cursor" | "opencode";

export interface CloudCpPutAgentConnectionRequest {
	credentialType: string;
	secret: string;
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

export interface CloudCpProviderConnectionsResponse {
	providerConnections: CloudCpProviderConnection[];
}

export interface CloudCpProviderConnectionResponse {
	providerConnection: CloudCpProviderConnection;
}

export type CloudCpAgentAccount = CloudAgentAccount;
export type CloudCpAddAgentAccountRequest = AddAgentAccountInput;
export type CloudCpSessionAgentAccount = SessionAgentAccount;

export interface CloudCpAgentAccountsResponse {
	agentAccounts: CloudCpAgentAccount[];
}

export interface CloudCpAgentAccountResponse {
	agentAccount: CloudCpAgentAccount;
}

export interface CloudCpSwitchSessionAgentAccountRequest {
	accountId: string;
	policy?: "drain" | "interrupt";
}

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

export interface CloudCpGitHubInstallationsResponse {
	installations: CloudCpGitHubInstallation[];
}

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

export interface CloudCpGitHubUserConnection {
	connected: boolean;
	login?: string;
	avatarUrl?: string;
	installations: CloudCpGitHubUserInstallation[];
	lastSyncedAt?: string;
}

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

export interface CloudCpCreateGitHubProjectRequest {
	githubRepositoryId: string;
	displayName?: string;
	config?: Record<string, unknown>;
}

export interface CloudCpCreateGitHubRepositoryRequest {
	name: string;
	owner?: string;
	description?: string;
}

export interface CloudCpCreateGitHubRepositoryResponse {
	repository: CloudCpGitHubAppRepository;
	created: boolean;
	pushPath: string;
}

export interface CloudCpPlanLimits {
	plan: string;
	maxActiveSandboxes: number;
	maxRunningSandboxes?: number;
	orchestratorSlots: number;
	windowHours: number;
	windowSessionHours: number;
	weeklySessionHours: number;
	manualResetsPerMonth: number;
}

export interface CloudCpBillingUsage {
	windowUsedMinutes: number;
	windowLimitMinutes: number;
	windowHours: number;
	windowResetsAt?: string;
	weeklyUsedMinutes: number;
	weeklyLimitMinutes: number;
	weeklyResetsAt?: string;
	manualResetsAllowed: number;
	manualResetsUsed: number;
	manualResetsRenewAt?: string;
	activeOrchestrators: number;
	activeWorkers: number;
	runningOrchestrators: number;
	runningWorkers: number;
}

export interface CloudCpBillingSummary {
	enabled: boolean;
	exempt: boolean;
	billingMode?: CloudCpBillingMode;
	plan: string;
	subscriptionStatus?: string;
	entitled: boolean;
	currentPeriodEnd?: string;
	cancelAt?: string;
	limits?: CloudCpPlanLimits;
	usage?: CloudCpBillingUsage;
	plans?: CloudCpPlanOffer[];
}

export interface CloudCpPlanOffer {
	name: string;
	id: string;
	unitAmount?: number;
	currency?: string;
	interval?: string;
	limits?: CloudCpPlanLimits;
}

export interface CloudCpBillingLinkResponse {
	url: string;
}

export interface CloudCpComposerParticipant {
	userId: string;
	name: string;
}

export interface CloudCpComposerLease {
	holder: CloudCpComposerParticipant | null;
	epoch: number;
	waitlist: CloudCpComposerParticipant[];
	draft?: CloudCpComposerDraft | null;
}

export interface CloudCpComposerDraft {
	text: string;
	cursor: number;
}

export interface CloudCpComposerAnswer {
	composer: CloudCpComposerLease & {
		expiresInMs: number;
	};
	granted: boolean;
}

export interface CloudCpSessionViewer {
	userId: string;
	name: string;
	avatarUrl?: string;
	role: "member" | "editor" | "viewer";
}

export interface CloudCpSessionLive {
	type: "session.live";
	viewers: CloudCpSessionViewer[];
	composer: CloudCpComposerLease;
	canTakeOver: boolean;
	you: string;
	chatSettings: CloudCpSessionChatSettings;
	canConfigure: boolean;
	mergeRequestsVersion?: string;
}

export interface CloudCpPersonRef {
	userId: string;
	name: string;
}

export interface CloudCpMergeRequest {
	id: string;
	projectId: string;
	sessionId: string;
	sessionName: string;
	pullRequest: { id: string; number: number; url: string; title: string; repository: string };
	requestedBy: CloudCpPersonRef;
	status: "pending" | "merged" | "rejected";
	decidedBy?: CloudCpPersonRef;
	decidedAt?: string;
	createdAt: string;
}

export interface CloudCpSessionParticipant {
	userId: string;
	name: string;
	avatarUrl?: string;
	role: "owner" | "member" | "editor" | "viewer";
	present: boolean;
	state: "working" | "waiting" | "idle";
}

export interface CloudCpSharedSessionParticipants {
	sessionId: string;
	participants: CloudCpSessionParticipant[];
}

export interface CloudCpSessionChatSettings {
	model?: string;
	reasoningEffort?: string;
	mode?: "read-only" | "standard" | "trusted";
	approvalMode?: "default" | "accept-edits" | "auto" | "bypass-permissions";
}

export type CloudCpShareRole = "viewer" | "editor";

export interface CloudCpCreateShareLinkRequest {
	sessionId?: string;
	role?: CloudCpShareRole;
	accessScope?: "anyone" | "restricted";
	recipients?: string[];
	interaction?: "view" | "interact";
	modeCap?: "read-only" | "standard" | "trusted";
	deniedCommands?: string[];
	expiresInHours?: number;
}

export interface CloudCpShareLink {
	id: string;
	orgId: string;
	projectId: string;
	sessionId?: string;
	role: CloudCpShareRole;
	status: "active" | "revoked";
	accessScope: "anyone" | "restricted";
	recipients: string[];
	interaction: "view" | "interact";
	modeCap?: string;
	deniedCommands: string[];
	expiresAt?: string;
	createdAt: string;
	updatedAt: string;
	token?: string;
	url?: string;
	code?: string;
	shortUrl?: string;
	joinedCount?: number;
}

export interface CloudCpSharedProject {
	grant: {
		id: string;
		role: CloudCpShareRole;
		status: string;
		modeCap?: string;
		deniedCommands: string[];
		redeemedAt: string;
	};
	project: CloudCpProject;
	member?: boolean;
	sessionId?: string;
	sessionName?: string;
	sharedByName?: string;
	viaTeamId?: string;
	viaTeamName?: string;
	source?: "link" | "direct" | "team";
}

export interface CloudCpTeam {
	id: string;
	orgId: string;
	orgName?: string;
	name: string;
	memberCount: number;
	yourRole?: "owner" | "member";
	canManage: boolean;
	sharedSessions: number;
	sharedProjects: number;
	createdAt: string;
	updatedAt: string;
}

export interface CloudCpTeamMember {
	id: string;
	teamId: string;
	userId?: string;
	email?: string;
	name?: string;
	role: "owner" | "member";
	invited: boolean;
	you: boolean;
	workspaceMember: boolean;
	addedAt: string;
}

export interface CloudCpShareSubject {
	kind: "user" | "email" | "team";
	userId?: string;
	email?: string;
	name?: string;
	teamId?: string;
	teamName?: string;
	memberCount?: number;
	workspaceMember?: boolean;
}

export interface CloudCpShareEntry {
	grantId: string;
	subject: CloudCpShareSubject;
	scope: "session" | "project";
	sessionId?: string;
	sessionName?: string;
	projectId: string;
	projectName: string;
	role: CloudCpShareRole;
	modeCap?: "read-only" | "standard" | "trusted";
	deniedCommands: string[];
	source: "link" | "direct" | "team";
	sharedByName?: string;
	createdAt: string;
}

export interface CloudCpTeamDetail {
	team: CloudCpTeam;
	members: CloudCpTeamMember[];
	shares: CloudCpShareEntry[];
}

export interface CloudCpTeamImpact {
	people: number;
	sessions: number;
	projects: number;
	keeps: string[];
}

export interface CloudCpShareAccess {
	owner?: CloudCpShareSubject;
	entries: CloudCpShareEntry[];
}

export interface CloudCpEffectiveAccess {
	role?: "member" | "editor" | "viewer";
	modeCap?: "read-only" | "standard" | "trusted";
	source?: "workspace" | "team" | "direct" | "link" | "project";
	teamId?: string;
	teamName?: string;
}

export interface CloudCpTeamMemberAccess {
	member: CloudCpTeamMember;
	access: CloudCpEffectiveAccess;
}

export interface CloudCpShareCandidate {
	kind: "team" | "user";
	teamId?: string;
	name: string;
	email?: string;
	memberCount?: number;
	memberNames?: string[];
	workspaceMember?: boolean;
}

export interface CloudCpTeamMemberInput {
	email: string;
	role?: "owner" | "member";
}

export interface CloudCpCreateShareGrantsRequest {
	sessionId?: string;
	subjects: Array<{ teamId: string } | { email: string }>;
	role: CloudCpShareRole;
	modeCap?: "read-only" | "standard" | "trusted";
	deniedCommands?: string[];
}
