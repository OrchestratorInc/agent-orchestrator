import { APP_SHORTCUTS, type AppShortcutId } from "../../shared/shortcuts";
import { isUiSurface, UI_SURFACES, type UiSurface } from "./ui-surfaces";

// Closed vocabularies for UI usage telemetry, imported by the call sites and by
// the sanitizer so the two cannot drift. Only these ids, counts and durations
// leave the machine: never text, a path, a URL, a name or a DOM path.

/**
 * Every control the usage summary counts. A control's DOM element carries
 * `data-telemetry-id="<id>"`; a click on it (or inside it) is counted against
 * the id. Ids are stable product names, not DOM paths: renaming a component
 * keeps its id. An id that is not listed here is ignored.
 */
export const UI_CONTROLS = [
	// Sidebar
	"sidebar.home",
	"sidebar.collapse",
	"sidebar.new_project",
	"sidebar.settings",
	"sidebar.connect_mobile",
	"sidebar.project_row",
	"sidebar.project_toggle",
	"sidebar.project_new_session",
	"sidebar.project_orchestrator",
	"sidebar.project_menu",
	"sidebar.project_unpin",
	"sidebar.session_row",
	"sidebar.session_pin",
	"sidebar.session_archive",
	"sidebar.archived_sessions",
	// Top bar and tabs
	"tab.select",
	"tab.close",
	"tab.new",
	"topbar.notifications",
	"topbar.inspector",
	"topbar.new_task",
	"topbar.open_board",
	"topbar.open_orchestrator",
	"topbar.open_editor",
	// Composer
	"composer.send",
	"composer.stop",
	"composer.attach",
	"composer.model_picker",
	"composer.dictation",
	// Chat transcript
	"chat.retry",
	// Inspector tabs
	"inspector.tab_summary",
	"inspector.tab_reviews",
	"inspector.tab_browser",
	"inspector.tab_files",
	"inspector.tab_artifacts",
	"inspector.tab_side_chat",
	"inspector.tab_subagents",
	"inspector.tab_background_tasks",
	// Board
	"board.card",
	// Toasts and banners
	"toast.action",
	"toast.dismiss",
	"daemon_banner.retry",
] as const;

export type UiControl = (typeof UI_CONTROLS)[number];

const UI_CONTROL_SET = new Set<string>(UI_CONTROLS);

export function isUiControl(value: unknown): value is UiControl {
	return typeof value === "string" && UI_CONTROL_SET.has(value);
}

/** Controls whose click is a retry after a failure. */
export const RETRY_CONTROLS: ReadonlySet<UiControl> = new Set<UiControl>([
	"chat.retry",
	"daemon_banner.retry",
]);

/** Controls with a hover affordance (hover card, preview), so hover→click and hover-only are counted. */
export const HOVER_CONTROLS: ReadonlySet<UiControl> = new Set<UiControl>(["sidebar.session_row", "tab.select", "board.card"]);

/** Features whose first successful use on an install is sent once (`ao.features.first_used`). */
export const UI_FEATURES = [
	"cmdk",
	"slash_menu",
	"mention_menu",
	"model_change",
	"attachment",
	"steer",
	"queue",
	"keyboard_send",
	"split_pane",
	"terminal_tab",
	"side_chat",
	"board",
	"pin_session",
	"archive_modal",
	"dictation",
	"swipe_navigation",
	"notifications_panel",
	"inspector_summary",
	"inspector_reviews",
	"inspector_browser",
	"inspector_files",
	"inspector_artifacts",
	"inspector_subagents",
	"inspector_background_tasks",
] as const;

export type UiFeature = (typeof UI_FEATURES)[number];

/** A control whose first click counts as the first use of a feature. */
export const CONTROL_FEATURES: Partial<Record<UiControl, UiFeature>> = {
	"sidebar.session_pin": "pin_session",
	"composer.dictation": "dictation",
};

/**
 * A surface whose first deliberate open (not restored at launch, not opened by
 * the app) counts as the first use of a feature.
 */
