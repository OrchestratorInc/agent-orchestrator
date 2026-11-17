import { useCallback, useEffect, useRef } from "react";
import { captureRendererEvent, isAnalyticsOptedOut, isDeniedEvent, telemetryStorage } from "./telemetry";
import {
	CONTROL_FEATURES,
	CONTROL_STAT_KEYS,
	HOVER_CONTROLS,
	isUiControl,
	RETRY_CONTROLS,
	SURFACE_FEATURES,
	SURFACE_STAT_KEYS,
	UI_FEATURES,
	UI_USAGE_EVENTS,
	type AttachmentSource,
	type ControlStatKey,
	type DirectCounter,
	type ErrorKind,
	type ErrorPresentation,
	type SendInput,
	type SurfaceOpenedBy,
	type SurfaceStatKey,
	type SwitchSource,
	type UiControl,
	type UiFeature,
} from "./ui-usage-registry";
import { surfaceElementOf, TELEMETRY_SURFACE_ATTRIBUTE, type UiSurface } from "./ui-surfaces";

// UI usage telemetry: which surfaces people look at and for how long, which
// controls they use and how (pointer or keyboard, after a hover or not), and
// where they get stuck (rage clicks, dead clicks, errors, abandoned menus).
//
// Everything is counted in memory and sent as one ao.ui.usage_summary per
// 45-60 minute window, like the session management summary. The hot paths are
// cheap on purpose: one passive delegated listener per event type on the
// document and Element.closest() walks only. The one layout read is for a
// click on nothing interactive, checked after the next paint.

const STATE_KEY = "ao.telemetry.uiUsage.v1";
const SURFACE_VIEW_KEY = "ao.telemetry.surfaceViewsByDate";
const RAGE_KEY = "ao.telemetry.rageSurfacesByDate";
const FEATURE_KEY = "ao.telemetry.featuresFirstUsed.v1";
const SUMMARY_EVENT = "ao.ui.usage_summary";

const CHECKPOINT_MS = 5 * 60_000;
const IDLE_MS = 5 * 60_000;
const FLUSH_MIN_MS = 45 * 60_000;
const FLUSH_JITTER_MS = 15 * 60_000;
/** A surface must stay visible this long before its daily view is sent. */
const VIEW_DEBOUNCE_MS = 1_000;
/** An input this recent explains why a surface opened or a session switched. */
const INTENT_MS = 1_500;
/** Surfaces shown this soon after start were restored, not opened. */
const RESTORE_MS = 10_000;
/** An error is attributed to the surface last used this recently. */
const ERROR_ATTRIBUTION_MS = 10_000;
const RAGE_CLICKS = 4;
const RAGE_WINDOW_MS = 1_000;
const HOVER_DWELL_MS = 500;
/** A pick reported this soon after a menu closed still counts as a pick. */
const LATE_PICK_MS = 500;
/** A surface back on screen this soon after it left (a remount, a flicker) is the same open. */
const REOPEN_GRACE_MS = 1_000;
/** The same error, shown again within this time, is counted but not sent again. */
const ERROR_REPEAT_MS = 5 * 60_000;
const TELEMETRY_ID_SELECTOR = "[data-telemetry-id]";
// What a click can land on and still be "doing something". Anything else that
// also has no pointer cursor is a dead click.
const INTERACTIVE_SELECTOR = [
	"a[href]",
	"button",
	"input",
	"textarea",
	"select",
	"label",
	"summary",
	"video",
	"audio",
	"[contenteditable]:not([contenteditable=false])",
	"[draggable=true]",
	"[tabindex]",
	"[role=button]",
	"[role=link]",
	"[role=menuitem]",
	"[role=menuitemcheckbox]",
	"[role=menuitemradio]",
	"[role=option]",
	"[role=tab]",
	"[role=checkbox]",
	"[role=radio]",
	"[role=switch]",
	"[role=treeitem]",
	"[role=slider]",
	"[role=combobox]",
	"[role=separator]",
	".xterm",
	TELEMETRY_ID_SELECTOR,
].join(",");

type SurfaceStats = Partial<Record<SurfaceStatKey | "visible_ms" | "focused_ms", number>>;
type ControlStats = Partial<Record<ControlStatKey, number>>;

type PersistedState = {
	version: 1;
	windowId: string;
	windowStartedAt: number;
	windowFlushAt: number;
	activeMs: number;
	counters: Partial<Record<DirectCounter, number>>;
	surfaces: Partial<Record<UiSurface, SurfaceStats>>;
	controls: Partial<Record<UiControl, ControlStats>>;
};

type Intent = { kind: "click" | "keyboard" | "shortcut" | "cmdk" | "swipe"; surface?: UiSurface; at: number };

type SurfaceToken = {
	surface: UiSurface;
	choice: boolean;
	picked: boolean;
	shownAt: number;
	closedAt?: number;
	closed: boolean;
};

