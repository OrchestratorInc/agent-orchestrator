// The Cloud standalone agents this desktop started, by session: a new agent's
// session opens on its own project's route, which the new chat page, having
// asked for no project, learns only from the create's answer.
const agentProjects = new Map<string, string>();

export function rememberStandaloneAgent(sessionId: string, projectId: string): void {
	agentProjects.set(sessionId, projectId);
}

/** The project a standalone agent this desktop started holds its session in. */
export function standaloneAgentProjectId(sessionId: string): string | undefined {
	return agentProjects.get(sessionId);
}
