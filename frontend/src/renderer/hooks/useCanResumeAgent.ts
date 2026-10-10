import {
	interfaceTransitionIsActive,
	type SessionInterfaceTransition,
	useSessionInterfaceTransitionStatus,
} from "./useSessionInterfaceTransition";
import { sessionAgentExited, type WorkspaceSession } from "../types/workspace";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";

const resumesInFlight = new Map<string, Promise<void>>();

/**
 * Resume a local session's stopped agent, sharing one request per session.
 * Hovering a session starts it early; opening the session then joins the same
 * request instead of being refused as a second resume.
 */
export function resumeAgentOnOpen(sessionId: string): Promise<void> {
	return resumeOnce(sessionId, async () => {
		const { error, response } = await clientForSessionHost().POST("/api/v1/sessions/{sessionId}/resume-agent", {
			params: { path: { sessionId } },
		});
		if (error) throw new Error(apiErrorMessage(error, `Failed to resume agent (${response.status})`));
	});
}

/** Run one resume per local session; later callers join the request in flight. */
export function resumeOnce(sessionId: string, run: () => Promise<void>): Promise<void> {
	const existing = resumesInFlight.get(sessionId);
	if (existing) return existing;
	const request = run().finally(() => resumesInFlight.delete(sessionId));
	resumesInFlight.set(sessionId, request);
	return request;
}

export function canResumeAgent(
	session: WorkspaceSession | undefined,
	transition?: SessionInterfaceTransition,
): boolean {
	return Boolean(
		session &&
			sessionAgentExited(session) &&
			!session.activeAgentSwitch &&
			!session.cloud &&
			!interfaceTransitionIsActive(transition),
	);
}

export function useCanResumeAgent(session: WorkspaceSession | undefined, hostId?: string): boolean {
	const baseEligible = canResumeAgent(session);
	const interfaceTransition = useSessionInterfaceTransitionStatus(baseEligible ? session?.id : undefined, hostId);
	return Boolean(
		baseEligible &&
			!interfaceTransition.isLoading &&
			!interfaceTransition.statusError &&
			!interfaceTransitionIsActive(interfaceTransition.transition),
	);
}