function newWindowId(): string {
	if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
	return "00000000-0000-4000-8000-000000000000";
}

function freshState(now: number, flushAt = now + FLUSH_MIN_MS + Math.random() * FLUSH_JITTER_MS): PersistedState {
	return { version: 1, windowId: newWindowId(), windowStartedAt: now, windowFlushAt: flushAt, activeMs: 0, counters: {}, surfaces: {}, controls: {} };
}

function isFiniteNonNegative(value: unknown): value is number {
	return typeof value === "number" && Number.isFinite(value) && value >= 0;
}

function loadState(storage: Pick<Storage, "getItem"> | undefined, now: number): PersistedState {
	try {
		const parsed = JSON.parse(storage?.getItem(STATE_KEY) ?? "null") as Partial<PersistedState> | null;
		if (
			parsed?.version === 1 &&
			isFiniteNonNegative(parsed.windowStartedAt) &&
			isFiniteNonNegative(parsed.windowFlushAt) &&
			isFiniteNonNegative(parsed.activeMs) &&
			typeof parsed.windowId === "string" &&
			parsed.counters && typeof parsed.counters === "object" &&
			parsed.surfaces && typeof parsed.surfaces === "object" &&
			parsed.controls && typeof parsed.controls === "object"
		) {
			// Values are re-checked by the sanitizer at send time; a malformed
			// entry is dropped there rather than here.
			return parsed as PersistedState;
		}
	} catch {
		// Replace malformed local state.
	}
	return freshState(now);
}

function utcDay(now: number): string {
	return new Date(now).toISOString().slice(0, 10);
}

/** Reserves `slot` for today in a { date, slots } record. False when already taken. */
export function reserveDailySlot(storage: Pick<Storage, "getItem" | "setItem"> | undefined, key: string, slot: string, now = Date.now()): boolean {
	if (!storage) return false;
	const day = utcDay(now);
	let parsed: { date?: unknown; slots?: unknown } | null = null;
	try {
		parsed = JSON.parse(storage.getItem(key) ?? "null") as typeof parsed;
	} catch {
		// Unreadable state is replaced below, as if no slot were taken today.
	}
	try {
		const slots = parsed?.date === day && Array.isArray(parsed.slots) ? parsed.slots.filter((value): value is string => typeof value === "string") : [];
		if (slots.includes(slot)) return false;
		slots.push(slot);
		storage.setItem(key, JSON.stringify({ date: day, slots }));
		return true;
	} catch {
		return false;
	}
}

export function releaseDailySlot(storage: Pick<Storage, "getItem" | "setItem"> | undefined, key: string, slot: string, now = Date.now()): void {
	if (!storage) return;
	try {
		const parsed = JSON.parse(storage.getItem(key) ?? "null") as { date?: unknown; slots?: unknown } | null;
		if (parsed?.date !== utcDay(now) || !Array.isArray(parsed.slots)) return;
		storage.setItem(key, JSON.stringify({ date: parsed.date, slots: parsed.slots.filter((value) => value !== slot) }));
	} catch {
		// Best effort: a stuck slot only skips one daily event.
	}
}

type Capture = (event: string, properties: Record<string, unknown>) => Promise<boolean>;
type FlushListener = (info: { windowId: string; reason: "interval" | "startup" }) => void;

export type UiUsageRuntimeOptions = {
	storage?: Pick<Storage, "getItem" | "setItem" | "removeItem">;
	now?: () => number;
	capture?: Capture;
	optedOut?: () => boolean;
	isWindowActive?: () => boolean;
	afterPaint?: (callback: () => void) => void;
};

/**
 * The window's accumulator. Exported for tests; the app uses the singleton
 * started by startUiUsageTelemetry().
 */
export class UiUsageRuntime {
	private state: PersistedState;
	private readonly storage: UiUsageRuntimeOptions["storage"];
	private readonly now: () => number;
	private readonly capture: Capture;
	private readonly optedOut: () => boolean;
	private readonly isWindowActive: () => boolean;
	private readonly afterPaint: (callback: () => void) => void;
	private readonly startedAt: number;
	private readonly visible = new Map<UiSurface, number>();
	private readonly lastHiddenAt = new Map<UiSurface, number>();
	private readonly lastErrorSentAt = new Map<string, number>();
	private readonly viewTimers = new Map<UiSurface, ReturnType<typeof setTimeout>>();
	private readonly reportedViews = new Set<string>();
	private featureMarkers: Set<string> | null = null;
	private readonly pendingFeatures = new Set<string>();
	private focused: UiSurface | undefined;
	private segmentStart: number | null = null;
	private lastActivityAt: number;
	private intent: Intent | undefined;
	private rage: { key: unknown; times: number[]; reported: boolean } | undefined;
	private hover: { control: UiControl; element: Element; start: number; clicked: boolean } | undefined;
	private pendingDeadClicks: Array<{ element: Element; surface: UiSurface; x: number; y: number }> = [];
	private dirty = false;
	private previousSessionKey: string | undefined;
	private readonly flushListeners = new Set<FlushListener>();

