// The mobile app's event vocabulary and the property allowlist for each event.
//
// Split from any React Native import so the rules are unit-testable, the same
// split the codebase already uses for onboarding.ts vs onboardingStore.ts.
//
// The allowlist is the privacy contract. The phone can see session titles, PR
// titles, project names, terminal output, and the connection password, none of
// which may ever reach PostHog. sanitize.ts iterates THIS allowlist, never the
// caller's payload, so a property nobody registered here is dropped rather than
// forwarded. Adding a field is a deliberate edit to this file, reviewable in one
// place.

import { LAUNCH_EVENT } from "./launchTiming";
import { SURFACES } from "./surfaces";
import { ERROR_KINDS, ERROR_PRESENTATIONS, SESSION_OPEN_SOURCES, UI_EVENTS } from "./uiUsage";

export const MOBILE_EVENTS = {
	// Once per UTC day, the phone's counterpart of ao.app.active (desktop) and
	// ao.cli.active (CLI). Same shape; union the three for total active users.
	active: "mobile.app.active",

	// A brand-new credential was validated by scanning a QR or manual entry. The
	// phone-side confirmation of the daemon's ao.mobile.device_connected.
	paired: "mobile.app.paired",
	// The stored config reconnected on launch (no user pairing action).
	connected: "mobile.app.connected",

	// Onboarding has no skip since the native rebuild (#7838): a start with no
	// completion is the drop-off. mobile.app.onboarding_skipped is retired.
	onboardingStarted: "mobile.app.onboarding_started",
	onboardingCompleted: "mobile.app.onboarding_completed",

	// A push notification opened the app. The retention signal.
	notificationOpened: "mobile.app.notification_opened",

	// A core action was taken (spawn, merge, kill, ...). Drives "most-used
	// feature". Screen-view tracking was dropped by product decision: feature
	// events answer the usage question without a second per-navigation stream.
	featureUsed: "mobile.app.feature_used",

	// UI usage and launch timing, the counterparts of the desktop's
	// ao.surfaces.viewed, ao.ui.usage_summary, ao.ui.error_shown and
	// ao.launches.timed. Counted in uiUsage.ts and launchTiming.ts.
	surfaceViewed: UI_EVENTS.viewed,
	usageSummary: UI_EVENTS.summary,
	errorShown: UI_EVENTS.errorShown,
	launchTimed: LAUNCH_EVENT,
} as const;

export type MobileEventName = (typeof MOBILE_EVENTS)[keyof typeof MOBILE_EVENTS];

/**
 * Allowed property keys per event, with the closed vocabulary for enum fields.
 *
 * A key absent here is dropped. A key present with a `oneOf` is dropped unless
 * the value is in that set. A key present with `kind: "count"` keeps a finite
 * non-negative integer; `kind: "flag"` keeps a boolean. No `kind: "string"`
 * exists on purpose: there is no free-text field in the mobile vocabulary, so a
 * title or path can never ride along.
 */
export type PropRule =
	| { readonly oneOf: readonly string[] }
	| { readonly kind: "count" }
	| { readonly flag: true };

const COUNT = { kind: "count" } as const;

// One key per surface and per session-open source, so a summary is plain
// numeric properties that PostHog can sum without unpacking a list.
const SUMMARY_RULES: Record<string, PropRule> = {
	measurement_schema_version: COUNT,
	window_seconds: COUNT,
	active_seconds: COUNT,
	flush_reason: { oneOf: ["background", "startup"] },
	foreground_count: COUNT,
	error_shown_count: COUNT,
	session_open_count: COUNT,
	...Object.fromEntries(SESSION_OPEN_SOURCES.map((source) => [`session_open_from_${source}_count`, COUNT])),
	...Object.fromEntries(SURFACES.flatMap((surface) => [[`surface_${surface}_seconds`, COUNT], [`surface_${surface}_opens`, COUNT]])),
};

export const MOBILE_ALLOWLIST: Record<string, Readonly<Record<string, PropRule>>> = {
	[MOBILE_EVENTS.active]: {},
	[MOBILE_EVENTS.paired]: {
		method: { oneOf: ["qr", "manual"] },
		from_onboarding: { flag: true },
	},
	[MOBILE_EVENTS.connected]: {
		trigger: { oneOf: ["launch", "reconnect"] },
	},
	[MOBILE_EVENTS.onboardingStarted]: {},
	[MOBILE_EVENTS.onboardingCompleted]: {},
	[MOBILE_EVENTS.notificationOpened]: {
		// notificationView maps a push to a small set of destinations; `cloud` is
		// an Orchestrator Cloud notification, opened on its Cloud page.
		target: { oneOf: ["session", "review", "prs", "cloud"] },
		cold_start: { flag: true },
	},
	[MOBILE_EVENTS.featureUsed]: {
		feature: {
			oneOf: ["spawn", "merge", "kill", "restore", "conductor", "send", "handoff"],
		},
		outcome: { oneOf: ["succeeded", "failed"] },
		// The chat/tui interface. Set on spawn and conductor (the mode the session
		// starts in) and on handoff (the mode it switches to). Absent on the other
		// features, which are mode-agnostic. Only the two enum values ever leave the
		// device; no titles or paths ride on this.
		mode: { oneOf: ["chat", "tui"] },
	},
	[MOBILE_EVENTS.surfaceViewed]: {
		surface: { oneOf: SURFACES },
	},
	[MOBILE_EVENTS.usageSummary]: SUMMARY_RULES,
	[MOBILE_EVENTS.errorShown]: {
		presentation: { oneOf: ERROR_PRESENTATIONS },
		error_kind: { oneOf: ERROR_KINDS },
		surface: { oneOf: SURFACES },
	},
	[MOBILE_EVENTS.launchTimed]: {
		launch_kind: { oneOf: ["first", "cold", "update"] },
		outcome: { oneOf: ["usable", "timeout", "no_machine", "backgrounded"] },
		clock_origin: { oneOf: ["process", "runtime"] },
		landing_surface: { oneOf: SURFACES },
		to_js_start_ms: COUNT,
		to_visible_ms: COUNT,
		to_usable_ms: COUNT,
	},
};
