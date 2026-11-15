// The warnings above the spawn controls, decided in one place so a single cause
// produces a single line. Pure, so it is unit-testable.

import { isDesktopUnreachable } from "./connectionError";
import { classifyLink, EMPTY_LINK_TRACKER, type LinkTracker, type PhoneNetwork } from "./linkHealth";

/**
 * Whether the chosen machine is offline, by the same rule as every other
 * screen (store.tsx's HostScope): the link tracker has given up on it and its
 * last poll failed as unreachable. A connection closed on purpose (the app
 * going to the background drops it every time) is not offline: reading it as
 * offline flashed the notice after every app switch, into the app-switcher
 * snapshot too.
 */
export function spawnHostOffline(
	host: { linkTracker?: LinkTracker; connection: string; error: string | null; errorStatus: number | null } | undefined,
	phone: PhoneNetwork,
	now: number,
): boolean {
	if (!host) return false;
	return classifyLink(host.linkTracker ?? EMPTY_LINK_TRACKER, phone, now) === "unreachable"
		&& isDesktopUnreachable({ connection: host.connection, error: host.error, errorStatus: host.errorStatus });
}

export const SPAWN_OFFLINE_NOTICE = "This machine is offline. You can start a worker once it reconnects.";
export const NO_CHAT_AGENT_NOTICE =
	"No installed agent on this Orchestrator host currently supports Chat. Choose Terminal UI or install/authenticate a Chat-capable agent.";

export type SpawnNoticeInput = {
	/** The board's poll has lost the desktop. */
	offline: boolean;
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
	if (input.offline) return [SPAWN_OFFLINE_NOTICE];
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
