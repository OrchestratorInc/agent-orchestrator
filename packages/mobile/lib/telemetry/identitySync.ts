import type { ServerConfig } from "../config";
import type { DesktopTelemetryIdentity } from "./telemetry";

/**
 * Builds the sync that adopts the connected desktops' telemetry identity. A
 * later call supersedes an earlier one: if responses finish out of order, only
 * the newest call may apply its result, so a slow response from a desktop that
 * is no longer the current set cannot undo a newer opt-out.
 * ponytail: any desktop that has opted out wins; otherwise the first with an
 * identity is used.
 */
export function createDesktopIdentitySync(
	fetchIdentity: (cfg: ServerConfig) => Promise<DesktopTelemetryIdentity>,
	adopt: (identity: DesktopTelemetryIdentity) => void,
): (configs: ServerConfig[]) => Promise<void> {
	let generation = 0;
	return async (configs) => {
		const mine = ++generation;
		if (configs.length === 0) return;
		const results = await Promise.all(configs.map((cfg) => fetchIdentity(cfg).catch(() => null)));
		if (mine !== generation) return;
		const reported = results.filter((r): r is DesktopTelemetryIdentity => r !== null);
		const next = reported.find((r) => r.optedOut) ?? reported.find((r) => r.distinctId);
		if (next) adopt(next);
	};
}