	constructor(options: UiUsageRuntimeOptions = {}) {
		this.storage = options.storage;
		this.now = options.now ?? Date.now;
		this.capture = options.capture ?? ((event, properties) => captureRendererEvent(event, properties));
		this.optedOut = options.optedOut ?? (() => false);
		this.isWindowActive = options.isWindowActive ?? (() => true);
		this.afterPaint = options.afterPaint ?? ((callback) => setTimeout(callback, 0));
		const now = this.now();
		this.startedAt = now;
		this.lastActivityAt = now;
		this.state = loadState(this.storage, now);
		if (this.isWindowActive()) this.segmentStart = now;
	}

	// --- time accounting -------------------------------------------------

	/** Adds the time since the last checkpoint to every visible and the focused surface. */
	private accrue(now = this.now()): void {
		if (this.segmentStart !== null && !this.optedOut()) {
			const end = Math.min(now, this.lastActivityAt + IDLE_MS);
			const elapsed = Math.max(0, end - this.segmentStart);
			if (elapsed > 0) {
				this.state.activeMs += elapsed;
				for (const [surface, count] of this.visible) {
					if (count > 0) this.bumpSurface(surface, "visible_ms", elapsed);
				}
				if (this.focused) this.bumpSurface(this.focused, "focused_ms", elapsed);
			}
		}
		this.segmentStart = this.isWindowActive() && now - this.lastActivityAt <= IDLE_MS ? now : null;
	}

	activity(now = this.now()): void {
		if (now - this.lastActivityAt > IDLE_MS) this.accrue(now);
		this.lastActivityAt = now;
		if (this.segmentStart === null && this.isWindowActive()) this.segmentStart = now;
	}

	/** The window gained or lost focus or visibility. */
	windowStateChanged(): void {
		const now = this.now();
		this.accrue(now);
		if (this.isWindowActive()) {
			this.lastActivityAt = now;
			this.segmentStart = now;
		} else {
			this.endHover(now);
			this.persist();
		}
	}

	// --- surfaces ----------------------------------------------------------

	surfaceShown(surface: UiSurface, choice = false): SurfaceToken {
		const now = this.now();
		const token: SurfaceToken = { surface, choice, picked: false, shownAt: now, closed: false };
		const count = this.visible.get(surface) ?? 0;
		this.accrue(now);
		this.visible.set(surface, count + 1);
		if (count === 0 && !this.optedOut()) {
			// Menus count every open, since each ends in a pick or an abandon.
			const remount = !choice && now - (this.lastHiddenAt.get(surface) ?? Number.NEGATIVE_INFINITY) <= REOPEN_GRACE_MS;
			if (!remount) this.bumpSurface(surface, "open_count");
			this.scheduleView(surface, this.openedBy(now));
		}
		return token;
	}

	surfaceHidden(token: SurfaceToken): void {
		if (token.closed) return;
		token.closed = true;
		const now = this.now();
		token.closedAt = now;
		const count = this.visible.get(token.surface) ?? 0;
		this.accrue(now);
		if (count <= 1) {
			this.visible.delete(token.surface);
			this.lastHiddenAt.set(token.surface, now);
			const timer = this.viewTimers.get(token.surface);
			if (timer !== undefined) {
				clearTimeout(timer);
				this.viewTimers.delete(token.surface);
			}
		} else {
			this.visible.set(token.surface, count - 1);
		}
		if (token.choice && !this.optedOut()) this.bumpSurface(token.surface, token.picked ? "pick_count" : "abandon_count");
	}

	/** The person chose something in a menu-like surface (a command, a model, a skill). */
	markPick(token: SurfaceToken | undefined): void {
		if (!token || token.picked || !token.choice || this.optedOut()) return;
		token.picked = true;
		// onSelect can run just after the menu reported closing.
		if (token.closed && token.closedAt !== undefined && this.now() - token.closedAt <= LATE_PICK_MS) {
			this.bumpSurface(token.surface, "abandon_count", -1);
			this.bumpSurface(token.surface, "pick_count");
		}
	}

	dismissed(surface: UiSurface): void {
		if (this.optedOut()) return;
		this.bumpSurface(surface, "dismiss_count");
	}

	focusChanged(surface: UiSurface | undefined): void {
		if (surface === this.focused) return;
		this.accrue();
		this.focused = surface;
	}

	private openedBy(now: number): SurfaceOpenedBy {
		const intent = this.recentIntent(now);
		if (!intent) return now - this.startedAt < RESTORE_MS ? "restore" : "auto";
		if (intent.kind === "cmdk" || intent.surface === "cmdk") return "cmdk";
		if (intent.kind === "swipe") return "shortcut";
		return intent.kind;
	}

