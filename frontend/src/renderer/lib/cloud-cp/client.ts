import { CloudCpAuthError, CloudCpError } from "./errors";
import { createSseFrameParser } from "./sse";
import type {
	CloudCpOffering,
	CloudCpAgentProvider,
	CloudCpAddAgentAccountRequest,
	CloudCpAgentAccountResponse,
	CloudCpAgentAccountsResponse,
	CloudCpSessionAgentAccount,
	CloudCpSwitchSessionAgentAccountRequest,
	CloudCpCancelTurnResponse,
	CloudCpChatEventsQuery,
	CloudCpChatEventsResponse,
	CloudCpPromptSuggestionRequest,
	CloudCpPromptSuggestionResponse,
	CloudCpChatModelsResponse,
	CloudCpCoderTemplateParametersResponse,
	CloudCpCoderTemplatesResponse,
	CloudCpClientEvent,
	CloudCpCreateOrganizationRequest,
	CloudCpCreateOrganizationResponse,
	CloudCpCreateGitHubProjectRequest,
	CloudCpCreateGitHubRepositoryRequest,
	CloudCpCreateGitHubRepositoryResponse,
	CloudCpCreateStandaloneProjectRequest,
	CloudCpCreateStandaloneAgentRequest,
	CloudCpProjectListQuery,
	CloudCpStandaloneAgentResponse,
	CloudCpCreateProjectRequest,
	CloudCpCreateSessionRequest,
	CloudCpErrorEnvelope,
	CloudCpInvitationsResponse,
	CloudCpListQuery,
	CloudCpListSessionsQuery,
	CloudCpMeResponse,
	CloudCpNotificationEventsResponse,
	CloudCpNotificationListQuery,
	CloudCpNotificationListResponse,
	CloudCpOrgCoderConfigResponse,
	CloudCpOrgMembersResponse,
	CloudCpBillingLinkResponse,
	CloudCpBillingSummary,
	CloudCpPutOrgCoderConfigRequest,
	CloudCpProjectDeletedResponse,
	CloudCpProjectListResponse,
	CloudCpProjectResponse,
	CloudCpSessionPullRequestsResponse,
	CloudCpSessionReviewState,
	CloudCpProjectSettingsRequest,
	CloudCpProjectEnvironment,
	CloudCpProjectIntakeStatus,
	CloudCpCue,
	CloudCpCueDefinition,
	CloudCpInvokeCueResult,
	CloudCpProviderConnectionResponse,
	CloudCpProviderConnectionsResponse,
	CloudCpGitHubReposResponse,
	CloudCpStartGitHubInstallationResponse,
	CloudCpGitHubInstallationsResponse,
	CloudCpSyncGitHubInstallationResponse,
	CloudCpGitHubUserConnection,
	CloudCpGitHubRepositoriesPage,
	CloudCpPutAgentConnectionRequest,
	CloudCpSendMessageRequest,
	CloudCpSendMessageResponse,
	CloudCpSteerTurnResponse,
	CloudCpEditQueuedMessageRequest,
	CloudCpEditQueuedMessageResponse,
	CloudCpComposerAnswer,
	CloudCpSessionChatSettings,
	CloudCpSharedSessionParticipants,
	CloudCpMergeRequest,
	CloudCpCreateShareLinkRequest,
	CloudCpShareLink,
	CloudCpSharedProject,
	CloudCpCreateShareGrantsRequest,
	CloudCpShareAccess,
	CloudCpShareCandidate,
	CloudCpShareEntry,
	CloudCpShareRole,
	CloudCpTeam,
	CloudCpTeamDetail,
	CloudCpTeamImpact,
	CloudCpTeamMemberAccess,
	CloudCpTeamMemberInput,
	CloudCpSession,
	CloudCpSessionChildrenResponse,
	CloudCpHarnessInspectResponse,
	CloudCpHarnessStatus,
	CloudCpHarnessVersion,
	CloudCpHarnessVersionsResponse,
	CloudCpSessionDeletedResponse,
	CloudCpSessionListResponse,
	CloudCpResumeSessionResponse,
	CloudCpRestoreSessionResponse,
	CloudCpRetrySessionStartupResponse,
	CloudCpSessionResponse,
	CloudCpSessionPinResponse,
	CloudCpArtifactAccessResponse,
	CloudCpArtifactVersion,
	CloudCpArtifactVersionSummary,
	CloudCpChatSearchResponse,
	CloudCpSaveArtifactRequest,
	CloudCpSessionArtifact,
	CloudCpAcknowledgeInterfaceTransitionNoticeResponse,
	CloudCpCancelInterfaceTransitionResponse,
	CloudCpInterfaceTransitionStatusResponse,
	CloudCpStartInterfaceTransitionRequest,
	CloudCpStartInterfaceTransitionResponse,
	CloudCpWorkspaceDiff,
	CloudCpWorkspaceDiffFileDetail,
	CloudCpWorkspaceReviewDiffsRequest,
	CloudCpWorkspaceReviewDiffsResponse,
	CloudCpWorkspaceReviewFileQuery,
	CloudCpWorkspaceReviewFileResponse,
	CloudCpWorkspaceReviewResponse,
	CloudCpWorkspaceReviewRevisionQuery,
	CloudCpWorkspaceReviewRevisionResponse,
	CloudCpWorkspaceReviewSearchQuery,
	CloudCpWorkspaceReviewSearchResponse,
	CloudCpWorkspaceReviewTreeResponse,
	CloudCpWorkspaceReviewWriteRequest,
	CloudCpWorkspaceReviewWriteResponse,
	CloudCpTerminalTicketRequest,
	CloudCpTerminalTicketResponse,
	CloudCpUpdateProjectRequest,
	CloudCpUpdateSessionPreferencesRequest,
} from "./types";

const API_PREFIX = "/api/cloud/v1";

export interface CloudCpClientOptions {
	baseUrl: string;
	getToken: () => Promise<string | null>;
	fetchImpl?: typeof fetch;
	onForbidden?: () => void;
	onPlanRefused?: (code: string) => void;
}

export interface CloudCpRequestOptions {
	signal?: AbortSignal;
}

export interface CloudCpWorkspaceRequestOptions extends CloudCpRequestOptions {
	repository?: string;
}

export interface CloudCpMutationOptions extends CloudCpRequestOptions {
	idempotencyKey?: string;
}

export interface CloudCpSessionEventsOptions {
	onEvent: (event: CloudCpClientEvent) => void;
	onError?: (error: CloudCpError) => void;
	signal?: AbortSignal;
	after?: number;
}

export interface CloudCpNotificationEventsOptions {
	onEvent: (event: import("./types").CloudCpNotificationEvent) => void;
	onError?: (error: CloudCpError) => void;
	signal?: AbortSignal;
	after?: number;
}

