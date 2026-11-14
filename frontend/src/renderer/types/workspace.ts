import { deliverableArtifacts, type Deliverable } from "../../../../packages/shared/artifacts/deliverables";
import { attentionZone as presentationAttentionZone } from "../lib/session-presentation";
import {
	AGENT_OPTIONS,
	toKanbanColumn,
	toSessionActivity,
	toSessionStatus,
	type AgentId,
	type KanbanColumn,
	type SessionActivity,
	type SessionActivityState,
	type SessionStatus,
} from "@aoagents/product-ui";

import type { ReviewerHarnessId } from "../lib/reviewer-harnesses";

export { toKanbanColumn, toSessionActivity, toSessionStatus };
export type { KanbanColumn, SessionActivity, SessionActivityState, SessionStatus };

export type AgentProvider = AgentId | "fake";

export type ChangedFile = {
	path: string;
	additions: number;
	deletions: number;
	staged?: boolean;
};

export type SessionKind = "worker" | "orchestrator";

export type PRState = "open" | "draft" | "merged" | "closed";

export type PullRequestFacts = {
	url: string;
	number: number;
	state: PRState;
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
	updatedAt: string;
};

export type SessionOutputType = "none" | "pr" | "artifact" | "pr_artifact";

export type ArtifactKind = "html" | "markdown" | "file";

export type SessionArtifact = {
	id?: string;
	kind: ArtifactKind;
	name: string;
	path: string;
	previewUrl?: string;
	rawUrl?: string;
	inlineUrl?: string;
	size: number;
	updatedAt: string;
	registered?: boolean;
	previousPaths?: string[];
	shareUrl?: string;
	sha256?: string;
	shareAccess?: "restricted" | "anyone";
};

export type SessionMode = "chat" | "tui";

export type SessionProvisionStep = {
	id: "fetch" | "worktree" | "setup" | "agent";
	status: "pending" | "running" | "done";
	startedAt?: string;
	endedAt?: string;
};

export type SessionBranchState = {
	commits: number;
	remoteBranch?: string;
	remoteHead?: string;
	unpushed: number;
	base?: string;
	baseRef?: string;
	behind: number;
	conflicts?: boolean;
	head?: string;
};

export type SessionStaleBinary = { version: string; since: string; switchFailedAt?: string };

export type WorkspaceSession = {
	workspaceCleanup?: "pending" | "removed" | "preserved_dirty" | "failed" | "not_applicable";
	workspaceCleanupReportOnly?: boolean;
	id: string;
	hostId?: string;
	terminalHandleId?: string;
	terminalGeneration?: string;
	workspaceId: string;
	workspaceName: string;
	title: string;
	issueId?: string;
	provider: AgentProvider;
	reviewerHarness?: ReviewerHarnessId;
	reviewerConfig?: {
		model?: string;
		mode?: string;
		effort?: string;
		permissions?: string;
	};
	autoReviewEnabled?: boolean;
	kind?: SessionKind;
	mode?: SessionMode;
	branch?: string;
	status: SessionStatus;
	scmStatus?: SessionStatus;
	kanbanColumn?: KanbanColumn;
	displayStatus?: string;
	statusReadiness?: "checking" | "ready" | "unavailable";
	provisionState?: "provisioning" | "ready" | "failed";
	provisionError?: string;
	provisionSteps?: SessionProvisionStep[];
	workspacePublished?: boolean;
	branchState?: SessionBranchState;
	branchNaming?: boolean;
	staleBinary?: SessionStaleBinary;
	isTerminated?: boolean;
	runtimeConnected?: boolean;
	chatProviderPreserved?: boolean;
	needsResume?: boolean;
	terminateOnPrMerge?: boolean;
	autoInjectReview?: boolean;
	autoInjectCI?: boolean;
	createdAt?: string;
	updatedAt: string;
	lastUserMessageAt?: string;
	lastInteractionAt?: string;
	isPinned?: boolean;
	pinnedAt?: string;
	activity?: SessionActivity;
	previewUrl?: string;
	previewRevision?: number;
	changedFiles?: ChangedFile[];
	commitMessage?: string;
	prs: PullRequestFacts[];
	outputType?: SessionOutputType;
	requestedOutput?: "artifact";
	artifactFiles?: SessionArtifact[];
	artifactFilesPending?: boolean;
	cloud?: {
		orgId: string;
		shared?: boolean;
		sharedVia?: string;
		requestedBy?: { userId: string; name: string };
		createdByUserId?: string;
		permissionMode?: "read-only" | "standard" | "trusted";
		sandboxProvider?: string;
		desiredState?: string;
		observedState?: string;
		runtimeState?: string;
		runtimeError?: string;
		startupError?: { code: string; message: string; at: string; detail?: string };
		coderWorkspaceUrl?: string;
	};
};

