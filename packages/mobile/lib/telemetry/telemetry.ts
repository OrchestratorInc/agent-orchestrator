import { type ActiveStorage, reserveDailyActive } from "./dailyActive";
import { MOBILE_ALLOWLIST, MOBILE_EVENTS, type MobileEventName } from "./events";
import { sanitizeMobileProperties } from "./sanitize";

// The capture facade. Everything the app calls goes through here, so the
// sanitizer and the once-per-day gate are the only paths to PostHog. The
// concrete posthog-react-native client is injected as a narrow interface, so
// this whole file is unit-testable without the SDK or a device; runtime.ts is
// the thin edge that constructs the real client.

export interface MobileTelemetryClient {
	capture(event: string, properties?: Record<string, unknown>): void;
	// register returns a Promise in the SDK; we never await it (best-effort).
	register(properties: Record<string, unknown>): void | Promise<void>;
	identify?(distinctId: string): void;
	reset(): void;
	unregister(property: string): void | Promise<void>;
	optOut(): void | Promise<void>;
	optIn(): void | Promise<void>;
}

/**
 * What a paired desktop reports at GET /api/v1/telemetry/identity. The phone
 * keeps its own PostHog identity. The desktop's Cloud ID and installation ID
 * are properties only. No email crosses this boundary.
 */
export type DesktopTelemetryIdentity = {
	distinctId?: string;
	installId?: string;
	cloudUserId?: string;
	githubLogin?: string;
	optedOut: boolean;
};

export type MobileTelemetry = {
	/** Emit an allowlisted event. Unknown event names and unknown props are dropped. */
	capture(event: MobileEventName, properties?: Record<string, unknown>): void;
	/** Emit the daily-active heartbeat at most once per UTC day. */
	active(storage?: ActiveStorage, now?: Date): Promise<void>;
	/** Apply the paired desktop's properties and opt-out. */
	adoptDesktopIdentity(identity: DesktopTelemetryIdentity | null): void;
	/** Remove an SDK identity inherited from older desktop adoption code. */
	resetPhoneIdentity(): void;
	releasePhoneIdentity(): void;
	setOptedOut(optedOut: boolean): void;
	/**
	 * Applies the saved opt-out read from storage and releases capture. Ignored
	 * for the opt-out value if a connected desktop has already reported its live
	 * state, because that is newer than what was saved.
	 */
	restoreOptOut(saved: boolean): void;
};

export type MobileTelemetryOptions = {
	disabledEvents?: readonly string[];
	/** The phone's existing persisted installation ID, when primed. */
	phoneInstallId?: () => string | null;
	awaitIdentityMigration?: boolean;
	/**
	 * Returns false to drop an event that has hit the per-name rate cap. Sync so
	 * capture() stays non-blocking; runtime.ts backs it with an in-memory,
	 * persisted limiter. Omitted in tests that are not exercising the cap.
	 */
	allow?: (event: string) => boolean;
	/** Called when the opt-out flips, so the runtime can persist it. */
	onOptOutChange?: (optedOut: boolean) => void;
	/**
	 * Drop every capture until setOptedOut() is called with the saved preference.
	 * The runtime sets this because the preference loads asynchronously and a
	 * previously opted-out phone must not send in that gap.
	 */
	awaitPreference?: boolean;
};

