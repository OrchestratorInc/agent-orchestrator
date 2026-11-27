import type { DashboardSession, OrchestratorLink } from "../api";

/**
 * The project line under a local chat's title. It is never empty: a worker
 * whose project is still loading (or was deleted) shows the project id rather
 * than a bare state dot, and a worker without a project reads "Standalone".
 */
export function conversationProjectLabel(
	session: DashboardSession | OrchestratorLink,
	projects: readonly { id: string; name?: string | null }[],
): string {
	if ("projectName" in session) return session.projectName || session.projectId;
	if (!session.projectId) return "Standalone";
	return projects.find((project) => project.id === session.projectId)?.name || session.projectId;
}
