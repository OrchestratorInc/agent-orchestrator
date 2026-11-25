import AsyncStorage from "@react-native-async-storage/async-storage";
import Constants from "expo-constants";
import * as Device from "expo-device";
import * as Updates from "expo-updates";
import { Platform } from "react-native";
import PostHog from "posthog-react-native";
import { getTelemetryIdentity, type SessionMode } from "../api";
import type { ServerConfig } from "../config";
import { cachedInstallId } from "../installId";
import { setMobileDaemonVersions, setMobileSentryConsent, setMobileSentryPairedInstallId } from "../sentry";
import { buildMobileContext } from "./context";
import { ACTIVE_STORAGE_KEY, type ActiveStorage } from "./dailyActive";
import {
	MOBILE_DISABLED_EVENTS,
	MOBILE_POSTHOG_HOST,
	MOBILE_POSTHOG_KEY,
	MOBILE_TELEMETRY_DISABLED,
} from "./config";
import { MOBILE_EVENTS } from "./events";
import { checkRateLimit, mergeRateState, type RateLimitState } from "./rateLimit";
import { createDesktopIdentitySync } from "./identitySync";
import { createMobileTelemetry, type MobileTelemetry } from "./telemetry";
import { launchKind, launchProperties, LAUNCH_TIMEOUT_MS, type LaunchOutcome, type StartupMarks } from "./launchTiming";
import { createUiUsageTracker, type PersistedUiUsage, type UiUsageTracker } from "./uiUsage";
import { setUiUsageTracker } from "./uiUsageRegistry";

// The one file that touches the SDK and the native runtime. Everything else in
// telemetry/ is pure and unit-tested; this wires the real PostHog client and is
// verified on a device via an EAS build, not in unit tests.

let telemetry: MobileTelemetry | null = null;
let connectedConfigs: ServerConfig[] = [];
let uiUsageTracker: UiUsageTracker | null = null;

const UI_USAGE_STORAGE_KEY = "ao.telemetry.uiUsage";
const LAST_LAUNCH_STORAGE_KEY = "ao.telemetry.lastLaunch";

// Resolved once loadMobileOptOut() has applied the saved preference; the launch
// event waits on it rather than being dropped by the facade's preference gate.
let preferenceLoaded!: () => void;
const preferenceReady = new Promise<void>((resolve) => { preferenceLoaded = resolve; });

/**
 * Builds the PostHog client once and returns the capture facade.
 *
 * Screen capture is off by every switch the ask requires:
 *   - `enableSessionReplay: false` — no screen recording.
 *   - the imperative client, not PostHogProvider — no touch/screen autocapture.
 *   - `captureAppLifecycleEvents: false` — no auto lifecycle stream; the daily
 *     active heartbeat we emit ourselves is the one lifecycle signal, capped to
 *     once per day.
 *
 * Idempotent: repeated calls return the same instance, so mounting the init
 * component more than once cannot create a second client.
 */
const RATE_STORAGE_KEY = "ao.telemetry.rateLimit";
let rateState: RateLimitState = {};
let rateStateLoaded = false;

// Seed the per-name counters from storage once, so a crash-restart loop cannot
// reset the daily ceiling. Best-effort: a read failure just starts from empty.
function loadRateState(): void {
	if (rateStateLoaded) return;
	rateStateLoaded = true;
	void AsyncStorage.getItem(RATE_STORAGE_KEY).then((raw) => {
		if (!raw) return;
		try {
			// Take the max per name so a count that advanced in the race before
			// this read resolved cannot lower the persisted daily ceiling.
			rateState = mergeRateState(JSON.parse(raw) as RateLimitState, rateState);
		} catch {
			/* ignore corrupt state; start fresh */
		}
	});
}

// Sync predicate for the facade: advances the in-memory window and persists it
// fire-and-forget. Returns false once a name is over its minute or day cap.
function allowEvent(name: string): boolean {
	const { allowed, state } = checkRateLimit(rateState, name, Date.now());
	rateState = state;
	void AsyncStorage.setItem(RATE_STORAGE_KEY, JSON.stringify(state)).catch(() => {});
	return allowed;
}

