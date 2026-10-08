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
	/** Restore a persisted opt-out before anything is captured. */
	setOptedOut(optedOut: boolean): void;
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
	let identified = false;
	let adoptedId: string | null = null;

	const setOptedOut = (next: boolean): void => {
		if (next === optedOut) return;
		optedOut = next;
		options.onOptOutChange?.(next);
		if (next) {
			void client.optOut();
			// Drops the distinct id and every super property, so nothing of the
			// adopted identity survives in the SDK.
			client.reset();
			identified = false;
			adoptedId = null;
			return;
		}
		void client.optIn();
		void client.register(context);
	};

	const capture = (event: MobileEventName, properties?: Record<string, unknown>): void => {
		// Fail closed on the event name: an event not in the allowlist is never
		// sent, so a typo cannot ship a bare untracked event.
		if (optedOut) return;
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

	const adoptDesktopIdentity = (identity: DesktopTelemetryIdentity): void => {
		setOptedOut(identity.optedOut);
		if (optedOut || !identity.distinctId || adoptedId === identity.distinctId) return;
		adoptedId = identity.distinctId;
		identified = true;
		// Same distinct id as the desktop (and its daemon), so the phone's events
		// land on the person the desktop already identified.
		client.identify(identity.distinctId);
		void client.register({
			...(identity.githubLogin ? { github_actor: identity.githubLogin } : {}),
			...(identity.cloudUserId ? { ao_cloud_user_id: identity.cloudUserId } : {}),
		});
	};

	return {
		capture,
		adoptDesktopIdentity,
		setOptedOut,
		active: async (storage, now) => {
			if (await reserveDailyActive(storage, now)) {
				capture(MOBILE_EVENTS.active);
			}
		},
	};
}