	private scheduleView(surface: UiSurface, openedBy: SurfaceOpenedBy): void {
		if (this.viewTimers.has(surface)) return;
		this.viewTimers.set(
			surface,
			setTimeout(() => {
				this.viewTimers.delete(surface);
				if ((this.visible.get(surface) ?? 0) === 0) return;
				const feature = SURFACE_FEATURES[surface];
				if (feature && openedBy !== "auto" && openedBy !== "restore") this.featureUsed(feature);
				this.sendDailyView(surface, openedBy);
			}, VIEW_DEBOUNCE_MS),
		);
	}

	private sendDailyView(surface: UiSurface, openedBy: SurfaceOpenedBy): void {
		const slotKey = `${utcDay(this.now())}|${surface}`;
		if (this.reportedViews.has(slotKey) || this.optedOut() || isDeniedEvent("ao.surfaces.viewed")) return;
		if (!reserveDailySlot(this.storage, SURFACE_VIEW_KEY, surface, this.now())) {
			this.reportedViews.add(slotKey);
			return;
		}
		this.reportedViews.add(slotKey);
		void this.capture("ao.surfaces.viewed", { surface, opened_by: openedBy }).then((sent) => {
			if (sent) return;
			this.reportedViews.delete(slotKey);
			releaseDailySlot(this.storage, SURFACE_VIEW_KEY, surface, this.now());
		});
	}

	// --- features ------------------------------------------------------------

	featureUsed(feature: UiFeature): void {
		if (this.optedOut() || isDeniedEvent("ao.features.first_used")) return;
		const markers = this.loadFeatureMarkers();
		if (markers.has(feature) || this.pendingFeatures.has(feature)) return;
		this.pendingFeatures.add(feature);
		void this.capture("ao.features.first_used", { feature }).then((sent) => {
			this.pendingFeatures.delete(feature);
			// No marker while opted out, so nothing is "already used" after an opt-in.
			if (!sent || this.optedOut()) return;
			markers.add(feature);
			try {
				this.storage?.setItem(FEATURE_KEY, JSON.stringify([...markers]));
			} catch {
				// Without the marker the event can repeat once; never break the action.
			}
		});
	}

	private loadFeatureMarkers(): Set<string> {
		if (this.featureMarkers) return this.featureMarkers;
		let markers: string[] = [];
		try {
			const parsed = JSON.parse(this.storage?.getItem(FEATURE_KEY) ?? "[]") as unknown;
			if (Array.isArray(parsed)) markers = parsed.filter((value): value is string => typeof value === "string" && (UI_FEATURES as readonly string[]).includes(value));
		} catch {
			// Start over with no markers.
		}
		this.featureMarkers = new Set(markers);
		return this.featureMarkers;
	}

	// --- input ---------------------------------------------------------------

	private recentIntent(now: number): Intent | undefined {
		return this.intent && now - this.intent.at <= INTENT_MS ? this.intent : undefined;
	}

	noteIntent(kind: Intent["kind"], surface?: UiSurface): void {
		this.intent = { kind, surface, at: this.now() };
	}

	keyDown(event: Pick<KeyboardEvent, "key" | "metaKey" | "ctrlKey" | "altKey" | "target" | "repeat">): void {
		const now = this.now();
		this.activity(now);
		if (this.optedOut() || event.repeat) return;
		const chord = event.metaKey || event.ctrlKey || event.altKey;
		if (chord) {
			if (event.key === "Meta" || event.key === "Control" || event.key === "Alt" || event.key === "Shift") return;
			// Clipboard, undo and select-all inside a text field are editing, not shortcuts.
			if (isEditable(event.target) && /^[acvxyz]$/i.test(event.key)) return;
			this.intent = { kind: "shortcut", surface: surfaceOfElement(event.target), at: now };
			this.bumpCounter("shortcut_count");
			return;
		}
		// Typing is not an intent; only keys that act on a focused control are.
		if (event.key === "Enter" || event.key === " " || event.key === "Escape" || event.key.startsWith("Arrow") || event.key === "Tab") {
			this.intent = { kind: "keyboard", surface: surfaceOfElement(event.target), at: now };
		}
	}