export function initMobileTelemetry(): MobileTelemetry | null {
	if (telemetry) return telemetry;
	// Dev gate: a dev client (npm start / Expo Go) must never send to the
	// production project. Desktop constructs no client unless packaged.
	if (__DEV__ || MOBILE_TELEMETRY_DISABLED) return null;

	const client = new PostHog(MOBILE_POSTHOG_KEY, {
		host: MOBILE_POSTHOG_HOST,
		enableSessionReplay: false,
		captureAppLifecycleEvents: false,
		// Keep the phone's own anonymous SDK identity across desktop pairings.
		personProfiles: "identified_only",
	});

	const version =
		(Constants.expoConfig?.version as string | undefined) ??
		(Constants.nativeAppVersion as string | undefined) ??
		"unknown";

	const context = buildMobileContext({
		platformOS: Platform.OS,
		isPhysicalDevice: Device.isDevice === true,
		dev: __DEV__,
		appVersion: version,
		update: Updates.isEnabled
			? { id: Updates.updateId, embedded: Updates.isEmbeddedLaunch, channel: Updates.channel }
			: undefined,
	});

	loadRateState();
	telemetry = createMobileTelemetry(client, context, {
		disabledEvents: MOBILE_DISABLED_EVENTS,
		phoneInstallId: cachedInstallId,
		awaitIdentityMigration: true,
		allow: allowEvent,
		// Nothing is captured until loadMobileOptOut() has read the saved preference.
		awaitPreference: true,
		onOptOutChange: (optedOut) => {
			setMobileSentryConsent(!optedOut);
			uiUsageTracker?.setEnabled(!optedOut);
			if (!optedOut && connectedConfigs.length) void syncDesktopTelemetryIdentity(connectedConfigs).catch(() => {});
			void (optedOut
				? AsyncStorage.setItem(OPT_OUT_STORAGE_KEY, "1")
				: AsyncStorage.removeItem(OPT_OUT_STORAGE_KEY)
			).catch(() => {});
		},
	});
	// Read before the daily heartbeat can write the active marker this launch.
	launchHistory = Promise.all([
		AsyncStorage.getItem(LAST_LAUNCH_STORAGE_KEY),
		AsyncStorage.getItem(ACTIVE_STORAGE_KEY),
	]).then(([previous, active]) => ({ previous, everActive: active !== null }), () => ({ previous: null, everActive: true }));
	// Kept on the phone only, to tell a new version or OTA update from a plain relaunch.
	launchIdentity = `${version}|${Updates.isEnabled && !Updates.isEmbeddedLaunch ? Updates.updateId ?? "embedded" : "embedded"}`;
	const live = telemetry;
	uiUsageTracker = createUiUsageTracker({
		now: Date.now,
		capture: (event, properties) => live.capture(event, properties),
		save: (state) => void AsyncStorage.setItem(UI_USAGE_STORAGE_KEY, JSON.stringify(state)).catch(() => {}),
		clear: () => void AsyncStorage.removeItem(UI_USAGE_STORAGE_KEY).catch(() => {}),
	});
	setUiUsageTracker(uiUsageTracker);
	launchTimer = setTimeout(() => void sendLaunch("timeout"), LAUNCH_TIMEOUT_MS);
	return telemetry;
}

// The desktop's opt-out is sticky on the phone: it survives unpairing and only a
// paired desktop that is opted in lifts it.
const OPT_OUT_STORAGE_KEY = "ao.telemetry.optedOut";
const PHONE_IDENTITY_MIGRATION_KEY = "ao.telemetry.phoneIdentity.v2";

/** Restores the persisted opt-out and releases capture. Awaited before the first heartbeat. */
export async function loadMobileOptOut(): Promise<void> {
	if (telemetry) {
		let reset = false;
		try {
			if ((await AsyncStorage.getItem(PHONE_IDENTITY_MIGRATION_KEY)) !== "1") {
				telemetry.resetPhoneIdentity();
				reset = true;
				await AsyncStorage.setItem(PHONE_IDENTITY_MIGRATION_KEY, "1");
			} else {
				telemetry.releasePhoneIdentity();
			}
		} catch {
			// If the marker is unreadable, reset again on the next launch.
			if (!reset) telemetry.resetPhoneIdentity();
		}
	}
	let stored = false;
	try {
		stored = (await AsyncStorage.getItem(OPT_OUT_STORAGE_KEY)) === "1";
	} catch {
		/* unreadable storage: stay opted in rather than guess */
	}
	telemetry?.restoreOptOut(stored);
	// After the preference, so an opted-out phone never sends a saved window.
	await restoreUiUsage();
	preferenceLoaded();
}

