// OTA update policy. No expo-updates/React Native imports so vitest can run it;
// callers pass the real `expo-updates` module in. Native already checks and
// downloads on cold start (ON_LOAD) and applies on the next one; this adds the
// resume path: after the app has been in the background a while, offer a
// downloaded update again or check for one. The app never restarts on its own:
// a downloaded update raises the update screen (updateScreenState.ts), and the
// restart waits for the user's tap.

import type { UpdateCheckResult, UpdateFetchResult } from "expo-updates";

/** The slice of `expo-updates` this module calls. */
export type UpdatesApi = {
	readonly isEnabled: boolean;
	checkForUpdateAsync(): Promise<UpdateCheckResult>;
	fetchUpdateAsync(): Promise<UpdateFetchResult>;
};

export type UpdateOutcome =
	| { kind: "disabled" }
	| { kind: "up-to-date" }
	| { kind: "downloaded" }
	| { kind: "error"; error: unknown };

/** Time in the background after which a resume offers a pending update again or checks for one. */
export const MIN_BACKGROUND_MS = 15 * 60 * 1000;

export function onForeground(
	pending: boolean,
	backgroundedAt: number | null,
	now: number,
	minBackground = MIN_BACKGROUND_MS,
): "prompt" | "check" | "none" {
	if (backgroundedAt === null || now - backgroundedAt < minBackground) return "none";
	return pending ? "prompt" : "check";
}

let inflight: Promise<UpdateOutcome> | null = null;

/** Check and download. Never throws, never reloads; concurrent callers share one request. */
export function checkAndDownload(api: UpdatesApi): Promise<UpdateOutcome> {
	if (!api.isEnabled) return Promise.resolve({ kind: "disabled" });
	if (inflight) return inflight;
	inflight = run(api).finally(() => {
		inflight = null;
	});
	return inflight;
}

async function run(api: UpdatesApi): Promise<UpdateOutcome> {
	try {
		const check = await api.checkForUpdateAsync();
		if (!check.isAvailable && !check.isRollBackToEmbedded) return { kind: "up-to-date" };
		const fetched = await api.fetchUpdateAsync();
		return fetched.isNew || fetched.isRollBackToEmbedded ? { kind: "downloaded" } : { kind: "up-to-date" };
	} catch (error) {
		return { kind: "error", error };
	}
}

export type UpdateRowInput = {
	enabled: boolean;
	pending: boolean;
	phase: "idle" | "checking" | "downloading";
	lastManual: UpdateOutcome | null;
};

export type UpdateRow = {
	value: string;
	tone: "default" | "good" | "bad";
	busy: boolean;
	action: "check" | "restart" | null;
};

/** What the Settings "App updates" row shows and does. */
export function describeUpdateRow(input: UpdateRowInput): UpdateRow {
	if (!input.enabled) return { value: "Off in development builds", tone: "default", busy: false, action: null };
	if (input.pending) return { value: "Ready — tap to restart", tone: "good", busy: false, action: "restart" };
	if (input.phase === "downloading") return { value: "Downloading…", tone: "default", busy: true, action: null };
	if (input.phase === "checking") return { value: "Checking…", tone: "default", busy: true, action: null };
	if (input.lastManual?.kind === "error") return { value: "Couldn't check", tone: "bad", busy: false, action: "check" };
	if (input.lastManual?.kind === "up-to-date") return { value: "Up to date", tone: "good", busy: false, action: "check" };
	return { value: "Check now", tone: "default", busy: false, action: "check" };
}

/**
 * Whether a downloaded update should raise the update screen: once per update,
 * so "Not now" holds until a different update lands (or a long absence offers
 * it again through `onForeground`). An update without an id — a rollback to the
 * embedded bundle — is keyed as one stand-in update.
 */
export function shouldOfferUpdate(pending: boolean, updateId: string | undefined, lastOffered: string | null): boolean {
	return pending && (updateId ?? "embedded") !== lastOffered;
}
