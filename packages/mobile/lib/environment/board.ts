import type { DashboardSession, ProjectInfo } from "../api";
import type { SessionSource } from "./types";

export interface SessionSourceBoard {
	projects: ProjectInfo[];
	sessions: DashboardSession[];
}

export async function loadSessionSourceBoard(source: SessionSource): Promise<SessionSourceBoard> {
	const [projects, sessions] = await Promise.all([
		source.listProjects(),
		source.listSessions(),
	]);

	return { projects, sessions };
}