	click(event: Pick<MouseEvent, "target" | "detail" | "clientX" | "clientY">): void {
		const now = this.now();
		this.activity(now);
		if (this.optedOut()) return;
		const target = event.target instanceof Element ? event.target : null;
		if (!target) return;
		const surface = surfaceOfElement(target);
		const controlElement = target.closest(TELEMETRY_ID_SELECTOR);
		const controlId = controlElement?.getAttribute("data-telemetry-id");
		const control = isUiControl(controlId) ? controlId : undefined;
		// A click with detail 0 came from Enter or Space on a focused control.
		const keyboard = event.detail === 0;
		this.intent = { kind: keyboard ? "keyboard" : "click", surface, at: now };
		this.bumpCounter(keyboard ? "keyboard_activation_count" : "click_count");
		if (control) {
			this.bumpControl(control, keyboard ? "keyboard_count" : "click_count");
			if (RETRY_CONTROLS.has(control)) {
				this.bumpCounter("retry_click_count");
				if (surface) this.bumpSurface(surface, "retry_count");
			}
			const feature = CONTROL_FEATURES[control];
			if (feature) this.featureUsed(feature);
			if (this.hover && this.hover.control === control && !this.hover.clicked && now - this.hover.start >= HOVER_DWELL_MS) {
				this.hover.clicked = true;
				this.bumpControl(control, "hover_click_count");
				this.bumpCounter("hover_click_count");
			}
		}
		if (keyboard) return;
		this.trackRage(controlElement ?? target, control, surface, now);
		// detail > 1 is a double or triple click selecting text, never dead.
		if (!surface || control || event.detail !== 1 || target.closest(INTERACTIVE_SELECTOR)) return;
		this.pendingDeadClicks.push({ element: target, surface, x: event.clientX, y: event.clientY });
		if (this.pendingDeadClicks.length === 1) this.afterPaint(() => this.processDeadClicks());
	}

	/** Checks clicks on nothing interactive once their frame has painted. */
	private processDeadClicks(): void {
		const clicks = this.pendingDeadClicks;
		this.pendingDeadClicks = [];
		if (this.optedOut()) return;
		for (const click of clicks) {
			if (!isDeadClick(click.element, click.x, click.y)) continue;
			this.bumpSurface(click.surface, "dead_click_count");
			this.bumpCounter("dead_click_count");
		}
	}

	private trackRage(key: unknown, control: UiControl | undefined, surface: UiSurface | undefined, now: number): void {
		if (!this.rage || this.rage.key !== key || now - (this.rage.times.at(-1) ?? 0) > RAGE_WINDOW_MS) {
			this.rage = { key, times: [now], reported: false };
			return;
		}
		this.rage.times.push(now);
		if (this.rage.times.length > RAGE_CLICKS) this.rage.times.shift();
		if (this.rage.reported || this.rage.times.length < RAGE_CLICKS || now - this.rage.times[0] > RAGE_WINDOW_MS) return;
		this.rage.reported = true;
		this.bumpCounter("rage_click_count");
		if (control) this.bumpControl(control, "rage_click_count");
		if (!surface) return;
		this.bumpSurface(surface, "rage_click_count");
		if (isDeniedEvent("ao.ui.rage_clicked") || !reserveDailySlot(this.storage, RAGE_KEY, surface, now)) return;
		void this.capture("ao.ui.rage_clicked", { surface }).then((sent) => {
			if (!sent) releaseDailySlot(this.storage, RAGE_KEY, surface, this.now());
		});
	}

	pointerOver(target: EventTarget | null): void {
		if (this.hover?.element.contains(target as Node | null)) return;
		const now = this.now();
		this.endHover(now);
		const element = target instanceof Element ? target.closest(TELEMETRY_ID_SELECTOR) : null;
		const id = element?.getAttribute("data-telemetry-id");
		if (element && isUiControl(id) && HOVER_CONTROLS.has(id)) this.hover = { control: id, element, start: now, clicked: false };
	}

	endHover(now = this.now()): void {
		const hover = this.hover;
		this.hover = undefined;
		if (!hover || hover.clicked || now - hover.start < HOVER_DWELL_MS || this.optedOut()) return;
		this.bumpControl(hover.control, "hover_only_count");
		this.bumpCounter("hover_only_count");
	}

	// --- product actions -----------------------------------------------------

	sessionViewed(key: string | undefined): void {
		if (!key) return;
		const previous = this.previousSessionKey;
		this.previousSessionKey = key;
		if (!previous || previous === key || this.optedOut()) return;
		const source = this.switchSource(this.now());
		this.bumpCounter("session_switch_count");
		this.bumpCounter(`switch_from_${source}_count`);
	}

	private switchSource(now: number): SwitchSource {
		const intent = this.recentIntent(now);
		if (!intent) return "other";
		if (intent.kind === "shortcut") return "shortcut";
		if (intent.kind === "swipe") return "swipe";
		if (intent.kind === "cmdk") return "cmdk";
		switch (intent.surface) {
			case "sidebar":
				return "sidebar";
			case "topbar":
				return "tab";
			case "cmdk":
				return "cmdk";
			case "notifications_panel":
				return "notification";
			case "board":
				return "board";
			case "archive_modal":
				return "archive";
			default:
				return "other";
		}
	}

	messageSent(input: SendInput, mode: "send" | "steer" | "queue", withAttachment: boolean): void {
		if (this.optedOut()) return;
		this.bumpCounter("send_count");
		this.bumpCounter(input === "keyboard" ? "send_keyboard_count" : "send_button_count");
		if (input === "keyboard") this.featureUsed("keyboard_send");
		if (mode === "steer") {
			this.bumpCounter("steer_count");
			this.featureUsed("steer");
		}
		if (mode === "queue") {
			this.bumpCounter("queue_count");
			this.featureUsed("queue");
		}
		if (withAttachment) this.bumpCounter("send_with_attachment_count");
	}

