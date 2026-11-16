import { useEffect, useRef } from "react";
import { sessionUiKey } from "../lib/hosts";
import { agentTurnRunning } from "../lib/session-presentation";
import type { WorkspaceSession } from "../types/workspace";

type TurnSession = Pick<WorkspaceSession, "id" | "hostId" | "activity">;

/**
 * Tells the Browser's tab host whether each session's agent turn is running,
 * for every session, shown or not. While a turn runs, a tab the person brings
 * forward leaves the agent working on its own (browser-view-host agentTabId);
 * a session in the background must still report its turn ending.
 */
export function useBrowserAgentTurns(projects: readonly { sessions: readonly TurnSession[] }[] | undefined): void {
	const reported = useRef(new Map<string, boolean>());
	useEffect(() => {
		const setAgentTurn = window.ao?.browser?.setAgentTurn;
		if (!setAgentTurn || !projects) return;
		for (const project of projects) {
			for (const session of project.sessions) {
				const sessionId = sessionUiKey(session.id, session.hostId);
				const active = agentTurnRunning(session.activity);
				if (reported.current.get(sessionId) === active) continue;
				reported.current.set(sessionId, active);
				void setAgentTurn({ sessionId, active })?.catch(() => undefined);
			}
		}
	}, [projects]);
}
