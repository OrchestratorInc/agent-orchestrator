import { isOrchestratorSession, type WorkspaceSession } from "../types/workspace";
import { defaultReviewerHarness } from "./reviewer-harnesses";

export type AgentInfo = {
	authentication: {
		state: "authorized" | "unauthorized" | "unknown" | "not_applicable" | "configured";
		freshness: "fresh" | "stale" | "checking";
	};
	effectiveReadiness: "ready" | "not_ready" | "unknown";
	id: string;
	installation: {
		state: "installed" | "not_installed" | "unknown";
		freshness: "fresh" | "stale" | "checking";
	};
	label: string;
	lastUsedAt?: string | null;
	usageCount: number;
};

export type RoleSession = Pick<WorkspaceSession, "id" | "provider" | "kind" | "createdAt">;

export const ROLE_HISTORY_WINDOW_MS = 48 * 60 * 60 * 1000;

export const DEFAULT_AGENT_PRIORITY = ["claude-code", "codex", "cursor", "opencode", "opencode-v2", "aider"] as const;
export const DEFAULT_AGENT_PRIORITY_RANK = new Map<string, number>(
	DEFAULT_AGENT_PRIORITY.map((agent, index) => [agent, index]),
);

export type AgentStatusTone = "success" | "warning" | "muted";

export type RankedAgentOption = AgentInfo & {
	disabled: boolean;
	priorityRank: number;
	rank: number;
	status: string;
	statusTone: AgentStatusTone;
};

export function unknownAgentReadiness(id: string, label: string): AgentInfo {
	return {
		id,
		label,
		installation: {
			state: "unknown",
			freshness: "stale",
		},
		authentication: {
			state: "unknown",
			freshness: "stale",
		},
		effectiveReadiness: "unknown",
		usageCount: 0,
		lastUsedAt: null,
	};
}

export function agentLabelCompare(a: AgentInfo, b: AgentInfo): number {
	return a.label.localeCompare(b.label) || a.id.localeCompare(b.id);
}

export function agentUsageCompare(a: AgentInfo, b: AgentInfo): number {
	const byFrequency = (b.usageCount ?? 0) - (a.usageCount ?? 0);
	if (byFrequency !== 0) return byFrequency;
	const byRecency = (b.lastUsedAt ?? "").localeCompare(a.lastUsedAt ?? "");
	if (byRecency !== 0) return byRecency;
	return 0;
}

export function defaultAuthorizedAgentForRole(
	authorizedAgents: AgentInfo[],
	sessions: RoleSession[],
	role: "worker" | "orchestrator",
): string {
	const eligible = new Set(authorizedAgents.map((agent) => agent.id));
	const usage = new Map<string, { count: number; latest: number }>();
	const cutoff = Date.now() - ROLE_HISTORY_WINDOW_MS;
	for (const session of sessions) {
		if (!isRoleSession(session, role) || !eligible.has(session.provider)) continue;
		const at = session.createdAt ? Date.parse(session.createdAt) : Number.NaN;
		if (Number.isNaN(at) || at < cutoff) continue;
		const prev = usage.get(session.provider) ?? { count: 0, latest: Number.NEGATIVE_INFINITY };
		usage.set(session.provider, { count: prev.count + 1, latest: Math.max(prev.latest, at) });
	}
	const empty = { count: 0, latest: Number.NEGATIVE_INFINITY };
	return [...authorizedAgents]
		.sort((a, b) => {
			const aUsage = usage.get(a.id) ?? empty;
			const bUsage = usage.get(b.id) ?? empty;
			return (
				bUsage.count - aUsage.count ||
				bUsage.latest - aUsage.latest ||
				(DEFAULT_AGENT_PRIORITY_RANK.get(a.id) ?? Number.MAX_SAFE_INTEGER) -
					(DEFAULT_AGENT_PRIORITY_RANK.get(b.id) ?? Number.MAX_SAFE_INTEGER) ||
				agentLabelCompare(a, b)
			);
		})[0]?.id ?? "";
}

function isRoleSession(session: RoleSession, role: "worker" | "orchestrator"): boolean {
	return role === "orchestrator" ? isOrchestratorSession(session) : !isOrchestratorSession(session);
}

export type ProjectAgentDefaults = {
	workerAgent: string;
	orchestratorAgent: string;
	reviewerAgent: string;
};