	attachmentAdded(source: AttachmentSource): void {
		if (this.optedOut()) return;
		this.bumpCounter(`attachment_add_${source}_count`);
		this.featureUsed("attachment");
	}

	draftDiscarded(): void {
		if (this.optedOut()) return;
		this.bumpCounter("composer_draft_discarded_count");
	}

	modelChanged(): void {
		if (this.optedOut()) return;
		this.bumpCounter("model_picker_change_count");
		this.featureUsed("model_change");
	}

	errorShown(kind: ErrorKind, presentation: ErrorPresentation, surface?: UiSurface, errorCode?: string): void {
		if (this.optedOut()) return;
		const now = this.now();
		const attributed = surface ?? (this.intent && now - this.intent.at <= ERROR_ATTRIBUTION_MS ? this.intent.surface : undefined);
		this.bumpCounter("error_shown_count");
		if (presentation === "toast") this.bumpCounter("error_toast_count");
		if (attributed) this.bumpSurface(attributed, "error_count");
		// A toast or banner that keeps coming back is counted every time but sent once per few minutes.
		const key = `${kind}|${presentation}|${attributed ?? ""}|${errorCode ?? ""}`;
		const lastSent = this.lastErrorSentAt.get(key);
		if (lastSent !== undefined && now - lastSent < ERROR_REPEAT_MS) return;
		this.lastErrorSentAt.set(key, now);
		void this.capture("ao.ui.error_shown", {
			presentation,
			error_kind: kind,
			...(attributed ? { surface: attributed } : {}),
			...(errorCode ? { error_code: errorCode } : {}),
		});
	}

	// --- window --------------------------------------------------------------

	checkpoint(): void {
		if (this.optedOut()) {
			this.discard();
			return;
		}
		this.accrue();
		this.persist();
	}

	/** Drops everything counted so far: used when analytics is turned off. */
	discard(): void {
		const now = this.now();
		this.state = freshState(now);
		this.dirty = false;
		this.pendingDeadClicks = [];
		// Time up to now is not counted later either, even after an opt-in.
		this.segmentStart = this.segmentStart === null ? null : now;
		try {
			this.storage?.removeItem(STATE_KEY);
		} catch {
			// Nothing persisted to remove.
		}
	}

	isFlushOverdue(): boolean {
		return this.now() >= this.state.windowFlushAt;
	}

	flushDelayMs(): number {
		return Math.max(0, this.state.windowFlushAt - this.now());
	}

	onFlushed(listener: FlushListener): () => void {
		this.flushListeners.add(listener);
		return () => this.flushListeners.delete(listener);
	}

	/** The summary this window would send now. */
	summaryProperties(reason: "interval" | "startup" = "interval"): Record<string, unknown> {
		const now = this.now();
		const { state } = this;
		const surfaces = Object.entries(state.surfaces).map(([surface, stats]) => {
			const row: Record<string, unknown> = { surface };
			for (const key of SURFACE_STAT_KEYS) {
				const value = key === "visible_seconds" ? (stats.visible_ms ?? 0) / 1000 : key === "focused_seconds" ? (stats.focused_ms ?? 0) / 1000 : stats[key];
				if (value) row[key] = Math.round(value);
			}
			return row;
		});
		const controls = Object.entries(state.controls).map(([control_id, stats]) => {
			const row: Record<string, unknown> = { control_id };
			for (const key of CONTROL_STAT_KEYS) if (stats[key]) row[key] = stats[key];
			return row;
		});
		const derived: Record<string, number> = {};
		for (const surface of ["cmdk", "model_picker", "slash_menu", "mention_menu", "notifications_panel"] as const) {
			const stats = state.surfaces[surface];
			if (!stats) continue;
			for (const key of ["open", "pick", "abandon"] as const) {
				const value = stats[`${key}_count`];
				if (value) derived[`${surface}_${key}_count`] = value;
			}
		}
		return {
			measurement_schema_version: 1,
			window_id: state.windowId,
			window_duration_seconds: Math.round((now - state.windowStartedAt) / 1000),
			active_seconds: Math.round(state.activeMs / 1000),
			flush_reason: reason,
			...state.counters,
			...derived,
			surfaces,
			controls,
		};
	}

	async flush(reason: "interval" | "startup" = "interval"): Promise<void> {
		const now = this.now();
		if (this.optedOut()) {
			this.discard();
			return;
		}
		this.accrue(now);
		const properties = this.summaryProperties(reason);
		const windowId = this.state.windowId;
		const hasActivity = this.state.activeMs >= 1_000 || Object.keys(this.state.counters).length > 0;
		this.state = freshState(now);
		this.dirty = true;
		this.persist();
		// A window with nothing counted is not sent.
		if (hasActivity) await this.capture(SUMMARY_EVENT, properties);
		for (const listener of this.flushListeners) {
			try {
				listener({ windowId, reason });
			} catch {
				// A listener must not break the next window.
			}
		}
	}