export const SURFACE_FEATURES: Partial<Record<UiSurface, UiFeature>> = {
	cmdk: "cmdk",
	slash_menu: "slash_menu",
	mention_menu: "mention_menu",
	split_view: "split_pane",
	terminal: "terminal_tab",
	inspector_side_chat: "side_chat",
	board: "board",
	archive_modal: "archive_modal",
	notifications_panel: "notifications_panel",
	inspector_summary: "inspector_summary",
	inspector_reviews: "inspector_reviews",
	inspector_browser: "inspector_browser",
	inspector_files: "inspector_files",
	inspector_artifacts: "inspector_artifacts",
	inspector_subagents: "inspector_subagents",
	inspector_background_tasks: "inspector_background_tasks",
};

/** How a surface came into view. */
export const SURFACE_OPENED_BY = ["click", "keyboard", "shortcut", "cmdk", "auto", "restore"] as const;
export type SurfaceOpenedBy = (typeof SURFACE_OPENED_BY)[number];

/** Where a session switch started. */
export const SWITCH_SOURCES = ["sidebar", "tab", "cmdk", "shortcut", "notification", "board", "swipe", "archive", "other"] as const;
export type SwitchSource = (typeof SWITCH_SOURCES)[number];

/** How a user-visible error was presented. */
export const ERROR_PRESENTATIONS = ["toast", "banner", "inline", "dialog"] as const;
export type ErrorPresentation = (typeof ERROR_PRESENTATIONS)[number];

/** Which kind of user-visible error rendered. */
export const ERROR_KINDS = [
	"toast",
	"daemon_unavailable",
	"chat_turn_failed",
	"composer_send_failed",
	"other",
] as const;
export type ErrorKind = (typeof ERROR_KINDS)[number];

/**
 * The only error codes `ao.ui.error_shown` may carry: the daemon's failure
 * codes and a chat turn's known provider failures, upper-cased. Anything else
 * is dropped, however it is spelled.
 */
export const ERROR_CODES = [
	"NOT_CONFIGURED",
	"DAEMON_UNREACHABLE",
	"BINARY_MISSING",
	"SPAWN_FAILED",
	"EXITED",
	"PORT_UNCONFIRMED",
	"NOT_READY",
	"IDENTITY_MISMATCH",
	"DATADIR_UNWRITABLE",
	"AUTH_REQUIRED",
	"PROVIDER_KEY_MISSING",
	"USAGE_LIMIT",
] as const;
export type ErrorCode = (typeof ERROR_CODES)[number];

/** Whether a value is one of the error codes `ao.ui.error_shown` may send. */
export function isErrorCode(value: unknown): value is ErrorCode {
	return typeof value === "string" && (ERROR_CODES as readonly string[]).includes(value);
}

/** Where an attachment came from. */
export const ATTACHMENT_SOURCES = ["paste", "drop", "picker"] as const;
export type AttachmentSource = (typeof ATTACHMENT_SOURCES)[number];

/** How a message left the composer. */
export const SEND_INPUTS = ["keyboard", "button"] as const;
export type SendInput = (typeof SEND_INPUTS)[number];

/** Every app shortcut, by its id in the keyboard shortcuts list. */
export const SHORTCUT_IDS: readonly AppShortcutId[] = APP_SHORTCUTS.map((shortcut) => shortcut.id);

const SHORTCUT_ID_SET = new Set<string>(SHORTCUT_IDS);

export function isShortcutId(value: unknown): value is AppShortcutId {
	return typeof value === "string" && SHORTCUT_ID_SET.has(value);
}

/** How a shortcut's action ran: its key chord, or picked from the command palette. */
export const SHORTCUT_VIAS = ["key", "cmdk"] as const;
export type ShortcutVia = (typeof SHORTCUT_VIAS)[number];

/** Numeric keys of one entry in the summary's `shortcuts` array. */
export const SHORTCUT_STAT_KEYS = ["key_count", "cmdk_count"] as const;
export type ShortcutStatKey = (typeof SHORTCUT_STAT_KEYS)[number];

/**
 * The control a shortcut does the same thing as. Its key chord is counted on
 * that control too, as `shortcut_count` beside `click_count`.
 */
