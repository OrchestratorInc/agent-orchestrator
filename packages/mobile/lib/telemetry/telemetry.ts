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
	identify(distinctId: string): void;
	reset(): void;
	unregister(property: string): void | Promise<void>;
	optOut(): void | Promise<void>;
	optIn(): void | Promise<void>;
}

/**
 * What a paired desktop reports at GET /api/v1/telemetry/identity. distinctId is
 * the AO Cloud user ID when the desktop is signed in, else its install ID. There
 * is no email here by design: the desktop sets it once as a person property.
 */
export type DesktopTelemetryIdentity = {
	distinctId?: string;
	cloudUserId?: string;
	githubLogin?: string;
	optedOut: boolean;
};

export type MobileTelemetry = {
	/** Emit an allowlisted event. Unknown event names and unknown props are dropped. */
	capture(event: MobileEventName, properties?: Record<string, unknown>): void;
	/** Emit the daily-active heartbeat at most once per UTC day. */
	active(storage?: ActiveStorage, now?: Date): Promise<void>;
	/** Join the paired desktop's person, or stop everything if it has opted out. */
	adoptDesktopIdentity(identity: DesktopTelemetryIdentity): void;
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
	/**
	 * Returns false to drop an event that has hit the per-name rate cap. Sync so
	 * capture() stays non-blocking; runtime.ts backs it with an in-memory,
	 * persisted limiter. Omitted in tests that are not exercising the cap.
	 */
	allow?: (event: string) => boolean;
	/** Called when the opt-out flips, so the runtime can persist it. */
	onOptOutChange?: (optedOut: boolean) => void;
	/** Start opted out (restored from storage). */
	optedOut?: boolean;
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
	let optedOut = options.optedOut ?? false;
	let blocked = options.awaitPreference ?? false;
	let liveStateApplied = false;
	let identified = false;
	let adoptedId: string | null = null;
	let adoptedKey: string | null = null;

	const setOptedOut = (next: boolean): void => {
		blocked = false;
		if (next === optedOut) {
			// First load of a saved opt-out: the SDK has not been told yet.
			if (next && options.awaitPreference) {
				client.reset();
				void client.optOut();
			}
			return;
		}
		optedOut = next;
		options.onOptOutChange?.(next);
		if (next) {
			// Drops the distinct id and every super property, so nothing of the
			// adopted identity survives in the SDK. reset() also clears the SDK's own
			// opt-out flag, so optOut() must come after it.
			client.reset();
			void client.optOut();
			identified = false;
			adoptedId = null;
			adoptedKey = null;
			return;
		}
		void client.optIn();
		void client.register(context);
	};

	const capture = (event: MobileEventName, properties?: Record<string, unknown>): void => {
		// Fail closed on the event name: an event not in the allowlist is never
		// sent, so a typo cannot ship a bare untracked event.
		if (blocked || optedOut) return;
		if (!(event in MOBILE_ALLOWLIST)) return;
		// Build-time kill switch, mirroring the desktop denylist.
		if (denied.has(event)) return;
		// Per-name rate cap: the runaway backstop. Checked last so a legitimate
		// event is only dropped when the name is genuinely over its window.
		if (!allow(event)) return;
		client.capture(event, {
			...sanitizeMobileProperties(event, properties),
			// Anonymous rate until a paired desktop's identity is adopted.
			// Matches every desktop and daemon event.
			...(identified ? {} : { $process_person_profile: false }),
		});
	};

	const restoreOptOut = (saved: boolean): void => {
		if (liveStateApplied) {
			blocked = false;
			return;
		}
		setOptedOut(saved);
	};

	const adoptDesktopIdentity = (identity: DesktopTelemetryIdentity): void => {
		liveStateApplied = true;
		setOptedOut(identity.optedOut);
		if (optedOut || !identity.distinctId) return;
		const key = JSON.stringify([identity.distinctId, identity.cloudUserId, identity.githubLogin]);
		if (key === adoptedKey) return;
		adoptedKey = key;
		identified = true;
		if (adoptedId !== identity.distinctId) {
			adoptedId = identity.distinctId;
			// Same distinct id as the desktop (and its daemon), so the phone's events
			// land on the person the desktop already identified.
			client.identify(identity.distinctId);
		}
		// Registered properties merge in the SDK, so a property the new identity
		// lacks (signed out, or a desktop with no GitHub login) must be removed.
		const props: Record<string, string> = {};
		if (identity.githubLogin) props.github_actor = identity.githubLogin;
		if (identity.cloudUserId) props.ao_cloud_user_id = identity.cloudUserId;
		for (const name of ["github_actor", "ao_cloud_user_id"]) {
			if (!(name in props)) void client.unregister(name);
		}
		if (Object.keys(props).length > 0) void client.register(props);
	};

	return {
		capture,
		adoptDesktopIdentity,
		setOptedOut,
		restoreOptOut,
		active: async (storage, now) => {
			if (await reserveDailyActive(storage, now)) {
				capture(MOBILE_EVENTS.active);
			}
		},
	};
}
