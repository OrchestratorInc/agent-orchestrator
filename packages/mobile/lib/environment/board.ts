import type { DashboardSession, OrchestratorLink, ProjectInfo, SpawnOptions } from "../api";
import type { SessionSource } from "./types";

export interface SessionSourceBoard {
	projects: ProjectInfo[];
	sessions: DashboardSession[];
	orchestrators: OrchestratorLink[];
}

function newerSession(a: DashboardSession, b: DashboardSession): DashboardSession {
	const aStamp = a.createdAt || a.lastActivityAt || "";
	const bStamp = b.createdAt || b.lastActivityAt || "";
	if (aStamp !== bStamp) return aStamp > bStamp ? a : b;
	if (a.lastActivityAt !== b.lastActivityAt) return a.lastActivityAt > b.lastActivityAt ? a : b;
	return a.id > b.id ? a : b;
}

function preferredOrchestrator(current: DashboardSession | undefined, candidate: DashboardSession): DashboardSession {
	if (!current) return candidate;
	if (current.isTerminated !== candidate.isTerminated) return current.isTerminated ? candidate : current;
	return newerSession(current, candidate);
}

export async function loadSessionSourceBoard(source: SessionSource): Promise<SessionSourceBoard> {
	const [projects, listedSessions] = await Promise.all([
		source.listProjects(),
		source.listSessions(),
	]);
	const projectNames = new Map(projects.map((project) => [project.id, project.name]));
	const bestByProject = new Map<string, DashboardSession>();
	const sessions: DashboardSession[] = [];
	for (const session of listedSessions) {
		if (session.kind !== "orchestrator") {
			sessions.push(session);
			continue;
		}
		bestByProject.set(session.projectId, preferredOrchestrator(bestByProject.get(session.projectId), session));
	}
	const orchestrators: OrchestratorLink[] = [...bestByProject.values()].map((session) => ({
		id: session.id,
		projectId: session.projectId,
		projectName: projectNames.get(session.projectId) ?? session.projectId,
		status: session.status,
		activity: session.activity,
		harness: session.harness,
		mode: session.mode,
		updatedAt: session.lastActivityAt,
		runtimeConnected: session.runtimeConnected,
		cloud: session.cloud,
		hasRuntime: session.isTerminated !== true,
		isTerminal: session.isTerminated === true,
	}));

	return { projects, sessions, orchestrators };
}

/** Create through whichever environment is active, then refresh that same board. */
export async function spawnSessionThroughSource(
	source: SessionSource,
	options: SpawnOptions,
	refresh: () => Promise<void>,
): Promise<{ id: string; projectId: string }> {
	if (!options.projectId) throw new Error("Pick a project first");
	const session = await source.createSession(options);
	await refresh();
	return { id: session.id, projectId: options.projectId };
}