const FALLBACK_PROJECT_AGENT = DEFAULT_AGENT_PRIORITY[0];

function isConfirmedAgent(agent: AgentInfo): boolean {
	return agent.installation.state === "installed" &&
		["authorized", "not_applicable", "configured"].includes(agent.authentication.state);
}

export function preferredAgentList(agents: readonly AgentInfo[]): string[] {
	const priority = (agent: AgentInfo) => DEFAULT_AGENT_PRIORITY_RANK.get(agent.id) ?? Number.MAX_SAFE_INTEGER;
	return agents
		.filter(isLaunchableAgent)
		.sort(
			(a, b) =>
				Number(isConfirmedAgent(b)) - Number(isConfirmedAgent(a)) ||
				priority(a) - priority(b) ||
				agentLabelCompare(a, b),
		)
		.map((agent) => agent.id);
}

export function preferredAgentNeedsChecks(agents: readonly AgentInfo[]): boolean {
	const priority = (agent: AgentInfo) => DEFAULT_AGENT_PRIORITY_RANK.get(agent.id) ?? Number.MAX_SAFE_INTEGER;
	const first = agents
		.filter(isLaunchableAgent)
		.sort((a, b) => priority(a) - priority(b) || agentLabelCompare(a, b))[0];
	return first !== undefined && !isConfirmedAgent(first);
}

export function defaultProjectAgents(agents: readonly AgentInfo[]): ProjectAgentDefaults {
	const ranked = preferredAgentList(agents);
	const top = ranked[0] ?? FALLBACK_PROJECT_AGENT;
	return {
		workerAgent: top,
		orchestratorAgent: top,
		reviewerAgent: ranked.find((id) => defaultReviewerHarness(id) === id) ?? defaultReviewerHarness(top),
	};
}

function agentStatus(agent: AgentInfo): Pick<RankedAgentOption, "status" | "statusTone"> {
	if (agent.installation.state === "not_installed") {
		return { status: "Needs install", statusTone: "muted" };
	}
	if (agent.authentication.state === "unauthorized") {
		return { status: "Needs auth", statusTone: "warning" };
	}
	if (agent.installation.state === "unknown") {
		return { status: "Install unknown", statusTone: "warning" };
	}
	if (agent.authentication.state === "unknown") {
		return { status: "", statusTone: "muted" };
	}
	if (agent.authentication.state === "configured") {
		return { status: "", statusTone: "muted" };
	}
	return { status: "", statusTone: "success" };
}

export function isReadyAgent(agent: AgentInfo): boolean {
	return agent.installation.state === "installed" &&
		(agent.authentication.state === "authorized" || agent.authentication.state === "not_applicable");
}

export function isLaunchableAgent(agent: AgentInfo): boolean {
	return agent.installation.state !== "not_installed" && agent.authentication.state !== "unauthorized";
}

export function buildRankedAgentOptions({
	agents,
	priorityRank,
	fallbackAgents,
	filter,
}: {
	agents?: AgentInfo[];
	priorityRank: Map<string, number>;
	fallbackAgents: AgentInfo[];
	filter?: (agent: AgentInfo) => boolean;
}): RankedAgentOption[] {
	return (agents ?? fallbackAgents)
		.filter((agent) => (filter ? filter(agent) : true))
		.map((agent) => {
			const isInstallationUnknown = agent.installation.state === "unknown";
			const isAuthUnknown =
				agent.authentication.state === "unknown" || agent.authentication.state === "configured";
			const isAuthorized =
				agent.authentication.state === "authorized" || agent.authentication.state === "not_applicable";
			const isDefinitelyUnavailable =
				agent.installation.state === "not_installed" || agent.authentication.state === "unauthorized";
			const isSelectable = !isDefinitelyUnavailable;
			const rank =
				isAuthorized && agent.installation.state === "installed"
					? 0
					: !isDefinitelyUnavailable && (isInstallationUnknown || isAuthUnknown)
						? 1
						: agent.installation.state === "installed"
							? 2
							: 3;
			return {
				...agent,
				disabled: !isSelectable,
				priorityRank: priorityRank.get(agent.id) ?? Number.MAX_SAFE_INTEGER,
				rank,
				...agentStatus(agent),
			};
		})
		.sort(
			(a, b) =>
				a.rank - b.rank ||
				agentUsageCompare(a, b) ||
				a.priorityRank - b.priorityRank ||
				agentLabelCompare(a, b),
		);
}
