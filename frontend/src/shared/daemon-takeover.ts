import type { DaemonProbe } from "./daemon-attach";
import { parseRunFile } from "./daemon-discovery";

/** Only the Go bootstrap helper may verify and signal a saved process owner. */
export function orphanDaemonTakeoverDecision(runFileContents: string | null, probe: DaemonProbe | null): "none" | "check" | "blocked" {
	// A new responder appeared since attach failed. Retry discovery rather than
	// replacing a daemon whose ownership this launch has not established.
	if (probe) return "blocked";
	if (runFileContents === null) return "none";
	const info = parseRunFile(runFileContents);
	if (!info || !Number.isSafeInteger(info.pid) || info.pid <= 0) return "blocked";
	return "check";
}

export type BrowserDaemonOwnershipDecision =
	| { action: "attach" }
	| { action: "replace"; keepAlive: boolean };

/**
 * A daemon can authenticate to only the Electron launch that handed it the
 * memory-only browser runtime token. Reuse within that launch is safe. Across
 * launches we replace it gracefully, preserving the original persistence mode:
 * app-owned daemons remain app-owned; persistent/headless daemons remain alive
 * after the window closes.
 */
export function browserDaemonOwnershipDecision(
	currentAppRunId: string,
	existing: { owner?: string; appRunId?: string },
): BrowserDaemonOwnershipDecision {
	if (existing.appRunId === currentAppRunId) return { action: "attach" };
	return { action: "replace", keepAlive: existing.owner !== "app" };
}
