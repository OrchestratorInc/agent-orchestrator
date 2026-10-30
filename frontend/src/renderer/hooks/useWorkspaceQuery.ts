import { type QueryClient, useQueries, useQuery, useQueryClient, type Query } from "@tanstack/react-query";
import type { TraySessionEntry } from "../../shared/tray";
import { useEffect, useMemo, useRef } from "react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorCode, hasTrustedApiBaseUrl } from "../lib/api-client";
import { FREESTYLE_PROVIDER } from "../lib/cloud-offering";
import { CloudCpError, type CloudCpProject, type CloudCpSession, type CloudCpSharedProject, type CloudCpSessionArtifact } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";
import { cloudArtifactFilesQueryKey } from "./useCloudArtifacts";
import { useCloudOrg } from "./useCloudOrg";
import { mockWorkspaces } from "../lib/mock-data";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { toReviewerHarnessId } from "../lib/reviewer-harnesses";
import { captureRendererEvent } from "../lib/telemetry";
import { aoBridge } from "../lib/bridge";
import { clientForHost } from "../lib/host-clients";
import { useConnectedHosts } from "./useHostConnection";
import { requestRemoteHostsRefresh } from "./useRemoteHosts";
import { applyOptimisticSessionKills } from "./optimistic-session-kills";
import { appI18n } from "../i18n";
import {
	type PRState,
	type PullRequestFacts,
	type SessionArtifact,
	toAgentProvider,
	toKanbanColumn,
	toProjectKind,
	toSessionActivity,
	toSessionStatus,
	newestActiveOrchestrator,
	attentionZone,
	workerSessions,
	type WorkspaceSession,
	type WorkspaceSummary,
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
} from "../types/workspace";

function standaloneWorkspaceName(): string {
	return appI18n.t("standalone.workspaceName");
}

function placeStandaloneWorkspaceLast(workspaces: WorkspaceSummary[]): WorkspaceSummary[] {
	const standalone = workspaces.find((workspace) => workspace.id === STANDALONE_WORKSPACE_ID);
	if (!standalone) return workspaces;
	return [...workspaces.filter((workspace) => workspace.id !== STANDALONE_WORKSPACE_ID), standalone];
}

function toPullRequestFacts(pr: components["schemas"]["SessionPRFacts"]): PullRequestFacts {
	return {
		url: pr.url,
		number: pr.number,
		state: pr.state as PRState,
		ci: pr.ci,
		review: pr.review,
		mergeability: pr.mergeability,
		reviewComments: pr.reviewComments,
		updatedAt: pr.updatedAt,
	};
}

function toSessionArtifact(artifact: components["schemas"]["SessionArtifact"]): SessionArtifact {
	return {
		kind: artifact.kind,
		name: artifact.name,
		path: artifact.path,
		previewUrl: artifact.previewUrl,
		rawUrl: artifact.rawUrl,
		inlineUrl: artifact.inlineUrl,
		size: artifact.size,
		updatedAt: artifact.updatedAt,
	};
}

export function toWorkspaceSession(
	session: components["schemas"]["ControllersSessionView"],
	project: Pick<WorkspaceSummary, "id" | "name">,
): WorkspaceSession {
	const statusReadiness = session.statusReadiness ?? "ready";
	const status = statusReadiness === "ready" ? toSessionStatus(session.status, session.isTerminated) : "unknown";
	const scmStatus = session.scmStatus ? toSessionStatus(session.scmStatus) : undefined;
	const kanbanColumn = toKanbanColumn(session.kanbanColumn, status);
	const activity = statusReadiness === "ready" ? toSessionActivity(session.activity) : undefined;
	if (statusReadiness === "ready" && status === "unknown") reportUnknownSessionField("status", session.status);
	if (statusReadiness === "ready" && (!activity || activity.state === "unknown")) {
		reportUnknownSessionField("activity", session.activity?.state);
	}
	return {
		id: session.id,
		terminalHandleId: session.terminalHandleId,
		terminalGeneration: session.terminalGeneration,
		workspaceId: project.id,
		workspaceName: project.name,
		title: session.displayName ?? session.issueId ?? session.id,
		issueId: session.issueId,
		provider: toAgentProvider(session.harness),
		reviewerHarness: toReviewerHarnessId(session.reviewerHarness),
		reviewerConfig: session.reviewerConfig
			? {
					model: session.reviewerConfig.model ?? undefined,
					mode: session.reviewerConfig.mode ?? undefined,
					permissions: session.reviewerConfig.permissions ?? undefined,
				}
			: undefined,
		autoReviewEnabled: session.autoReviewEnabled ?? false,
		kind: session.kind === "orchestrator" ? "orchestrator" : session.kind === "worker" ? "worker" : undefined,
		mode: session.mode === "chat" ? "chat" : "tui",
		branch: session.branch || undefined,
		status,
		scmStatus,
		kanbanColumn,
		displayStatus: session.displayStatus || undefined,
		statusReadiness,
		provisionState: session.provisionState,
		provisionError: session.provisionError || undefined,
		provisionSteps: session.provisionSteps?.map((step) => ({
			id: step.id,
			status: step.status,
			startedAt: step.startedAt ?? undefined,
			endedAt: step.endedAt ?? undefined,
		})),
		branchState: session.branchState,
		branchNaming: session.branchNaming,
		staleBinary: session.staleBinary ? { version: session.staleBinary.version, since: session.staleBinary.since, switchFailedAt: session.staleBinary.switchFailedAt ?? undefined } : undefined,
		isTerminated: session.isTerminated,
		workspaceCleanup: session.workspaceCleanup,
		chatProviderPreserved: session.chatProviderPreserved,
		terminateOnPrMerge: session.terminateOnPrMerge ?? false,
		autoInjectReview: session.autoInjectReview ?? true,
		autoInjectCI: session.autoInjectCI ?? true,
		createdAt: session.createdAt,
		updatedAt: session.updatedAt,
		lastUserMessageAt: session.lastUserMessageAt ?? undefined,
		lastInteractionAt: session.lastInteractionAt ?? undefined,
		activity,
		previewUrl: session.previewUrl,
		previewRevision: session.previewRevision,
		isPinned: session.isPinned ?? false,
		pinnedAt: session.pinnedAt ?? undefined,
		prs: (session.prs ?? []).map(toPullRequestFacts),
		outputType: session.outputType,
		...(session.requestedOutput ? { requestedOutput: session.requestedOutput } : {}),
		artifactFiles: session.artifactFiles?.map(toSessionArtifact),
	};
}

