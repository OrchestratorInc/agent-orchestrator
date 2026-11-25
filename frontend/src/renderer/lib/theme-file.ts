import type { ThemeDefinition, ThemePalette } from "./theme-palettes";

/**
 * Theme files: one JSON document per custom theme, the thing people share.
 *
 *   { "version": 1, "name": "Sumi", "appearance": "dark",
 *     "colors": { "canvas": "#101012", "sidebar": "#3f0e40", ... },
 *     "variants": { "light": { "canvas": "#fafafa", ... } },
 *     "css": "..." }
 *
 * `colors` paints the theme's own appearance and `variants` the other one.
 * Every role is optional: an unset role follows the roles that are set, and
 * then Orchestrator's default look. The daemon validates and stores files
 * (service/apptheme); this module names the roles for the renderer.
 */
export const THEME_FILE_VERSION = 1;

export type ThemeAppearance = "dark" | "light";

export const THEME_COLOR_ROLES = [
	// Surfaces
	"canvas",
	"chrome",
	"toolbar",
	"toolbarForeground",
	"toolbarBorder",
	"toolbarControl",
	"toolbarControlForeground",
	"toolbarControlHover",
	"surface",
	"surfaceRaised",
	"surfaceOverlay",
	// Text and lines
	"text",
	"textMuted",
	"border",
	"input",
	"focus",
	// Fills
	"accent",
	"accentForeground",
	"secondary",
	"secondaryForeground",
	"muted",
	"mutedForeground",
	"placeholder",
	"secondaryLabel",
	"iconMuted",
	// Status
	"error",
	"errorForeground",
	"errorSurface",
	"warning",
	"warningForeground",
	"warningSurface",
	"update",
	"updateForeground",
	"updateSurface",
	"accentSurface",
	"accentSurfaceForeground",
	// Chat
	"messageSurface",
	"messageForeground",
	"messageAction",
	"messageActionForeground",
	"messageActionHover",
	"codeBackground",
	"codeForeground",
	"searchMatchBackground",
	"searchMatchForeground",
	"searchMatchActiveBackground",
	"searchMatchActiveForeground",
	// Sidebar
	"sidebar",
	"sidebarForeground",
	"sidebarMutedForeground",
	"sidebarControlSurface",
	"sidebarRowHover",
	"sidebarRowActive",
	"sidebarRowSelected",
	"sidebarBorder",
	// Terminal
	"terminalBackground",
	"terminalForeground",
	"terminalCursor",
	"terminalSelection",
	"terminalScrollbar",
	"terminalScrollbarHover",
	// Links, and the hues behind status and terminal colors.
	"link",
	"red",
	"orange",
	"yellow",
	"green",
	"blue",
	"purple",
	"cyan",
] as const;

export type ThemeColorRole = (typeof THEME_COLOR_ROLES)[number];
export type ThemeColors = Partial<Record<ThemeColorRole, string>>;

/**
 * Roles a file may carry that this app keeps (so a shared file exports as it
 * came in) but has no surface for. custom-theme.test.ts fails
 * if the resolver starts or stops painting one. The daemon names them when a
 * theme is saved: keep in step with UnpaintedRoles in
 * backend/internal/service/apptheme/apptheme.go.
 */
export const UNPAINTED_THEME_ROLES: readonly ThemeColorRole[] = [
	"toolbar",
	"toolbarForeground",
	"toolbarBorder",
	"toolbarControl",
	"toolbarControlForeground",
	"toolbarControlHover",
	"secondaryLabel",
	"iconMuted",
	"errorForeground",
	"errorSurface",
	"warningForeground",
	"warningSurface",
	"update",
	"updateForeground",
	"updateSurface",
	"accentSurface",
	"accentSurfaceForeground",
	"messageAction",
	"messageActionForeground",
	"messageActionHover",
	"searchMatchBackground",
	"searchMatchForeground",
	"searchMatchActiveBackground",
	"searchMatchActiveForeground",
	"terminalScrollbar",
	"terminalScrollbarHover",
];

const ROLE_SET: ReadonlySet<string> = new Set(THEME_COLOR_ROLES);

export function isThemeColorRole(name: string): name is ThemeColorRole {
	return ROLE_SET.has(name);
}

/** A theme as the daemon stores it and as a file carries it. */
export type ThemeFile = {
	version: typeof THEME_FILE_VERSION;
	id: string;
	name: string;
	appearance: ThemeAppearance;
	colors: Record<string, string>;
	variants?: Partial<Record<ThemeAppearance, Record<string, string>>>;
	css?: string;
};

type ThemeFileSource = {
	id: string;
	name: string;
	appearance: string;
	colors?: Record<string, string> | null;
	variants?: Record<string, Record<string, string>> | null;
	css?: string | null;
};

/** The file for a theme, ready to copy or save: stable key order, no empty parts. */
export function serializeThemeFile(theme: ThemeFileSource): string {
	const appearance: ThemeAppearance = theme.appearance === "light" ? "light" : "dark";
	const other: ThemeAppearance = appearance === "light" ? "dark" : "light";
	const variant = theme.variants?.[other];
	const file: ThemeFile = {
		version: THEME_FILE_VERSION,
		id: theme.id,
		name: theme.name,
		appearance,
		colors: { ...(theme.colors ?? {}) },
	};
	if (variant && Object.keys(variant).length > 0) file.variants = other === "light" ? { light: { ...variant } } : { dark: { ...variant } };
	if (theme.css) file.css = theme.css;
	return `${JSON.stringify(file, null, 2)}\n`;
}

function paletteColors(palette: ThemePalette): Record<string, string> {
	return {
		canvas: palette.bg,
		sidebar: palette.sidebar,
		surface: palette.panel,
		secondary: palette.element,
		text: palette.text,
		textMuted: palette.muted,
		accent: palette.primary ?? palette.accent,
		link: palette.accent,
		red: palette.red,
		orange: palette.orange,
		yellow: palette.yellow,
		green: palette.green,
		blue: palette.blue,
		purple: palette.purple,
		cyan: palette.cyan,
	};
}

/** A built-in theme as a file, so it can be exported and used as a starting point. */
export function builtInThemeFile(theme: ThemeDefinition): ThemeFileSource {
	return {
		id: `${theme.id}-copy`,
		name: `${theme.label} copy`,
		appearance: "dark",
		colors: paletteColors(theme.dark),
		variants: { light: paletteColors(theme.light) },
	};
}
