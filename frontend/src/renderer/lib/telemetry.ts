import type posthogModule from "posthog-js/dist/module.full.no-external";
import { aoBridge } from "./bridge";
import { isLoopbackHostname } from "./loopback";
import { ONBOARDING_STEPS } from "./onboarding-steps";
import { ORCHESTRATOR_SPAWN_SOURCES } from "./orchestrator-spawn-sources";
import { DEFAULT_POSTHOG_HOST, DEFAULT_POSTHOG_PROJECT_KEY } from "../../shared/posthog-config";
import { EDITOR_IDS } from "../../shared/editor-handoff";
import { captureExceptionToSentry, initSentry } from "./sentry";

const POSTHOG_KEY = import.meta.env.VITE_AO_POSTHOG_KEY?.trim() || DEFAULT_POSTHOG_PROJECT_KEY;
const POSTHOG_HOST = import.meta.env.VITE_AO_POSTHOG_HOST?.trim() || DEFAULT_POSTHOG_HOST;
const RELEASE_TAG = "2026-01-30";
const TELEMETRY_SCHEMA_VERSION = 2;
const REDACTED_LOCAL_URL = "[redacted-local-url]";
const REDACTED_LOCAL_PATH = "[redacted-local-path]";
const ACTIVE_STORAGE_KEY = "ao.telemetry.activeSlotsByDate";
const ROUTE_VIEW_STORAGE_KEY = "ao.telemetry.routeViewsByDate";
const UPDATE_FAILURE_STORAGE_KEY = "ao.telemetry.updateFailures.v1";
const EMBEDDED_LOCAL_URL_PATTERN =
	/(?:\bfile:\/\/\/\S+|\bapp:\/\/renderer\/\S+|\bhttps?:\/\/(?:localhost|127\.0\.0\.1|\[::1\])(?::\d+)?\S*)/gi;
const POSTHOG_EVENT_NAME_ALIASES: Record<string, string> = {
	"ao.app.active": "ao.v2.app.active",
	"ao.renderer.route_viewed": "ao.v2.renderer.route_viewed",
	"ao.renderer.loaded": "ao.v2.renderer.loaded",
	"ao.renderer.api_error": "ao.v2.renderer.api_error",
	"ao.renderer.daemon_failure": "ao.v2.renderer.daemon_failure",
	"ao.renderer.session_management_summary": "ao.v2.renderer.session_management_summary",
};

let initPromise: Promise<boolean> | null = null;
let errorHandlersBound = false;
let telemetryContext: TelemetryProperties = {};
let installId = "";
let fallbackActiveDate = "";
let fallbackRouteViewDate = "";
let fallbackRouteViewSurfaces = new Set<string>();

// Bounds how many captures of a single event (or exception) name reach
// PostHog. Mirrors the daemon-side RateLimitedSink: a re-render loop or
// repeatedly-thrown exception must not turn into an unbounded PostHog bill.
// A per-minute cap alone doesn't bound the daily total — a loop paced just
// under it would sit under the ceiling forever — so this pairs a small burst
// allowance with a hard daily ceiling per name.
const EVENTS_PER_NAME_PER_MINUTE = 5;
const EVENTS_PER_NAME_PER_DAY = 200;
const MINUTE_MS = 60_000;
const DAY_MS = 24 * 60 * 60_000;
const minuteWindows = new Map<string, { start: number; count: number }>();
const dayWindows = new Map<string, { start: number; count: number }>();

function reserveWindow(
	windows: Map<string, { start: number; count: number }>,
	name: string,
	now: number,
	size: number,
	limit: number,
): boolean {
	const window = windows.get(name);
	if (!window || now - window.start >= size) {
		windows.set(name, { start: now, count: 1 });
		return true;
	}
	if (window.count >= limit) return false;
	window.count += 1;
	return true;
}

export function reserveCapture(name: string, now = Date.now()): boolean {
	if (!reserveWindow(minuteWindows, name, now, MINUTE_MS, EVENTS_PER_NAME_PER_MINUTE)) return false;
	return reserveWindow(dayWindows, name, now, DAY_MS, EVENTS_PER_NAME_PER_DAY);
}

type PendingUpdateDay = { day: string; failure_count: number };

type UpdateFailureState = {
	streaks?: Record<string, number>;
	day?: string;
	dayCount?: number;
	pendingDays?: PendingUpdateDay[];
	pendingDay?: string;
	pendingCount?: number;
};

const PENDING_UPDATE_DAY_CAP = 14;

function utcDay(now: number): string {
	return new Date(now).toISOString().slice(0, 10);
}

function readUpdateFailureState(storage: DailyActiveStorage): UpdateFailureState {
	try {
		const parsed = JSON.parse(storage.getItem(UPDATE_FAILURE_STORAGE_KEY) ?? "{}") as Record<string, unknown>;
		if (parsed && typeof parsed.streaks === "object" && parsed.streaks !== null) return parsed as UpdateFailureState;
		const streaks: Record<string, number> = {};
		for (const [key, value] of Object.entries(parsed)) {
			if (typeof value === "number" && Number.isFinite(value)) streaks[key] = value;
		}
		return { streaks };
	} catch {
		return { streaks: {} };
	}
}

// One streak event until a successful download of that target clears it, plus
// one daily count after the UTC day that accumulated the retries has ended.
export function claimUpdateFailure(
	storage: DailyActiveStorage,
	properties: TelemetryProperties,
	now = Date.now(),
): { emit: boolean; extra: TelemetryProperties; streak?: boolean; daily?: TelemetryProperties; dailies?: TelemetryProperties[]; ackDaily: (day: string) => void; rollback: () => void } {
	const none = { emit: false, extra: {}, streak: false, ackDaily: () => undefined, rollback: () => undefined };
	const snapshot = storage.getItem(UPDATE_FAILURE_STORAGE_KEY);
	let restore = snapshot;
	const rollback = () => {
		if (restore === null) storage.removeItem?.(UPDATE_FAILURE_STORAGE_KEY);
		else storage.setItem(UPDATE_FAILURE_STORAGE_KEY, restore);
	};
	const ackDaily = (day: string) => {
		const state = restore === null ? {} : JSON.parse(restore) as UpdateFailureState;
		state.pendingDays = (state.pendingDays ?? []).filter((pending) => pending.day !== day);
		if (state.pendingDay === day) {
			delete state.pendingDay;
			delete state.pendingCount;
		}
		if (state.day === day) state.dayCount = 0;
		restore = JSON.stringify(state);
	};
	try {
		const state = readUpdateFailureState(storage);
		state.streaks ??= {};
		const day = utcDay(now);
		state.pendingDays ??= [];
		if (state.pendingDay && (state.pendingCount ?? 0) > 0) {
			state.pendingDays.push({ day: state.pendingDay, failure_count: state.pendingCount ?? 0 });
			delete state.pendingDay;
			delete state.pendingCount;
		}
		if (state.day && state.day !== day) {
			if ((state.dayCount ?? 0) > 0) {
				state.pendingDays.push({ day: state.day, failure_count: state.dayCount ?? 0 });
			}
			if (state.pendingDays.length > PENDING_UPDATE_DAY_CAP) {
				state.pendingDays = state.pendingDays.slice(-PENDING_UPDATE_DAY_CAP);
			}
			state.day = day;
			state.dayCount = 0;
		}
		if (!state.day) state.day = day;
		const key = updateFailureKey(properties);
		const streakOpen = state.streaks[key] !== undefined;
		state.dayCount = (state.dayCount ?? 0) + 1;
		if (!streakOpen) state.streaks[key] = now;
		const dailies = state.pendingDays.map((pending) => ({
			report: "daily" as const,
			day: pending.day,
			failure_count: pending.failure_count,
		}));
		const daily = dailies[0];
		if (dailies.length > 0) state.pendingDays = [];
		storage.setItem(UPDATE_FAILURE_STORAGE_KEY, JSON.stringify(state));
		if (streakOpen && dailies.length === 0) return none;
		return { emit: true, extra: {}, streak: !streakOpen, daily, dailies, ackDaily, rollback };
	} catch {
		return { emit: true, extra: {}, streak: true, ackDaily: () => undefined, rollback: () => undefined };
	}
}