export const workspaceQueryKey = ["workspaces"] as const;
export const remoteWorkspaceQueryKey = (hostId: string) => ["remote-workspaces", hostId] as const;
export const workspaceQueryKeyForHost = (hostId?: string) => hostId ? remoteWorkspaceQueryKey(hostId) : workspaceQueryKey;
const remoteProjectsQueryKey = (hostId: string) => [...remoteWorkspaceQueryKey(hostId), "projects"] as const;
const remoteSessionsQueryKey = (hostId: string) => [...remoteWorkspaceQueryKey(hostId), "sessions"] as const;
const lastRemoteHealthRecheck = new Map<string, number>();

function recheckRemoteHost(hostId: string, status: number): void {
	if (![401, 403, 426, 502, 503].includes(status)) return;
	const now = Date.now();
	const last = lastRemoteHealthRecheck.get(hostId);
	if (last !== undefined && now >= last && now - last < 15_000) return;
	lastRemoteHealthRecheck.set(hostId, now);
	requestRemoteHostsRefresh();
}
export function workspaceStatusesChecking(workspaces: WorkspaceSummary[] | undefined): boolean {
	return (
		workspaces?.some((workspace) => workspace.sessions.some((session) => session.statusReadiness === "checking")) ??
		false
	);
}
const reportedUnknownSessionFields = new Set<string>();

function reportUnknownSessionField(field: "status" | "activity", value?: string): void {
	const reason = value ? "unrecognized" : "missing";
	const key = `${field}:${reason}`;
	if (reportedUnknownSessionFields.has(key)) return;
	reportedUnknownSessionFields.add(key);
	void captureRendererEvent("ao.renderer.session_state_unknown", {
		field,
		reason,
	});
}

function toLocalWorkspaceSession(
	session: components["schemas"]["ControllersSessionView"],
	workspaceId: string,
	workspaceName: string,
): WorkspaceSession {
	const status = toSessionStatus(session.status, session.isTerminated);
	const scmStatus = session.scmStatus ? toSessionStatus(session.scmStatus) : undefined;
	const kanbanColumn = toKanbanColumn(session.kanbanColumn, status);
	const activity = toSessionActivity(session.activity);
	if (status === "unknown") reportUnknownSessionField("status", session.status);
	if (!activity || activity.state === "unknown") reportUnknownSessionField("activity", session.activity?.state);
	return {
		id: session.id,
		terminalHandleId: session.terminalHandleId,
		terminalGeneration: session.terminalGeneration,
		workspaceId,
		workspaceName,
		title: session.displayName ?? session.issueId ?? session.id,
		issueId: session.issueId,
		provider: toAgentProvider(session.harness),
		reviewerHarness: toReviewerHarnessId(session.reviewerHarness),
		reviewerConfig: session.reviewerConfig
			? {
					model: session.reviewerConfig.model ?? undefined,
					mode: session.reviewerConfig.mode ?? undefined,
					permissions: session.reviewerConfig.permissions ?? undefined,
				}
			: undefined,
		autoReviewEnabled: session.autoReviewEnabled ?? false,
		kind: session.kind === "orchestrator" ? "orchestrator" : session.kind === "worker" ? "worker" : undefined,
		// Carried through verbatim: the session surface must render from
		// the mode this session was created with, not from the current default.
		mode: session.mode === "chat" ? "chat" : "tui",
		branch: session.branch || undefined,
		status,
		scmStatus,
		kanbanColumn,
		displayStatus: session.displayStatus || undefined,
		provisionState: session.provisionState,
		provisionError: session.provisionError || undefined,
		provisionSteps: session.provisionSteps?.map((step) => ({
			id: step.id,
			status: step.status,
			startedAt: step.startedAt ?? undefined,
			endedAt: step.endedAt ?? undefined,
		})),
		branchState: session.branchState,
		branchNaming: session.branchNaming,
		staleBinary: session.staleBinary ? { version: session.staleBinary.version, since: session.staleBinary.since, switchFailedAt: session.staleBinary.switchFailedAt ?? undefined } : undefined,
		isTerminated: session.isTerminated,
		workspaceCleanup: session.workspaceCleanup,
		terminateOnPrMerge: session.terminateOnPrMerge ?? false,
		autoInjectReview: session.autoInjectReview ?? true,
		autoInjectCI: session.autoInjectCI ?? true,
		createdAt: session.createdAt,
		updatedAt: session.updatedAt,
		lastUserMessageAt: session.lastUserMessageAt ?? undefined,
		lastInteractionAt: session.lastInteractionAt ?? undefined,
		activity,
		previewUrl: session.previewUrl,
		previewRevision: session.previewRevision,
		isPinned: session.isPinned ?? false,
		pinnedAt: session.pinnedAt ?? undefined,
		prs: (session.prs ?? []).map(toPullRequestFacts),
		outputType: session.outputType,
		...(session.requestedOutput ? { requestedOutput: session.requestedOutput } : {}),
		artifactFiles: session.artifactFiles?.map(toSessionArtifact),
	};
}

// e2e seam (dev:web only): the Playwright fake-agent harness injects
// `window.__aoFakeAgent` (see e2e/support/fake-bridge.ts) to drive a
// deterministic, mutable session timeline off the SSE refetch path. Compiled
// out of the packaged build — the packaged renderer never sets VITE_NO_ELECTRON
// and always hits the real daemon.
type FakeAgentSeam = { snapshot: () => WorkspaceSummary[] };

class DaemonNotReadyError extends Error {
	constructor() {
		super("AO daemon API is not ready");
	}
}

