const agentProjects = new Map<string, string>();

export function rememberStandaloneAgent(sessionId: string, projectId: string): void {
	agentProjects.set(sessionId, projectId);
}

export function standaloneAgentProjectId(sessionId: string): string | undefined {
	return agentProjects.get(sessionId);
}