const TRACKER_PROVIDER_PREFIXES = ["github:"] as const;

export function canonicalTrackerIssueId(issueId?: string): string | undefined {
	if (!issueId) return undefined;
	return TRACKER_PROVIDER_PREFIXES.some((prefix) => issueId.startsWith(prefix)) ? issueId : undefined;
}

export type ProjectKind = "single_repo" | "workspace" | "scratch";

export const STANDALONE_WORKSPACE_ID = "__standalone__" as const;
export const STANDALONE_PROJECT_KIND = "standalone" as const;
export const CLOUD_STANDALONE_WORKSPACE_ID = "__cloud_standalone__" as const;

export const CLOUD_PROJECT_KIND = "cloud" as const;

const projectKinds = new Set<ProjectKind>(["single_repo", "workspace", "scratch"]);

export function toProjectKind(kind?: string): ProjectKind | undefined {
	return projectKinds.has(kind as ProjectKind) ? (kind as ProjectKind) : undefined;
}

export type WorkspaceRepoSummary = {
	name: string;
	relativePath: string;
	repo: string;
};

const prStateRank: Record<PRState, number> = { open: 0, draft: 1, merged: 2, closed: 3 };

export function sortedPRs(session: WorkspaceSession): PullRequestFacts[] {
	return [...session.prs].sort((a, b) => prStateRank[a.state] - prStateRank[b.state] || a.number - b.number);
}

export function openPRs(session: WorkspaceSession): PullRequestFacts[] {
	return session.prs.filter((pr) => pr.state === "open" || pr.state === "draft");
}

export function mergedPRCount(session: WorkspaceSession): number {
	return session.prs.filter((pr) => pr.state === "merged").length;
}

export function primaryPR(session: WorkspaceSession): PullRequestFacts | undefined {
	return sortedPRs(session)[0];
}

export function sessionArtifacts(session: WorkspaceSession): SessionArtifact[] {
	if (session.outputType !== "artifact" && session.outputType !== "pr_artifact") return [];
	return [...(session.artifactFiles ?? [])].sort(
		(a, b) => (Date.parse(b.updatedAt) || 0) - (Date.parse(a.updatedAt) || 0) || a.path.localeCompare(b.path),
	);
}

export function sessionDeliverables(session: WorkspaceSession): Deliverable<SessionArtifact>[] {
	return deliverableArtifacts(sessionArtifacts(session));
}

export function isOrchestratorSession(session: Pick<WorkspaceSession, "id" | "kind">): boolean {
	return session.kind === "orchestrator" || session.id.endsWith("-orchestrator");
}

export function findProjectOrchestrator(
	workspaces: WorkspaceSummary[],
	projectId: string,
): WorkspaceSession | undefined {
	const workspace = workspaces.find((w) => w.id === projectId);
	return newestActiveOrchestrator(workspace?.sessions ?? []);
}

export function newestActiveOrchestrator(sessions: WorkspaceSession[]): WorkspaceSession | undefined {
	const active = sessions.filter((session) => isOrchestratorSession(session) && sessionIsActive(session));
	return active.reduce<WorkspaceSession | undefined>(
		(newest, session) => (!newest || sessionNewer(session, newest) ? session : newest),
		undefined,
	);
}

