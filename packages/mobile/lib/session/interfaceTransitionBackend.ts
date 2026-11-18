import { ApiError } from "../api";
import {
	acknowledgeSessionInterfaceTransitionNotice,
	cancelSessionInterfaceTransition,
	getSessionInterfaceTransition,
	startSessionInterfaceTransition,
	type SessionInterfaceTransition,
	type SessionInterfaceTransitionStatus,
} from "../chat/api";
import type { ServerConfig } from "../config";
import { userFacingError } from "../connectionError";

export type InterfaceSwitchSettings = { model?: string; reasoningEffort?: string };

/**
 * Where a session's switch between Chat and Terminal UI is asked for: a
 * paired machine's daemon or Orchestrator Cloud, which answer in the same
 * shapes (Cloud's contract copies the daemon's).
 */
export type InterfaceTransitionBackend = {
	status(sessionId: string): Promise<SessionInterfaceTransitionStatus>;
	/** `settings`: the model and effort the chat had chosen, for a host that starts Terminal UI on them (Cloud). */
	start(sessionId: string, targetMode: "chat" | "tui", policy: "drain" | "interrupt", settings?: InterfaceSwitchSettings): Promise<SessionInterfaceTransition>;
	cancel(sessionId: string): Promise<void>;
	/** The notice's transition once acknowledged; undefined when the host does not return it (read the status again). */
	acknowledgeNotice(sessionId: string, transitionId: string): Promise<SessionInterfaceTransition | undefined>;
	/** How a failed request reads, and the HTTP status it was refused with (a refusal stops the poll). */
	describe(cause: unknown): { message: string; status?: number };
};

export function daemonInterfaceTransitionBackend(cfg: ServerConfig): InterfaceTransitionBackend {
	return {
		status: (sessionId) => getSessionInterfaceTransition(cfg, sessionId),
		start: (sessionId, targetMode, policy) => startSessionInterfaceTransition(cfg, sessionId, targetMode, policy),
		cancel: (sessionId) => cancelSessionInterfaceTransition(cfg, sessionId),
		acknowledgeNotice: (sessionId, transitionId) => acknowledgeSessionInterfaceTransitionNotice(cfg, sessionId, transitionId),
		describe: (cause) => ({ message: userFacingError(cause), status: cause instanceof ApiError ? cause.status : undefined }),
	};
}