export const SHORTCUT_CONTROLS: Partial<Record<AppShortcutId, UiControl>> = {
	"new-session": "topbar.new_task",
	"open-settings": "sidebar.settings",
	"toggle-sidebar": "sidebar.collapse",
	"toggle-inspector": "topbar.inspector",
	"open-orchestrator": "topbar.open_orchestrator",
	"open-in-editor": "topbar.open_editor",
	"archive-session": "sidebar.session_archive",
	"model-picker": "composer.model_picker",
	"dictate": "composer.dictation",
	"close-shell-terminal": "tab.close",
	"previous-session": "sidebar.session_row",
	"next-session": "sidebar.session_row",
	"jump-to-session": "sidebar.session_row",
	"previous-attention-session": "sidebar.session_row",
	"next-attention-session": "sidebar.session_row",
	"previous-tab": "tab.select",
	"next-tab": "tab.select",
};

/** Top-level numeric keys of `ao.ui.usage_summary`. A key left out means 0. */
export const USAGE_SUMMARY_COUNTERS = [
	"session_switch_count",
	...SWITCH_SOURCES.map((source) => `switch_from_${source}_count` as const),
	"send_count",
	"send_keyboard_count",
	"send_button_count",
	"steer_count",
	"queue_count",
	"send_with_attachment_count",
	"attachment_add_paste_count",
	"attachment_add_drop_count",
	"attachment_add_picker_count",
	"composer_draft_discarded_count",
	"model_picker_change_count",
	"cmdk_open_count",
	"cmdk_pick_count",
	"cmdk_abandon_count",
	"model_picker_open_count",
	"model_picker_abandon_count",
	"slash_menu_open_count",
	"slash_menu_pick_count",
	"slash_menu_abandon_count",
	"mention_menu_open_count",
	"mention_menu_pick_count",
	"mention_menu_abandon_count",
	"notifications_panel_open_count",
	"click_count",
	"keyboard_activation_count",
	"shortcut_count",
	"hover_click_count",
	"hover_only_count",
	"rage_click_count",
	"dead_click_count",
	"error_shown_count",
	"error_toast_count",
	"retry_click_count",
] as const;

export type UsageSummaryCounter = (typeof USAGE_SUMMARY_COUNTERS)[number];

/** The counters the runtime increments directly; the rest are derived from per-surface stats at flush. */
export type DirectCounter = Exclude<
	UsageSummaryCounter,
	| `${"cmdk" | "model_picker" | "slash_menu" | "mention_menu" | "notifications_panel"}_${"open" | "pick" | "abandon"}_count`
>;

/** Numeric keys of one entry in the summary's `surfaces` array. */
export const SURFACE_STAT_KEYS = [
	"visible_seconds",
	"focused_seconds",
	"open_count",
	"pick_count",
	"abandon_count",
	"dismiss_count",
	"dead_click_count",
	"rage_click_count",
	"error_count",
	"retry_count",
] as const;
export type SurfaceStatKey = (typeof SURFACE_STAT_KEYS)[number];

/** Numeric keys of one entry in the summary's `controls` array. */
export const CONTROL_STAT_KEYS = [
	"click_count",
	"keyboard_count",
	"shortcut_count",
	"hover_click_count",
	"hover_only_count",
	"rage_click_count",
] as const;
export type ControlStatKey = (typeof CONTROL_STAT_KEYS)[number];

type Properties = Record<string, unknown>;

function count(value: unknown, max = 1_000_000): number | undefined {
	return typeof value === "number" && Number.isFinite(value) && value >= 0 ? Math.min(max, Math.round(value)) : undefined;
}

function oneOf<T extends string>(value: unknown, allowed: readonly T[]): T | undefined {
	return typeof value === "string" && (allowed as readonly string[]).includes(value) ? (value as T) : undefined;
}

function sanitizeSurfaceStats(value: unknown): Properties[] | undefined {
	if (!Array.isArray(value)) return undefined;
	const seen = new Set<string>();
	const rows: Properties[] = [];
	for (const entry of value.slice(0, UI_SURFACES.length)) {
		if (!entry || typeof entry !== "object") continue;
		const raw = entry as Properties;
		if (!isUiSurface(raw.surface) || seen.has(raw.surface)) continue;
		seen.add(raw.surface);
		const row: Properties = { surface: raw.surface };
		for (const key of SURFACE_STAT_KEYS) {
			const n = count(raw[key]);
			if (n) row[key] = n;
		}
		if (Object.keys(row).length > 1) rows.push(row);
	}
	return rows.length ? rows : undefined;
}

