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
import { createUserOptOut } from "./userOptOut";

// The one file that touches the SDK and the native runtime. Everything else in
// telemetry/ is pure and unit-tested; this wires the real PostHog client and is
// verified on a device via an EAS build, not in unit tests.

let telemetry: MobileTelemetry | null = null;
let connectedConfigs: ServerConfig[] = [];
let uiUsageTracker: UiUsageTracker | null = null;
// Bumped on every opt-out change, so a saved window read across one is not restored.
let consentGeneration = 0;

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
		// Keep the phone's own anonymous SDK identity across desktop pairings, and
		// never create a PostHog person profile for it.
		personProfiles: "never",
		// No GeoIP: PostHog must not derive a location from the phone's IP.
		disableGeoip: true,
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
		// Nothing is captured until loadMobileOptOut() has read the saved preference,
		// and a desktop that answers first cannot release capture or Sentry consent
		// before the phone's own switch is known.
		awaitPreference: true,
		awaitUserPreference: true,
		onOptOutChange: (optedOut) => {
			setMobileSentryConsent(!optedOut);
			uiUsageTracker?.setEnabled(!optedOut);
			consentGeneration += 1;
			if (!optedOut && connectedConfigs.length) void syncDesktopTelemetryIdentity(connectedConfigs).catch(() => {});
		},
		onDesktopOptOutChange: (optedOut) => {
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
// The phone's own Settings switch: works without pairing, and no desktop lifts it.
const USER_OPT_OUT_STORAGE_KEY = "ao.telemetry.userOptedOut";
const PHONE_IDENTITY_MIGRATION_KEY = "ao.telemetry.phoneIdentity.v2";

// Orders Settings changes to the phone's switch against each other and against
// the startup restore (see userOptOut.ts).
const userOptOut = createUserOptOut({
	apply: (optedOut) => telemetry?.setUserOptedOut(optedOut),
	save: () => AsyncStorage.setItem(USER_OPT_OUT_STORAGE_KEY, "1"),
	clear: () => AsyncStorage.removeItem(USER_OPT_OUT_STORAGE_KEY),
});

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
	// Read independently, so one failure cannot erase the other. An unreadable
	// desktop marker stays opted in rather than guess, as before; an unreadable
	// phone switch fails closed for this launch, since the person may have
	// turned it off and nothing else can confirm otherwise.
	const startedAt = userOptOut.generation();
	const [desktopRead, userRead] = await Promise.allSettled([AsyncStorage.getItem(OPT_OUT_STORAGE_KEY), AsyncStorage.getItem(USER_OPT_OUT_STORAGE_KEY)]);
	const stored = desktopRead.status === "fulfilled" && desktopRead.value === "1";
	const user = userRead.status !== "fulfilled" || userRead.value === "1";
	// Both applied in one synchronous step, so capture is released only once
	// the phone's own switch is known too. A Settings change made while these
	// reads ran is newer than what they returned, so it wins; until that change
	// lands, capture stays held.
	userOptOut.restore(user, startedAt);
	telemetry?.restoreOptOut(stored);
	// After the preference, so an opted-out phone never sends a saved window.
	await restoreUiUsage();
	preferenceLoaded();
}

/** The phone's own "Share usage analytics" switch, as saved. */
export async function loadUserMobileOptOut(): Promise<boolean> {
	try {
		return (await AsyncStorage.getItem(USER_OPT_OUT_STORAGE_KEY)) === "1";
	} catch {
		return false;
	}
}

/**
 * Turns the phone's own analytics opt-out on or off. It applies without any
 * paired desktop, stops PostHog and Sentry on the phone at once, and is saved
 * so it holds across launches.
 */
export function setUserMobileOptOut(optedOut: boolean): Promise<void> {
	// Opting out stops capture before saving; opting back in clears the saved
	// opt-out first, so a failed write never leaves capture on behind a switch
	// that shows off, and a later change supersedes an opt-in still saving.
	return userOptOut.set(optedOut);
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

let launchHasMachine = true;

/** Whether the phone has anywhere to load from: a paired machine or Orchestrator Cloud. */
export function setLaunchHasMachine(has: boolean): void {
	launchHasMachine = has;
}

/** The app went to the background; a launch not yet usable is sent without its times. */
export function markLaunchBackgrounded(): void {
	if (launchUsableAt === undefined) void sendLaunch("backgrounded");
}

async function sendLaunch(outcome: LaunchOutcome): Promise<void> {
	if (launchSent || !telemetry || !launchHistory) return;
	launchSent = true;
	// A phone with nowhere to load from times out by definition; kept apart so the
	// timeout rate measures slow machines, not unpaired phones.
	if (outcome === "timeout" && !launchHasMachine) outcome = "no_machine";
	if (launchTimer) clearTimeout(launchTimer);
	// Before the waits below, so navigating meanwhile does not change it.
	const landingSurface = uiUsageTracker?.current() ?? undefined;
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
		landingSurface,
	}));
}

async function restoreUiUsage(): Promise<void> {
	if (!uiUsageTracker) return;
	const generation = consentGeneration;
	let saved: PersistedUiUsage | null = null;
	try {
		const raw = await AsyncStorage.getItem(UI_USAGE_STORAGE_KEY);
		saved = raw ? JSON.parse(raw) as PersistedUiUsage : null;
	} catch {
		/* unreadable: start a fresh window */
	}
	// An opt-out (even one undone) during the read discarded what was saved.
	if (generation !== consentGeneration) return;
	uiUsageTracker.restore(saved);
}