export interface CloudCpClient {
	me(options?: CloudCpRequestOptions): Promise<CloudCpMeResponse>;
	createOrganization(
		body: CloudCpCreateOrganizationRequest,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpCreateOrganizationResponse>;
	listMyInvitations(options?: CloudCpRequestOptions): Promise<CloudCpInvitationsResponse>;
	listOrgMembers(orgId: string, options?: CloudCpRequestOptions): Promise<CloudCpOrgMembersResponse>;

	listProjects(
		orgId: string,
		query?: CloudCpProjectListQuery,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpProjectListResponse>;
	createProject(
		orgId: string,
		body: CloudCpCreateProjectRequest,
		options?: CloudCpMutationOptions,
	): Promise<CloudCpProjectResponse>;
	getProject(orgId: string, projectId: string, options?: CloudCpRequestOptions): Promise<CloudCpProjectResponse>;
	updateProjectSettings(orgId: string, projectId: string, body: CloudCpProjectSettingsRequest, options?: CloudCpRequestOptions): Promise<CloudCpProjectResponse>;
	getProjectEnvironment(orgId: string, projectId: string, options?: CloudCpRequestOptions): Promise<CloudCpProjectEnvironment>;
	putProjectEnvironment(orgId: string, projectId: string, variables: Record<string, string>, options?: CloudCpRequestOptions): Promise<CloudCpProjectEnvironment>;
	getProjectIntakeStatus(orgId: string, projectId: string, options?: CloudCpRequestOptions): Promise<CloudCpProjectIntakeStatus>;
	listProjectCues(orgId: string, projectId: string, options?: CloudCpRequestOptions): Promise<{ cues: CloudCpCue[] }>;
	createProjectCue(orgId: string, projectId: string, body: CloudCpCueDefinition, options?: CloudCpRequestOptions): Promise<{ cue: CloudCpCue }>;
	updateProjectCue(orgId: string, cueId: string, body: CloudCpCueDefinition, options?: CloudCpRequestOptions): Promise<{ cue: CloudCpCue }>;
	deleteProjectCue(orgId: string, cueId: string, options?: CloudCpRequestOptions): Promise<void>;
	invokeProjectCue(orgId: string, cueId: string, sessionId: string, options?: CloudCpMutationOptions): Promise<CloudCpInvokeCueResult>;
	updateProject(
		orgId: string,
		projectId: string,
		body: CloudCpUpdateProjectRequest,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpProjectResponse>;
	deleteProject(
		orgId: string,
		projectId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpProjectDeletedResponse>;

	listSessions(
		orgId: string,
		query?: CloudCpListSessionsQuery,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpSessionListResponse>;
	createSession(
		orgId: string,
		body: CloudCpCreateSessionRequest,
		options?: CloudCpMutationOptions,
	): Promise<CloudCpSessionResponse>;
	getSession(orgId: string, sessionId: string, options?: CloudCpRequestOptions): Promise<CloudCpSessionResponse>;
	renameSession(orgId: string, sessionId: string, displayName: string, options?: CloudCpRequestOptions): Promise<CloudCpSessionResponse>;
	setSessionPinned(orgId: string, sessionId: string, pinned: boolean, options?: CloudCpRequestOptions): Promise<CloudCpSessionPinResponse>;
	createSessionArtifactAccess(
		orgId: string,
		sessionId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpArtifactAccessResponse>;
	listSessionArtifacts(
		orgId: string,
		sessionId: string,
		options?: CloudCpRequestOptions,
	): Promise<{ artifacts: CloudCpSessionArtifact[] }>;
	saveSessionArtifact(orgId: string, sessionId: string, body: CloudCpSaveArtifactRequest, options?: CloudCpRequestOptions): Promise<{ artifact: CloudCpSessionArtifact }>;
	listSessionArtifactVersions(orgId: string, sessionId: string, path: string, options?: CloudCpRequestOptions): Promise<{ versions: CloudCpArtifactVersionSummary[] }>;
	getSessionArtifactVersion(orgId: string, sessionId: string, path: string, version: number, options?: CloudCpRequestOptions): Promise<CloudCpArtifactVersion>;
	restoreSessionArtifactVersion(orgId: string, sessionId: string, path: string, version: number, options?: CloudCpRequestOptions): Promise<{ artifact: CloudCpSessionArtifact }>;
	restartSessionAgent(orgId: string, sessionId: string, options?: CloudCpRequestOptions): Promise<{ requested: boolean }>;
	searchChats(orgId: string, query: { query: string; limit?: number }, options?: CloudCpRequestOptions): Promise<CloudCpChatSearchResponse>;
	deleteSessionArtifact(orgId: string, sessionId: string, path: string, options?: CloudCpRequestOptions): Promise<void>;
	updateSessionPreferences(
		orgId: string,
		sessionId: string,
		body: CloudCpUpdateSessionPreferencesRequest,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpSessionResponse>;
	inspectSessionReviewerHarnesses(
		orgId: string,
		sessionId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpHarnessInspectResponse>;
	installSessionReviewerHarness(
		orgId: string,
		sessionId: string,
		harness: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpHarnessStatus>;
	listOrgHarnessVersions(orgId: string, query?: { refresh?: boolean }, options?: CloudCpRequestOptions): Promise<CloudCpHarnessVersionsResponse>;
	installOrgHarnessVersion(orgId: string, agent: string, expectedVersion: string, options?: CloudCpRequestOptions): Promise<CloudCpHarnessVersion>;
	resetOrgHarnessVersion(orgId: string, agent: string, options?: CloudCpRequestOptions): Promise<CloudCpHarnessVersion>;
	getInterfaceTransition(orgId: string, sessionId: string, options?: CloudCpRequestOptions): Promise<CloudCpInterfaceTransitionStatusResponse>;
	startInterfaceTransition(
		orgId: string,
		sessionId: string,
		body: CloudCpStartInterfaceTransitionRequest,
		options?: CloudCpMutationOptions,
	): Promise<CloudCpStartInterfaceTransitionResponse>;
	cancelInterfaceTransition(
		orgId: string,
		sessionId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpCancelInterfaceTransitionResponse>;
	acknowledgeInterfaceTransitionNotice(
		orgId: string,
		sessionId: string,
		transitionId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpAcknowledgeInterfaceTransitionNoticeResponse>;
	setSessionAutoInjectCI(orgId: string, sessionId: string, autoInjectCI: boolean, options?: CloudCpRequestOptions): Promise<CloudCpSessionResponse>;
	setSessionAutoInjectReview(orgId: string, sessionId: string, autoInjectReview: boolean, options?: CloudCpRequestOptions): Promise<CloudCpSessionResponse>;
	setSessionMergePolicy(orgId: string, sessionId: string, terminateOnPrMerge: boolean, options?: CloudCpRequestOptions): Promise<CloudCpSessionResponse>;
	listCoderTemplates(orgId: string, options?: CloudCpRequestOptions): Promise<CloudCpCoderTemplatesResponse>;
	getCoderTemplateParameters(
		orgId: string,
		templateId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpCoderTemplateParametersResponse>;
	evaluateCoderTemplateParameters(
		orgId: string,
		templateId: string,
		values: Readonly<Record<string, string>>,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpCoderTemplateParametersResponse>;
	getOrgCoderConfig(orgId: string, options?: CloudCpRequestOptions): Promise<CloudCpOrgCoderConfigResponse>;
	getBilling(orgId: string, options?: CloudCpRequestOptions): Promise<CloudCpBillingSummary>;
	createBillingCheckout(orgId: string, plan: string, options?: CloudCpRequestOptions): Promise<CloudCpBillingLinkResponse>;
	createBillingPortal(orgId: string, flow?: "cancel", options?: CloudCpRequestOptions): Promise<CloudCpBillingLinkResponse>;
	resetBillingUsage(orgId: string, options?: CloudCpRequestOptions): Promise<CloudCpBillingSummary>;
	touchSessionPresence(orgId: string, sessionId: string, options?: CloudCpRequestOptions): Promise<void>;
	putOrgSandboxProvider(orgId: string, defaultProvider: string, options?: CloudCpRequestOptions): Promise<CloudCpOffering>;
	putOrgCoderConfig(
		orgId: string,
		body: CloudCpPutOrgCoderConfigRequest,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpOrgCoderConfigResponse>;
	deleteOrgCoderConfig(orgId: string, options?: CloudCpRequestOptions): Promise<void>;
	listSessionChildren(
		orgId: string,
		sessionId: string,
		query?: CloudCpListQuery,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpSessionChildrenResponse>;
	listSessionPullRequests(
		orgId: string,
		sessionId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpSessionPullRequestsResponse>;
	getSessionReviewState(
		orgId: string,
		sessionId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpSessionReviewState>;
	triggerSessionReviews(
		orgId: string,
		sessionId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpSessionReviewState>;
	cancelSessionReviews(
		orgId: string,
		sessionId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpSessionReviewState>;
	mergePullRequest(orgId: string, sessionId: string, number: number, prUrl: string, expectedHeadSha: string, options?: CloudCpRequestOptions): Promise<{ status: string }>;
	requestPullRequestMerge(orgId: string, sessionId: string, number: number, prUrl: string, options?: CloudCpRequestOptions): Promise<{ mergeRequest: CloudCpMergeRequest }>;
	listProjectMergeRequests(orgId: string, projectId: string, options?: CloudCpRequestOptions): Promise<{ mergeRequests: CloudCpMergeRequest[] }>;
	approveMergeRequest(orgId: string, requestId: string, expectedHeadSha: string, options?: CloudCpRequestOptions): Promise<{ mergeRequest: CloudCpMergeRequest }>;
	rejectMergeRequest(orgId: string, requestId: string, options?: CloudCpRequestOptions): Promise<{ mergeRequest: CloudCpMergeRequest }>;
	deleteSession(
		orgId: string,
		sessionId: string,
		options?: CloudCpRequestOptions & { deleteArtifacts?: boolean },
	): Promise<CloudCpSessionDeletedResponse>;
	resumeSession(
		orgId: string,
		sessionId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpResumeSessionResponse>;
	requestWorkspaceCheckout(orgId: string, sessionId: string, options?: CloudCpRequestOptions): Promise<{ requested: boolean }>;
	getWorkspaceDiff(orgId: string, sessionId: string, options?: CloudCpRequestOptions): Promise<CloudCpWorkspaceDiff>;
	readWorkspaceDiffFile(
		orgId: string,
		sessionId: string,
		path: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpWorkspaceDiffFileDetail>;
	getWorkspaceReview(orgId: string, sessionId: string, options?: CloudCpWorkspaceRequestOptions): Promise<CloudCpWorkspaceReviewResponse>;
	getWorkspaceReviewTree(orgId: string, sessionId: string, path?: string, options?: CloudCpWorkspaceRequestOptions): Promise<CloudCpWorkspaceReviewTreeResponse>;
	searchWorkspaceReview(orgId: string, sessionId: string, query: CloudCpWorkspaceReviewSearchQuery, options?: CloudCpWorkspaceRequestOptions): Promise<CloudCpWorkspaceReviewSearchResponse>;
	getWorkspaceReviewFile(orgId: string, sessionId: string, query: CloudCpWorkspaceReviewFileQuery, options?: CloudCpWorkspaceRequestOptions): Promise<CloudCpWorkspaceReviewFileResponse>;
	getWorkspaceReviewDiffs(orgId: string, sessionId: string, body: CloudCpWorkspaceReviewDiffsRequest, options?: CloudCpWorkspaceRequestOptions): Promise<CloudCpWorkspaceReviewDiffsResponse>;
	getWorkspaceReviewRevision(orgId: string, sessionId: string, query: CloudCpWorkspaceReviewRevisionQuery, options?: CloudCpWorkspaceRequestOptions): Promise<CloudCpWorkspaceReviewRevisionResponse>;
	updateWorkspaceReviewFile(orgId: string, sessionId: string, body: CloudCpWorkspaceReviewWriteRequest, options?: CloudCpWorkspaceRequestOptions): Promise<CloudCpWorkspaceReviewWriteResponse>;
	restoreSession(
		orgId: string,
		sessionId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpRestoreSessionResponse>;
	retrySessionStartup(
		orgId: string,
		sessionId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpRetrySessionStartupResponse>;

	sendSessionMessage(
		orgId: string,
		sessionId: string,
		body: CloudCpSendMessageRequest,
		options?: CloudCpMutationOptions,
	): Promise<CloudCpSendMessageResponse>;
	sendSessionReviewToWorker(
		orgId: string,
		sessionId: string,
		reviewRunId: string,
		options?: CloudCpMutationOptions,
	): Promise<CloudCpSendMessageResponse>;
	listChatModels(orgId: string, sessionId: string, options?: CloudCpRequestOptions): Promise<CloudCpChatModelsResponse>;
	suggestChatPrompt(orgId: string, sessionId: string, body: CloudCpPromptSuggestionRequest, options?: CloudCpRequestOptions): Promise<CloudCpPromptSuggestionResponse>;
	listReviewChatEvents(orgId: string, sessionId: string, reviewRunId: string, query?: CloudCpChatEventsQuery, options?: CloudCpRequestOptions): Promise<CloudCpChatEventsResponse>;
	sendReviewChatMessage(orgId: string, sessionId: string, reviewRunId: string, text: string, options?: CloudCpMutationOptions): Promise<CloudCpSendMessageResponse>;
	interruptReviewChat(orgId: string, sessionId: string, reviewRunId: string, options?: CloudCpRequestOptions): Promise<void>;
	cancelTurn(
		orgId: string,
		sessionId: string,
		turnId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpCancelTurnResponse>;
	steerTurn(
		orgId: string,
		sessionId: string,
		turnId: string,
		body: CloudCpSendMessageRequest,
		options?: CloudCpMutationOptions,
	): Promise<CloudCpSteerTurnResponse>;
	editQueuedMessage(
		orgId: string,
		sessionId: string,
		turnId: string,
		body: CloudCpEditQueuedMessageRequest,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpEditQueuedMessageResponse>;
	steerQueuedMessage(
		orgId: string,
		sessionId: string,
		turnId: string,
		body: { composerEpoch?: number },
		options?: CloudCpMutationOptions,
	): Promise<CloudCpSteerTurnResponse>;
	deleteQueuedMessage(
		orgId: string,
		sessionId: string,
		turnId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpCancelTurnResponse & {
		pending?: boolean;
	}>;
	decideChatApproval(orgId: string, sessionId: string, requestId: string, decisionId: string, options?: CloudCpRequestOptions): Promise<{ ok: boolean }>;
	acquireComposer(orgId: string, sessionId: string, options?: CloudCpRequestOptions): Promise<CloudCpComposerAnswer>;
	renewComposer(orgId: string, sessionId: string, epoch: number, typing: boolean, options?: CloudCpRequestOptions): Promise<CloudCpComposerAnswer>;
	releaseComposer(orgId: string, sessionId: string, epoch: number, options?: CloudCpRequestOptions): Promise<CloudCpComposerAnswer>;
	requestComposerTurn(orgId: string, sessionId: string, options?: CloudCpRequestOptions): Promise<CloudCpComposerAnswer>;
	leaveComposerQueue(orgId: string, sessionId: string, options?: CloudCpRequestOptions): Promise<CloudCpComposerAnswer>;
	takeOverComposer(orgId: string, sessionId: string, options?: CloudCpRequestOptions): Promise<CloudCpComposerAnswer>;
	setSessionChatSettings(orgId: string, sessionId: string, settings: CloudCpSessionChatSettings, options?: CloudCpRequestOptions): Promise<{ chatSettings: CloudCpSessionChatSettings }>;
	listSessionParticipants(orgId: string, sessionIds: readonly string[], options?: CloudCpRequestOptions): Promise<{ sessions: CloudCpSharedSessionParticipants[] }>;
	publishComposerDraft(orgId: string, sessionId: string, epoch: number, text: string, cursor: number, options?: CloudCpRequestOptions): Promise<CloudCpComposerAnswer>;
	createShareLink(orgId: string, projectId: string, body: CloudCpCreateShareLinkRequest, options?: CloudCpRequestOptions): Promise<{ link: CloudCpShareLink }>;
	listShareLinks(orgId: string, projectId: string, options?: CloudCpRequestOptions): Promise<{ links: CloudCpShareLink[] }>;
	revokeShareLink(orgId: string, projectId: string, linkId: string, options?: CloudCpRequestOptions & { removeJoined?: boolean }): Promise<{ removedGrants?: number }>;
	redeemShareLink(orgId: string, token: string, options?: CloudCpRequestOptions): Promise<{ shared: CloudCpSharedProject }>;
	redeemShareLinkCode(code: string, options?: CloudCpRequestOptions): Promise<{ shared: CloudCpSharedProject }>;
	listSharedProjects(options?: CloudCpRequestOptions): Promise<{ shared: CloudCpSharedProject[] }>;
	listSharedProjectSessions(orgId: string, projectId: string, options?: CloudCpRequestOptions): Promise<{ sessions: CloudCpSession[] }>;
	listTeams(orgId: string, options?: CloudCpRequestOptions): Promise<{ teams: CloudCpTeam[] }>;
	createTeam(orgId: string, body: { name: string; members?: CloudCpTeamMemberInput[] }, options?: CloudCpRequestOptions): Promise<CloudCpTeamDetail>;
	getTeam(orgId: string, teamId: string, options?: CloudCpRequestOptions): Promise<CloudCpTeamDetail>;
	renameTeam(orgId: string, teamId: string, name: string, options?: CloudCpRequestOptions): Promise<CloudCpTeamDetail>;
	deleteTeam(orgId: string, teamId: string, options?: CloudCpRequestOptions): Promise<void>;
	teamImpact(orgId: string, teamId: string, options?: CloudCpRequestOptions): Promise<CloudCpTeamImpact>;
	addTeamMembers(orgId: string, teamId: string, members: CloudCpTeamMemberInput[], options?: CloudCpRequestOptions): Promise<CloudCpTeamDetail>;
	updateTeamMember(orgId: string, teamId: string, memberId: string, role: "owner" | "member", options?: CloudCpRequestOptions): Promise<CloudCpTeamDetail>;
	removeTeamMember(orgId: string, teamId: string, memberId: string, options?: CloudCpRequestOptions): Promise<void>;
	teamMemberImpact(orgId: string, teamId: string, memberId: string, options?: CloudCpRequestOptions): Promise<CloudCpTeamImpact>;
	listMyTeams(options?: CloudCpRequestOptions): Promise<{ teams: CloudCpTeam[] }>;
	getMyTeam(teamId: string, options?: CloudCpRequestOptions): Promise<CloudCpTeamDetail>;
	leaveTeam(teamId: string, options?: CloudCpRequestOptions): Promise<void>;
	leaveTeamImpact(teamId: string, options?: CloudCpRequestOptions): Promise<CloudCpTeamImpact>;
	listShareCandidates(orgId: string, query: string, options?: CloudCpRequestOptions): Promise<{ candidates: CloudCpShareCandidate[] }>;
	createShareGrants(orgId: string, projectId: string, body: CloudCpCreateShareGrantsRequest, options?: CloudCpRequestOptions): Promise<{ entries: CloudCpShareEntry[] }>;
	getShareAccess(orgId: string, projectId: string, sessionId: string | undefined, options?: CloudCpRequestOptions): Promise<CloudCpShareAccess>;
	getTeamShareAccess(orgId: string, projectId: string, sessionId: string | undefined, teamId: string, options?: CloudCpRequestOptions): Promise<{ members: CloudCpTeamMemberAccess[] }>;
	updateShareEntry(orgId: string, projectId: string, grantId: string, body: { role: CloudCpShareRole; modeCap?: "read-only" | "standard" | "trusted" }, options?: CloudCpRequestOptions): Promise<{ entry: CloudCpShareEntry }>;
	revokeShareGrant(orgId: string, projectId: string, grantId: string, options?: CloudCpRequestOptions): Promise<void>;
	listChatEvents(
		orgId: string,
		sessionId: string,
		query?: CloudCpChatEventsQuery,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpChatEventsResponse>;
	subscribeSessionEvents(orgId: string, sessionId: string, options: CloudCpSessionEventsOptions): Promise<void>;
	listNotifications(orgId: string, query?: CloudCpNotificationListQuery, options?: CloudCpRequestOptions): Promise<CloudCpNotificationListResponse>;
	listNotificationEvents(orgId: string, after?: number, options?: CloudCpRequestOptions): Promise<CloudCpNotificationEventsResponse>;
	markNotificationsRead(orgId: string, notificationIds?: string[], options?: CloudCpRequestOptions): Promise<{ updated: number }>;
	subscribeNotificationEvents(orgId: string, options: CloudCpNotificationEventsOptions): Promise<void>;

	createTerminalTicket(
		orgId: string,
		sessionId: string,
		body: CloudCpTerminalTicketRequest,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpTerminalTicketResponse>;

	listUserProviderConnections(options?: CloudCpRequestOptions): Promise<CloudCpProviderConnectionsResponse>;
	listUserAgentModels(orgId: string, agent: CloudCpAgentProvider, options?: CloudCpRequestOptions): Promise<CloudCpChatModelsResponse>;
	putUserAgentConnection(
		agent: CloudCpAgentProvider,
		body: CloudCpPutAgentConnectionRequest,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpProviderConnectionResponse>;
	listAgentAccounts(options?: CloudCpRequestOptions): Promise<CloudCpAgentAccountsResponse>;
	addAgentAccount(body: CloudCpAddAgentAccountRequest, options?: CloudCpRequestOptions): Promise<CloudCpAgentAccountResponse>;
	renameAgentAccount(accountId: string, name: string, options?: CloudCpRequestOptions): Promise<CloudCpAgentAccountResponse>;
	removeAgentAccount(accountId: string, options?: CloudCpRequestOptions): Promise<void>;
	makeAgentAccountDefault(accountId: string, options?: CloudCpRequestOptions): Promise<CloudCpAgentAccountResponse>;
	replaceAgentAccountCredential(accountId: string, body: CloudCpPutAgentConnectionRequest, options?: CloudCpRequestOptions): Promise<CloudCpAgentAccountResponse>;
	getSessionAgentAccount(orgId: string, sessionId: string, options?: CloudCpRequestOptions): Promise<CloudCpSessionAgentAccount>;
	switchSessionAgentAccount(
		orgId: string,
		sessionId: string,
		body: CloudCpSwitchSessionAgentAccountRequest,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpStartInterfaceTransitionResponse>;
	deleteGitHubPAT(options?: CloudCpRequestOptions): Promise<void>;
	listGitHubRepos(options?: CloudCpRequestOptions): Promise<CloudCpGitHubReposResponse>;

	startGitHubInstallation(
		orgId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpStartGitHubInstallationResponse>;
	getGitHubUser(options?: CloudCpRequestOptions): Promise<CloudCpGitHubUserConnection>;
	listGitHubInstallations(
		orgId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpGitHubInstallationsResponse>;
	syncGitHubInstallation(
		orgId: string,
		installationId: string,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpSyncGitHubInstallationResponse>;
	listGitHubRepositories(
		orgId: string,
		query?: CloudCpListQuery,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpGitHubRepositoriesPage>;
	createGitHubProject(
		orgId: string,
		body: CloudCpCreateGitHubProjectRequest,
		options?: CloudCpMutationOptions,
	): Promise<CloudCpProjectResponse>;
	createGitHubRepository(
		orgId: string,
		body: CloudCpCreateGitHubRepositoryRequest,
		options?: CloudCpRequestOptions,
	): Promise<CloudCpCreateGitHubRepositoryResponse>;
	createStandaloneProject(
		orgId: string,
		body: CloudCpCreateStandaloneProjectRequest,
		options?: CloudCpMutationOptions,
	): Promise<CloudCpProjectResponse>;
	createStandaloneAgent(
		orgId: string,
		body: CloudCpCreateStandaloneAgentRequest,
		options?: CloudCpMutationOptions,
	): Promise<CloudCpStandaloneAgentResponse>;
}

type QueryParams = Record<string, string | number | readonly string[] | undefined>;

interface SendOptions {
	body?: unknown;
	query?: QueryParams;
	signal?: AbortSignal;
	idempotencyKey?: string;
	accept?: string;
}

function buildUrl(baseUrl: string, path: string, query?: QueryParams): string {
	let url = `${baseUrl.replace(/\/+$/, "")}${API_PREFIX}${path}`;
	if (query !== undefined) {
		const params = new URLSearchParams();
		for (const [key, value] of Object.entries(query)) {
			if (Array.isArray(value)) {
				for (const item of value) params.append(key, item);
			} else if (value !== undefined) {
				params.set(key, String(value));
			}
		}
		const search = params.toString();
		if (search !== "") url += `?${search}`;
	}
	return url;
}

function repositoryQuery(options: CloudCpWorkspaceRequestOptions | undefined): QueryParams | undefined {
	const repository = options?.repository?.trim();
	return repository ? { repository } : undefined;
}

function seg(value: string): string {
	return encodeURIComponent(value);
}

export const PLAN_REFUSAL_CODES: ReadonlySet<string> = new Set([
	"PLAN_REQUIRED",
	"ORCHESTRATOR_SLOTS_FULL",
	"WORKER_SLOTS_FULL",
	"SESSION_LIMIT_REACHED",
	"WINDOW_LIMIT_REACHED",
	"WEEKLY_LIMIT_REACHED",
]);

export function retryAfterMs(value: string | null, now = Date.now()): number | undefined {
	const header = value?.trim();
	if (!header) return undefined;
	if (/^\d+$/.test(header)) return Number(header) * 1_000;
	const at = Date.parse(header);
	return Number.isFinite(at) ? Math.max(0, at - now) : undefined;
}

async function errorFromResponse(response: Response): Promise<CloudCpError> {
	let message = `Cloud control plane request failed with status ${response.status}.`;
	let code: string | undefined;
	let requestId: string | undefined;
	try {
		const body = (await response.json()) as Partial<CloudCpErrorEnvelope> | null;
		if (typeof body?.message === "string" && body.message !== "") {
			message = body.message;
		} else if (typeof body?.error === "string" && body.error !== "") {
			message = body.error;
		}
		if (typeof body?.code === "string" && body.code !== "") code = body.code;
		if (typeof body?.requestId === "string" && body.requestId !== "") requestId = body.requestId;
	} catch {
	}
	const options = { status: response.status, code, requestId, retryAfterMs: retryAfterMs(response.headers.get("retry-after")) };
	return response.status === 401 ? new CloudCpAuthError(message, options) : new CloudCpError(message, options);
}

function toCloudCpError(error: unknown): CloudCpError {
	if (error instanceof CloudCpError) return error;
	const message = error instanceof Error ? error.message : String(error);
	return new CloudCpError(message, { status: 0, cause: error });
}

function isAbortError(error: unknown): boolean {
	return error instanceof Error && error.name === "AbortError";
}

function newIdempotencyKey(): string {
	return crypto.randomUUID();
}

export const MAX_GITHUB_REPOSITORY_DESCRIPTION = 350;

function withoutProjectLayout(body: CloudCpProjectSettingsRequest): CloudCpProjectSettingsRequest {
	if (!body.config || !("layout" in body.config)) return body;
	const { layout: _layout, ...config } = body.config as Record<string, unknown>;
	return { ...body, config: config as CloudCpProjectSettingsRequest["config"] };
}

export function createCloudCpClient(options: CloudCpClientOptions): CloudCpClient {
	const { baseUrl, getToken, onForbidden, onPlanRefused } = options;
	const doFetch: typeof fetch = options.fetchImpl ?? ((input, init) => fetch(input, init));

	async function send(method: string, path: string, init: SendOptions = {}): Promise<Response> {
		const token = await getToken();
		if (token === null || token === "") {
			throw new CloudCpAuthError("No cloud control-plane token is available. Sign in and try again.", {
				status: 401,
				code: "no_token",
			});
		}
		const headers = new Headers({
			Authorization: `Bearer ${token}`,
			Accept: init.accept ?? "application/json",
		});
		if (init.body !== undefined) headers.set("Content-Type", "application/json");
		if (init.idempotencyKey !== undefined) headers.set("Idempotency-Key", init.idempotencyKey);
		const response = await doFetch(buildUrl(baseUrl, path, init.query), {
			method,
			headers,
			body: init.body === undefined ? undefined : JSON.stringify(init.body),
			signal: init.signal,
		});
		if (!response.ok) {
			if (response.status === 403) onForbidden?.();
			const error = await errorFromResponse(response);
			if (error.code && PLAN_REFUSAL_CODES.has(error.code)) onPlanRefused?.(error.code);
			throw error;
		}
		return response;
	}

	async function requestJson<T>(method: string, path: string, init: SendOptions = {}): Promise<T> {
		const response = await send(method, path, init);
		return (await response.json()) as T;
	}

	async function requestVoid(method: string, path: string, init: SendOptions = {}): Promise<void> {
		await send(method, path, init);
	}

	async function subscribeSessionEvents(
		orgId: string,
		sessionId: string,
		subscribeOptions: CloudCpSessionEventsOptions,
	): Promise<void> {
		const { onEvent, onError, signal, after } = subscribeOptions;
		const fail = (error: unknown): void => {
			if (signal?.aborted === true || isAbortError(error)) return;
			onError?.(toCloudCpError(error));
		};

		let response: Response;
		try {
			response = await send("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/events`, {
				query: { after },
				signal,
				accept: "text/event-stream",
			});
		} catch (error) {
			fail(error);
			return;
		}
		if (response.body === null) {
			fail(
				new CloudCpError("The event stream response has no body.", {
					status: response.status,
				}),
			);
			return;
		}

		const reader = response.body.getReader();
		const decoder = new TextDecoder();
		const parser = createSseFrameParser();
		const deliver = (data: string): void => {
			let event: CloudCpClientEvent;
			try {
				event = JSON.parse(data) as CloudCpClientEvent;
			} catch {
				fail(new CloudCpError("The event stream sent a frame with malformed JSON.", { status: 200 }));
				return;
			}
			onEvent(event);
		};
		try {
			for (;;) {
				const { done, value } = await reader.read();
				if (value !== undefined) {
					for (const frame of parser.push(decoder.decode(value, { stream: true }))) deliver(frame.data);
				}
				if (done) break;
			}
			for (const frame of parser.push(decoder.decode())) deliver(frame.data);
			for (const frame of parser.flush()) deliver(frame.data);
		} catch (error) {
			fail(error);
		} finally {
			reader.releaseLock();
		}
	}

	async function subscribeNotificationEvents(orgId: string, subscribeOptions: CloudCpNotificationEventsOptions): Promise<void> {
		const { onEvent, onError, signal, after } = subscribeOptions;
		const fail = (error: unknown): void => {
			if (signal?.aborted === true || isAbortError(error)) return;
			onError?.(toCloudCpError(error));
		};
		let response: Response;
		try {
			response = await send("GET", `/orgs/${seg(orgId)}/notification-events`, { query: { after }, signal, accept: "text/event-stream" });
		} catch (error) { fail(error); return; }
		if (response.body === null) { fail(new CloudCpError("The notification stream response has no body.", { status: response.status })); return; }
		const reader = response.body.getReader();
		const decoder = new TextDecoder();
		const parser = createSseFrameParser();
		try {
			for (;;) {
				const { done, value } = await reader.read();
				if (value !== undefined) for (const frame of parser.push(decoder.decode(value, { stream: true }))) {
					try { onEvent(JSON.parse(frame.data)); } catch { fail(new CloudCpError("The notification stream sent a frame with malformed JSON.", { status: 200 })); }
				}
				if (done) break;
			}
		} catch (error) { fail(error); } finally { reader.releaseLock(); }
	}

	return {
		me: (o) => requestJson("GET", "/me", { signal: o?.signal }),
		createOrganization: (body, o) => requestJson("POST", "/orgs", { body, signal: o?.signal }),
		listMyInvitations: (o) => requestJson("GET", "/invitations", { signal: o?.signal }),
		listOrgMembers: (orgId, o) => requestJson("GET", `/orgs/${seg(orgId)}/members`, { signal: o?.signal }),

		listProjects: (orgId, query, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/projects`, {
				query: { limit: query?.limit, cursor: query?.cursor, standaloneAgents: query?.standaloneAgents },
				signal: o?.signal,
			}),
		createProject: (orgId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/projects`, {
				body,
				signal: o?.signal,
				idempotencyKey: o?.idempotencyKey ?? newIdempotencyKey(),
			}),
		updateProject: (orgId, projectId, body, o) =>
			requestJson("PATCH", `/orgs/${seg(orgId)}/projects/${seg(projectId)}`, {
				body,
				signal: o?.signal,
			}),
		getProject: (orgId, projectId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/projects/${seg(projectId)}`, { signal: o?.signal }),
		updateProjectSettings: (orgId, projectId, body, o) =>
			requestJson("PATCH", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/settings`, { body: withoutProjectLayout(body), signal: o?.signal }),
		getProjectEnvironment: (orgId, projectId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/environment`, { signal: o?.signal }),
		putProjectEnvironment: (orgId, projectId, variables, o) =>
			requestJson("PUT", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/environment`, { body: { variables }, signal: o?.signal }),
		getProjectIntakeStatus: (orgId, projectId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/intake`, { signal: o?.signal }),
		listProjectCues: (orgId, projectId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/cues`, { signal: o?.signal }),
		createProjectCue: (orgId, projectId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/cues`, { body, signal: o?.signal }),
		updateProjectCue: (orgId, cueId, body, o) =>
			requestJson("PATCH", `/orgs/${seg(orgId)}/cues/${seg(cueId)}`, { body, signal: o?.signal }),
		deleteProjectCue: (orgId, cueId, o) =>
			requestVoid("DELETE", `/orgs/${seg(orgId)}/cues/${seg(cueId)}`, { signal: o?.signal }),
		invokeProjectCue: (orgId, cueId, sessionId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/cues/${seg(cueId)}/invoke`, {
				body: { sessionId },
				signal: o?.signal,
				idempotencyKey: o?.idempotencyKey ?? newIdempotencyKey(),
			}),
		deleteProject: (orgId, projectId, o) =>
			requestJson("DELETE", `/orgs/${seg(orgId)}/projects/${seg(projectId)}`, {
				signal: o?.signal,
			}),

		listSessions: (orgId, query, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions`, {
				query: {
					projectId: query?.projectId,
					limit: query?.limit,
					cursor: query?.cursor,
				},
				signal: o?.signal,
			}),
		createSession: (orgId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions`, {
				body,
				signal: o?.signal,
				idempotencyKey: o?.idempotencyKey ?? newIdempotencyKey(),
			}),
		getSession: (orgId, sessionId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}`, { signal: o?.signal }),
		renameSession: (orgId, sessionId, displayName, o) =>
			requestJson("PATCH", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}`, { body: { displayName }, signal: o?.signal }),
		setSessionPinned: (orgId, sessionId, pinned, o) =>
			requestJson(pinned ? "POST" : "DELETE", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/pin`, { signal: o?.signal }),
		createSessionArtifactAccess: (orgId, sessionId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/artifacts/access`, { signal: o?.signal }),
		listSessionArtifacts: (orgId, sessionId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/artifacts`, { signal: o?.signal }),
		saveSessionArtifact: (orgId, sessionId, body, o) =>
			requestJson("PUT", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/artifacts/content`, { body, signal: o?.signal }),
		listSessionArtifactVersions: (orgId, sessionId, path, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/artifacts/versions`, { query: { path }, signal: o?.signal }),
		getSessionArtifactVersion: (orgId, sessionId, path, version, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/artifacts/versions/${seg(String(version))}`, { query: { path }, signal: o?.signal }),
		restoreSessionArtifactVersion: (orgId, sessionId, path, version, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/artifacts/versions/restore`, { body: { path, version }, signal: o?.signal }),
		restartSessionAgent: (orgId, sessionId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/agent/restart`, { signal: o?.signal }),
		searchChats: (orgId, query, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/chat-search`, { query: { q: query.query, limit: query.limit }, signal: o?.signal }),
		deleteSessionArtifact: (orgId, sessionId, path, o) =>
			requestVoid("DELETE", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/artifacts?path=${encodeURIComponent(path)}`, { signal: o?.signal }),
		getInterfaceTransition: (orgId, sessionId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/interface-transition`, { signal: o?.signal }),
		startInterfaceTransition: (orgId, sessionId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/interface-transition`, {
				body,
				signal: o?.signal,
				idempotencyKey: o?.idempotencyKey ?? newIdempotencyKey(),
			}),
		cancelInterfaceTransition: (orgId, sessionId, o) =>
			requestJson("DELETE", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/interface-transition`, {
				signal: o?.signal,
			}),
		acknowledgeInterfaceTransitionNotice: (orgId, sessionId, transitionId, o) =>
			requestJson(
				"PUT",
				`/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/interface-transition/${seg(transitionId)}/notice-acknowledgement`,
				{ signal: o?.signal },
			),
		setSessionAutoInjectCI: (orgId, sessionId, autoInjectCI, o) =>
			requestJson("PATCH", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/auto-inject-ci`, { body: { autoInjectCI }, signal: o?.signal }),
		setSessionAutoInjectReview: (orgId, sessionId, autoInjectReview, o) =>
			requestJson("PATCH", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/auto-inject-review`, { body: { autoInjectReview }, signal: o?.signal }),
		setSessionMergePolicy: (orgId, sessionId, terminateOnPrMerge, o) =>
			requestJson("PATCH", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/merge-policy`, { body: { terminateOnPrMerge }, signal: o?.signal }),
		updateSessionPreferences: (orgId, sessionId, body, o) =>
			requestJson("PATCH", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/preferences`, { body, signal: o?.signal }),
		inspectSessionReviewerHarnesses: (orgId, sessionId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/reviewer-harnesses`, { signal: o?.signal }),
		installSessionReviewerHarness: (orgId, sessionId, harness, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/reviewer-harnesses/${seg(harness)}/install`, {
				signal: o?.signal,
			}),
		listOrgHarnessVersions: (orgId, query, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/harness-versions`, {
				query: { refresh: query?.refresh ? "true" : undefined },
				signal: o?.signal,
			}),
		installOrgHarnessVersion: (orgId, agent, expectedVersion, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/harness-versions/${seg(agent)}/install`, {
				body: { method: "ao-cloud", operation: "update", expectedVersion },
				signal: o?.signal,
			}),
		resetOrgHarnessVersion: (orgId, agent, o) =>
			requestJson("DELETE", `/orgs/${seg(orgId)}/harness-versions/${seg(agent)}`, { signal: o?.signal }),
		listCoderTemplates: (orgId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sandbox/coder/templates`, { signal: o?.signal }),
		getCoderTemplateParameters: (orgId, templateId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/coder-templates/${seg(templateId)}/parameters`, { signal: o?.signal }),
		evaluateCoderTemplateParameters: (orgId, templateId, values, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/coder-templates/${seg(templateId)}/parameters/evaluate`, {
				body: { values },
				signal: o?.signal,
			}),
		getOrgCoderConfig: (orgId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/coder-config`, { signal: o?.signal }),
		getBilling: (orgId, o) => requestJson("GET", `/orgs/${seg(orgId)}/billing`, { signal: o?.signal }),
		createBillingCheckout: (orgId, plan, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/billing/checkout`, { body: { plan }, signal: o?.signal }),
		createBillingPortal: (orgId, flow, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/billing/portal`, { body: flow ? { flow } : {}, signal: o?.signal }),
		resetBillingUsage: (orgId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/billing/usage/reset`, { body: {}, signal: o?.signal }),
		touchSessionPresence: (orgId, sessionId, o) =>
			requestVoid("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/presence`, { signal: o?.signal }),
		putOrgSandboxProvider: (orgId, defaultProvider, o) =>
			requestJson("PUT", `/orgs/${seg(orgId)}/sandbox-provider`, { body: { defaultProvider }, signal: o?.signal }),
		putOrgCoderConfig: (orgId, body, o) =>
			requestJson("PUT", `/orgs/${seg(orgId)}/coder-config`, { body, signal: o?.signal }),
		deleteOrgCoderConfig: (orgId, o) =>
			requestVoid("DELETE", `/orgs/${seg(orgId)}/coder-config`, { signal: o?.signal }),
		listSessionChildren: (orgId, sessionId, query, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/children`, {
				query: { limit: query?.limit, cursor: query?.cursor },
				signal: o?.signal,
			}),
		listSessionPullRequests: (orgId, sessionId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/pull-requests`, {
				signal: o?.signal,
			}),
		requestPullRequestMerge: (orgId, sessionId, number, prUrl, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/pull-requests/${seg(String(number))}/merge-requests`, {
				body: { prUrl }, signal: o?.signal,
			}),
		listProjectMergeRequests: (orgId, projectId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/merge-requests`, { signal: o?.signal }),
		approveMergeRequest: (orgId, requestId, expectedHeadSha, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/merge-requests/${seg(requestId)}/approve`, { body: { expectedHeadSha }, signal: o?.signal }),
		rejectMergeRequest: (orgId, requestId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/merge-requests/${seg(requestId)}/reject`, { signal: o?.signal }),
		mergePullRequest: (orgId, sessionId, number, prUrl, expectedHeadSha, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/pull-requests/${seg(String(number))}/merge`, {
				body: { prUrl, expectedHeadSha }, signal: o?.signal,
			}),
		getSessionReviewState: (orgId, sessionId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/reviews`, { signal: o?.signal }),
		triggerSessionReviews: (orgId, sessionId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/reviews/trigger`, { signal: o?.signal }),
		cancelSessionReviews: (orgId, sessionId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/reviews/cancel`, { signal: o?.signal }),
		deleteSession: (orgId, sessionId, o) =>
			requestJson("DELETE", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}${o?.deleteArtifacts ? "?deleteArtifacts=true" : ""}`, {
				signal: o?.signal,
			}),
		resumeSession: (orgId, sessionId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/resume`, {
				signal: o?.signal,
			}),
		requestWorkspaceCheckout: (orgId, sessionId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/workspace/checkout`, {
				signal: o?.signal,
			}),
		getWorkspaceDiff: (orgId, sessionId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/workspace/diff`, {
				signal: o?.signal,
			}),
		readWorkspaceDiffFile: (orgId, sessionId, path, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/workspace/file/diff`, {
				query: { path },
				signal: o?.signal,
			}),
		getWorkspaceReview: (orgId, sessionId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/workspace/review`, {
				query: repositoryQuery(o), signal: o?.signal,
			}),
		getWorkspaceReviewTree: (orgId, sessionId, path, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/workspace/tree`, {
				query: { path, ...repositoryQuery(o) }, signal: o?.signal,
			}),
		searchWorkspaceReview: (orgId, sessionId, query, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/workspace/search`, {
				query: { query: query.query, cursor: query.cursor, limit: query.limit, mode: query.mode, ...repositoryQuery(o) }, signal: o?.signal,
			}),
		getWorkspaceReviewFile: (orgId, sessionId, query, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/workspace/review/file`, {
				query: { path: query.path, scope: query.scope, commitSha: query.commitSha, ...repositoryQuery(o) }, signal: o?.signal,
			}),
		getWorkspaceReviewDiffs: (orgId, sessionId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/workspace/review/diffs`, {
				body, query: repositoryQuery(o), signal: o?.signal,
			}),
		getWorkspaceReviewRevision: (orgId, sessionId, query, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/workspace/review/revision`, {
				query: {
					path: query.path, scope: query.scope, side: query.side, workspaceVersion: query.workspaceVersion,
					expectedRevision: query.expectedRevision, commitSha: query.commitSha, ...repositoryQuery(o),
				}, signal: o?.signal,
			}),
		updateWorkspaceReviewFile: (orgId, sessionId, body, o) =>
			requestJson("PUT", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/workspace/review/file`, {
				body, query: repositoryQuery(o), signal: o?.signal,
			}),
		restoreSession: (orgId, sessionId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/restore`, {
				signal: o?.signal,
			}),
		retrySessionStartup: (orgId, sessionId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/startup-retry`, {
				signal: o?.signal,
			}),

		sendSessionMessage: (orgId, sessionId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/messages`, {
				body,
				signal: o?.signal,
				idempotencyKey: o?.idempotencyKey ?? newIdempotencyKey(),
			}),
		sendSessionReviewToWorker: (orgId, sessionId, reviewRunId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/reviews/${seg(reviewRunId)}/send`, {
				signal: o?.signal,
				idempotencyKey: o?.idempotencyKey ?? newIdempotencyKey(),
			}),
		listChatModels: (orgId, sessionId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/chat-models`, { signal: o?.signal }),
		suggestChatPrompt: (orgId, sessionId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/chat/prompt-suggestion`, { body, signal: o?.signal }),
		listReviewChatEvents: (orgId, sessionId, reviewRunId, query, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/reviews/${seg(reviewRunId)}/chat-events`, {
				query: { after: query?.after, limit: query?.limit },
				signal: o?.signal,
			}),
		sendReviewChatMessage: (orgId, sessionId, reviewRunId, text, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/reviews/${seg(reviewRunId)}/chat/messages`, {
				body: { text },
				signal: o?.signal,
				idempotencyKey: o?.idempotencyKey ?? newIdempotencyKey(),
			}),
		interruptReviewChat: (orgId, sessionId, reviewRunId, o) =>
			requestVoid("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/reviews/${seg(reviewRunId)}/chat/interrupt`, { signal: o?.signal }),
		cancelTurn: (orgId, sessionId, turnId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/turns/${seg(turnId)}/cancel`, {
				signal: o?.signal,
			}),
		steerTurn: (orgId, sessionId, turnId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/turns/${seg(turnId)}/steer`, {
				body,
				signal: o?.signal,
				idempotencyKey: o?.idempotencyKey ?? newIdempotencyKey(),
			}),
		editQueuedMessage: (orgId, sessionId, turnId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/turns/${seg(turnId)}/edit`, {
				body,
				signal: o?.signal,
			}),
		steerQueuedMessage: (orgId, sessionId, turnId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/turns/${seg(turnId)}/steer-queued`, {
				body,
				signal: o?.signal,
				idempotencyKey: o?.idempotencyKey ?? newIdempotencyKey(),
			}),
		deleteQueuedMessage: (orgId, sessionId, turnId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/turns/${seg(turnId)}/delete`, {
				signal: o?.signal,
			}),
		decideChatApproval: (orgId, sessionId, requestId, decisionId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/approvals/${seg(requestId)}/decide`, {
				body: { decisionId }, signal: o?.signal,
			}),
		acquireComposer: (orgId, sessionId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/composer/acquire`, { signal: o?.signal }),
		renewComposer: (orgId, sessionId, epoch, typing, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/composer/renew`, { body: { epoch, typing }, signal: o?.signal }),
		releaseComposer: (orgId, sessionId, epoch, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/composer/release`, { body: { epoch }, signal: o?.signal }),
		requestComposerTurn: (orgId, sessionId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/composer/queue`, { signal: o?.signal }),
		leaveComposerQueue: (orgId, sessionId, o) =>
			requestJson("DELETE", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/composer/queue`, { signal: o?.signal }),
		takeOverComposer: (orgId, sessionId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/composer/takeover`, { signal: o?.signal }),
		setSessionChatSettings: (orgId, sessionId, settings, o) =>
			requestJson("PUT", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/chat-settings`, { body: settings, signal: o?.signal }),
		listSessionParticipants: (orgId, sessionIds, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/session-participants`, { query: { sessionId: sessionIds }, signal: o?.signal }),
		publishComposerDraft: (orgId, sessionId, epoch, text, cursor, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/composer/draft`, { body: { epoch, text, cursor }, signal: o?.signal }),
		createShareLink: (orgId, projectId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/shares`, { body, signal: o?.signal }),
		listShareLinks: (orgId, projectId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/shares`, { signal: o?.signal }),
		revokeShareLink: async (orgId, projectId, linkId, o) => {
			const response = await send("POST", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/shares/${seg(linkId)}/revoke`, {
				...(o?.removeJoined ? { body: { removeJoined: true } } : {}),
				signal: o?.signal,
			});
			const text = response.status === 204 ? "" : await response.text();
			if (text.trim() === "") return {};
			try {
				const body = JSON.parse(text) as { removedGrants?: unknown } | null;
				return typeof body?.removedGrants === "number" ? { removedGrants: body.removedGrants } : {};
			} catch {
				return {};
			}
		},
		redeemShareLink: (orgId, token, o) =>
			requestJson("POST", "/share-links/redeem", { body: { orgId, token }, signal: o?.signal }),
		redeemShareLinkCode: (code, o) =>
			requestJson("POST", "/share-links/redeem", { body: { code }, signal: o?.signal }),
		listSharedProjects: (o) =>
			requestJson("GET", "/shared/projects", { signal: o?.signal }),
		listSharedProjectSessions: (orgId, projectId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/shared/projects/${seg(projectId)}/sessions`, { signal: o?.signal }),
		listTeams: (orgId, o) => requestJson("GET", `/orgs/${seg(orgId)}/teams`, { signal: o?.signal }),
		createTeam: (orgId, body, o) => requestJson("POST", `/orgs/${seg(orgId)}/teams`, { body, signal: o?.signal }),
		getTeam: (orgId, teamId, o) => requestJson("GET", `/orgs/${seg(orgId)}/teams/${seg(teamId)}`, { signal: o?.signal }),
		renameTeam: (orgId, teamId, name, o) =>
			requestJson("PATCH", `/orgs/${seg(orgId)}/teams/${seg(teamId)}`, { body: { name }, signal: o?.signal }),
		deleteTeam: (orgId, teamId, o) => requestVoid("DELETE", `/orgs/${seg(orgId)}/teams/${seg(teamId)}`, { signal: o?.signal }),
		teamImpact: (orgId, teamId, o) => requestJson("GET", `/orgs/${seg(orgId)}/teams/${seg(teamId)}/impact`, { signal: o?.signal }),
		addTeamMembers: (orgId, teamId, members, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/teams/${seg(teamId)}/members`, { body: { members }, signal: o?.signal }),
		updateTeamMember: (orgId, teamId, memberId, role, o) =>
			requestJson("PATCH", `/orgs/${seg(orgId)}/teams/${seg(teamId)}/members/${seg(memberId)}`, { body: { role }, signal: o?.signal }),
		removeTeamMember: (orgId, teamId, memberId, o) =>
			requestVoid("DELETE", `/orgs/${seg(orgId)}/teams/${seg(teamId)}/members/${seg(memberId)}`, { signal: o?.signal }),
		teamMemberImpact: (orgId, teamId, memberId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/teams/${seg(teamId)}/members/${seg(memberId)}/impact`, { signal: o?.signal }),
		listMyTeams: (o) => requestJson("GET", "/teams", { signal: o?.signal }),
		getMyTeam: (teamId, o) => requestJson("GET", `/teams/${seg(teamId)}`, { signal: o?.signal }),
		leaveTeam: (teamId, o) => requestVoid("POST", `/teams/${seg(teamId)}/leave`, { signal: o?.signal }),
		leaveTeamImpact: (teamId, o) => requestJson("GET", `/teams/${seg(teamId)}/leave-impact`, { signal: o?.signal }),
		listShareCandidates: (orgId, query, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/share-candidates`, { query: { q: query }, signal: o?.signal }),
		createShareGrants: (orgId, projectId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/shares/direct`, { body, signal: o?.signal }),
		getShareAccess: (orgId, projectId, sessionId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/shares/access`, {
				query: { sessionId: sessionId || undefined }, signal: o?.signal,
			}),
		getTeamShareAccess: (orgId, projectId, sessionId, teamId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/shares/access/teams/${seg(teamId)}`, {
				query: { sessionId: sessionId || undefined }, signal: o?.signal,
			}),
		updateShareEntry: (orgId, projectId, grantId, body, o) =>
			requestJson("PATCH", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/shares/access/${seg(grantId)}`, { body, signal: o?.signal }),
		revokeShareGrant: (orgId, projectId, grantId, o) =>
			requestVoid("POST", `/orgs/${seg(orgId)}/projects/${seg(projectId)}/shares/grants/${seg(grantId)}/revoke`, { signal: o?.signal }),
		listChatEvents: (orgId, sessionId, query, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/chat-events`, {
				query: { after: query?.after, limit: query?.limit, types: query?.types?.length ? query.types.join(",") : undefined, before: query?.before, since: query?.since },
				signal: o?.signal,
			}),
		subscribeSessionEvents,
		listNotifications: (orgId, query, o) => requestJson("GET", `/orgs/${seg(orgId)}/notifications`, { query: { status: query?.status, limit: query?.limit, cursor: query?.cursor }, signal: o?.signal }),
		listNotificationEvents: (orgId, after, o) => requestJson("GET", `/orgs/${seg(orgId)}/notification-events`, { query: { after }, signal: o?.signal }),
		markNotificationsRead: (orgId, notificationIds, o) => notificationIds === undefined || notificationIds.length === 0
			? requestJson("POST", `/orgs/${seg(orgId)}/notifications/read-all`, { signal: o?.signal })
			: Promise.all(notificationIds.map((id) => requestJson<{ updated: number }>("PATCH", `/orgs/${seg(orgId)}/notifications/${seg(id)}`, { body: { status: "read" }, signal: o?.signal }))).then((rows) => ({ updated: rows.reduce((total, row) => total + row.updated, 0) })),
		subscribeNotificationEvents,

		createTerminalTicket: (orgId, sessionId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/terminal-ticket`, {
				body,
				signal: o?.signal,
			}),

		listUserProviderConnections: (o) => requestJson("GET", "/me/providers", { signal: o?.signal }),
		listUserAgentModels: (orgId, agent, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/providers/${seg(agent)}/models`, { signal: o?.signal }),
		putUserAgentConnection: (agent, body, o) =>
			requestJson("PUT", `/me/providers/${seg(agent)}`, { body, signal: o?.signal }),
		listAgentAccounts: (o) => requestJson("GET", "/me/agent-accounts", { signal: o?.signal }),
		addAgentAccount: (body, o) => requestJson("POST", "/me/agent-accounts", { body, signal: o?.signal }),
		renameAgentAccount: (accountId, name, o) =>
			requestJson("PATCH", `/me/agent-accounts/${seg(accountId)}`, { body: { name }, signal: o?.signal }),
		removeAgentAccount: (accountId, o) => requestVoid("DELETE", `/me/agent-accounts/${seg(accountId)}`, { signal: o?.signal }),
		makeAgentAccountDefault: (accountId, o) =>
			requestJson("POST", `/me/agent-accounts/${seg(accountId)}/default`, { signal: o?.signal }),
		replaceAgentAccountCredential: (accountId, body, o) =>
			requestJson("PUT", `/me/agent-accounts/${seg(accountId)}/credential`, { body, signal: o?.signal }),
		getSessionAgentAccount: (orgId, sessionId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/agent-account`, { signal: o?.signal }),
		switchSessionAgentAccount: (orgId, sessionId, body, o) =>
			requestJson("PUT", `/orgs/${seg(orgId)}/sessions/${seg(sessionId)}/agent-account`, { body, signal: o?.signal }),
		deleteGitHubPAT: (o) => requestVoid("DELETE", "/me/github-pat", { signal: o?.signal }),
		listGitHubRepos: (o) => requestJson("GET", "/me/github/repos", { signal: o?.signal }),

		startGitHubInstallation: (orgId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/github/installations/start`, { signal: o?.signal }),
		getGitHubUser: (o) => requestJson("GET", "/github/user", { signal: o?.signal }),
		listGitHubInstallations: (orgId, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/github/installations`, { signal: o?.signal }),
		syncGitHubInstallation: (orgId, installationId, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/github/installations/${seg(installationId)}/sync`, {
				signal: o?.signal,
			}),
		listGitHubRepositories: (orgId, query, o) =>
			requestJson("GET", `/orgs/${seg(orgId)}/github/repositories`, {
				query: { limit: query?.limit, cursor: query?.cursor },
				signal: o?.signal,
			}),
		createGitHubProject: (orgId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/github/projects`, {
				body,
				signal: o?.signal,
				idempotencyKey: o?.idempotencyKey ?? newIdempotencyKey(),
			}),
		createGitHubRepository: (orgId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/github/repositories`, {
				body: body.description === undefined ? body : { ...body, description: [...body.description].slice(0, MAX_GITHUB_REPOSITORY_DESCRIPTION).join("") },
				signal: o?.signal,
			}),
		createStandaloneProject: (orgId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/projects/standalone`, {
				body,
				signal: o?.signal,
				idempotencyKey: o?.idempotencyKey ?? newIdempotencyKey(),
			}),
		createStandaloneAgent: (orgId, body, o) =>
			requestJson("POST", `/orgs/${seg(orgId)}/standalone-agents`, {
				body,
				signal: o?.signal,
				idempotencyKey: o?.idempotencyKey ?? newIdempotencyKey(),
			}),
	};
}