function updateFailureKey(properties: TelemetryProperties): string {
	return [properties.phase, properties.error_category, properties.to_version ?? ""].join("|");
}



export function clearUpdateFailures(storage: DailyActiveStorage, version: unknown): void {
	try {
		const state = readUpdateFailureState(storage);
		const suffix = `|${version ?? ""}`;
		for (const key of Object.keys(state.streaks ?? {})) {
			if (key.endsWith(suffix)) delete state.streaks?.[key];
		}
		storage.setItem(UPDATE_FAILURE_STORAGE_KEY, JSON.stringify(state));
	} catch {
		// A storage failure cannot affect the updater.
	}
}

export function clearCheckUpdateFailures(storage: DailyActiveStorage): void {
	try {
		const state = readUpdateFailureState(storage);
		for (const key of Object.keys(state.streaks ?? {})) {
			if (key.startsWith("check|")) delete state.streaks?.[key];
		}
		storage.setItem(UPDATE_FAILURE_STORAGE_KEY, JSON.stringify(state));
	} catch {
		// A storage failure cannot affect the updater.
	}
}

type TelemetryProperties = Record<string, unknown>;
type DailyActiveStorage = Pick<Storage, "getItem" | "setItem"> & { removeItem?: Storage["removeItem"] };
type DailyActiveEventTarget = {
	addEventListener: (type: string, listener: EventListener, options?: AddEventListenerOptions) => void;
	removeEventListener: (type: string, listener: EventListener, options?: EventListenerOptions) => void;
};

export type DailyActiveHeartbeatOptions = {
	storage?: DailyActiveStorage;
	now?: () => Date;
	capture: () => boolean | void | Promise<boolean | void>;
	window: DailyActiveEventTarget;
	document: DailyActiveEventTarget & Pick<Document, "visibilityState">;
};

/**
 * Release channel, read from the user's own Updates setting.
 *
 * Previously this had to be inferred by looking for "-nightly." inside the
 * version string, which broke for anyone pinned to a feature build and told you
 * nothing about someone who had switched channels but not yet updated. The
 * setting is the truth; the version is a consequence of it.
 */
export type ReleaseChannel = "stable" | "nightly" | "feature" | "unknown";

export function releaseChannelFrom(settings: { channel?: unknown; feature?: unknown } | null | undefined): ReleaseChannel {
	if (!settings) return "unknown";
	if (settings.feature != null) return "feature";
	if (settings.channel === "nightly") return "nightly";
	if (settings.channel === "latest") return "stable";
	return "unknown";
}

/**
 * Channel of the build actually running, read from the version string.
 *
 * Distinct from release_channel, which is what the user opted into. A nightly
 * build carries "-nightly." in its version (CI stamps 0.11.3-nightly.5); a plain
 * semver is stable. This is "what am I running", release_channel is "what did I
 * choose". The two disagree exactly when someone switched channels but has not
 * updated yet, and that gap is the adoption-lag signal.
 */
export type VersionChannel = "stable" | "nightly" | "unknown";

export function versionChannelFrom(appVersion: string): VersionChannel {
	const v = appVersion.trim();
	if (!v || v === "unknown") return "unknown";
	return /-nightly\./i.test(v) ? "nightly" : "stable";
}

export function buildTelemetryContext(
	appVersion: string,
	platform: string,
	channel: ReleaseChannel = "unknown",
): TelemetryProperties {
	const version = appVersion.trim() || "unknown";
	return {
		// Classifies this install as the desktop app across every event, so a
		// shared event like ao.app.active splits cleanly by `client` alongside
		// the mobile app (client="mobile") and the CLI (client="cli"), rather
		// than by inferring from the platform value set.
		client: "desktop",
		app_version: version,
		ao_version: version,
		platform,
		// What they opted into.
		release_channel: channel,
		// What they are actually running.
		version_channel: versionChannelFrom(version),
		build_mode: import.meta.env.DEV ? "dev" : "packaged",
		telemetry_schema_version: TELEMETRY_SCHEMA_VERSION,
	};
}

/** Refreshes the channel in context after the user changes it, without a restart. */
export function setReleaseChannelContext(channel: ReleaseChannel): void {
	telemetryContext = { ...telemetryContext, release_channel: channel };
}

/**
 * Best-effort read of the Updates setting for the initial context.
 *
 * Never throws and never blocks startup: if the bridge is unavailable the
 * channel reports "unknown" rather than delaying the first heartbeat.
 */
async function readUpdateSettingsForTelemetry(): Promise<{ channel?: unknown; feature?: unknown } | null> {
	try {
		return (await aoBridge.updateSettings.get()) as { channel?: unknown; feature?: unknown };
	} catch {
		return null;
	}
}

export function withTelemetryContext(properties: TelemetryProperties): TelemetryProperties {
	// Anonymous until an AO Cloud sign-in identifies this install. Once signed in
	// the flag is left to the SDK, which processes the identified person.
	return { ...telemetryContext, ...cloudContext, ...properties, ...(cloudUser ? {} : { $process_person_profile: false }), install_id: installId };
}

// Who this install is. The GitHub login and AO Cloud user ID ride every event as
// super properties; email is only ever a person property set at identify time.
let cloudUser: { id: string; email: string } | null = null;
let githubLogin: string | null = null;
let identifiedId: string | null = null;
let posthogReady = false;
let analyticsOptedOut = false;

function registerBaseProperties(): void {
	posthog.register({
		...telemetryContext,
		surface: "renderer",
		...(githubLogin ? { github_actor: githubLogin } : {}),
		...(cloudUser ? { ao_cloud_user_id: cloudUser.id } : {}),
	});
}

/**
 * Identifies the signed-in AO Cloud user with the AO Cloud user ID. Email is a
 * person property only. An unlinked install starts with its ins_ ID and can be
 * merged into the Cloud person; a linked install starts with a fresh anon_ ID.
 * The signed-in daemon sends events under the Cloud user ID directly.
 */
export async function identifyCloudUser(user: { id: string; email: string }): Promise<void> {
	cloudUser = user;
	if (analyticsOptedOut || identifiedId === user.id || !(await initTelemetry())) return;
	// A sign-out, a different account or an opt-out may have landed while init
	// was pending; this continuation must not identify who is no longer current.
	if (analyticsOptedOut || cloudUser?.id !== user.id) return;
	// Switching accounts without a sign-out: never layer B onto A's person.
	if (identifiedId !== null) posthog.reset();
	registerBaseProperties();
	posthog.identify(user.id, {
		email: user.email,
		ao_cloud_user_id: user.id,
		...(githubLogin ? { github_login: githubLogin } : {}),
	});
	identifiedId = user.id;
}