function sanitizeControlStats(value: unknown): Properties[] | undefined {
	if (!Array.isArray(value)) return undefined;
	const seen = new Set<string>();
	const rows: Properties[] = [];
	for (const entry of value.slice(0, UI_CONTROLS.length)) {
		if (!entry || typeof entry !== "object") continue;
		const raw = entry as Properties;
		if (!isUiControl(raw.control_id) || seen.has(raw.control_id)) continue;
		seen.add(raw.control_id);
		const row: Properties = { control_id: raw.control_id };
		for (const key of CONTROL_STAT_KEYS) {
			const n = count(raw[key]);
			if (n) row[key] = n;
		}
		if (Object.keys(row).length > 1) rows.push(row);
	}
	return rows.length ? rows : undefined;
}

function sanitizeShortcutStats(value: unknown): Properties[] | undefined {
	if (!Array.isArray(value)) return undefined;
	const seen = new Set<string>();
	const rows: Properties[] = [];
	for (const entry of value.slice(0, SHORTCUT_IDS.length)) {
		if (!entry || typeof entry !== "object") continue;
		const raw = entry as Properties;
		if (!isShortcutId(raw.shortcut_id) || seen.has(raw.shortcut_id)) continue;
		seen.add(raw.shortcut_id);
		const row: Properties = { shortcut_id: raw.shortcut_id };
		for (const key of SHORTCUT_STAT_KEYS) {
			const n = count(raw[key]);
			if (n) row[key] = n;
		}
		if (Object.keys(row).length > 1) rows.push(row);
	}
	return rows.length ? rows : undefined;
}

export const UI_USAGE_EVENTS = [
	"ao.surfaces.viewed",
	"ao.features.first_used",
	"ao.ui.usage_summary",
	"ao.ui.error_shown",
	"ao.ui.rage_clicked",
] as const;

const UI_USAGE_EVENT_SET = new Set<string>(UI_USAGE_EVENTS);

export function isUiUsageEvent(event: string): boolean {
	return UI_USAGE_EVENT_SET.has(event);
}

/**
 * Per-event allowlist for the UI usage events: closed enums, whole-number
 * counts and a window uuid. Everything else is dropped.
 */
export function sanitizeUiUsageProperties(event: string, properties?: Properties): Properties {
	const safe: Properties = {};
	const set = (key: string, value: unknown) => {
		if (value !== undefined) safe[key] = value;
	};
	switch (event) {
		case "ao.surfaces.viewed":
			set("surface", isUiSurface(properties?.surface) ? properties.surface : undefined);
			set("opened_by", oneOf(properties?.opened_by, SURFACE_OPENED_BY));
			break;
		case "ao.features.first_used":
			set("feature", oneOf(properties?.feature, UI_FEATURES));
			break;
		case "ao.ui.rage_clicked":
			set("surface", isUiSurface(properties?.surface) ? properties.surface : undefined);
			break;
		case "ao.ui.error_shown": {
			set("surface", isUiSurface(properties?.surface) ? properties.surface : undefined);
			set("presentation", oneOf(properties?.presentation, ERROR_PRESENTATIONS));
			set("error_kind", oneOf(properties?.error_kind, ERROR_KINDS));
			// A known daemon or provider code, never a message.
			set("error_code", isErrorCode(properties?.error_code) ? properties.error_code : undefined);
			break;
		}
		case "ao.ui.usage_summary": {
			for (const key of ["measurement_schema_version", "window_duration_seconds", "active_seconds", ...USAGE_SUMMARY_COUNTERS] as const) {
				const n = count(properties?.[key]);
				if (n !== undefined && (n > 0 || key === "measurement_schema_version" || key === "window_duration_seconds" || key === "active_seconds")) safe[key] = n;
			}
			if (typeof properties?.window_id === "string" && /^[0-9a-f-]{36}$/i.test(properties.window_id)) safe.window_id = properties.window_id;
			if (properties?.flush_reason === "interval" || properties?.flush_reason === "startup") safe.flush_reason = properties.flush_reason;
			set("surfaces", sanitizeSurfaceStats(properties?.surfaces));
			set("controls", sanitizeControlStats(properties?.controls));
			set("shortcuts", sanitizeShortcutStats(properties?.shortcuts));
			break;
		}
	}
	return safe;
}