async function fetchWorkspaces(): Promise<WorkspaceSummary[]> {
	if (usesPreviewWorkspaceData) {
		const fake =
			typeof window !== "undefined"
				? (window as unknown as { __aoFakeAgent?: FakeAgentSeam }).__aoFakeAgent
				: undefined;
		const snapshot = fake ? fake.snapshot() : mockWorkspaces;
		return applyOptimisticSessionKills(snapshot) ?? snapshot;
	}
	if (!hasTrustedApiBaseUrl()) {
		throw new DaemonNotReadyError();
	}

	const [{ data: projectsData, error: projectsError }, { data: sessionsData, error: sessionsError }] =
		await Promise.all([apiClient.GET("/api/v1/projects"), apiClient.GET("/api/v1/sessions")]);

	if (projectsError || sessionsError) {
		throw projectsError ?? sessionsError;
	}

	const sessions = sessionsData?.sessions ?? [];
	const standaloneName = standaloneWorkspaceName();
	const projects = (projectsData?.projects ?? []).map((project) => {
		const kind = toProjectKind(project.kind);
		return {
			id: project.id,
			name: project.name,
			kind,
			path: project.path,
			folderMissing: project.folderMissing,
			orchestratorAgent: project.orchestratorAgent ? toAgentProvider(project.orchestratorAgent) : undefined,
			sessions: sessions
				.filter((session) => session.projectId === project.id)
				.map((session) => toWorkspaceSession(session, project)),
		};
	});
	const standalone: WorkspaceSummary = {
		id: STANDALONE_WORKSPACE_ID,
		name: standaloneName,
		kind: STANDALONE_PROJECT_KIND,
		path: "Not attached to a project",
		sessions: sessions
			.filter((session) => !session.projectId)
			.map((session) => toLocalWorkspaceSession(session, STANDALONE_WORKSPACE_ID, standaloneName)),
	};
	const workspaces =
		standalone.sessions.length > 0 ? placeStandaloneWorkspaceLast([...projects, standalone]) : projects;
	// Pending optimistic kills must survive CDC/refetch while the daemon kill is in flight.
	return applyOptimisticSessionKills(workspaces) ?? workspaces;
}

async function fetchRemoteProjects(hostId: string) {
	const { data, error, response } = await clientForHost(hostId).GET("/api/v1/projects");
	if (error) {
		recheckRemoteHost(hostId, response.status);
		throw error;
	}
	return data?.projects ?? [];
}

async function fetchRemoteSessions(hostId: string) {
	const { data, error, response } = await clientForHost(hostId).GET("/api/v1/sessions");
	if (error) {
		recheckRemoteHost(hostId, response.status);
		throw error;
	}
	return Promise.all((data?.sessions ?? []).map(async (session) => ({
		...session,
		artifactFiles: await Promise.all((session.artifactFiles ?? []).map(async (artifact) => ({
			...artifact,
			rawUrl: artifact.rawUrl ? await aoBridge.remotes.previewUrl(hostId, session.id, artifact.rawUrl) : undefined,
			// The remote daemon's inline origin is a localhost name, which here
			// would mean this computer; a remote chat shows no frame anyway.
			inlineUrl: undefined,
		}))),
	})));
}

function toRemoteWorkspaces(
	hostId: string,
	remoteProjects: Awaited<ReturnType<typeof fetchRemoteProjects>>,
	sessions: Awaited<ReturnType<typeof fetchRemoteSessions>>,
): WorkspaceSummary[] {
	const projects: WorkspaceSummary[] = remoteProjects.map((project) => ({
		hostId,
		id: project.id,
		name: project.name,
		kind: toProjectKind(project.kind),
		path: project.path,
		folderMissing: project.folderMissing,
		orchestratorAgent: project.orchestratorAgent ? toAgentProvider(project.orchestratorAgent) : undefined,
		sessions: sessions.filter((session) => session.projectId === project.id).map((session) => ({
			...toWorkspaceSession(session, project),
			hostId,
		})),
	}));
	const standalone = sessions.filter((session) => !session.projectId).map((session) => ({
		...toWorkspaceSession(session, { id: STANDALONE_WORKSPACE_ID, name: standaloneWorkspaceName() }),
		hostId,
	}));
	if (standalone.length > 0) projects.push({
		hostId,
		id: STANDALONE_WORKSPACE_ID,
		name: standaloneWorkspaceName(),
		kind: STANDALONE_PROJECT_KIND,
		path: "Not attached to a project",
		sessions: standalone,
	});
	return applyOptimisticSessionKills(projects) ?? projects;
}

async function fetchRemoteWorkspaces(hostId: string): Promise<WorkspaceSummary[]> {
	const [projects, sessions] = await Promise.all([fetchRemoteProjects(hostId), fetchRemoteSessions(hostId)]);
	return toRemoteWorkspaces(hostId, projects, sessions);
}

// Shared so route loaders can prefetch via queryClient.ensureQueryData (paired
// with the router's defaultPreload: "intent") and the hook reads the same cache.
// No refetchInterval here: every observer that carries one runs its own timer,
// and timers started at different mount times do not dedupe. With ~18
// subscribers that meant several /projects + /sessions fetches per tick.
// useWorkspacePolling() is the single owner of the interval.
export const workspaceQueryOptions = {
	queryKey: workspaceQueryKey,
	queryFn: fetchWorkspaces,
	// Retrying before the daemon is up only parks the query in a 1s backoff.
	// Fetches started meanwhile (the ready-time invalidation, the shell's startup
	// fetch) join that backoff instead of running, which held the first
	// /projects + /sessions about 0.75s past "daemon listening" on cold launch.
	// Fail fast instead; the daemon status change refetches.
	retry: (failureCount: number, error: Error) => !(error instanceof DaemonNotReadyError) && failureCount < 1,
	staleTime: 10_000,
};

export const workspaceRefetchInterval = (query: Query<WorkspaceSummary[]>) =>
	workspaceStatusesChecking(query.state.data) ? 300 : 15_000;

