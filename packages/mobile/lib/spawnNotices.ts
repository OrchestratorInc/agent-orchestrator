// The warnings above the spawn controls, decided in one place so a single cause
// produces a single line. Pure, so it is unit-testable.

import { isDesktopUnreachable } from "./connectionError";
import { machineState, rejectedMachineLine, type MachineState } from "./hostRoute";
import { classifyLink, EMPTY_LINK_TRACKER, type LinkTracker, type PhoneNetwork } from "./linkHealth";

/**
 * A paired machine's state from its snapshot, by the same rule as every other
 * screen (store.tsx's HostScope, machineState): offline once the link tracker
 * has given up on it and its last poll failed as unreachable; rejected once
 * its last poll was refused (a rejected pairing, the lockout), which stops
 * the poll for good. A connection closed on purpose (the app going to the
 * background drops it every time) is neither: reading it as offline flashed
 * the notice after every app switch, into the app-switcher snapshot too.
 */
export function spawnHostState(
	host: { linkTracker?: LinkTracker; connection: string; error: string | null; errorStatus: number | null } | undefined,
	phone: PhoneNetwork,
	now: number,
): MachineState {
	if (!host) return "ok";
	const unreachable = classifyLink(host.linkTracker ?? EMPTY_LINK_TRACKER, phone, now) === "unreachable"
		&& isDesktopUnreachable({ connection: host.connection, error: host.error, errorStatus: host.errorStatus });
	return machineState({ unreachable, paired: true, connection: host.connection, error: host.error, errorStatus: host.errorStatus });
}

export const SPAWN_OFFLINE_NOTICE = "This machine is offline. You can start a worker once it reconnects.";
export const NO_CHAT_AGENT_NOTICE =
	"No installed agent on this Orchestrator host currently supports Chat. Choose Terminal UI or install/authenticate a Chat-capable agent.";
// A Start whose draft save was dropped: the account changed or the machine was
// forgotten while it waited. The saver can't tell which, so one line covers both.
export const START_DROPPED_NOTICE = "This task wasn't started because the account or machine changed. Check the details and press Start again.";

/**
 * The page's Start error once the form is edited: the dropped-Start notice
 * goes, since it asked for exactly that; any other error stays until the next
 * Start, as before.
 */
export function errorAfterEdit(error: string | null): string | null {
	return error === START_DROPPED_NOTICE ? null : error;
}

export type SpawnNoticeInput = {
	/** The chosen machine's state (spawnHostState): lost, or refusing this phone. */
	machine: MachineState;
	/** The status of the poll that machine refused, for its copy. */
	errorStatus?: number | null;
	mode: "chat" | "tui";
	/** The agent catalog is still loading. */
	loading: boolean;
	/** The agent catalog answered (as opposed to still loading or failed). */
	catalogLoaded: boolean;
	catalogError: string | null;
	/** Agents usable in the current mode. */
	agentCount: number;
	modelError?: string;
};

export function spawnNotices(input: SpawnNoticeInput): string[] {
	// Every request on this screen fails the same way while the desktop is gone,
	// so one line says so instead of one per request.
	if (input.machine === "offline") return [SPAWN_OFFLINE_NOTICE];
	// The same for a machine that refused this phone: one line, in the board's
	// words, which say whether to scan a new code. Its poll has stopped, so no
	// reconnect will clear it.
	if (input.machine === "rejected") return [rejectedMachineLine(input.errorStatus ?? null)];
	const notices: string[] = [];
	// Only a catalog that actually answered can say no agent supports Chat. A
	// failed load is empty too, and blaming the install for it misleads.
	if (input.mode === "chat" && !input.loading && input.catalogLoaded && !input.catalogError && input.agentCount === 0) {
		notices.push(NO_CHAT_AGENT_NOTICE);
	}
	if (input.catalogError) notices.push(input.catalogError);
	if (input.modelError) notices.push(input.modelError);
	return [...new Set(notices)];
}