/** Adopts the identity and opt-out of the connected desktops (see identitySync.ts). */
const desktopIdentitySync = createDesktopIdentitySync(
	getTelemetryIdentity,
	(identity) => {
		telemetry?.adoptDesktopIdentity(identity);
		setMobileSentryPairedInstallId(identity && !identity.optedOut ? identity.installId ?? null : null);
	},
	setMobileDaemonVersions,
);

export function syncDesktopTelemetryIdentity(configs: ServerConfig[]): Promise<void> {
	connectedConfigs = configs;
	return desktopIdentitySync(configs);
}

/** The capture facade, or null before init. Call sites no-op when null. */
export function mobileTelemetry(): MobileTelemetry | null {
	return telemetry;
}

/** AsyncStorage typed to the narrow ActiveStorage the heartbeat needs. */
export const telemetryActiveStorage: ActiveStorage = {
	getItem: (key) => AsyncStorage.getItem(key),
	setItem: (key, value) => AsyncStorage.setItem(key, value),
};

/** Feature ids the featureUsed allowlist accepts. */
export type MobileFeature = "spawn" | "merge" | "kill" | "restore" | "conductor" | "send" | "handoff";

/** Extra allowlisted context for a feature event. `mode` distinguishes chat vs tui. */
export type FeatureContext = { mode?: SessionMode };

/**
 * Runs an action and reports feature_used with its outcome, without changing the
 * action's own return value or error. `context` carries allowlisted extras (e.g.
 * the chat/tui `mode`) onto both the success and failure event. Best-effort: a
 * null client (pre-init) is a no-op, and telemetry never swallows or alters the
 * action's result.
 */
export async function trackFeature<T>(
	feature: MobileFeature,
	run: () => Promise<T>,
	context?: FeatureContext,
): Promise<T> {
	try {
		const result = await run();
		telemetry?.capture(MOBILE_EVENTS.featureUsed, { feature, outcome: "succeeded", ...context });
		return result;
	} catch (error) {
		telemetry?.capture(MOBILE_EVENTS.featureUsed, { feature, outcome: "failed", ...context });
		throw error;
	}
}

// ---- Launch timing ----------------------------------------------------------

let launchHistory: Promise<{ previous: string | null; everActive: boolean }> | null = null;
let launchIdentity = "";
let launchTimer: ReturnType<typeof setTimeout> | null = null;
let launchSent = false;
let launchVisibleAt: number | undefined;
let launchUsableAt: number | undefined;

function performanceNow(): number | undefined {
	const perf = globalThis.performance as (Performance & { rnStartupTiming?: StartupMarks }) | undefined;
	return typeof perf?.now === "function" ? perf.now() : undefined;
}

/** The app's first frame. */
export function markLaunchVisible(): void {
	launchVisibleAt ??= performanceNow();
}

/** The first frame showing a machine's live data. */
export function markLaunchUsable(): void {
	if (launchUsableAt !== undefined) return;
	launchUsableAt = performanceNow();
	void sendLaunch("usable");
}

/** The app went to the background; a launch not yet usable is sent without its times. */
export function markLaunchBackgrounded(): void {
	if (launchUsableAt === undefined) void sendLaunch("backgrounded");
}

async function sendLaunch(outcome: LaunchOutcome): Promise<void> {
	if (launchSent || !telemetry || !launchHistory) return;
	launchSent = true;
	if (launchTimer) clearTimeout(launchTimer);
	await preferenceReady;
	const { previous, everActive } = await launchHistory;
	void AsyncStorage.setItem(LAST_LAUNCH_STORAGE_KEY, launchIdentity).catch(() => {});
	const marks = (globalThis.performance as { rnStartupTiming?: StartupMarks } | undefined)?.rnStartupTiming;
	telemetry.capture(MOBILE_EVENTS.launchTimed, launchProperties({
		kind: launchKind(previous, launchIdentity, everActive),
		outcome,
		marks,
		visibleAt: launchVisibleAt,
		usableAt: launchUsableAt,
		landingSurface: uiUsageTracker?.current() ?? undefined,
	}));
}

async function restoreUiUsage(): Promise<void> {
	if (!uiUsageTracker) return;
	let saved: PersistedUiUsage | null = null;
	try {
		const raw = await AsyncStorage.getItem(UI_USAGE_STORAGE_KEY);
		saved = raw ? JSON.parse(raw) as PersistedUiUsage : null;
	} catch {
		/* unreadable: start a fresh window */
	}
	uiUsageTracker.restore(saved);
}