function sessionNewer(a: WorkspaceSession, b: WorkspaceSession): boolean {
	const aCreated = timestamp(a.createdAt);
	const bCreated = timestamp(b.createdAt);
	if (aCreated !== bCreated) return aCreated > bCreated;
	const aUpdated = timestamp(a.updatedAt);
	const bUpdated = timestamp(b.updatedAt);
	if (aUpdated !== bUpdated) return aUpdated > bUpdated;
	return a.id > b.id;
}

function sessionRecentlyMessagedNewer(a: WorkspaceSession, b: WorkspaceSession): boolean {
	const aMessaged = sessionLastMessageTimestamp(a);
	const bMessaged = sessionLastMessageTimestamp(b);
	if (aMessaged !== bMessaged) return aMessaged > bMessaged;
	const aUpdated = timestamp(a.updatedAt);
	const bUpdated = timestamp(b.updatedAt);
	if (aUpdated !== bUpdated) return aUpdated > bUpdated;
	return a.id > b.id;
}

function sessionLastMessageTimestamp(session: WorkspaceSession): number {
	return (
		validTimestamp(session.lastInteractionAt) ??
		validTimestamp(session.lastUserMessageAt) ??
		(session.needsResume ? validTimestamp(session.activity?.lastActivityAt) : undefined) ??
		validTimestamp(session.createdAt) ??
		0
	);
}

function timestamp(value?: string): number {
	return validTimestamp(value) ?? 0;
}

function validTimestamp(value?: string): number | undefined {
	if (!value) return undefined;
	const parsed = Date.parse(value);
	return Number.isNaN(parsed) ? undefined : parsed;
}

export function workerSessions(sessions: WorkspaceSession[]): WorkspaceSession[] {
	return sessions.filter((s) => !isOrchestratorSession(s));
}

export function sortedWorkerSessions(sessions: WorkspaceSession[]): WorkspaceSession[] {
	return workerSessions(sessions).sort((a, b) =>
		sessionRecentlyMessagedNewer(b, a) ? 1 : sessionRecentlyMessagedNewer(a, b) ? -1 : 0,
	);
}

export function sessionWorkspaceReady(session: Pick<WorkspaceSession, "workspacePublished">): boolean {
	return session.workspacePublished !== false;
}

export function sessionIsActive(session: WorkspaceSession): boolean {
	return session.isTerminated !== true && session.status !== "terminated";
}

export function canArchiveWorkerSession(session: WorkspaceSession): boolean {
	return !isOrchestratorSession(session) && session.cloud?.shared !== true && sessionIsActive(session);
}

export function sessionAgentExited(session: WorkspaceSession | undefined): boolean {
	return Boolean(session && !session.needsResume && session.activity?.state === "exited" && sessionIsActive(session));
}

export function sessionCueTargetAvailable(session: WorkspaceSession | undefined): boolean {
	return Boolean(session && sessionIsActive(session) && !session.needsResume && session.activity?.state !== "exited" && session.activity?.state !== "blocked");
}

export function sessionNeedsAttention(session: WorkspaceSession): boolean {
	return presentationAttentionZone(session) === "action";
}

export { attentionZone, attentionZoneLabel, attentionZoneOrder } from "../lib/session-presentation";
export type { AttentionZone } from "../lib/session-presentation";

export type WorkspaceShareSource = { byName?: string; viaTeamName?: string; grantId?: string; source?: "link" | "direct" | "team" };

export type WorkspaceShare = WorkspaceShareSource & {
	wholeProject?: WorkspaceShareSource;
	sessions?: Record<string, WorkspaceShareSource>;
};

export type WorkspaceSummary = {
	id: string;
	cloudOrgId?: string;
	shared?: WorkspaceShare;
	hostId?: string;
	name: string;
	kind?: ProjectKind | typeof CLOUD_PROJECT_KIND | typeof STANDALONE_PROJECT_KIND;
	cloudLayout?: "workspace";
	standaloneAgent?: boolean;
	path: string;
	folderMissing?: boolean;
	autoSpawnPauses?: import("../hooks/useAutoSpawnPauseNotice").AutoSpawnPause[];
	workspaceRepos?: WorkspaceRepoSummary[];
	type?: "main" | "worktree";
	orchestratorAgent?: AgentProvider;
	accentColor?: string;
	diff?: {
		additions: number;
		deletions: number;
	};
	sessions: WorkspaceSession[];
};

