import type { DashboardSession } from "./api";
import { sessionTitle } from "./sessionStatus";

export function filterWorkerSessions(
	sessions: readonly DashboardSession[],
	query: string,
	projectNameFor: (projectId: string, session: DashboardSession) => string,
	statusLabelFor: (status: string | null) => string,
): DashboardSession[] {
	const needle = query.trim().toLocaleLowerCase();
	if (!needle) return [...sessions];

	return sessions.filter((session) => {
		const hostName = "hostName" in session && typeof session.hostName === "string" ? session.hostName : undefined;
		const fields = [
			sessionTitle(session),
			projectNameFor(session.projectId, session),
			hostName,
			session.branch,
			statusLabelFor(session.status),
		];
		return fields.some((field) => field?.toLocaleLowerCase().includes(needle));
	});
}
