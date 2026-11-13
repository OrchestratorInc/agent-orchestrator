import { useEffect, useRef, useState } from "react";
import type { SessionInterfaceTransition } from "../chat/api";
import { mobileInterfaceTransitionIsActive, mobileInterfaceTransitionIsCancellable } from "../session/interfaceTransition";
import type { InterfaceTransitionBackend } from "../session/interfaceTransitionBackend";
import { INTERFACE_HANDOFF_WAIT_MS, type InterfaceHandoff } from "../session/useInterfaceHandoff";
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
 * Whether the switch card covers a Cloud session's screen. A machine's screens
 * keep a banner until the source starts stopping, since a drain lasts as long
 * as the agent's turn; there an idle source passes those phases in a moment.
 * On Cloud they take seconds (the sandbox's worker has to pick the request
 * up), and a banner over a screen with nothing to wait for read as a false
 * start before the card. So the card covers the screen from the tap
 * (`launching`), and the banner is kept only while a drain waits on a turn the
 * agent is still working through ("Finish, then switch").
 */
export function cloudSwitchCovers(
	transition: Pick<SessionInterfaceTransition, "phase" | "policy"> | undefined,
	{ launching, sourceBusy }: { launching: boolean; sourceBusy: boolean },
): boolean {
	if (launching) return true;
	if (!transition || !mobileInterfaceTransitionIsActive(transition)) return false;
	return !(mobileInterfaceTransitionIsCancellable(transition) && transition.policy === "drain" && sourceBusy);
}

type AwaitedSwitch = { id: string; targetMode: "chat" | "tui"; read: boolean };

/**
 * A Cloud session's handoff to the interface it switched to: Cloud's version
 * of `useInterfaceHandoff`. Cloud's status names only a switch in progress, so
 * a finished one vanishes rather than reading "completed", which is what a
 * machine's handoff waits on. Once a switch this screen watched vanishes, this
 * reads the session again (`onEnded`) and stays `pending` until the screen
 * shows the new interface and that read has landed. Without it the card
 * dropped at once and the old view showed through: the terminal it had just
 * stopped read "Agent terminal ended" until the session's next read.
 *
 * A switch that failed or was cancelled stays in the status: it is read again
 * all the same (its notice says what happened), and nothing is held.
 */
export function useCloudSwitchHandoff(
	transition: SessionInterfaceTransition | undefined,
	screenMode: "chat" | "tui",
	onEnded: () => Promise<void>,
): InterfaceHandoff {
	const watched = useRef<{ id: string; targetMode: "chat" | "tui" } | undefined>(undefined);
	const ended = useRef(onEnded);
	ended.current = onEnded;
	const [awaited, setAwaited] = useState<AwaitedSwitch>();
	const [stalledId, setStalledId] = useState<string>();
	const activeId = transition && mobileInterfaceTransitionIsActive(transition) ? transition.id : undefined;
	const activeTarget = activeId ? transition?.targetMode : undefined;
	// Gone from the status: Cloud finished it. (A completed one, should Cloud ever list it, is the same.)
	const finished = !activeId && (!transition || transition.phase === "completed");
	useEffect(() => {
		if (activeId && activeTarget) {
			watched.current = { id: activeId, targetMode: activeTarget };
			return;
		}
		const last = watched.current;
		if (!last) return;
		watched.current = undefined;
		if (finished) setAwaited({ ...last, read: false });
		const markRead = () => setAwaited((current) => current?.id === last.id ? { ...current, read: true } : current);
		void ended.current().then(markRead, markRead);
	}, [activeId, activeTarget, finished]);
	const arrived = awaited !== undefined && awaited.read && awaited.targetMode === screenMode;
	useEffect(() => {
		if (arrived) setAwaited(undefined);
	}, [arrived]);
	const pendingId = awaited && !arrived ? awaited.id : undefined;
	useEffect(() => {
		if (!pendingId) return;
		const timer = setTimeout(() => setStalledId(pendingId), INTERFACE_HANDOFF_WAIT_MS);
		return () => clearTimeout(timer);
	}, [pendingId]);
	return { pending: pendingId !== undefined, stalled: pendingId !== undefined && stalledId === pendingId };
}