/**
 * Drives the workspace poll from one place. Mounted once by the shell; it never
 * re-renders its host (notifyOnChangeProps: []), it only keeps the timer.
 */
export function useWorkspacePolling(enabled = true) {
	useQuery({ ...workspaceQueryOptions, refetchInterval: workspaceRefetchInterval, notifyOnChangeProps: [], enabled });
}

// Cloud projects are a separate query so a control-plane failure can never
// break the local list: on error TanStack keeps this query's last known data,
// and until the first successful fetch the merge below simply omits cloud
// items. Invalidated by the cloud create flow (CreateProjectFlow).
export const cloudProjectsQueryKey = ["cloud-projects"] as const;
export const cloudSessionsQueryKey = ["cloud-sessions"] as const;
export const sharedCloudWorkspacesQueryKey = ["cloud-shared-workspaces"] as const;

/**
 * Adds a session the desktop just created to the sessions list, when it runs
 * on Freestyle, so its view opens on the in-place start instead of the
 * startup screen shown while a session it does not know yet resolves. Other
 * providers (Coder) are left to the next list refresh, so their start is
 * unchanged.
 */
export function seedCreatedFreestyleSession(queryClient: QueryClient, baseUrl: string, orgId: string, session: CloudCpSession): void {
	if (session.sandboxProvider !== FREESTYLE_PROVIDER) return;
	queryClient.setQueryData<CloudCpSession[]>([...cloudSessionsQueryKey, baseUrl, orgId], (current) =>
		current === undefined || current.some((candidate) => candidate.id === session.id) ? current : [...current, session]);
}

// Maps one control-plane session onto the board's session shape. Cloud sessions
// carry the same status/activity/harness vocabulary as local ones, so the same
// product-ui mappers apply; fields with no cloud analogue take safe defaults.
export function toCloudWorkspaceSession(
	session: CloudCpSession,
	project: Pick<CloudCpProject, "id" | "displayName">,
	orgId: string,
): WorkspaceSession {
	return {
		id: session.id,
		// The terminal pane only mounts for a session that has a terminal handle.
		// A cloud session's PTY is addressed by the session id over its ticketed
		// CP WebSocket, so the session id is its handle.
		terminalHandleId: session.id,
		// The worker epoch advances on every fresh worker connection (resume from
		// idle-pause, restore, re-provision). Folding it into the terminal
		// generation makes the terminal pane re-mint against the new epoch and
		// attach to the live agent, instead of clinging to the previous epoch's
		// exited terminal (the "connected but TERMINAL ENDED / can't type" loop,
		// which then idle-pauses the session again because nothing attached).
		// Stable within an epoch, so it does not churn the pane between resumes.
		terminalGeneration: session.workerEpoch ? String(session.workerEpoch) : undefined,
		workspaceId: project.id,
		workspaceName: project.displayName,
		title: session.displayName || session.id,
		provider: toAgentProvider(session.harness),
		reviewerHarness: toReviewerHarnessId(session.reviewerHarness),
		autoReviewEnabled: session.autoReviewEnabled ?? false,
		kind: session.kind === "orchestrator" ? "orchestrator" : "worker",
		mode: session.interfaceMode ?? "tui",
		branch: session.branch || undefined,
		status: toSessionStatus(session.status, session.isTerminated),
		isTerminated: session.isTerminated,
		autoInjectCI: session.autoInjectCI ?? true,
		autoInjectReview: session.autoInjectReview ?? true,
		terminateOnPrMerge: session.terminateOnPrMerge ?? true,
		runtimeConnected: session.runtimeConnected,
		createdAt: session.createdAt,
		updatedAt: session.updatedAt,
		activity: toSessionActivity({ state: session.activityState }),
		prs: (session.prs ?? []).map((pr) => ({
			url: pr.url,
			number: pr.number,
			state: pr.state as PRState,
			ci: pr.ci,
			review: pr.review,
			mergeability: pr.mergeability,
			failingChecks: pr.failingChecks,
			reviewComments: pr.reviewComments,
			updatedAt: pr.updatedAt,
		})),
		// Marks this as a control-plane session so the terminal opens against the
		// CP (ticket + sandbox WebSocket) instead of the local daemon mux.
		cloud: {
			orgId,
			permissionMode: session.mode === "read-only" || session.mode === "standard" || session.mode === "trusted" ? session.mode : undefined,
			sandboxProvider: session.sandboxProvider,
			desiredState: session.desiredState,
			observedState: session.observedState,
			runtimeState: session.runtimeState,
			runtimeError: session.runtimeError,
			startupError: session.startupError,
			coderWorkspaceUrl: session.coderWorkspaceUrl,
			...(session.requestedBy ? { requestedBy: session.requestedBy } : {}),
		},
		outputType: session.outputType,
		requestedOutput: session.requestedOutput,
		// The list route sends only a count; useCloudSessionsQuery fills in the
		// files from each session's own artifact list query. File URLs come from a short-lived access prefix when a file is
		// opened (useCloudArtifactAccess), not from the list.
		artifactFiles: session.artifactFiles?.map((file) => ({
			kind: file.kind,
			name: file.name,
			path: file.path,
			size: file.size,
			updatedAt: file.updatedAt,
		})),
	};
}

export function toCloudWorkspace(project: CloudCpProject, sessions: CloudCpSession[], orgId: string): WorkspaceSummary {
	return {
		id: project.id,
		cloudOrgId: orgId,
		name: project.displayName,
		kind: "cloud",
		// Cloud projects run in control-plane sandboxes; there is no local folder.
		path: "",
		sessions: sessions
			.filter((session) => session.projectId === project.id)
			.map((session) => toCloudWorkspaceSession(session, project, orgId)),
	};
}

type WorkspaceSubscriptionOptions = {
	subscribed?: boolean;
	enabled?: boolean;
	includeCloud?: boolean;
};

// How long sightings of unknown projects are gathered into one project-list
// refetch.
export const UNKNOWN_CLOUD_PROJECT_REFETCH_DEBOUNCE_MS = 500;