/**
 * Sign-out: stop attributing renderer events to the user.
 * ponytail: reset() mints a random anonymous ID that lasts until the next
 * launch, when the bootstrap restores the ins_ install ID.
 */
export function clearCloudUser(): void {
	cloudUser = null;
	if (!posthogReady || identifiedId === null) return;
	identifiedId = null;
	posthog.reset();
	registerBaseProperties();
}

/**
 * Applies the in-app analytics opt-out. Opting out stops capture and calls
 * posthog.reset() so no identity survives in the SDK. Opting back in resumes
 * capture, initializing the client first if this launch started opted out.
 * ponytail: after an in-session opt-in the SDK uses a fresh anonymous ID until
 * the next launch chooses an install ID or a fresh anonymous ID from the
 * durable binding state.
 */
export async function applyAnalyticsOptOut(optedOut: boolean): Promise<void> {
	analyticsOptedOut = optedOut;
	if (optedOut) {
		// Updater failures counted before the opt-out must not be exported
		// after a later opt-in, so the streak and pending daily counts go too.
		try {
			telemetryStorage()?.removeItem?.(UPDATE_FAILURE_STORAGE_KEY);
		} catch {
			// Storage is best-effort; capture is already gated on the flag.
		}
		if (posthogReady) {
			posthog.opt_out_capturing();
			posthog.reset();
		}
		identifiedId = null;
		return;
	}
	if (posthogReady) {
		posthog.opt_in_capturing({ captureEventName: false });
		registerBaseProperties();
	} else {
		initPromise = null;
		await initTelemetry();
	}
	if (cloudUser) await identifyCloudUser(cloudUser);
}

// A fresh install often has no GitHub token yet, so the daemon-resolved login is
// polled on a short bounded schedule rather than read once.
const GITHUB_LOGIN_RETRY_MS = [2_000, 30_000, 120_000, 600_000];

async function syncGithubLogin(): Promise<void> {
	for (const delay of GITHUB_LOGIN_RETRY_MS) {
		await new Promise((resolve) => setTimeout(resolve, delay));
		if (analyticsOptedOut) return;
		const login = await Promise.resolve()
			.then(() => aoBridge.telemetry.getGithubLogin())
			.catch(() => null);
		if (!login) continue;
		githubLogin = login;
		registerBaseProperties();
		if (identifiedId) posthog.setPersonProperties({ github_login: login });
		return;
	}
}

// The signed-in cloud org, tagged on every event and exception once /me has
// resolved: the org id only as a hash, plus the offering's kind and billing
// mode from a fixed vocabulary.
let cloudContext: TelemetryProperties = {};
const CLOUD_ORG_KINDS = new Set(["b2b", "b2c", "both", "none", "unknown"]);
const CLOUD_BILLING_MODES = new Set(["self_serve", "contract", "off", "unknown"]);

function enumValue(value: unknown, allowed: ReadonlySet<string>, fallback = "unknown"): string {
	return typeof value === "string" && allowed.has(value) ? value : fallback;
}

export function setCloudTelemetryContext(context: { orgId?: string; orgKind?: string; billingMode?: string }): void {
	const next: TelemetryProperties = {
		cloud_org_kind: enumValue(context.orgKind, CLOUD_ORG_KINDS),
		cloud_billing_mode: enumValue(context.billingMode, CLOUD_BILLING_MODES),
	};
	cloudContext = { ...cloudContext, ...next };
	void hashedTelemetryID(context.orgId).then((hash) => {
		cloudContext = hash ? { ...cloudContext, cloud_org_id_hash: hash } : omitKey(cloudContext, "cloud_org_id_hash");
	});
}

function omitKey(properties: TelemetryProperties, key: string): TelemetryProperties {
	const { [key]: _omitted, ...rest } = properties;
	return rest;
}

/** Test seam. */
export function resetCloudTelemetryContextForTest(): void {
	cloudContext = {};
}

export function postHogEventName(event: string): string {
	return POSTHOG_EVENT_NAME_ALIASES[event] ?? event;
}

// Streams the supervisor has silenced, delivered on the telemetry bootstrap.
// The daemon enforces the same list on its own sink, but renderer events go
// straight to PostHog, so without this the kill switch would only cover half
// the producers.
let disabledEventMatchers: string[] = [];

/**
 * Whether a stream is silenced.
 *
 * Mirrors the daemon's DenylistSink: case-insensitive, `*` matches by prefix,
 * and both the internal name and the exported alias are checked so an operator
 * can type whichever one they see.
 */
export function isDeniedEvent(event: string, denied: string[] = disabledEventMatchers): boolean {
	if (denied.length === 0) return false;
	const candidates = [event.trim().toLowerCase(), postHogEventName(event).trim().toLowerCase()];
	return denied.some((raw) => {
		const rule = raw.trim().toLowerCase();
		if (rule === "" || rule === "*") return false;
		if (rule.endsWith("*")) {
			const prefix = rule.slice(0, -1);
			return prefix !== "" && candidates.some((name) => name.startsWith(prefix));
		}
		return candidates.includes(rule);
	});
}

/** Test seam: the real value arrives with the bootstrap in initTelemetry. */
export function setDisabledEventsForTest(denied: string[]): void {
	disabledEventMatchers = denied;
}

export function reserveDailyActiveCapture(storage?: DailyActiveStorage, now = new Date()): boolean {
	const utcDate = now.toISOString().slice(0, 10);
	const reserveFallback = () => {
		if (fallbackActiveDate === utcDate) return false;
		fallbackActiveDate = utcDate;
		return true;
	};

	if (!storage) return reserveFallback();
	try {
		const raw = storage.getItem(ACTIVE_STORAGE_KEY);
		const parsed = raw ? (JSON.parse(raw) as { date?: unknown }) : {};
		// Any record carrying today's date counts as reserved, which includes the
		// older { date, slots } shape written by builds that emitted four times a
		// day. That is what stops an install upgrading mid-day from emitting a
		// second time on a day it has already reported.
		if (parsed.date === utcDate) return false;
		storage.setItem(ACTIVE_STORAGE_KEY, JSON.stringify({ date: utcDate }));
		return true;
	} catch {
		return reserveFallback();
	}
}

function releaseDailyActiveCapture(storage?: DailyActiveStorage, now = new Date()): void {
	const utcDate = now.toISOString().slice(0, 10);
	if (fallbackActiveDate === utcDate) fallbackActiveDate = "";
	if (!storage) return;

	try {
		const raw = storage.getItem(ACTIVE_STORAGE_KEY);
		const parsed = raw ? (JSON.parse(raw) as { date?: unknown }) : {};
		if (parsed.date !== utcDate) return;
		// DailyActiveStorage is deliberately narrow (getItem/setItem only), so the
		// reservation is cleared by blanking the date rather than widening it.
		storage.setItem(ACTIVE_STORAGE_KEY, JSON.stringify({ date: "" }));
	} catch {
		// The fallback reservation was already released above.
	}
}

