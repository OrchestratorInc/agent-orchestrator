import type { DashboardSession, OrchestratorLink, ProjectInfo } from "./api";
import { machineAppearance, type MachineAppearance } from "./machineAppearance";
import { orchestratorProjectSections, type OrchestratorProjectRow, type OrchestratorProjectSection } from "./orchestratorView";

export type HostedSession = DashboardSession & { hostId: string; hostName: string; hostAppearance?: MachineAppearance };
export type HostedProject = ProjectInfo & { hostId: string; hostName: string; hostAppearance?: MachineAppearance };
export type HostedOrchestrator = OrchestratorLink & { hostId: string; hostName: string };
export type HostedProjectRow = Omit<OrchestratorProjectRow, "project"> & { project: HostedProject };

/** Keep presentation objects stable when unrelated host status or notification counts change. */
export function createHostedRowsCache<T extends { id: string }>() {
	type Hosted = T & { hostId: string; hostName: string; hostAppearance?: MachineAppearance };
	let previous: Hosted[] = [];
	let cache = new Map<string, { source: T; row: Hosted }>();
	return (hosts: readonly { hostId: string; name: string; appearance?: MachineAppearance; rows: readonly T[] }[]): Hosted[] => {
		const nextCache = new Map<string, { source: T; row: Hosted }>();
		const next = hosts.flatMap((host) => host.rows.map((source) => {
			const key = hostedRowKey(host.hostId, source.id);
			const old = cache.get(key);
			const row = old?.source === source && old.row.hostName === host.name
				&& old.row.hostAppearance?.symbol === host.appearance?.symbol
				? old.row
				: { ...source, hostId: host.hostId, hostName: host.name, hostAppearance: host.appearance };
			nextCache.set(key, { source, row });
			return row;
		}));
		cache = nextCache;
		if (next.length !== previous.length || next.some((row, index) => row !== previous[index])) previous = next;
		return previous;
	};
}

export function hostedRowKey(hostId: string, id: string): string {
	return JSON.stringify([hostId, id]);
}

export function hostedProjectKey(project: { id: string; hostId?: string }): string {
	return project.hostId ? hostedRowKey(project.hostId, project.id) : project.id;
}

export function sessionHostId(session: DashboardSession): string | undefined {
	return "hostId" in session && typeof session.hostId === "string" ? session.hostId : undefined;
}

export function sessionMachineIdentity(session: DashboardSession): { name: string; appearance: MachineAppearance } | undefined {
	if (!("hostId" in session) || typeof session.hostId !== "string" || !("hostName" in session) || typeof session.hostName !== "string") return undefined;
	const appearance = "hostAppearance" in session ? session.hostAppearance as MachineAppearance | undefined : undefined;
	return { name: session.hostName, appearance: machineAppearance({ id: session.hostId, name: session.hostName, appearance }) };
}

export function hostedSessionKey(session: DashboardSession): string {
	const hostId = sessionHostId(session);
	return hostId ? hostedRowKey(hostId, session.id) : `${session.projectId}:${session.id}`;
}

/** Build each host's project rows separately so matching project IDs never share workers or an orchestrator. */
export function hostedProjectSections(hosts: readonly {
	hostId: string;
	name: string;
	appearance?: MachineAppearance;
	connection?: "closed" | "connecting" | "open";
	projects: readonly ProjectInfo[];
	sessions: readonly DashboardSession[];
	orchestrators: readonly OrchestratorLink[];
}[]): Array<Omit<OrchestratorProjectSection, "data"> & { data: HostedProjectRow[] }> {
	const sections = new Map<OrchestratorProjectSection["key"], Omit<OrchestratorProjectSection, "data"> & { data: HostedProjectRow[] }>();
	for (const host of hosts) {
		for (const section of orchestratorProjectSections(host.projects, host.sessions, host.orchestrators)) {
			const current = sections.get(section.key) ?? { key: section.key, title: section.title, data: [] };
			current.data.push(...section.data.map((row) => ({
				...row,
				project: {
					...row.project,
					hostId: host.hostId,
					hostName: `${host.name}${host.connection === "closed" ? " (offline)" : ""}`,
					hostAppearance: machineAppearance({ id: host.hostId, name: host.name, appearance: host.appearance }),
				},
			})));
			sections.set(section.key, current);
		}
	}
	return ["attention", "coordinating", "not-running"].flatMap((key) => {
		const section = sections.get(key as OrchestratorProjectSection["key"]);
		return section ? [section] : [];
	});
}