type UnknownProjectRefetches = { asked: Set<string>; timers: Map<string, ReturnType<typeof setTimeout>> };
// Per query client, so each client (and each test's) starts afresh.
const unknownProjectRefetches = new WeakMap<QueryClient, UnknownProjectRefetches>();

/**
 * Refetches the cloud project list when a cloud session (from the session
 * list, a session lookup, or a session's events) belongs to a project the
 * cached list does not have: one created elsewhere (the API, another device,
 * an automation) since the list was fetched, which nothing else refetches.
 * Without it the project and its sessions stay out of the sidebar and the
 * board until a reload. Each unknown project asks once per org, and the
 * sightings within UNKNOWN_CLOUD_PROJECT_REFETCH_DEBOUNCE_MS make one refetch,
 * so a project the list never returns (past its first page) cannot loop. A
 * list not fetched yet is left alone: its own fetch brings the project.
 */
export function refetchCloudProjectsForUnknownSessions(
	queryClient: QueryClient,
	baseUrl: string,
	orgId: string,
	sessions: ReadonlyArray<Pick<CloudCpSession, "projectId">>,
): boolean {
	const queryKey = [...cloudProjectsQueryKey, baseUrl, orgId];
	const projects = queryClient.getQueryData<CloudCpProject[]>(queryKey);
	if (projects === undefined) return false;
	const known = new Set(projects.map((project) => project.id));
	let state = unknownProjectRefetches.get(queryClient);
	if (!state) {
		state = { asked: new Set(), timers: new Map() };
		unknownProjectRefetches.set(queryClient, state);
	}
	let unknown = false;
	for (const { projectId } of sessions) {
		const marker = `${baseUrl}|${orgId}|${projectId}`;
		if (!projectId || known.has(projectId) || state.asked.has(marker)) continue;
		state.asked.add(marker);
		unknown = true;
	}
	if (!unknown) return false;
	const timerKey = `${baseUrl}|${orgId}`;
	if (!state.timers.has(timerKey)) {
		const timers = state.timers;
		timers.set(timerKey, setTimeout(() => {
			timers.delete(timerKey);
			void queryClient.invalidateQueries({ queryKey, exact: true });
		}, UNKNOWN_CLOUD_PROJECT_REFETCH_DEBOUNCE_MS));
	}
	return true;
}

export function useCloudProjectsQuery(options: WorkspaceSubscriptionOptions = {}) {
	const { client, ready, baseUrl } = useCloudCp(options.enabled !== false);
	const { org } = useCloudOrg(options.enabled !== false);
	const orgId = org?.id;
	return useQuery({
		queryKey: [...cloudProjectsQueryKey, baseUrl, orgId ?? ""],
		enabled: options.enabled !== false && ready && orgId !== undefined,
		subscribed: options.subscribed,
		retry: 1,
		// Projects change rarely and are not polled; returning to the app picks up
		// one made elsewhere meanwhile, as the cloud org and offering do.
		refetchOnWindowFocus: true,
		queryFn: async (): Promise<CloudCpProject[]> => {
			if (orgId === undefined) return [];
			// First page only (control-plane max page size); pagination UI is a
			// later phase alongside cloud sessions.
			const response = await client.listProjects(orgId, { limit: 100 });
			return response.items;
		},
	});
}

/** At most this many cloud artifact lists are read at once. */
const CLOUD_ARTIFACT_LIST_CONCURRENCY = 4;
let cloudArtifactListsRunning = 0;
const cloudArtifactListWaiters: Array<() => void> = [];

async function withCloudArtifactListSlot<T>(read: () => Promise<T>): Promise<T> {
	if (cloudArtifactListsRunning >= CLOUD_ARTIFACT_LIST_CONCURRENCY) {
		await new Promise<void>((resolve) => cloudArtifactListWaiters.push(resolve));
	}
	cloudArtifactListsRunning++;
	try {
		return await read();
	} finally {
		cloudArtifactListsRunning--;
		cloudArtifactListWaiters.shift()?.();
	}
}

/** A cloud session's artifact version: its file count and newest commit time. */
function cloudArtifactVersion(session: CloudCpSession): string {
	return `${session.artifactCount ?? 0}:${session.artifactsUpdatedAt ?? session.updatedAt}`;
}

/** Each query's data. useQueries keeps the array while no query changes. */
function queryResultsData<T>(results: { data?: T }[]): (T | undefined)[] {
	return results.map((result) => result.data);
}