export function reserveRouteViewCapture(
	storage: DailyActiveStorage | undefined,
	surface: string,
	now = new Date(),
): boolean {
	const normalizedSurface = surface.trim() || "unknown";
	const utcDate = now.toISOString().slice(0, 10);
	const reserveFallback = () => {
		if (fallbackRouteViewDate !== utcDate) {
			fallbackRouteViewDate = utcDate;
			fallbackRouteViewSurfaces = new Set<string>();
		}
		if (fallbackRouteViewSurfaces.has(normalizedSurface)) return false;
		fallbackRouteViewSurfaces.add(normalizedSurface);
		return true;
	};

	if (!storage) return reserveFallback();
	try {
		const raw = storage.getItem(ROUTE_VIEW_STORAGE_KEY);
		const parsed = raw ? (JSON.parse(raw) as { date?: unknown; surfaces?: unknown }) : {};
		const surfaces =
			parsed.date === utcDate && Array.isArray(parsed.surfaces)
				? parsed.surfaces.filter((value): value is string => typeof value === "string")
				: [];
		if (surfaces.includes(normalizedSurface)) return false;
		surfaces.push(normalizedSurface);
		storage.setItem(ROUTE_VIEW_STORAGE_KEY, JSON.stringify({ date: utcDate, surfaces }));
		return true;
	} catch {
		return reserveFallback();
	}
}

export function telemetryStorage(): DailyActiveStorage | undefined {
	try {
		return window.localStorage;
	} catch {
		return undefined;
	}
}

export function startDailyActiveHeartbeat({
	storage,
	now = () => new Date(),
	capture,
	window,
	document,
}: DailyActiveHeartbeatOptions): () => void {
	const maybeCapture = () => {
		const captureTime = now();
		if (!reserveDailyActiveCapture(storage, captureTime)) return;

		let result: boolean | void | Promise<boolean | void>;
		try {
			result = capture();
		} catch {
			releaseDailyActiveCapture(storage, captureTime);
			return;
		}
		void Promise.resolve(result).then(
			(captured) => {
				if (captured === false) releaseDailyActiveCapture(storage, captureTime);
			},
			() => releaseDailyActiveCapture(storage, captureTime),
		);
	};
	const onVisibilityChange = () => {
		if (document.visibilityState === "visible") {
			maybeCapture();
		}
	};
	const activityEvents = ["pointerdown", "keydown"] as const;
	const passiveOptions = { passive: true };

	maybeCapture();
	window.addEventListener("focus", maybeCapture);
	document.addEventListener("visibilitychange", onVisibilityChange);
	for (const event of activityEvents) {
		document.addEventListener(event, maybeCapture, passiveOptions);
	}

	return () => {
		window.removeEventListener("focus", maybeCapture);
		document.removeEventListener("visibilitychange", onVisibilityChange);
		for (const event of activityEvents) {
			document.removeEventListener(event, maybeCapture);
		}
	};
}

function normalizeException(reason: unknown): Error {
	if (reason instanceof Error) return reason;
	if (typeof reason === "string") return new Error(reason);
	try {
		return new Error(JSON.stringify(reason));
	} catch {
		return new Error("Unknown renderer exception");
	}
}

function routeSurface(pathname: string): string {
	if (pathname === "/") return "home";
	if (/^\/settings(?:\/|$)/.test(pathname)) return "global_settings";
	if (/^\/projects\/[^/]+\/sessions\/[^/]+$/.test(pathname)) return "session_detail";
	if (/^\/projects\/[^/]+(?:\/|$)/.test(pathname)) {
		if (/\/settings$/.test(pathname)) return "project_settings";
		return "project_board";
	}
	if (/^\/sessions\/[^/]+$/.test(pathname)) return "session_detail";
	if (/^\/automations(?:\/|$)/.test(pathname)) return "automations";
	if (/^\/artifacts(?:\/|$)/.test(pathname)) return "artifacts";
	return "other";
}

const AUTOMATION_SCHEDULE_PRESET_SET = new Set(["daily", "weekly", "custom"]);
const AUTOMATION_PROMPT_LENGTH_BUCKET_SET = new Set(["xs", "s", "m", "l", "xl"]);

/**
 * Coarse size of an automation prompt, never the prompt itself. Measured in
 * bytes because that is what the daemon's 4096 cap counts; counting UTF-16
 * units instead would compress the bands for non-Latin prompts.
 */
export function automationPromptLengthBucket(prompt: string): string {
	const length = new TextEncoder().encode(prompt).length;
	if (length <= 80) return "xs";
	if (length <= 240) return "s";
	if (length <= 800) return "m";
	if (length <= 2000) return "l";
	return "xl";
}

async function sha256Hex(raw: string): Promise<string> {
	const subtle = globalThis.crypto?.subtle;
	if (!subtle) return "redacted";
	const bytes = new TextEncoder().encode(raw);
	const digest = await subtle.digest("SHA-256", bytes);
	return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

async function hashedTelemetryID(value: unknown): Promise<string | undefined> {
	if (typeof value !== "string") return undefined;
	const trimmed = value.trim();
	if (!trimmed) return undefined;
	return sha256Hex(trimmed);
}

function isLocalURL(value: string): boolean {
	try {
		const url = new URL(value);
		return (
			url.protocol === "file:" ||
			(url.protocol === "app:" && url.host === "renderer") ||
			isLoopbackHostname(url.hostname)
		);
	} catch {
		return false;
	}
}

function redactEmbeddedLocalURLs(value: string): string {
	return value.replace(EMBEDDED_LOCAL_URL_PATTERN, REDACTED_LOCAL_URL);
}

function redactEmbeddedAbsolutePaths(value: string): string {
	return value
		.replace(/(?:\/Users\/|\/home\/|\/tmp\/|\/private\/var\/|\/var\/folders\/)\S+/g, REDACTED_LOCAL_PATH)
		.replace(/\b[A-Za-z]:\\[^\s)]+/g, REDACTED_LOCAL_PATH);
}

function sanitizeSensitiveString(value: string): string {
	const trimmed = value.trim();
	if (!trimmed) return trimmed;
	if (isLocalURL(trimmed)) return REDACTED_LOCAL_URL;
	return redactEmbeddedAbsolutePaths(redactEmbeddedLocalURLs(trimmed));
}

function sanitizePostHogValue(value: unknown): unknown {
	if (typeof value === "string") return sanitizeSensitiveString(value);
	if (Array.isArray(value)) return value.map((item) => sanitizePostHogValue(item));
	if (value && typeof value === "object") {
		return Object.fromEntries(Object.entries(value).map(([key, nested]) => [key, sanitizePostHogValue(nested)]));
	}
	return value;
}

export function sanitizePostHogEvent(event: Record<string, unknown>): Record<string, unknown> {
	return sanitizePostHogValue(event) as Record<string, unknown>;
}

export function sanitizeReplayRequestName(name: string): string {
	const withoutQuery = name.split("?")[0] ?? name;
	return sanitizeSensitiveString(withoutQuery);
}

function sanitizePostHogCaptureResult<T>(event: T): T {
	const safe = sanitizePostHogEvent(event as unknown as Record<string, unknown>);
	if (installId) safe.properties = { ...((safe.properties as Record<string, unknown>) ?? {}), install_id: installId };
	return safe as T;
}

