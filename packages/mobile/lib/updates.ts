// OTA update policy. No expo-updates/React Native imports so vitest can run it;
// callers pass the real `expo-updates` module in. Native already checks and
// downloads on cold start (ON_LOAD) and applies on the next one; this adds the
// resume path: coming back to the app checks again once the last check is a
// few minutes old, and offers a put-off update again once a day. Android keeps
// the process alive between uses far longer than iOS, so without the resume
// check an Android phone could go days without a cold start, and so without
// seeing an update. The app never restarts on its own: a downloaded update
// raises the update screen (updateScreenState.ts), and the restart waits for
// the user's tap.

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

/** A resume checks for an update when the last check, by native or by us, is at least this old. */
export const CHECK_STALE_MS = 10 * 60 * 1000;

/** A downloaded update the user put off is offered again, on a resume, at most this often. */
export const REOFFER_MS = 24 * 60 * 60 * 1000;

export type ForegroundInput = {
	/** An update is downloaded and waiting for a restart. */
	pending: boolean;
	/** When the last check started or finished (ms), or null if there has been none this run. */
	lastCheckAt: number | null;
	/** When the update screen last offered the pending update (ms), or null if it never has. */
	lastOfferedAt: number | null;
	now: number;
};

/**
 * What a return to the foreground does. A put-off update comes back once a day;
 * otherwise a stale check runs again, silently, and a new download is offered
 * by `shouldOfferUpdate` like any other. A pending update never offered yet
 * (one that landed during onboarding) is left to `shouldOfferUpdate` as well.
 */
export function onForeground({ pending, lastCheckAt, lastOfferedAt, now }: ForegroundInput): "prompt" | "check" | "none" {
	if (pending && lastOfferedAt !== null && now - lastOfferedAt >= REOFFER_MS) return "prompt";
	// Also while an update is pending: a newer one may have been published since.
	if (lastCheckAt === null || now - lastCheckAt >= CHECK_STALE_MS) return "check";
	return "none";
}

/** The later of two optional times (ms); null when neither is known. */
export function latestTime(a: number | null | undefined, b: number | null | undefined): number | null {
	if (a == null) return b ?? null;
	if (b == null) return a;
	return Math.max(a, b);
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
 * Onboarding and pairing: a user here is still setting the app up, has nothing
 * to lose by not restarting, and gets the update on the next launch anyway, so
 * the update screen waits until they leave.
 */
export function isSetupRoute(pathname: string): boolean {
	return /^\/(onboarding|pair)(\/|$)/.test(pathname);
}

/**
 * Whether a downloaded update should raise the update screen: once per update,
 * so "Not now" holds until a different update lands (or a day later, when
 * `onForeground` offers it again), and never over onboarding or pairing. An
 * update without an id — a rollback to the embedded bundle — is keyed as one
 * stand-in update.
 */
export function shouldOfferUpdate(
	pending: boolean,
	updateId: string | undefined,
	lastOffered: string | null,
	pathname = "/",
): boolean {
	return pending && !isSetupRoute(pathname) && (updateId ?? "embedded") !== lastOffered;
}
