import type { AgentProvider } from "../types/workspace";

const TURN_MAY_BE_IN_FLIGHT: ReadonlySet<string> = new Set(["working", "no_signal", "needs_input"]);

export type UpdateRiskSession = {
	id: string;
	title: string;
	workspaceName: string;
	provider: AgentProvider;
	mode?: "chat" | "tui";
	status: string;
	isTerminated?: boolean;
	chatProviderPreserved?: boolean;
};

export function sessionsAtRiskFromInstall<T extends UpdateRiskSession>(sessions: readonly T[]): T[] {
	return sessions.filter(
		(session) =>
			session.isTerminated !== true &&
			session.mode === "chat" &&
			session.chatProviderPreserved !== true &&
			TURN_MAY_BE_IN_FLIGHT.has(session.status),
	);
}