async function sanitizeRendererContextProperties(properties?: TelemetryProperties): Promise<TelemetryProperties> {
	const safe: TelemetryProperties = {};
	if (typeof properties?.source === "string" && properties.source.trim() !== "") {
		safe.source = properties.source;
	}
	if (typeof properties?.operation === "string" && properties.operation.trim() !== "") {
		safe.operation = properties.operation;
	}
	if (typeof properties?.surface === "string" && properties.surface.trim() !== "") {
		safe.surface = properties.surface;
	}
	if (typeof properties?.unhandled === "boolean") {
		safe.unhandled = properties.unhandled;
	}
	const projectIDHash = await hashedTelemetryID(properties?.project_id);
	if (projectIDHash) {
		safe.project_id_hash = projectIDHash;
	}
	const sessionIDHash = await hashedTelemetryID(properties?.session_id);
	if (sessionIDHash) {
		safe.session_id_hash = sessionIDHash;
	}
	return safe;
}

// Values the cloud funnel events may carry. Anything outside a vocabulary is
// reported as "other" so a free-form string can never leak through.
const CLOUD_PROVIDERS = new Set(["coder", "freestyle", "docker", "nodeops", "ecs", "daytona", "default"]);
const CLOUD_SIGN_IN_METHODS = new Set(["workos", "local"]);
const CLOUD_SESSION_KINDS = new Set(["worker", "orchestrator"]);
const CLOUD_FAILURE_STAGES = new Set(["create", "startup"]);
const CLOUD_UPGRADE_SOURCES = new Set(["settings", "plan_refusal", "banner", "other"]);
const CLOUD_CODE_PATTERN = /^[A-Za-z][A-Za-z0-9_]{0,63}$/;
const CLOUD_PLAN_PATTERN = /^[a-z][a-z0-9_-]{0,31}$/;

function cloudCode(value: unknown): string {
	return typeof value === "string" && CLOUD_CODE_PATTERN.test(value) ? value : "other";
}

function cloudPlan(value: unknown): string {
	return typeof value === "string" && CLOUD_PLAN_PATTERN.test(value) ? value : "other";
}

function nonNegativeNumber(value: unknown): number | undefined {
	return typeof value === "number" && Number.isFinite(value) && value >= 0 ? Math.round(value) : undefined;
}

async function sanitizeCloudFunnelProperties(event: string, properties?: TelemetryProperties): Promise<TelemetryProperties> {
	const safe: TelemetryProperties = {};
	switch (event) {
		case "ao.renderer.cloud_signed_in":
			safe.method = enumValue(properties?.method, CLOUD_SIGN_IN_METHODS, "other");
			break;
		case "ao.renderer.cloud_offering_resolved": {
			safe.org_kind = enumValue(properties?.org_kind, CLOUD_ORG_KINDS);
			safe.billing_mode = enumValue(properties?.billing_mode, CLOUD_BILLING_MODES);
			safe.default_provider = enumValue(properties?.default_provider, CLOUD_PROVIDERS, "other");
			for (const key of ["provider_count", "org_count"]) {
				const value = nonNegativeNumber(properties?.[key]);
				if (value !== undefined) safe[key] = value;
			}
			break;
		}
		case "ao.renderer.cloud_project_created":
		case "ao.renderer.cloud_session_create_requested":
		case "ao.renderer.cloud_session_ready":
		case "ao.renderer.cloud_session_failed": {
			safe.provider = enumValue(properties?.provider, CLOUD_PROVIDERS, "other");
			const projectIDHash = await hashedTelemetryID(properties?.project_id);
			if (projectIDHash) safe.project_id_hash = projectIDHash;
			if (event !== "ao.renderer.cloud_project_created") {
				safe.session_kind = enumValue(properties?.session_kind, CLOUD_SESSION_KINDS, "other");
			}
			const ttr = nonNegativeNumber(properties?.ttr_ms);
			if (ttr !== undefined && event !== "ao.renderer.cloud_session_create_requested") safe.ttr_ms = ttr;
			if (event === "ao.renderer.cloud_session_failed") {
				safe.stage = enumValue(properties?.stage, CLOUD_FAILURE_STAGES, "other");
				safe.code = cloudCode(properties?.code);
				const status = nonNegativeNumber(properties?.status);
				if (status !== undefined) safe.status = status;
			}
			break;
		}
		case "ao.renderer.cloud_upgrade_viewed":
			safe.source = enumValue(properties?.source, CLOUD_UPGRADE_SOURCES, "other");
			break;
		case "ao.renderer.cloud_checkout_started":
		case "ao.renderer.cloud_checkout_completed": {
			safe.plan = cloudPlan(properties?.plan);
			const duration = nonNegativeNumber(properties?.duration_ms);
			if (duration !== undefined && event === "ao.renderer.cloud_checkout_completed") safe.duration_ms = duration;
			break;
		}
		case "ao.renderer.cloud_plan_refusal_shown":
		case "ao.renderer.cloud_limit_reached":
			safe.code = cloudCode(properties?.code);
			if (typeof properties?.shown === "boolean") safe.shown = properties.shown;
			break;
	}
	return safe;
}

const CLOUD_FUNNEL_EVENTS = new Set([
	"ao.renderer.cloud_signed_in",
	"ao.renderer.cloud_offering_resolved",
	"ao.renderer.cloud_project_created",
	"ao.renderer.cloud_session_create_requested",
	"ao.renderer.cloud_session_ready",
	"ao.renderer.cloud_session_failed",
	"ao.renderer.cloud_upgrade_viewed",
	"ao.renderer.cloud_checkout_started",
	"ao.renderer.cloud_checkout_completed",
	"ao.renderer.cloud_plan_refusal_shown",
	"ao.renderer.cloud_limit_reached",
]);

const ORCHESTRATOR_SPAWN_SOURCE_SET = new Set<string>(ORCHESTRATOR_SPAWN_SOURCES);

const EDITOR_ID_SET = new Set<string>(EDITOR_IDS);
const OPEN_TARGET_KIND_SET = new Set(["editor", "file_manager", "terminal"]);

