/**
 * The closed list of UI surfaces product telemetry can name.
 *
 * A surface's DOM root carries `data-telemetry-surface="<surface>"`, so a
 * delegated listener can attribute a click, focus or slow interaction to the
 * nearest surface without a DOM path or coordinates. Both the usage summary and
 * the performance summary read this list; the sanitizer drops any other value.
 */
export const UI_SURFACES = [
	"sidebar",
	"topbar",
	"chat",
	"composer",
	"new_chat",
	"board",
	"pr_tab",
	"inspector_summary",
	"inspector_reviews",
	"inspector_browser",
	"inspector_files",
	"inspector_artifacts",
	"inspector_side_chat",
	"inspector_subagents",
	"inspector_background_tasks",
	"cmdk",
	"notifications_panel",
	"archive_modal",
	"model_picker",
	"slash_menu",
	"mention_menu",
	"split_view",
	"terminal",
	"onboarding",
	"toast",
	"settings_general",
	"settings_agents",
	"settings_teams",
	"settings_billing",
	"settings_browser",
	"settings_computer_use",
	"settings_coder",
	"settings_remote_hosts",
	"settings_mobile",
	"settings_shortcuts",
	"settings_diagnostics",
	"settings_updates",
	"settings_help",
	"settings_project",
] as const;

export type UiSurface = (typeof UI_SURFACES)[number];

const UI_SURFACE_SET = new Set<string>(UI_SURFACES);

export function isUiSurface(value: unknown): value is UiSurface {
	return typeof value === "string" && UI_SURFACE_SET.has(value);
}

export const TELEMETRY_SURFACE_ATTRIBUTE = "data-telemetry-surface";
const SURFACE_SELECTOR = `[${TELEMETRY_SURFACE_ATTRIBUTE}]`;

/**
 * The innermost surface an element sits in, or undefined. A DOM walk only
 * (Element.closest), never a layout read, so it is safe in event handlers.
 */
export function surfaceElementOf(target: EventTarget | null): HTMLElement | undefined {
	const element = target instanceof Element ? target : target instanceof Node ? target.parentElement : null;
	const found = element?.closest<HTMLElement>(SURFACE_SELECTOR);
	return found && isUiSurface(found.getAttribute(TELEMETRY_SURFACE_ATTRIBUTE)) ? found : undefined;
}

export function surfaceOf(target: EventTarget | null): UiSurface | undefined {
	return surfaceElementOf(target)?.getAttribute(TELEMETRY_SURFACE_ATTRIBUTE) as UiSurface | undefined;
}

/** The surface a global settings page reports as. Unknown pages are `settings_general`. */
export function settingsSurface(section: string): UiSurface {
	switch (section) {
		case "harness":
			return "settings_agents";
		case "teams":
			return "settings_teams";
		case "billing":
			return "settings_billing";
		case "browserProfiles":
			return "settings_browser";
		case "computerUse":
			return "settings_computer_use";
		case "coder":
			return "settings_coder";
		case "remoteHosts":
			return "settings_remote_hosts";
		case "mobile":
			return "settings_mobile";
		case "shortcuts":
			return "settings_shortcuts";
		case "diagnostics":
			return "settings_diagnostics";
		case "updates":
			return "settings_updates";
		case "help":
			return "settings_help";
		default:
			return "settings_general";
	}
}
