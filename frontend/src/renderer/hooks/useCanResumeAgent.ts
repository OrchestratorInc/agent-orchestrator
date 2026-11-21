import {
	interfaceTransitionIsActive,
	type SessionInterfaceTransition,
	useSessionInterfaceTransitionStatus,
} from "./useSessionInterfaceTransition";
import { sessionAgentExited, sessionIsActive, type WorkspaceSession } from "../types/workspace";
import { apiErrorCode, apiErrorMessage } from "../lib/api-client";
import { appI18n } from "../i18n";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionUiKey } from "../lib/hosts";

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

/** A failed resume as a thrown Error that keeps the API error code. */
export function resumeAgentError(error: unknown, status?: number): Error {
	return Object.assign(new Error(resumeAgentErrorMessage(error, status)), { code: apiErrorCode(error), status });
}

const RESUME_BUSY_CODES = new Set(["AGENT_RESUME_IN_PROGRESS", "AGENT_EXIT_IN_PROGRESS", "SESSION_OPERATION_IN_PROGRESS"]);

export function resumeMayRecover(error: unknown): boolean {
	const status = (error as { status?: number } | undefined)?.status;
	return status === undefined || status === 502 || status === 503 || status === 504 || RESUME_BUSY_CODES.has(apiErrorCode(error) ?? "");
}

/** A failed resume says another app still holds the chat. */
export function resumeOpenElsewhere(error: unknown): boolean {
	return apiErrorCode(error) === "SESSION_OPEN_ELSEWHERE";
}

/**
 * Resume a session's stopped agent on its own host, sharing one request per
 * session. Hovering a session starts it early; opening the session then joins
 * the same request instead of being refused as a second resume.
 */
export function resumeAgentOnOpen(sessionId: string, hostId?: string): Promise<void> {
	return resumeOnce(sessionUiKey(sessionId, hostId), async () => {
		const { error, response } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/resume-agent", {
			params: { path: { sessionId } },
		});
		if (error) throw resumeAgentError(error, response.status);
	});
}

/** Run one resume per key; later callers join the request in flight. */
export function resumeOnce(key: string, run: () => Promise<void>): Promise<void> {
	const existing = resumesInFlight.get(key);
	if (existing) return existing;
	const request = run().finally(() => resumesInFlight.delete(key));
	resumesInFlight.set(key, request);
	return request;
}

export function canResumeAgent(
	session: WorkspaceSession | undefined,
	transition?: SessionInterfaceTransition,
): boolean {
	return Boolean(
		session &&
			(session.needsResume && sessionIsActive(session) || sessionAgentExited(session)) &&
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