export async function sanitizeRendererProperties(
	event: string,
	properties?: TelemetryProperties,
): Promise<TelemetryProperties> {
	if (CLOUD_FUNNEL_EVENTS.has(event)) return sanitizeCloudFunnelProperties(event, properties);
	const safe: TelemetryProperties = {};
	switch (event) {
		case "ao.app.active":
			if (properties?.channel === "renderer") safe.channel = "renderer";
			break;
		case "ao.renderer.route_viewed":
			if (typeof properties?.surface === "string" && properties.surface.trim() !== "") {
				safe.surface = properties.surface;
			}
			break;
		case "ao.onboarding.step_viewed":
		case "ao.onboarding.step_completed":
		case "ao.onboarding.step_abandoned":
			if (typeof properties?.step === "string" && ONBOARDING_STEPS.some((step) => step === properties.step)) safe.step = properties.step;
			break;
		case "ao.renderer.loaded":
			break;
		case "ao.renderer.project_add_succeeded":
		case "ao.renderer.project_removed":
		case "ao.renderer.orchestrator_open_requested":
		case "ao.renderer.task_create_succeeded":
		case "ao.renderer.task_create_failed":
		case "ao.renderer.session_kill_succeeded":
		case "ao.renderer.session_kill_failed":
		case "ao.renderer.settings_save_succeeded":
		case "ao.renderer.settings_save_failed": {
			const projectIDHash = await hashedTelemetryID(properties?.project_id);
			if (projectIDHash) safe.project_id_hash = projectIDHash;
			if ((event === "ao.renderer.task_create_failed" || event === "ao.renderer.task_create_succeeded") && properties?.scope === "standalone") {
				safe.scope = "standalone";
			}
			break;
		}
		case "ao.renderer.cloud_orchestrator_spawn_succeeded":
		case "ao.renderer.cloud_orchestrator_spawn_failed": {
			// Cloud orchestrator spawns are the adoption signal for the hosted
			// offering's core loop; only a hashed project id rides along.
			const cloudProjectIDHash = await hashedTelemetryID(properties?.project_id);
			if (cloudProjectIDHash) safe.project_id_hash = cloudProjectIDHash;
			break;
		}
		case "ao.renderer.cloud_workers_viewed":
			// How many workers an orchestrator is coordinating when the human
			// looks: the count alone answers "do people use one worker or many".
			if (typeof properties?.worker_count === "number") safe.worker_count = properties.worker_count;
			break;
		case "ao.renderer.cloud_worker_opened":
			// Whether the click-through happens before or after a PR exists
			// separates "checking on a worker" from "reviewing its output".
			if (typeof properties?.has_pr === "boolean") safe.has_pr = properties.has_pr;
			break;
		case "ao.renderer.orchestrator_spawn_succeeded":
		case "ao.renderer.orchestrator_spawn_failed": {
			const projectIDHash = await hashedTelemetryID(properties?.project_id);
			if (projectIDHash) safe.project_id_hash = projectIDHash;
			if (typeof properties?.source === "string" && ORCHESTRATOR_SPAWN_SOURCE_SET.has(properties.source)) {
				safe.source = properties.source;
			}
			break;
		}
		case "ao.renderer.automation_create_opened":
			break;
		case "ao.renderer.automation_create_succeeded":
		case "ao.renderer.automation_create_failed": {
			// Which kind of automation a prompt describes stays on the machine;
			// only the schedule shape and a size band ride along.
			const projectIDHash = await hashedTelemetryID(properties?.project_id);
			if (projectIDHash) safe.project_id_hash = projectIDHash;
			if (typeof properties?.schedule_preset === "string" && AUTOMATION_SCHEDULE_PRESET_SET.has(properties.schedule_preset)) {
				safe.schedule_preset = properties.schedule_preset;
			}
			if (typeof properties?.prompt_length_bucket === "string" && AUTOMATION_PROMPT_LENGTH_BUCKET_SET.has(properties.prompt_length_bucket)) {
				safe.prompt_length_bucket = properties.prompt_length_bucket;
			}
			break;
		}
		case "ao.renderer.artifact_create_succeeded":
		case "ao.renderer.artifact_create_failed":
			// Where the session lives, never which project.
			if (properties?.scope === "standalone" || properties?.scope === "project") safe.scope = properties.scope;
			break;
		case "ao.renderer.notification_opened":
			if (properties?.target === "pr" || properties?.target === "session") safe.target = properties.target;
			break;
		case "ao.renderer.notification_mark_read_succeeded":
		case "ao.renderer.notification_mark_read_failed":
			if (properties?.scope === "single" || properties?.scope === "all") safe.scope = properties.scope;
			break;
		case "ao.renderer.daemon_failure":
			if (typeof properties?.daemon_state === "string") safe.daemon_state = properties.daemon_state;
			if (typeof properties?.code === "string") safe.code = properties.code;
			if (typeof properties?.exit_code === "number") safe.exit_code = properties.exit_code;
			if (typeof properties?.signal === "string") safe.signal = properties.signal;
			break;
		case "ao.renderer.api_error":
			if (typeof properties?.operation === "string") safe.operation = properties.operation;
			if (typeof properties?.error_category === "string") safe.error_category = properties.error_category;
			if (typeof properties?.status === "number") safe.status = properties.status;
			break;
		case "ao.renderer.terminal_attach_failed":
			if (properties?.reason === "open_timeout" || properties?.reason === "pane_error") {
				safe.reason = properties.reason;
			}
			break;
		case "ao.renderer.agents_available": {
			// Counts and a fixed-vocabulary id list only. Agent ids come from AO's own
			// registry, never from user input, so they carry no user data.
			for (const key of ["installed_count", "authorized_count", "supported_count"] as const) {
				if (typeof properties?.[key] === "number") safe[key] = properties[key];
			}
			if (typeof properties?.authorized_agents === "string") {
				safe.authorized_agents = properties.authorized_agents;
			}
			break;
		}
		case "ao.renderer.open_in_editor_succeeded": {
			const projectIDHash = await hashedTelemetryID(properties?.project_id);
			if (projectIDHash) safe.project_id_hash = projectIDHash;
			if (typeof properties?.editor_id === "string" && EDITOR_ID_SET.has(properties.editor_id)) {
				safe.editor_id = properties.editor_id;
			}
			if (typeof properties?.target_kind === "string" && OPEN_TARGET_KIND_SET.has(properties.target_kind)) {
				safe.target_kind = properties.target_kind;
			}
			break;
		}
		case "ao.renderer.open_in_editor_failed": {
			const projectIDHash = await hashedTelemetryID(properties?.project_id);
			if (projectIDHash) safe.project_id_hash = projectIDHash;
			break;
		}
		case "ao.renderer.session_state_unknown":
			if (properties?.field === "status" || properties?.field === "activity") safe.field = properties.field;
			if (properties?.reason === "missing" || properties?.reason === "unrecognized") safe.reason = properties.reason;
			break;
		case "ao.renderer.project_add_failed":
		case "ao.renderer.project_clone_failed":
			if (typeof properties?.error_code === "string" && (/^[A-Z0-9_]+$/.test(properties.error_code) || properties.error_code === "unknown")) {
				safe.error_code = properties.error_code;
			}
			break;
		case "ao.renderer.update_failures_daily":
			if (typeof properties?.day === "string" && /^\d{4}-\d{2}-\d{2}$/.test(properties.day)) safe.day = properties.day;
			if (typeof properties?.failure_count === "number" && Number.isFinite(properties.failure_count) && properties.failure_count >= 0) {
				safe.failure_count = properties.failure_count;
			}
			break;
		case "ao.renderer.update_failed":
		case "ao.renderer.update_downloaded":
		case "ao.renderer.update_unsupported":
			// Version strings are release identifiers, not user data. The updater's
			// raw error message is deliberately absent: it can carry feed URLs and
			// local paths, so update-telemetry.ts maps it to error_category first.
			if (properties?.report === "daily") safe.report = "daily";
			if (typeof properties?.day === "string" && /^\d{4}-\d{2}-\d{2}$/.test(properties.day)) safe.day = properties.day;
			if (typeof properties?.failure_count === "number" && Number.isFinite(properties.failure_count) && properties.failure_count >= 0) {
				safe.failure_count = properties.failure_count;
			}
			if (typeof properties?.to_version === "string") safe.to_version = properties.to_version;
			if (typeof properties?.error_category === "string") safe.error_category = properties.error_category;
			if (properties?.phase === "check" || properties?.phase === "download") safe.phase = properties.phase;
			if (properties?.trigger === "automatic" || properties?.trigger === "manual") safe.trigger = properties.trigger;
			for (const key of ["differential_eligible", "fallback"] as const) {
				if (typeof properties?.[key] === "boolean") safe[key] = properties[key];
			}
			if (properties?.transfer_mode === "differential" || properties?.transfer_mode === "full") {
				safe.transfer_mode = properties.transfer_mode;
			}
			for (const key of ["transferred_bytes", "target_bytes"] as const) {
				const value = properties?.[key];
				if (typeof value === "number" && Number.isFinite(value) && value >= 0) safe[key] = value;
			}
			break;
		case "ao.renderer.mobile_connect_opened":
			// Whether the bridge was already on when the modal opened separates
			// "came to set this up" from "came back to re-scan the QR".
			if (typeof properties?.bridge_enabled === "boolean") safe.bridge_enabled = properties.bridge_enabled;
			break;
		case "ao.renderer.update_channel_changed":
			// Closed vocabulary on both ends. A feature build's PR number or branch
			// name is deliberately absent: it names unreleased work.
			for (const key of ["from_channel", "to_channel"] as const) {
				const value = properties?.[key];
				if (value === "stable" || value === "nightly" || value === "feature" || value === "unknown") {
					safe[key] = value;
				}
			}
			break;
		case "ao.renderer.support_opened":
			break;
		case "ao.renderer.support_submitted":
			// The report's summary, details, and diagnostics block are the user's own
			// words and machine state. Only the chosen destination and whether the
			// hand-off worked are reported.
			if (properties?.destination === "github" || properties?.destination === "discord" || properties?.destination === "email") {
				safe.destination = properties.destination;
			}
			if (properties?.outcome === "succeeded" || properties?.outcome === "failed") safe.outcome = properties.outcome;
			break;
		case "ao.renderer.review_auto_review_toggled":
			// The session-scoped switch duplicates a project-level setting, so
			// whether anyone reaches for it decides if it stays a separate control.
			if (typeof properties?.enabled === "boolean") safe.enabled = properties.enabled;
			break;
		case "ao.renderer.mobile_bridge_toggled":
			// The host, port, and connection password in the QR never leave the
			// machine: only the direction of the switch and whether it worked.
			if (typeof properties?.enabled === "boolean") safe.enabled = properties.enabled;
			if (properties?.outcome === "succeeded" || properties?.outcome === "failed") safe.outcome = properties.outcome;
			break;
		case "ao.renderer.session_management_summary": {
			const numericKeys = [
				"measurement_schema_version",
				"window_duration_seconds",
				"orchestrator_active_seconds",
				"worker_active_seconds",
				"transition_orchestrator_to_worker_count",
				"transition_orchestrator_same_count",
				"transition_orchestrator_switch_count",
				"transition_worker_to_orchestrator_count",
				"transition_worker_same_count",
				"transition_worker_switch_count",
				"manual_worker_open_count",
				"direct_worker_chat_steer_count",
				"direct_worker_terminal_input_burst_count",
				"direct_worker_lifecycle_action_count",
				"pattern_orchestrator_worker_orchestrator_count",
				"pattern_orchestrator_multiple_workers_count",
			] as const;
			for (const key of numericKeys) {
				const value = properties?.[key];
				if (typeof value === "number" && Number.isFinite(value) && value >= 0) safe[key] = value;
			}
			if (typeof properties?.window_id === "string" && /^[0-9a-f-]{36}$/i.test(properties.window_id)) {
				safe.window_id = properties.window_id;
			}
			if (properties?.flush_reason === "interval" || properties?.flush_reason === "startup") {
				safe.flush_reason = properties.flush_reason;
			}
			break;
		}
	}
	return safe;
}

