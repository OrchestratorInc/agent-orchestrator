import { contrastRatio } from "../../../packages/shared/theme/color";
import type { ThemeDefinition, ThemePalette } from "../../../packages/shared/theme/palettes";

export const CUSTOM_THEMES_LIST_CHANNEL = "themes:list";
export const CUSTOM_THEMES_CHANGED_CHANNEL = "themes:changed";
export const CUSTOM_THEMES_OPEN_FOLDER_CHANNEL = "themes:open-folder";

/** Largest theme file read; a palette is under 1 KB. */
export const CUSTOM_THEME_MAX_BYTES = 64 * 1024;

export type CustomThemeId = `custom:${string}`;
export type ThemeMode = "dark" | "light";

const HUE_ROLES = ["accent", "red", "orange", "yellow", "green", "blue", "purple", "cyan"] as const;
const SURFACE_ROLES = ["bg", "panel", "sidebar"] as const;

export type CustomThemeProblem =
	| { code: "read" | "fileName" | "duplicate" | "json" | "name" }
	| { code: "color"; mode: ThemeMode; role: keyof ThemePalette };

export type CustomTheme = ThemeDefinition & {
	id: CustomThemeId;
	file: string;
	/** Roles too faint to read in each mode, e.g. `dark.muted`; the theme still loads. */
	lowContrast: string[];
};

export type CustomThemeError = { file: string; problem: CustomThemeProblem };

export type CustomThemeList = {
	directory: string;
	themes: CustomTheme[];
	errors: CustomThemeError[];
};

export function isCustomThemeId(value: unknown): value is CustomThemeId {
	return typeof value === "string" && /^custom:[a-z0-9][a-z0-9_-]*$/.test(value);
}

/**
 * Roles that miss the contrast floor the built-in palettes are held to: text
 * 7:1 on every surface, muted text 4.5:1 and each hue 3:1 on the canvas.
 */
export function lowContrastRoles(palette: ThemePalette): (keyof ThemePalette)[] {
	const failing: (keyof ThemePalette)[] = [];
	if (SURFACE_ROLES.some((surface) => contrastRatio(palette.text, palette[surface]) < 7)) failing.push("text");
	if (contrastRatio(palette.muted, palette.bg) < 4.5) failing.push("muted");
	for (const role of HUE_ROLES) if (contrastRatio(palette[role], palette.bg) < 3) failing.push(role);
	return failing;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

// Values are written into a stylesheet, so only plain hex gets through.
function isHexColor(value: unknown): value is string {
	return typeof value === "string" && /^#(?:[0-9a-f]{3}|[0-9a-f]{6})$/i.test(value);
}

function parsePalette(value: unknown, mode: ThemeMode): ThemePalette | CustomThemeProblem {
	const source = isRecord(value) ? value : {};
	let invalid: keyof ThemePalette | undefined;
	const color = (role: keyof ThemePalette): string => {
		const candidate = source[role];
		if (isHexColor(candidate)) return candidate;
		invalid ??= role;
		return "";
	};
	const palette: ThemePalette = {
		bg: color("bg"), sidebar: color("sidebar"), panel: color("panel"), element: color("element"),
		text: color("text"), muted: color("muted"), accent: color("accent"),
		red: color("red"), orange: color("orange"), yellow: color("yellow"), green: color("green"),
		blue: color("blue"), purple: color("purple"), cyan: color("cyan"),
	};
	if (source.primary !== undefined) palette.primary = color("primary");
	return invalid ? { code: "color", mode, role: invalid } : palette;
}

/** Parse one `~/.ao/themes/<file>.json`. The file name, lowercased, is the theme's id. */
export function parseCustomTheme(file: string, text: string): CustomTheme | CustomThemeError {
	const id = `custom:${file.replace(/\.json$/i, "").toLowerCase()}`;
	if (!isCustomThemeId(id)) return { file, problem: { code: "fileName" } };
	let parsed: unknown;
	try {
		parsed = JSON.parse(text);
	} catch {
		return { file, problem: { code: "json" } };
	}
	if (!isRecord(parsed)) return { file, problem: { code: "json" } };
	const label = typeof parsed.name === "string" ? parsed.name.trim() : "";
	if (!label || label.length > 60) return { file, problem: { code: "name" } };
	const dark = parsePalette(parsed.dark, "dark");
	if ("code" in dark) return { file, problem: dark };
	const light = parsePalette(parsed.light, "light");
	if ("code" in light) return { file, problem: light };
	const lowContrast = [
		...lowContrastRoles(dark).map((role) => `dark.${role}`),
		...lowContrastRoles(light).map((role) => `light.${role}`),
	];
	return { id, label, file, dark, light, lowContrast };
}