export function useCloudSessionsQuery(options: WorkspaceSubscriptionOptions = {}) {
	const { client, ready, baseUrl } = useCloudCp();
	const { org } = useCloudOrg();
	const orgId = org?.id;
	const list = useQuery({
		queryKey: [...cloudSessionsQueryKey, baseUrl, orgId ?? ""],
		enabled: options.enabled !== false && ready && orgId !== undefined,
		subscribed: options.subscribed,
		retry: 1,
		// A provisioning sandbox changes state without a client action, so poll to
		// reflect requested -> running -> ready the same way local sessions stream.
		refetchInterval: 5000,
		queryFn: async (): Promise<CloudCpSession[]> => {
			if (orgId === undefined) return [];
			const response = await client.listSessions(orgId, { limit: 100 });
			return response.items;
		},
	});
	// The list carries only an artifact count. Each live session's files are
	// read in their own query, once per artifact version, without holding up
	// the list.
	const withFiles = (list.data ?? []).filter((session) => (session.artifactCount ?? 0) > 0 && !session.isTerminated);
	const fileData = useQueries({
		queries: withFiles.map((session) => ({
			queryKey: cloudArtifactFilesQueryKey(baseUrl, orgId ?? "", session.id, cloudArtifactVersion(session)),
			enabled: orgId !== undefined && options.enabled !== false,
			subscribed: options.subscribed,
			staleTime: Infinity,
			retry: 1,
			queryFn: ({ signal }: { signal: AbortSignal }) =>
				withCloudArtifactListSlot(async () => (await client.listSessionArtifacts(orgId ?? "", session.id, { signal })).artifacts),
		})),
		combine: queryResultsData,
	});
	// A session keeps its last good list while a new version loads or after a
	// failed read, so its Artifacts tab and open file stay.
	const lastFiles = useRef(new Map<string, CloudCpSessionArtifact[]>());
	const data = useMemo(() => {
		if (list.data === undefined) return undefined;
		const read = new Map(withFiles.map((session, index) => [session.id, fileData[index]] as const));
		return list.data.map((session) => {
			const files = read.get(session.id) ?? lastFiles.current.get(session.id);
			if (read.get(session.id)) lastFiles.current.set(session.id, read.get(session.id)!);
			return files && (session.artifactCount ?? 0) > 0 ? { ...session, artifactFiles: files } : session;
		});
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [list.data, fileData]);
	return { ...list, data };
}

/**
 * Cloud projects and sessions people in other organizations shared with this
 * person, as workspaces beside their own. A project shared both whole and
 * one session at a time shows once.
 */
export function useSharedCloudWorkspacesQuery(options: WorkspaceSubscriptionOptions = {}) {
	const { client, ready, baseUrl } = useCloudCp();
	return useQuery({
		queryKey: [...sharedCloudWorkspacesQueryKey, baseUrl],
		enabled: options.enabled !== false && ready,
		subscribed: options.subscribed,
		retry: 1,
		refetchInterval: 15_000,
		queryFn: async (): Promise<WorkspaceSummary[]> => {
			const { shared } = await client.listSharedProjects();
			const byProject = new Map<string, CloudCpSharedProject>();
			for (const item of shared) {
				const key = `${item.project.orgId}/${item.project.id}`;
				if (!byProject.has(key)) byProject.set(key, item);
			}
			return Promise.all([...byProject.values()].map(async (item) => {
				const { sessions } = await client.listSharedProjectSessions(item.project.orgId, item.project.id);
				const workspace = toCloudWorkspace(item.project, sessions, item.project.orgId);
				return {
					...workspace,
					sessions: workspace.sessions.map((session) => session.cloud ? { ...session, cloud: { ...session.cloud, shared: true } } : session),
					shared: { byName: item.sharedByName || undefined },
				};
			}));
		},
	});
}

export function useRemoteWorkspaces(options: WorkspaceSubscriptionOptions = {}) {
	const connected = useConnectedHosts();
	const projects = useQueries({
		queries: connected.map((hostId) => ({
			queryKey: remoteProjectsQueryKey(hostId),
			queryFn: () => fetchRemoteProjects(hostId),
			retry: 1,
			refetchInterval: 15_000,
			subscribed: options.subscribed,
		})),
	});
	const sessions = useQueries({
		queries: connected.map((hostId) => ({
			queryKey: remoteSessionsQueryKey(hostId),
			queryFn: () => fetchRemoteSessions(hostId),
			retry: 1,
			refetchInterval: 15_000,
			subscribed: options.subscribed,
		})),
	});
	return {
		data: connected.flatMap((hostId, index) => projects[index]?.data
			? toRemoteWorkspaces(hostId, projects[index].data, sessions[index]?.data ?? [])
			: []),
		loadedProjectHostIds: connected.filter((_, index) => projects[index]?.data !== undefined),
		loadedSessionHostIds: connected.filter((_, index) => sessions[index]?.data !== undefined),
		failedHostIds: connected.filter((_, index) => projects[index]?.isError || sessions[index]?.isError),
		refetch: () => Promise.all([...projects, ...sessions].map((query) => query.refetch())),
	};
}

/** A host-qualified project detail query for the board. It shares the remote
 * session query's key, so session mutations refresh both detail surfaces. */
export function useRemoteProjectQuery(hostId: string, projectId: string) {
	return useQuery({
		queryKey: remoteWorkspaceQueryKey(hostId),
		queryFn: () => fetchRemoteWorkspaces(hostId),
		select: (workspaces) => workspaces.find((workspace) => workspace.id === projectId),
		enabled: Boolean(hostId && projectId),
		retry: 1,
	});
}

// Route-level recovery for a Cloud session that has just been created or whose
// list cache is stale. The session screen must resolve it through the control
// plane, never try the local daemon just because the list query has not caught
// up yet.
export function useCloudSessionQuery(
	orgId: string | undefined,
	sessionId: string,
	enabled = true,
	// Poll like the sessions list does while the caller shows this lookup
	// rather than the list's row (a session opened before the list has it):
	// otherwise its lifecycle stays where it was when the view opened.
	poll = false,
) {
	const { client, ready, baseUrl } = useCloudCp();
	const queryClient = useQueryClient();
	const projects = useCloudProjectsQuery({ enabled });
	const lookup = useQuery({
		queryKey: ["cloud-session", baseUrl, orgId ?? "", sessionId],
		enabled: enabled && ready && orgId !== undefined && sessionId !== "",
		// A session the control plane does not have (deleted) is not asked for
		// again.
		retry: (failures, error) => failures < 1 && !isCloudNotFound(error),
		// The poll stops once the lookup fails (a deleted session, or the
		// control plane unreachable: a window focus refetch resumes it once it
		// answers again) or the session is terminated, whose lifecycle no longer
		// moves on its own.
		refetchInterval: (query) =>
			poll && query.state.status !== "error" && !query.state.data?.isTerminated ? 5000 : false,
		queryFn: async (): Promise<CloudCpSession | undefined> => {
			if (orgId === undefined) return undefined;
			const response = await client.getSession(orgId, sessionId);
			return response.session;
		},
	});
	// A session of a project made elsewhere (opened from a link, or right after
	// the API made it) brings that project into the list. Checked once both are
	// in, whichever arrives last.
	const lookedUp = lookup.data;
	const projectsLoaded = projects.data !== undefined;
	useEffect(() => {
		if (orgId === undefined || !lookedUp || !projectsLoaded) return;
		refetchCloudProjectsForUnknownSessions(queryClient, baseUrl, orgId, [lookedUp]);
	}, [baseUrl, lookedUp, orgId, projectsLoaded, queryClient]);
	return lookup;
}

function isCloudNotFound(error: unknown): boolean {
	return error instanceof CloudCpError && error.status === 404;
}

export function useWorkspaceQuery(options: WorkspaceSubscriptionOptions = {}) {
	const local = useQuery({ ...workspaceQueryOptions, subscribed: options.subscribed, enabled: options.enabled });
	const includeCloud = options.includeCloud !== false;
	const cloudOptions = { ...options, enabled: includeCloud && options.enabled !== false };
	const cloud = useCloudProjectsQuery(cloudOptions);
	const cloudSessions = useCloudSessionsQuery(cloudOptions);
	const sharedWorkspaces = useSharedCloudWorkspacesQuery(cloudOptions);
	const { org, ready } = useCloudOrg();
	const orgId = org?.id;
	const localData = local.data;
	const cloudData = cloud.data;
	const cloudSessionData = cloudSessions.data;
	const sharedData = sharedWorkspaces.data;
	const queryClient = useQueryClient();
	const { baseUrl: cloudBaseUrl } = useCloudCp();
	// Checked where both lists meet, so it runs whichever arrives last: the
	// session list can resolve before the project list on the first load.
	useEffect(() => {
		if (!includeCloud || !ready || orgId === undefined || cloudData === undefined || !cloudSessionData) return;
		refetchCloudProjectsForUnknownSessions(queryClient, cloudBaseUrl, orgId, cloudSessionData);
	}, [cloudBaseUrl, cloudData, cloudSessionData, includeCloud, orgId, queryClient, ready]);
	const data = useMemo(() => {
		if (!includeCloud) return localData;
		// Local stays authoritative for loading/error semantics: cloud items only
		// render once the local list exists, and never replace it.
		if (localData === undefined) return localData;
		// Signing out (or turning the offering off) disables the cloud queries,
		// but react-query keeps their last data; without this gate the stale
		// cloud projects would keep rendering for a signed-out user.
		if (!ready || orgId === undefined) return localData;
		const sessions = cloudSessionData ?? [];
		const own = (cloudData ?? []).map((project) => toCloudWorkspace(project, sessions, orgId));
		// A project shared from the person's own organization is already listed.
		const shared = (sharedData ?? []).filter((workspace) => !own.some((ownWorkspace) => ownWorkspace.id === workspace.id));
		if (own.length === 0 && shared.length === 0) return localData;
		return placeStandaloneWorkspaceLast([...localData, ...own, ...shared]);
	}, [localData, cloudData, cloudSessionData, sharedData, includeCloud, orgId, ready]);
	return { ...local, data };
}

/**
 * Subscribe a detail surface to one session instead of the complete workspace
 * tree. TanStack Query applies structural sharing to the selected value, so an
 * activity update elsewhere no longer redraws the open session workspace.
 */
export function useWorkspaceSession(sessionId: string, hostId?: string, localLookupEnabled = true) {
	const queryClient = useQueryClient();
	const selectRemoteSession = useMemo(
		() => (workspaces: WorkspaceSummary[]) => workspaces.flatMap((workspace) => workspace.sessions).find((session) => session.id === sessionId),
		[sessionId],
	);
	const remote = useQuery({
		queryKey: remoteWorkspaceQueryKey(hostId ?? ""),
		queryFn: () => fetchRemoteWorkspaces(hostId ?? ""),
		select: selectRemoteSession,
		enabled: Boolean(hostId && sessionId),
	});
	const selectLocalSession = useMemo(
		() => (workspaces: WorkspaceSummary[]) =>
			workspaces.flatMap((workspace) => workspace.sessions).find((session) => session.id === sessionId),
		[sessionId],
	);
	const local = useQuery({ ...workspaceQueryOptions, select: selectLocalSession, enabled: localLookupEnabled && !hostId });
	const localWorkspaces = useQuery({ ...workspaceQueryOptions, subscribed: false, enabled: localLookupEnabled && Boolean(sessionId) && !hostId });
	const direct = useQuery({
		queryKey: ["session", sessionId],
		enabled: localLookupEnabled && Boolean(sessionId) && !hostId && local.data === undefined,
		retry: (attempt, error) => apiErrorCode(error) === "SESSION_NOT_FOUND" && attempt < 4,
		retryDelay: 250,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}", {
				params: { path: { sessionId } },
			});
			if (error) throw error;
			const session = data?.session;
			if (!session) return undefined;
			const project = session.projectId
				? (localWorkspaces.data?.find((workspace) => workspace.id === session.projectId) ??
					({ id: session.projectId, name: "" } satisfies Pick<WorkspaceSummary, "id" | "name">))
				: ({
						id: STANDALONE_WORKSPACE_ID,
						name: standaloneWorkspaceName(),
					} satisfies Pick<WorkspaceSummary, "id" | "name">);
			return toWorkspaceSession(session, project);
		},
	});
	const cloud = useCloudProjectsQuery({ enabled: !hostId });
	const cloudSessions = useCloudSessionsQuery({ enabled: !hostId });
	const { org, ready } = useCloudOrg();
	const resolvedDirectSession = useMemo(() => {
		if (!direct.data) return undefined;
		const workspace = localWorkspaces.data?.find((candidate) => candidate.id === direct.data?.workspaceId);
		if (!workspace || direct.data.workspaceName === workspace.name) return direct.data;
		return { ...direct.data, workspaceName: workspace.name };
	}, [direct.data, localWorkspaces.data]);
	const cloudSession = useMemo(() => {
		if (!ready || !org?.id || !cloud.data || !cloudSessions.data) return undefined;
		const session = cloudSessions.data.find((candidate) => candidate.id === sessionId);
		if (!session) return undefined;
		const project = cloud.data.find((candidate) => candidate.id === session.projectId);
		return project ? toCloudWorkspaceSession(session, project, org.id) : undefined;
	}, [cloud.data, cloudSessions.data, org?.id, ready, sessionId]);
	useEffect(() => {
		if (hostId || !localLookupEnabled) return;
		if (!resolvedDirectSession) return;
		queryClient.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, (current) => {
			if (!current) return current;
			let changed = false;
			const next = current.map((workspace) => {
				if (workspace.id !== resolvedDirectSession.workspaceId) return workspace;
				if (workspace.sessions.some((session) => session.id === resolvedDirectSession.id)) return workspace;
				changed = true;
				return {
					...workspace,
					sessions: [...workspace.sessions, resolvedDirectSession],
				};
			});
			return changed ? next : current;
		});
	}, [queryClient, resolvedDirectSession, hostId, localLookupEnabled]);
	if (hostId) return remote;
	return {
		...local,
		data: local.data ?? resolvedDirectSession ?? cloudSession,
		isLoading: local.isLoading || direct.isLoading,
	};
}