function exceptionName(error: unknown): string {
	if (error instanceof Error && error.name.trim() !== "") return error.name.trim();
	if (typeof error === "string") return "string";
	return "unknown";
}

export async function sanitizeRendererExceptionProperties(
	error: unknown,
	properties?: TelemetryProperties,
): Promise<TelemetryProperties> {
	const safe: TelemetryProperties = {
		error_name: exceptionName(error),
	};
	return { ...safe, ...(await sanitizeRendererContextProperties(properties)) };
}

function bindErrorHandlers() {
	if (errorHandlersBound) return;
	errorHandlersBound = true;
	window.addEventListener("error", (event) => {
		void captureRendererException(event.error ?? new Error(event.message), {
			source: "window-error",
			unhandled: true,
		});
	});
	window.addEventListener("unhandledrejection", (event) => {
		void captureRendererException(normalizeException(event.reason), {
			source: "unhandledrejection",
			unhandled: true,
		});
	});
}

type PostHogClient = typeof posthogModule;
type PostHogInitOptions = NonNullable<Parameters<PostHogClient["init"]>[1]>;

// The SDK (~650 KB) is loaded inside initTelemetry instead of at module load, so
// it stays off the renderer's first-paint path. Every use below runs after
// initTelemetry() resolved true, which is where this is assigned.
let posthog!: PostHogClient;

async function loadPostHog(): Promise<PostHogClient> {
	return (await import("posthog-js/dist/module.full.no-external")).default;
}

export function buildPostHogConfig(distinctId: string): PostHogInitOptions {
	return {
		api_host: POSTHOG_HOST,
		defaults: RELEASE_TAG,
		autocapture: false,
		capture_pageview: false,
		capture_exceptions: false,
		capture_performance: false,
		// Session replay is billed per recording, not per event, so it bypasses
		// every limiter in this file. AO never watches replays, so keep the
		// recorder off in the client instead of relying on the project-side
		// toggle staying off.
		disable_session_recording: true,
		// AO reads no feature flags and ships no surveys. Both of these poll
		// PostHog on init, and /flags requests are billed, so every one of these
		// requests is pure cost for data nothing consumes.
		advanced_disable_flags: true,
		disable_surveys: true,
		// AO owns the stable random installation ID. Memory-only SDK
		// persistence prevents legacy identified state from replacing it after
		// an upgrade; the AO-owned heartbeat and route reservations continue to
		// use window.localStorage independently.
		persistence: "memory",
		// Anonymous (no profile) until identifyCloudUser() runs on an AO Cloud
		// sign-in; only then does PostHog process a person.
		person_profiles: "identified_only",
		// The SDK flags a localhost page as an internal user, which turns person
		// processing on for every event; a dev build driving the real export path
		// would otherwise bill anonymous events as identified.
		internal_or_test_user_hostname: null,
		bootstrap: {
			distinctID: distinctId,
			isIdentifiedID: false,
		},
		before_send: (event) => (event ? sanitizePostHogCaptureResult(event) : event),
		session_recording: {
			maskCapturedNetworkRequestFn: (request) => {
				if (request.name) {
					request.name = sanitizeReplayRequestName(request.name);
				}
				return request;
			},
		},
	};
}