export function createMobileTelemetry(
	client: MobileTelemetryClient,
	context: Record<string, unknown>,
	options: MobileTelemetryOptions = {},
): MobileTelemetry {
	// Context rides as super-properties, so every event is tagged with
	// client/platform/version without the call sites repeating it.
	void client.register(context);
	const denied = new Set(options.disabledEvents ?? []);
	const allow = options.allow ?? (() => true);
	let optedOut = false;
	let blocked = options.awaitPreference ?? false;
	let identityPending = options.awaitIdentityMigration ?? false;
	let liveStateApplied = false;
	let adoptedKey: string | null = null;
	let pairedInstallId: string | null = null;
	let cloudUserId: string | null = null;
	let githubActor: string | null = null;
	void client.unregister("ao_cloud_user_id");
	void client.unregister("github_actor");
	const clearPairedAttribution = (): void => {
		adoptedKey = null;
		pairedInstallId = null;
		cloudUserId = null;
		githubActor = null;
		void client.unregister("paired_install_id");
		void client.unregister("github_actor");
		void client.unregister("ao_cloud_user_id");
	};

	// With awaitPreference the facade's initial `optedOut` is a guess, not what the
	// SDK and the saved marker hold (a previous run may have left both opted out).
	// The first decision, from the saved preference or a live desktop, is therefore
	// always pushed to both even when it equals the guess.
	let settled = !(options.awaitPreference ?? false);

	const setOptedOut = (next: boolean): void => {
		blocked = false;
		if (next === optedOut && settled) return;
		settled = true;
		optedOut = next;
		options.onOptOutChange?.(next);
		if (next) {
			// Drops the distinct id and every super property, so nothing of the
			// adopted identity survives in the SDK. reset() also clears the SDK's own
			// opt-out flag, so optOut() must come after it.
			client.reset();
			void client.optOut();
			adoptedKey = null;
			pairedInstallId = null;
			cloudUserId = null;
			githubActor = null;
			return;
		}
		void client.optIn();
		void client.register(context);
	};

	const capture = (event: MobileEventName, properties?: Record<string, unknown>): void => {
		// Fail closed on the event name: an event not in the allowlist is never
		// sent, so a typo cannot ship a bare untracked event.
		if (blocked || identityPending || optedOut) return;
		if (!(event in MOBILE_ALLOWLIST)) return;
		// Build-time kill switch, mirroring the desktop denylist.
		if (denied.has(event)) return;
		// Per-name rate cap: the runaway backstop. Checked last so a legitimate
		// event is only dropped when the name is genuinely over its window.
		if (!allow(event)) return;
		const phoneInstallId = options.phoneInstallId?.();
		client.capture(event, {
			...sanitizeMobileProperties(event, properties),
			...(phoneInstallId ? { install_id: phoneInstallId } : {}),
			...(pairedInstallId ? { paired_install_id: pairedInstallId } : {}),
			ao_cloud_user_id: cloudUserId,
			github_actor: githubActor,
			$process_person_profile: false,
		});
	};

	const restoreOptOut = (saved: boolean): void => {
		if (liveStateApplied) {
			blocked = false;
			return;
		}
		setOptedOut(saved);
	};

	const adoptDesktopIdentity = (identity: DesktopTelemetryIdentity | null): void => {
		if (!identity) {
			clearPairedAttribution();
			return;
		}
		liveStateApplied = true;
		setOptedOut(identity.optedOut);
		if (optedOut || !identity.distinctId) {
			clearPairedAttribution();
			return;
		}
		pairedInstallId = typeof identity.installId === "string" && /^ins_[0-9a-f-]{36}$/.test(identity.installId) ? identity.installId : null;
		const key = JSON.stringify([identity.distinctId, identity.cloudUserId, identity.githubLogin, pairedInstallId]);
		if (key === adoptedKey) return;
		adoptedKey = key;
		cloudUserId = identity.cloudUserId ?? null;
		githubActor = identity.githubLogin ?? null;
	};

	return {
		capture,
		adoptDesktopIdentity,
		resetPhoneIdentity: () => {
			client.reset();
			void (optedOut ? client.optOut() : client.optIn());
			void client.register(context);
			identityPending = false;
		},
		releasePhoneIdentity: () => { identityPending = false; },
		setOptedOut,
		restoreOptOut,
		active: async (storage, now) => {
			if (blocked || identityPending || optedOut) return;
			if (await reserveDailyActive(storage, now)) {
				capture(MOBILE_EVENTS.active);
			}
		},
	};
}
