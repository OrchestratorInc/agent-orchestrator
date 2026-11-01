// Sheets are native `formSheet` routes now, and a route can't be handed an
// `onSelect` callback the way a component could. The opener parks its callback
// here and passes the key as a route param; the sheet route looks it up when a
// choice is made.
//
// Deliberately a module-level map rather than context: the opener and the sheet
// are separate screens in the navigator, so there is no shared React subtree to
// hang a provider on. Keys are released when the route unmounts (including a
// swipe-dismiss with no selection), so a closure can't outlive its sheet.

import type { MachineAppearance } from "./machineAppearance";
import type { MachineNameChoice } from "./pairFlow";

const handlers = new Map<string, (value: unknown) => void>();
let seq = 0;

/** Parks `fn` and returns the key to pass along as a route param. */
export function parkSheetResult<T>(fn: (value: T) => void): string {
	const key = `sheet-${++seq}`;
	handlers.set(key, fn as (value: unknown) => void);
	return key;
}

/** The parked callback for `key`, or undefined if it was already released. */
export function takeSheetResult<T>(key?: string): ((value: T) => void) | undefined {
	if (!key) return undefined;
	return handlers.get(key) as ((value: T) => void) | undefined;
}

export function releaseSheetResult(key?: string): void {
	if (key) handlers.delete(key);
}

/** Test seam — the map is module state, so suites must be able to reset it. */
export function resetSheetResults(): void {
	handlers.clear();
	seq = 0;
}

// Route + params for each sheet, kept here beside `parkSheetResult` so callers
// can't forget to park the callback, and so the string encoding of each param
// (route params are strings) lives in one place rather than at every call site.

export function agentSheetRoute(opts: { selected: string; onSelect: (id: string) => void; allowed?: string[]; mode?: "chat" | "tui" }) {
	return {
		pathname: "/sheets/agent" as const,
		params: {
			resultKey: parkSheetResult(opts.onSelect),
			selected: opts.selected,
			...(opts.allowed ? { allowed: opts.allowed.join("|") } : {}),
			...(opts.mode ? { mode: opts.mode } : {}),
		},
	};
}

export function modelSheetRoute(opts: { agentId: string; projectId: string; selected: string; onSelect: (id: string) => void }) {
	return {
		pathname: "/sheets/model" as const,
		params: {
			resultKey: parkSheetResult(opts.onSelect),
			agentId: opts.agentId,
			projectId: opts.projectId,
			selected: opts.selected,
		},
	};
}

export function connectSheetRoute(onConnected: () => void, hostId?: string) {
	return {
		pathname: "/sheets/connect" as const,
		params: {
			resultKey: parkSheetResult(onConnected),
			...(hostId ? { hostId } : {}),
		},
	};
}

/** Opens the required-name prompt used after a new QR code is verified. */
export function machineNameSheetRoute(opts: {
	suggested: string;
	appearance: MachineAppearance;
	/** The machine being named, so the sheet can compare icons with the others. */
	hostId: string;
	onSelect: (choice: MachineNameChoice | null) => void;
}) {
	return {
		pathname: "/sheets/machine-name" as const,
		params: {
			resultKey: parkSheetResult(opts.onSelect),
			suggested: opts.suggested,
			symbol: opts.appearance.symbol,
			hostId: opts.hostId,
		},
	};
}
