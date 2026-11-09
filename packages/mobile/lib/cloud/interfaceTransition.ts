import { useEffect, useRef } from "react";
import type { InterfaceTransitionBackend } from "../session/interfaceTransitionBackend";
import type { CloudClient } from "./client";
import { CloudError, cloudErrorMessage } from "./errors";
import type { CloudInterfaceTransition, CloudSession } from "./types";

/**
 * A Cloud session's switch between Chat and Terminal UI, through the same
 * hook a machine's uses (useInterfaceTransition): the desktop's
 * SessionInterfaceSwitch asks the control plane these same routes. Failures
 * read in Cloud's words, never a machine's ("rejected this phone's password").
 */
export function cloudInterfaceTransitionBackend(client: CloudClient, orgId: string): InterfaceTransitionBackend {
	return {
		status: (sessionId) => client.getInterfaceTransition(orgId, sessionId),
		// The chat's model and effort go only with a switch to Terminal UI: Cloud refuses them otherwise.
		start: async (sessionId, targetMode, policy, settings) => (await client.startInterfaceTransition(orgId, sessionId, {
			targetMode, policy,
			...(targetMode === "tui" && settings?.model ? { model: settings.model } : {}),
			...(targetMode === "tui" && settings?.reasoningEffort ? { reasoningEffort: settings.reasoningEffort } : {}),
		})).transition,
		cancel: async (sessionId) => { await client.cancelInterfaceTransition(orgId, sessionId); },
		// The control plane answers only "ok": the hook reads the status again.
		acknowledgeNotice: async (sessionId, transitionId) => {
			await client.acknowledgeInterfaceTransitionNotice(orgId, sessionId, transitionId);
			return undefined;
		},
		describe: (cause) => ({ message: cloudErrorMessage(cause), status: cause instanceof CloudError ? cause.status : undefined }),
	};
}

/**
 * A Terminal UI session at work, as the desktop reads it before switching one
 * back to Chat: then the person chooses to let it finish or stop it. An idle
 * terminal is stopped directly: Cloud cannot see a terminal's turn end, so a
 * drain from one waits for nothing and fails (SOURCE_DRAIN_FAILED).
 */
export function cloudTerminalBusy(session: Pick<CloudSession, "activityState" | "status">): boolean {
	return ["active", "waiting_input", "blocked"].includes(session.activityState) || session.status === "working" || session.status === "needs_input";
}

/**
 * The notice a failed switch leaves, except a terminal-to-Chat drain's: the
 * next switch from the idle terminal stops it directly, so there is nothing
 * to tell (as the desktop hides it).
 */
export function cloudSwitchNoticeShown(transition: Pick<CloudInterfaceTransition, "sourceMode" | "targetMode" | "errorCode"> | undefined): boolean {
	return !(transition?.sourceMode === "tui" && transition.targetMode === "chat" && transition.errorCode === "SOURCE_DRAIN_FAILED");
}

/**
 * Calls `onEnded` once a switch this screen watched is no longer under way.
 * Cloud's status names only a switch in progress, so a finished one vanishes
 * rather than reading "completed" (which is what tells the hook to read the
 * session again): without this the screen sat on the old interface, with no
 * banner, until the next poll of the session.
 */
export function useCloudSwitchEnded(activeTransitionId: string | undefined, onEnded: () => void): void {
	const watched = useRef<string | undefined>(undefined);
	const ended = useRef(onEnded);
	ended.current = onEnded;
	useEffect(() => {
		if (activeTransitionId) {
			watched.current = activeTransitionId;
			return;
		}
		if (!watched.current) return;
		watched.current = undefined;
		ended.current();
	}, [activeTransitionId]);
}