/**
 * The kind of project a remote session belongs to. It reads the same host list
 * as useWorkspaceSession, from the same moment, so it adds no request.
 */
export function useRemoteSessionProjectKind(sessionId: string, hostId?: string): WorkspaceSummary["kind"] {
	const selectKind = useMemo(
		() => (workspaces: WorkspaceSummary[]) => workspaces.find((workspace) => workspace.sessions.some((session) => session.id === sessionId))?.kind,
		[sessionId],
	);
	return useQuery({
		queryKey: remoteWorkspaceQueryKey(hostId ?? ""),
		queryFn: () => fetchRemoteWorkspaces(hostId ?? ""),
		select: selectKind,
		enabled: Boolean(hostId && sessionId),
	}).data;
}

export type WorkspaceScope = {
	project?: Pick<WorkspaceSummary, "id" | "kind" | "name" | "orchestratorAgent">;
	hasWorkerSessions: boolean;
	session?: WorkspaceSession;
	orchestrator?: WorkspaceSession;
};

function selectWorkspaceScope(
	workspaces: WorkspaceSummary[],
	projectId: string | undefined,
	sessionId: string | undefined,
): WorkspaceScope {
	const session = sessionId
		? workspaces.flatMap((workspace) => workspace.sessions).find((candidate) => candidate.id === sessionId)
		: undefined;
	const resolvedProjectId = session?.workspaceId ?? projectId;
	const workspace = resolvedProjectId ? workspaces.find((candidate) => candidate.id === resolvedProjectId) : undefined;
	// Do not carry the project's complete sessions array into shell chrome. With
	// React Query's structural sharing, this small metadata projection retains
	// its identity when another session in the same project streams an update.
	const project = workspace
		? {
				id: workspace.id,
				kind: workspace.kind,
				name: workspace.name,
				orchestratorAgent: workspace.orchestratorAgent,
			}
		: undefined;
	return {
		project,
		session,
		hasWorkerSessions: workspace ? workerSessions(workspace.sessions).length > 0 : false,
		orchestrator: workspace ? newestActiveOrchestrator(workspace.sessions) : undefined,
	};
}

