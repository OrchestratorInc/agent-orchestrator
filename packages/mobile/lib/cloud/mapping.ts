import type { DashboardPR, DashboardSession, OrchestratorLink, ProjectInfo } from "../api";
import type { MachineAppearance } from "../machineAppearance";
import { cloudLifecycleStage, cloudStageLabel, cloudStartupProblem } from "./lifecycle";
import type { CloudProject, CloudSession, CloudSessionPullRequest } from "./types";

// Orchestrator Cloud joins the app's existing lists as one more environment beside the
// paired machines: its workers sit on the Workers board, its projects in the
// Projects tab, and it is a choice in the spawn sheet's machine picker. It is
// not a paired host (no daemon, no pairing token), so it never enters
// hostStates; screens add it where they list environments, and the store
// routes its actions to the control plane by this id.
export const CLOUD_HOST_ID = "orchestrator-cloud";
/** The machine label: short, beside the cloud icon, where a machine shows its name. */
export const CLOUD_HOST_NAME = "Cloud";
export const CLOUD_APPEARANCE: MachineAppearance = { symbol: "cloud" };

export function isCloudHost(hostId: string | null | undefined): boolean {
	return hostId === CLOUD_HOST_ID;
}

/** The same mapping the daemon's wire PR gets (api.ts mapPR): the control plane sends the same summary shape. */
export function cloudPullRequest(pr: CloudSessionPullRequest): DashboardPR {
	const ci = pr.ci === "passing" || pr.ci === "failing" || pr.ci === "pending" ? pr.ci : "none";
	const mergeability = (["unknown", "mergeable", "conflicting", "blocked", "unstable"] as const).find((state) => state === pr.mergeability) ?? "unknown";
	const review = pr.review === "approved" ? "approved"
		: pr.review === "changes_requested" ? "changes_requested"
			: pr.review === "review_required" || pr.review === "pending" ? "pending"
				: "none";
	return {
		number: pr.number,
		url: pr.url,
		state: pr.state === "merged" ? "merged" : pr.state === "closed" ? "closed" : "open",
		isDraft: pr.state === "draft",
		branch: pr.sourceBranch,
		baseBranch: pr.targetBranch,
		ciStatus: ci,
		reviewDecision: review,
		mergeability: { state: mergeability, mergeable: mergeability === "mergeable" },
	};
}

/**
 * A cloud session as a board row. Status uses the same vocabulary as the
 * daemon's (both derive it with the public contract's DeriveStatus), so the
 * board groups it the same way; while the sandbox is not usable yet, the
 * row's phrase says what it is waiting on instead.
 */
export function cloudSessionRow(session: CloudSession): DashboardSession {
	const stage = cloudLifecycleStage(session);
	const problem = cloudStartupProblem(session);
	const prs = (session.prs ?? []).map(cloudPullRequest);
	const displayStatus = session.isTerminated ? null
		: problem?.phase === "failed" ? "Failed to start"
			: stage && stage !== "connected" ? cloudStageLabel(stage) ?? null
				: null;
	return {
		id: session.id,
		projectId: session.projectId,
		status: session.status || null,
		displayStatus,
		activity: session.activityState || null,
		harness: session.harness,
		mode: session.interfaceMode === "tui" ? "tui" : "chat",
		branch: session.branch || null,
		issueId: null,
		issueTitle: null,
		userPrompt: null,
		displayName: session.displayName?.trim() || null,
		summary: null,
		createdAt: session.createdAt,
		lastActivityAt: session.updatedAt,
		lastEventAt: session.updatedAt,
		updatedAt: session.updatedAt,
		pr: prs.find((pr) => pr.state === "open") ?? prs[0] ?? null,
		prs,
		isTerminated: session.isTerminated,
		kind: session.kind,
		isPinned: session.pinned === true,
		pinnedAt: session.pinned ? session.pinnedAt ?? null : null,
		// The review page and its actions sheet read these, as from a daemon's row.
		reviewerHarness: session.reviewerHarness || null,
		autoReviewEnabled: session.autoReviewEnabled === true,
		autoInjectReview: session.autoInjectReview ?? true,
		autoInjectCI: session.autoInjectCI ?? true,
	};
}

/** A cloud orchestrator as the Projects tab's orchestrator link. */
export function cloudOrchestratorLink(session: CloudSession, projectName: string): OrchestratorLink {
	return {
		id: session.id,
		projectId: session.projectId,
		projectName,
		status: session.status || null,
		activity: session.activityState || null,
		harness: session.harness,
		mode: session.interfaceMode === "tui" ? "tui" : "chat",
		updatedAt: session.updatedAt,
		runtimeState: session.runtimeState ?? session.observedState ?? null,
		hasRuntime: session.runtimeConnected,
		isTerminal: session.isTerminated,
		isRestorable: session.isTerminated,
	};
}

export function cloudProjectInfo(project: CloudProject): ProjectInfo {
	return { id: project.id, name: project.displayName, kind: project.standalone || !project.repositoryUrl ? "scratch" : "single_repo" };
}

export type CloudEnvironment = {
	hostId: typeof CLOUD_HOST_ID;
	name: string;
	appearance: MachineAppearance;
	connection: "open";
	projects: ProjectInfo[];
	sessions: DashboardSession[];
	orchestrators: OrchestratorLink[];
};

/** Reuse a mapped row while its source is unchanged, so a poll re-renders only rows that moved. */
function stableRows<S extends { id: string }, R>() {
	let cache = new Map<string, { key: string; row: R }>();
	return (sources: readonly S[], map: (source: S) => R, extra = ""): R[] => {
		const next = new Map<string, { key: string; row: R }>();
		const rows = sources.map((source) => {
			const key = JSON.stringify(source) + extra;
			const old = cache.get(source.id);
			const entry = old?.key === key ? old : { key, row: map(source) };
			next.set(source.id, entry);
			return entry.row;
		});
		cache = next;
		return rows;
	};
}

/**
 * Orchestrator Cloud's rows in the shapes the existing screens read. Orchestrators are
 * listed apart from workers, as a daemon lists them; only live ones count, so
 * an archived orchestrator does not hold its project's slot. Create one per
 * owner: it keeps unchanged rows identical between polls.
 */
export function createCloudEnvironment() {
	const projectRows = stableRows<CloudProject, ProjectInfo>();
	const sessionRows = stableRows<CloudSession, DashboardSession>();
	const orchestratorRows = stableRows<CloudSession, OrchestratorLink>();
	return (projects: readonly CloudProject[], sessions: readonly CloudSession[]): CloudEnvironment => {
		const names = new Map(projects.map((project) => [project.id, project.displayName]));
		return {
			hostId: CLOUD_HOST_ID,
			name: CLOUD_HOST_NAME,
			appearance: CLOUD_APPEARANCE,
			connection: "open",
			projects: projectRows(projects, cloudProjectInfo),
			sessions: sessionRows(sessions.filter((session) => session.kind !== "orchestrator"), cloudSessionRow),
			orchestrators: orchestratorRows(
				sessions.filter((session) => session.kind === "orchestrator" && !session.isTerminated),
				(session) => cloudOrchestratorLink(session, names.get(session.projectId) ?? "Project"),
				JSON.stringify([...names]),
			),
		};
	};
}