	persist(): void {
		if (!this.dirty || this.optedOut()) return;
		this.dirty = false;
		try {
			this.storage?.setItem(STATE_KEY, JSON.stringify(this.state));
		} catch {
			// Telemetry must never affect the product.
		}
	}

	private surfaceStats(surface: UiSurface): SurfaceStats {
		let stats = this.state.surfaces[surface];
		if (!stats) {
			stats = {};
			this.state.surfaces[surface] = stats;
		}
		return stats;
	}

	private bumpSurface(surface: UiSurface, key: SurfaceStatKey | "visible_ms" | "focused_ms", by = 1): void {
		const stats = this.surfaceStats(surface);
		stats[key] = Math.max(0, (stats[key] ?? 0) + by);
		this.dirty = true;
	}

	private bumpControl(control: UiControl, key: ControlStatKey): void {
		const stats = (this.state.controls[control] ??= {});
		stats[key] = (stats[key] ?? 0) + 1;
		this.dirty = true;
	}

	private bumpCounter(key: DirectCounter): void {
		this.state.counters[key] = (this.state.counters[key] ?? 0) + 1;
		this.dirty = true;
	}
}

function surfaceOfElement(target: EventTarget | null): UiSurface | undefined {
	return surfaceElementOf(target)?.getAttribute(TELEMETRY_SURFACE_ATTRIBUTE) as UiSurface | undefined;
}

function isEditable(target: EventTarget | null): boolean {
	if (!(target instanceof HTMLElement)) return false;
	return target.isContentEditable || target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.tagName === "SELECT";
}

/**
 * Whether a click on a non-interactive element did nothing: no pointer cursor,
 * no text selected by it, and not on the element's own scrollbar. Runs after
 * paint, once per click that already passed the cheap DOM checks.
 */
function isDeadClick(element: Element, x: number, y: number): boolean {
	if (!element.isConnected) return false;
	const selection = typeof window === "undefined" ? null : window.getSelection();
	if (selection && !selection.isCollapsed) return false;
	if (element instanceof HTMLElement) {
		const rect = element.getBoundingClientRect();
		if (x - rect.left > element.clientWidth || y - rect.top > element.clientHeight) return false;
		if (getComputedStyle(element).cursor === "pointer") return false;
	}
	return true;
}

// --- singleton ------------------------------------------------------------------

let runtime: UiUsageRuntime | null = null;

/** Called in the composer and menus; a no-op until telemetry starts. */
export const uiUsage = {
	sessionViewed: (key: string | undefined) => runtime?.sessionViewed(key),
	messageSent: (input: SendInput, mode: "send" | "steer" | "queue", withAttachment: boolean) => runtime?.messageSent(input, mode, withAttachment),
	attachmentAdded: (source: AttachmentSource) => runtime?.attachmentAdded(source),
	draftDiscarded: () => runtime?.draftDiscarded(),
	modelChanged: () => runtime?.modelChanged(),
	featureUsed: (feature: UiFeature) => runtime?.featureUsed(feature),
	errorShown: (kind: ErrorKind, presentation: ErrorPresentation, surface?: UiSurface, errorCode?: string) => runtime?.errorShown(kind, presentation, surface, errorCode),
	dismissed: (surface: UiSurface) => runtime?.dismissed(surface),
	/** A navigation the app started for a gesture or Cmd+K, so the next switch or open is attributed to it. */
	noteIntent: (kind: "cmdk" | "swipe" | "shortcut") => runtime?.noteIntent(kind),
	onWindowFlushed: (listener: FlushListener) => runtime?.onFlushed(listener) ?? (() => undefined),
};

// Surfaces that rendered before telemetry started. They are handed to the
// runtime when it starts, so a layout restored at launch is still timed.
const earlyTokens = new Map<object, { surface: UiSurface; choice: boolean; token?: SurfaceToken }>();

/**
 * Marks a surface as on screen while `visible` is true, for daily views, open
 * counts and visible time. Visibility comes from React state the component
 * already has (its open flag, the active tab), so nothing is observed.
 * `choice` surfaces (menus, pickers) also count each close as a pick or an
 * abandon; call the returned function when the person picks something.
 */
export function useTelemetrySurface(surface: UiSurface, visible = true, options?: { choice?: boolean }): () => void {
	const tokenRef = useRef<SurfaceToken | undefined>(undefined);
	const choice = options?.choice === true;
	useEffect(() => {
		if (!visible) return;
		const key = {};
		if (runtime) {
			const token = runtime.surfaceShown(surface, choice);
			tokenRef.current = token;
			return () => runtime?.surfaceHidden(token);
		}
		earlyTokens.set(key, { surface, choice });
		return () => {
			const early = earlyTokens.get(key);
			earlyTokens.delete(key);
			if (early?.token) runtime?.surfaceHidden(early.token);
		};
	}, [choice, surface, visible]);
	return useCallback(() => runtime?.markPick(tokenRef.current), []);
}