/**
 * Subscribe shell chrome to just the routed project and session. This avoids
 * redrawing the topbar for streamed activity from every other project.
 */
export function useWorkspaceScope(projectId?: string, sessionId?: string, hostId?: string) {
	const selectLocalScope = useMemo(
		() => (workspaces: WorkspaceSummary[]) => selectWorkspaceScope(workspaces, projectId, sessionId),
		[projectId, sessionId],
	);
	const local = useQuery({ ...workspaceQueryOptions, select: selectLocalScope, enabled: !hostId });
	const remote = useQuery({ queryKey: remoteWorkspaceQueryKey(hostId ?? ""), queryFn: () => fetchRemoteWorkspaces(hostId ?? ""), select: selectLocalScope, enabled: Boolean(hostId) });
	const cloud = useCloudProjectsQuery({ enabled: !hostId });
	const cloudSessions = useCloudSessionsQuery({ enabled: !hostId });
	const { org, ready } = useCloudOrg();
	const cloudScope = useMemo(() => {
		if (!ready || !org?.id || !cloud.data) return undefined;
		const workspaces = cloud.data.map((project) => toCloudWorkspace(project, cloudSessions.data ?? [], org.id));
		return selectWorkspaceScope(workspaces, projectId, sessionId);
	}, [cloud.data, cloudSessions.data, org?.id, projectId, ready, sessionId]);
	// Match useWorkspaceQuery's local-first semantics: do not reveal cloud
	// records before the local workspace query has resolved successfully.
	const data = local.data?.project || local.data?.session || !local.isSuccess
		? local.data
		: cloudScope ?? local.data;
	return hostId ? remote : { ...local, data };
}

function selectTraySessions(workspaces: WorkspaceSummary[]): TraySessionEntry[] {
	const entries: TraySessionEntry[] = [];
	for (const workspace of workspaces) {
		for (const session of workerSessions(workspace.sessions)) {
			const zone = attentionZone(session);
			if ((zone === "merge" && session.status === "merged") || (zone !== "action" && zone !== "merge")) continue;
			entries.push({
				projectId: session.workspaceId,
				projectName: workspace.name,
				sessionId: session.id,
				title: session.title,
				zone,
			});
		}
	}
	return entries;
}

/**
 * The tray lives for the whole app lifetime, but only attention-worthy worker
 * sessions affect its native payload. Select that compact projection at the
 * query boundary so ordinary streamed activity does not wake the runtime.
 */
export function useWorkspaceTraySessions() {
	const local = useQuery({
		...workspaceQueryOptions,
		select: selectTraySessions,
	});
	const cloud = useCloudProjectsQuery();
	const cloudSessions = useCloudSessionsQuery();
	const { org, ready } = useCloudOrg();
	const cloudEntries = useMemo(() => {
		if (!ready || !org?.id || !cloud.data) return [];
		return selectTraySessions(cloud.data.map((project) => toCloudWorkspace(project, cloudSessions.data ?? [], org.id)));
	}, [cloud.data, cloudSessions.data, org?.id, ready]);
	const data = useMemo(() => {
		if (local.data === undefined) return undefined;
		return cloudEntries.length === 0 ? local.data : [...local.data, ...cloudEntries];
	}, [cloudEntries, local.data]);
	return { ...local, data };
}
