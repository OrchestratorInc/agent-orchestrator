import {
	interfaceTransitionIsActive,
	type SessionInterfaceTransition,
	useSessionInterfaceTransitionStatus,
} from "./useSessionInterfaceTransition";
import { sessionAgentExited, type WorkspaceSession } from "../types/workspace";
import { apiErrorCode, apiErrorMessage } from "../lib/api-client";
import { appI18n } from "../i18n";
import { clientForSessionHost } from "../lib/host-clients";

const resumesInFlight = new Map<string, Promise<void>>();

export function resumeAgentErrorMessage(error: unknown, status?: number): string {
	switch (apiErrorCode(error)) {
		case "AGENT_NOT_EXITED": return appI18n.t("inspector.resumeAlreadyRunning");
		case "AGENT_RESUME_IN_PROGRESS": return appI18n.t("inspector.resumeInProgress");
		case "CHAT_RESUME_FAILED": return appI18n.t("inspector.resumeSavedConversationFailed");
		case "CHAT_AUTH_REQUIRED":
		case "AGENT_AUTH_REQUIRED": return appI18n.t("inspector.resumeAuthRequired");
		default: return apiErrorMessage(error, `Failed to resume agent (${status ?? "unknown"})`);
	}
}

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
		if (error) throw new Error(resumeAgentErrorMessage(error, response.status));
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