export function isScratchpadWorkspace(workspace: Pick<WorkspaceSummary, "kind" | "standaloneAgent"> | undefined): boolean {
	return workspace?.kind === STANDALONE_PROJECT_KIND || workspace?.standaloneAgent === true;
}

export function ownScratchpadWorkspaces(workspaces: readonly WorkspaceSummary[]): WorkspaceSummary[] {
	return workspaces.filter((workspace) =>
		workspace.kind === STANDALONE_PROJECT_KIND || (workspace.standaloneAgent === true && !workspace.shared));
}

export function hasConfiguredOrchestratorAgent(
	workspace: Pick<WorkspaceSummary, "orchestratorAgent"> | undefined,
): boolean {
	return Boolean(workspace?.orchestratorAgent);
}

export function orchestratorNeedsRestart(workspace: WorkspaceSummary, orchestrator?: WorkspaceSession): boolean {
	if (!orchestrator || !workspace.orchestratorAgent) return false;
	return orchestrator.provider !== workspace.orchestratorAgent;
}

export type OrchestratorHealth =
	| { state: "ok" }
	| { state: "restarting"; message: string }
	| { state: "restart_needed"; message: string }
	| { state: "missing"; message: string }
	| { state: "duplicates"; message: string };

export function orchestratorHealth(workspace: WorkspaceSummary, restarting = false): OrchestratorHealth {
	if (restarting) {
		return {
			state: "restarting",
			message: "Restarting orchestrator. New tasks wait until the replacement is ready.",
		};
	}
	const active = workspace.sessions.filter((session) => isOrchestratorSession(session) && sessionIsActive(session));
	if (active.length > 1) {
		return {
			state: "duplicates",
			message:
				"Multiple orchestrators are active. The newest one is used; stale ones will be cleaned up on daemon reconcile.",
		};
	}
	const orchestrator = newestActiveOrchestrator(workspace.sessions);
	if (!orchestrator) {
		return { state: "missing", message: "No orchestrator is running for this project." };
	}
	if (orchestratorNeedsRestart(workspace, orchestrator)) {
		return {
			state: "restart_needed",
			message: `Configured orchestrator agent is ${workspace.orchestratorAgent}; running agent is ${orchestrator.provider}.`,
		};
	}
	return { state: "ok" };
}

export function toAgentProvider(provider?: string): AgentProvider {
	if (provider === "fake") return provider;
	return AGENT_OPTIONS.find((candidate) => candidate === provider) ?? "codex";
}

export type NextSessionNavigation =
	| { target: "session"; sessionId: string }
	| { target: "project" };

export function resolveNextNavigationAfterSessionKill(
	workspace: WorkspaceSummary | undefined,
	killedSessionId: string,
	sessionsInDisplayOrder?: WorkspaceSession[],
): NextSessionNavigation {
	if (!workspace) return { target: "project" };

	const workerList = (sessionsInDisplayOrder ?? sortedWorkerSessions(workspace.sessions)).filter(
		(s) => s.isTerminated !== true,
	);
	const currentIndex = workerList.findIndex((s) => s.id === killedSessionId);
	const remaining = workerList.filter((s) => s.id !== killedSessionId);

	if (remaining.length > 0) {
		const nextIndex = currentIndex > 0 ? currentIndex - 1 : 0;
		const nextSession = remaining[nextIndex] ?? remaining[0];
		return { target: "session", sessionId: nextSession.id };
	}

	const orchestrator = newestActiveOrchestrator(workspace.sessions);
	if (orchestrator && orchestrator.id !== killedSessionId && orchestrator.isTerminated !== true) {
		return { target: "session", sessionId: orchestrator.id };
	}

	return { target: "project" };
}