/** Stops counting and drops what was counted when the person turns analytics off. */
export function discardUiUsage(): void {
	runtime?.discard();
}

export function startUiUsageTelemetry(): () => void {
	if (runtime || typeof document === "undefined") return () => undefined;
	if (UI_USAGE_EVENTS.every((event) => isDeniedEvent(event))) return () => undefined;
	const storage = telemetryStorage() as UiUsageRuntimeOptions["storage"];
	const active = () => document.visibilityState === "visible" && document.hasFocus();
	const current = new UiUsageRuntime({
		storage,
		optedOut: isAnalyticsOptedOut,
		isWindowActive: active,
		afterPaint: (callback) => requestAnimationFrame(() => setTimeout(callback, 0)),
	});
	runtime = current;
	for (const early of earlyTokens.values()) early.token = current.surfaceShown(early.surface, early.choice);

	const passiveCapture = { passive: true, capture: true } as const;
	const onClick = (event: MouseEvent) => current.click(event);
	const onKeyDown = (event: KeyboardEvent) => current.keyDown(event);
	const onActivity = () => current.activity();
	const onFocusIn = (event: FocusEvent) => current.focusChanged(surfaceOfElement(event.target));
	const onFocusOut = (event: FocusEvent) => {
		if (!event.relatedTarget) current.focusChanged(undefined);
	};
	// pointerover fires on every element the mouse crosses, so only the latest
	// target is checked, once per animation frame.
	let hoverTarget: EventTarget | null = null;
	let hoverFrame = 0;
	const onPointerOver = (event: PointerEvent) => {
		if (event.pointerType !== "mouse") return;
		hoverTarget = event.target;
		if (hoverFrame) return;
		hoverFrame = requestAnimationFrame(() => {
			hoverFrame = 0;
			current.pointerOver(hoverTarget);
		});
	};
	const onPointerOut = (event: PointerEvent) => {
		if (event.relatedTarget) return;
		cancelAnimationFrame(hoverFrame);
		hoverFrame = 0;
		current.endHover();
	};
	const onWindowState = () => current.windowStateChanged();
	const onPageHide = () => current.checkpoint();
	document.addEventListener("click", onClick, passiveCapture);
	document.addEventListener("keydown", onKeyDown, passiveCapture);
	document.addEventListener("pointerdown", onActivity, passiveCapture);
	document.addEventListener("wheel", onActivity, passiveCapture);
	document.addEventListener("focusin", onFocusIn, passiveCapture);
	document.addEventListener("focusout", onFocusOut, passiveCapture);
	document.addEventListener("pointerover", onPointerOver, passiveCapture);
	document.addEventListener("pointerout", onPointerOut, passiveCapture);
	document.addEventListener("visibilitychange", onWindowState);
	window.addEventListener("focus", onWindowState);
	window.addEventListener("blur", onWindowState);
	window.addEventListener("pagehide", onPageHide);
	const checkpoint = window.setInterval(() => current.checkpoint(), CHECKPOINT_MS);
	let flushTimer = 0;
	const scheduleFlush = () => {
		flushTimer = window.setTimeout(() => {
			void current.flush().finally(scheduleFlush);
		}, current.flushDelayMs());
	};
	if (current.isFlushOverdue()) void current.flush("startup").finally(scheduleFlush);
	else scheduleFlush();

	if (import.meta.env.DEV) {
		// Dev only: inspect or send the current window without waiting 45 minutes.
		(window as unknown as Record<string, unknown>).__uiUsage = {
			snapshot: () => current.summaryProperties(),
			flush: () => current.flush(),
		};
	}

	return () => {
		window.clearInterval(checkpoint);
		window.clearTimeout(flushTimer);
		cancelAnimationFrame(hoverFrame);
		document.removeEventListener("click", onClick, passiveCapture);
		document.removeEventListener("keydown", onKeyDown, passiveCapture);
		document.removeEventListener("pointerdown", onActivity, passiveCapture);
		document.removeEventListener("wheel", onActivity, passiveCapture);
		document.removeEventListener("focusin", onFocusIn, passiveCapture);
		document.removeEventListener("focusout", onFocusOut, passiveCapture);
		document.removeEventListener("pointerover", onPointerOver, passiveCapture);
		document.removeEventListener("pointerout", onPointerOut, passiveCapture);
		document.removeEventListener("visibilitychange", onWindowState);
		window.removeEventListener("focus", onWindowState);
		window.removeEventListener("blur", onWindowState);
		window.removeEventListener("pagehide", onPageHide);
		current.checkpoint();
		runtime = null;
	};
}