export async function initTelemetry(): Promise<boolean> {
	if (initPromise) return initPromise;
	const attempt = (async () => {
		if (!POSTHOG_KEY) return false;
		const bootstrap = await aoBridge.telemetry.getBootstrap();
		// Null means the supervisor withheld it: no key, no data dir, or an
		// unpackaged build that has not opted in. The client is never created.
		if (!bootstrap) return false;
		installId = bootstrap.installId;
		disabledEventMatchers = bootstrap.disabledEvents ?? [];
		const channel = releaseChannelFrom(await readUpdateSettingsForTelemetry());
		// The user may have opted out while the awaits above were pending; applyAnalyticsOptOut
		// could only set the flag then, so honor it before any client exists.
		if (analyticsOptedOut) return false;
		telemetryContext = buildTelemetryContext(bootstrap.appVersion, bootstrap.platform, channel);
		posthog = await loadPostHog();
		// Loading the SDK is asynchronous; an opt-out in that gap must prevent
		// both SDK initialization and the unconditional opt-in below.
		if (analyticsOptedOut) return false;
		posthog.init(POSTHOG_KEY, buildPostHogConfig(bootstrap.distinctId));
		// A previous launch's opt-out is remembered by the SDK; the AO marker is the
		// source of truth and the bootstrap is only handed out when it is absent.
		posthog.opt_in_capturing({ captureEventName: false });
		posthogReady = true;
		registerBaseProperties();
		void syncGithubLogin();
		// Typed renderer fault intake has its own main-owned policy gate. PostHog
		// product analytics are intentionally independent of that preference.
		void initSentry({
			release: bootstrap.appVersion,
			channel,
			platform: bootstrap.platform,
			distinctId: bootstrap.distinctId,
		});
		bindErrorHandlers();
		startDailyActiveHeartbeat({
			storage: telemetryStorage(),
			window,
			document,
			// Ride the batched request queue like every other renderer event
			// (loaded, route_viewed). An earlier `send_instantly: true` here
			// fired an immediate, un-retried send during init that never
			// reached PostHog in packaged builds, so renderer app-active
			// dropped to zero while batched events kept landing.
			capture: async () =>
				isDeniedEvent("ao.app.active")
					? true
					: Boolean(
					posthog.capture(
						postHogEventName("ao.app.active"),
						withTelemetryContext(await sanitizeRendererProperties("ao.app.active", { channel: "renderer" })),
					),
				),
		});
		if (!isDeniedEvent("ao.renderer.loaded")) {
			posthog.capture(
				postHogEventName("ao.renderer.loaded"),
				withTelemetryContext(await sanitizeRendererProperties("ao.renderer.loaded")),
			);
		}
		return true;
	})().catch(() => {
		// A thrown error is transient (the bridge not ready yet, or a hiccup in
		// posthog.init): drop the memoized promise so the next captured event
		// retries init rather than the whole session going dark after one bad
		// moment. A deliberate `return false` above (no key, not opted in) is
		// not an error and stays cached, so an intentionally-withheld client is
		// not retried forever.
		if (initPromise === attempt) initPromise = null;
		return false;
	});
	initPromise = attempt;
	return attempt;
}

/**
 * Acknowledges the renderer part of failure-reporting cleanup. Renderer fault
 * intake forwards directly through preload and owns no durable or retry queue;
 * PostHog product-analytics queues are deliberately outside this policy.
 */
export function clearRendererTelemetryQueues(): void {}

// Failure-reporting enablement is enforced in preload/main. It never opts the
// independent PostHog client in or out.
export function applyRendererTelemetryPolicy(_enabled: boolean): void {}

export async function captureRendererEvent(event: string, properties?: Record<string, unknown>): Promise<boolean> {
	// An opted-out renderer must not touch rate-limit slots or the persisted
	// updater counts, which would otherwise be exported after an opt-in.
	if (analyticsOptedOut) return false;
	// Checked before the reservations so a silenced stream does not consume a
	// rate-limit slot on its way to being discarded, matching the daemon, where
	// the denylist sits outermost.
	if (isDeniedEvent(event)) return false;
	const sanitizedProperties = await sanitizeRendererProperties(event, properties);
	if (event === "ao.renderer.route_viewed") {
		const surface = typeof sanitizedProperties.surface === "string" ? sanitizedProperties.surface : "other";
		if (!reserveRouteViewCapture(telemetryStorage(), surface)) return false;
	} else if (event !== "ao.renderer.update_failed" && !reserveCapture(event)) {
		return false;
	}
	if (!(await initTelemetry()) || analyticsOptedOut) return false;
	const storage = telemetryStorage();
	let releaseUpdateFailureClaim: (() => void) | undefined;
	if (event === "ao.renderer.update_failed") {
		if (!reserveCapture(event)) return false;
		if (storage) {
			const decision = claimUpdateFailure(storage, sanitizedProperties, Date.now());
			if (!decision.emit) return false;
			// A rollback after an opt-out would restore the counts the opt-out discarded.
			releaseUpdateFailureClaim = () => {
				if (!analyticsOptedOut) decision.rollback();
			};
			const dailyName = "ao.renderer.update_failures_daily";
			for (const daily of decision.dailies ?? []) {
				if (isDeniedEvent(dailyName)) continue;
				if (!reserveCapture(dailyName)) {
					releaseUpdateFailureClaim();
					return false;
				}
				let sent = false;
				try {
					sent = Boolean(posthog.capture(dailyName, withTelemetryContext(await sanitizeRendererProperties(dailyName, daily))));
				} catch {
					sent = false;
				}
				if (!sent) {
					releaseUpdateFailureClaim();
					return false;
				}
				if (typeof daily.day === "string") decision.ackDaily(daily.day);
			}
			if (!decision.streak) return true;
		}
	}
	if (storage && event === "ao.renderer.update_downloaded") clearUpdateFailures(storage, sanitizedProperties.to_version);
	const safeProperties = withTelemetryContext(sanitizedProperties);
	let captured = false;
	try {
		captured = Boolean(posthog.capture(postHogEventName(event), safeProperties));
		return captured;
	} finally {
		if (!captured && releaseUpdateFailureClaim) releaseUpdateFailureClaim();
	}
}

export async function captureRendererException(error: unknown, properties?: Record<string, unknown>): Promise<void> {
	// "$exception" is the name this lands under in PostHog, so that is what an
	// operator would type to silence a crash loop.
	if (isDeniedEvent("$exception")) return;
	if (!reserveCapture(`exception:${exceptionName(error)}`)) return;
	if (!(await initTelemetry())) return;
	const safeProperties = withTelemetryContext(await sanitizeRendererExceptionProperties(error, properties));
	posthog.captureException(normalizeException(error), safeProperties);
	// Mirror into Sentry (no-op unless a DSN is configured). Source drives the
	// category so a boundary crash classifies as render_crash.
	const source = typeof properties?.source === "string" ? properties.source : undefined;
	captureExceptionToSentry(normalizeException(error), {
		category: source === "react-error-boundary" ? "render_crash" : undefined,
		operation: typeof properties?.operation === "string" ? properties.operation : undefined,
		unhandled: properties?.unhandled === true,
	});
}

export async function addRendererExceptionStep(message: string, properties?: Record<string, unknown>): Promise<void> {
	if (!(await initTelemetry())) return;
	const safeProperties = withTelemetryContext(await sanitizeRendererContextProperties(properties));
	posthog.addExceptionStep(message, safeProperties);
}

export { routeSurface };
