import type { components } from "../../api/schema";
import { contrastRatio, lightness, mixColor, withAlpha } from "./theme-color";
import { cssColorToHex } from "./theme-css-color";
import { themeCssLoadsFile } from "./theme-css-guard";
import { isThemeColorRole, type ThemeAppearance, type ThemeColorRole } from "./theme-file";
import { DEFAULT_THEME_STYLE, getThemeDefinition, type ThemePalette } from "./theme-palettes";
import { textOn } from "./theme-tokens";

/**
 * Custom themes: theme files (theme-file.ts) the user imports or their agents
 * make (`ao theme`), listed in the theme picker after the built-in ones. The
 * daemon owns them (GET /api/v1/settings/themes); this module turns the one on
 * screen into a stylesheet. A custom theme's style id is `custom:<id>`, so it
 * lives in the same preference as the built-in themes.
 */
export type CustomTheme = components["schemas"]["CustomThemeResponse"];
export type CustomThemeSwitchRequest = components["schemas"]["ThemeSwitchResponse"];
export type CustomThemeStyle = `custom:${string}`;

const CUSTOM_PREFIX = "custom:";
const CUSTOM_STYLE = /^custom:[a-z0-9][a-z0-9-]{0,47}$/;

/** Last custom theme stylesheet shown, so a restart paints it before the daemon answers. */
export const customThemeCssStorageKey = "ao.custom-theme-css";
/** Last switch request this app applied. */
export const customThemeSwitchStorageKey = "ao.custom-theme-switch";
// An embedded font or image can make a theme a few MB. Past this, skip the
// startup copy rather than crowd the other settings out of localStorage; such
// a theme appears once the daemon answers.
const MAX_CACHED_CSS_CHARS = 1_000_000;

export function isCustomThemeStyle(value: unknown): value is CustomThemeStyle {
	return typeof value === "string" && CUSTOM_STYLE.test(value);
}

export function customThemeStyle(id: string): CustomThemeStyle {
	return `${CUSTOM_PREFIX}${id}`;
}

export function customThemeId(style: CustomThemeStyle): string {
	return style.slice(CUSTOM_PREFIX.length);
}

type ThemeSource = Pick<CustomTheme, "appearance" | "colors" | "variants">;
type Roles = Partial<Record<ThemeColorRole, string>>;

/** The appearance a theme's `colors` paint; `variants` paint the other. */
export function customThemeOwnAppearance(theme: Pick<CustomTheme, "appearance">): ThemeAppearance {
	return theme.appearance === "light" ? "light" : "dark";
}

/**
 * The colors a theme sets for one appearance, as hex: `colors` for its own
 * appearance, `variants` for the other. Null when it sets none there, which
 * leaves that appearance on Orchestrator's default look.
 */
export function customThemeColors(theme: ThemeSource, mode: ThemeAppearance): Roles | null {
	const source = mode === customThemeOwnAppearance(theme) ? theme.colors : theme.variants?.[mode];
	const roles: Roles = {};
	for (const [name, value] of Object.entries(source ?? {})) {
		if (!isThemeColorRole(name)) continue;
		const hex = cssColorToHex(value);
		if (hex) roles[name] = hex;
	}
	return Object.keys(roles).length > 0 ? roles : null;
}

/**
 * True when the theme paints something in `mode`. A file may carry roles this
 * app keeps but does not paint (`toolbar`, `update`, …); those alone
 * do not make an appearance, or picking the theme would switch light or dark
 * for no visible change.
 */
export function customThemeHasAppearance(theme: ThemeSource, mode: ThemeAppearance): boolean {
	const colors = customThemeColors(theme, mode);
	if (!colors) return false;
	const resolved = resolveCustomTheme(colors, mode);
	return Object.keys(resolved.root).length > 0 || resolved.frame !== null || resolved.message !== null || resolved.code !== null || resolved.placeholder !== undefined;
}

/** `color` moved toward white or black until it reads on `surface`. */
function readableOn(color: string, surface: string, minContrast: number): string {
	if (contrastRatio(color, surface) >= minContrast) return color;
	const toward = lightness(surface) < 0.5 ? "#ffffff" : "#000000";
	for (let amount = 0.1; amount < 1; amount += 0.1) {
		const mixed = mixColor(color, toward, amount);
		if (contrastRatio(mixed, surface) >= minContrast) return mixed;
	}
	return toward;
}

type Declarations = Record<string, string>;

type ResolvedTheme = {
	/** Tokens on :root. */
	root: Declarations;
	/** Text and fills on the window frame (sidebar rail, top band). */
	frame: Declarations | null;
	/**
	 * Sidebar row fills, when the theme sets one. The app paints hovered and
	 * selected rows from one token, so telling them apart takes rules.
	 */
	rows: { hover: string; active: string; selected: string } | null;
	/** The user's chat bubble, code blocks and input placeholders. */
	message: Declarations | null;
	code: Declarations | null;
	placeholder: string | undefined;
	palette: ThemePalette;
};

function defined(declarations: Record<string, string | undefined>): Declarations {
	return Object.fromEntries(Object.entries(declarations).filter((entry): entry is [string, string] => entry[1] !== undefined));
}

/**
 * Turns the roles a theme sets into the app's tokens for one appearance. A
 * token is written only when a role behind it is set (directly, or through a
 * role it follows, e.g. `sidebar` from `canvas`), so a theme that sets three
 * colors changes those and leaves the rest of Orchestrator's look alone.
 */
export function resolveCustomTheme(set: Roles, mode: ThemeAppearance): ResolvedTheme {
	const dark = mode === "dark";
	const base = getThemeDefinition(DEFAULT_THEME_STYLE)[mode];
	const bg = set.canvas ?? base.bg;
	const fg = set.text ?? base.text;
	// Roles that follow the canvas and text only do so once the theme sets one.
	const core = set.canvas !== undefined || set.text !== undefined;
	const derived = (value: () => string): string | undefined => (core ? value() : undefined);
	const bright = (color: string | undefined) => (color ? mixColor(color, dark ? "#ffffff" : fg, 0.22) : undefined);

	const element = set.secondary ?? set.muted ?? set.surfaceRaised ?? derived(() => mixColor(bg, fg, dark ? 0.1 : 0.07));
	const surface =
		set.surface ??
		derived(() => (dark ? mixColor(bg, fg, 0.05) : lightness(bg) > 0.97 ? mixColor(bg, fg, 0.025) : mixColor(bg, "#ffffff", 0.6)));
	const popover =
		set.surfaceOverlay ??
		(surface !== undefined || element !== undefined
			? dark
				? mixColor(surface ?? base.panel, element ?? base.element, 0.6)
				: mixColor(surface ?? base.panel, "#ffffff", 0.6)
			: undefined);
	const textMuted = set.textMuted ?? set.mutedForeground ?? derived(() => mixColor(fg, bg, 0.36));
	const mutedForeground = set.mutedForeground ?? textMuted;
	const accent = set.accent;
	const link = set.link ?? (accent ? readableOn(accent, bg, 4.5) : undefined);
	const ring = set.focus ?? link;

	const red = set.red ?? set.error;
	const orange = set.orange ?? set.warning;
	const { yellow, green, blue, purple, cyan } = set;

	// The window frame: sidebar rail, top band and the gap around the center pane.
	const sidebar = set.sidebar ?? set.chrome ?? (set.canvas ? mixColor(set.canvas, "#000000", dark ? 0.22 : 0.035) : undefined);
	const framed =
		sidebar !== undefined ||
		set.sidebarForeground !== undefined ||
		set.sidebarMutedForeground !== undefined ||
		set.sidebarControlSurface !== undefined ||
		set.sidebarRowHover !== undefined ||
		set.sidebarRowActive !== undefined ||
		set.sidebarRowSelected !== undefined ||
		set.sidebarBorder !== undefined;
	const frameBg = sidebar ?? base.sidebar;
	// Text on the frame stays the theme's text while it reads there. A dark
	// sidebar in a light theme (or the reverse) gets its own.
	const frameFg = set.sidebarForeground ?? (contrastRatio(fg, frameBg) >= 4.5 ? fg : textOn(frameBg));
	const mutedOnCanvas = mutedForeground ?? base.muted;
	const frameMuted =
		set.sidebarMutedForeground ?? (frameFg === fg && contrastRatio(mutedOnCanvas, frameBg) >= 3 ? mutedOnCanvas : mixColor(frameFg, frameBg, 0.35));
	const rowHover = set.sidebarRowHover ?? withAlpha(frameFg, 0.06);
	const rowActive = set.sidebarRowActive ?? set.sidebarRowSelected ?? withAlpha(frameFg, 0.09);
	const rowSelected = set.sidebarRowSelected ?? set.sidebarRowActive ?? withAlpha(frameFg, 0.1);
	const frameControl = set.sidebarControlSurface ?? withAlpha(frameFg, 0.08);
	const frameBorder = set.sidebarBorder ?? withAlpha(frameFg, lightness(frameBg) < 0.5 ? 0.09 : 0.11);
	const frameCard = withAlpha(frameFg, 0.05);

	const terminalBg = set.terminalBackground;
	const selection = set.terminalSelection ?? (link ? withAlpha(link, dark ? 0.3 : 0.25) : undefined);
	const fade = (amount: number) => derived(() => mixColor(bg, fg, amount));

	const root = defined({
		"--background": set.canvas,
		"--foreground": set.text,
		"--card": surface,
		"--card-foreground": set.text,
		"--popover": popover,
		"--popover-foreground": set.text,
		"--primary": accent,
		"--primary-foreground": set.accentForeground ?? (accent ? textOn(accent) : undefined),
		"--secondary": set.secondary ?? element,
		"--secondary-foreground": set.secondaryForeground ?? set.text,
		"--muted": set.muted ?? element,
		"--muted-foreground": mutedForeground,
		"--accent": element,
		"--accent-foreground": set.text,
		"--color-bg-tertiary": set.surfaceRaised,
		"--color-text-muted": set.textMuted,
		"--border": set.border ?? derived(() => withAlpha(fg, dark ? 0.09 : 0.11)),
		"--input": set.input ?? derived(() => withAlpha(fg, dark ? 0.06 : 0.07)),
		"--ring": ring,
		"--chart-1": fade(0.85),
		"--chart-2": fade(0.65),
		"--chart-3": fade(0.5),
		"--chart-4": fade(0.38),
		"--chart-5": fade(0.22),
		"--sidebar": sidebar,
		"--sidebar-foreground": framed ? frameFg : undefined,
		"--sidebar-primary": accent,
		"--sidebar-primary-foreground": set.accentForeground ?? (accent ? textOn(accent) : undefined),
		"--sidebar-accent": framed ? rowSelected : undefined,
		"--sidebar-accent-foreground": framed ? frameFg : undefined,
		"--sidebar-border": framed ? frameBorder : undefined,
		"--sidebar-ring": ring,

		"--color-text-passive": fade(0.5),
		"--color-brand-logo": link,
		"--color-brand-logo-bright": bright(link),
		"--color-brand-logo-foreground": link ? textOn(link) : undefined,
		"--color-text-markdown-code": link,
		"--color-text-markdown-link": link,
		"--color-text-markdown-link-hover": bright(link),

		"--destructive": set.error ?? set.red,
		"--color-status-working": blue,
		"--color-status-needs-you": orange,
		"--color-status-validating": red || purple ? mixColor(red ?? base.red, purple ?? base.purple, 0.5) : undefined,
		"--color-status-in-review": orange || yellow ? mixColor(orange ?? base.orange, yellow ?? base.yellow, 0.5) : undefined,
		"--color-status-ready": green,
		"--color-status-merged": purple,
		"--color-project-accent-violet": purple,

		"--color-bg-terminal": terminalBg,
		"--color-bg-terminal-opaque": terminalBg,
		"--color-text-terminal": set.terminalForeground,
		// Read by terminal-themes.ts ahead of the canvas and text colors.
		"--theme-terminal-background": terminalBg,
		"--theme-terminal-foreground": set.terminalForeground,
		"--theme-terminal-cursor": set.terminalCursor,
		"--color-term-black": terminalBg ?? set.canvas,
		"--color-term-red": red,
		"--color-term-green": green,
		"--color-term-yellow": yellow,
		"--color-term-blue": blue,
		"--color-term-magenta": purple,
		"--color-term-cyan": cyan,
		"--color-term-white": textMuted,
		"--color-term-bright-black": textMuted || set.canvas ? mixColor(textMuted ?? base.muted, bg, 0.25) : undefined,
		"--color-term-bright-red": bright(red),
		"--color-term-bright-green": bright(green),
		"--color-term-bright-yellow": bright(yellow),
		"--color-term-bright-blue": bright(blue),
		"--color-term-bright-magenta": bright(purple),
		"--color-term-bright-cyan": bright(cyan),
		"--color-term-bright-white": set.terminalForeground ?? set.text,
		"--color-term-selection-dark": selection,
		"--color-term-selection-light": selection,
	});

	// Tokens resolve on :root, so text and fills on the frame are set again on
	// the frame's own elements. `color` too: they inherit the shell's computed
	// text color, not the token.
	const frame = framed
		? {
				color: frameFg,
				"--foreground": frameFg,
				"--color-text-primary": frameFg,
				"--sidebar-foreground": frameFg,
				"--muted-foreground": frameMuted,
				"--color-text-muted": frameMuted,
				"--color-text-passive": mixColor(frameFg, frameBg, 0.5),
				// Row highlights: the hovered row, and the selected one.
				"--color-interactive-hover": rowHover,
				"--color-interactive-active": rowSelected,
				"--sidebar-accent": rowSelected,
				"--sidebar-accent-foreground": frameFg,
				"--secondary": frameControl,
				"--muted": frameControl,
				"--accent": frameControl,
				"--color-bg-secondary": frameCard,
				"--color-bg-tertiary": frameControl,
				"--border": frameBorder,
				"--color-border": frameBorder,
				"--color-border-strong": frameBorder,
				"--sidebar-border": frameBorder,
				// Copies Tailwind's utilities and some hand-written rules read
				// instead of the tokens above. They resolve on :root as well.
				"--color-foreground": frameFg,
				"--color-muted-foreground": frameMuted,
				"--color-surface": frameCard,
				"--bridge-interactive-hover": rowHover,
				"--bridge-interactive-active": rowSelected,
				"--bridge-border": frameBorder,
				"--bridge-border-strong": frameBorder,
			}
		: null;

	const message = defined({ "background-color": set.messageSurface, color: set.messageForeground });
	const code = defined({ "background-color": set.codeBackground, color: set.codeForeground });

	const rowsSet = set.sidebarRowHover !== undefined || set.sidebarRowActive !== undefined || set.sidebarRowSelected !== undefined;

	return {
		root,
		frame,
		rows: rowsSet ? { hover: rowHover, active: rowActive, selected: rowSelected } : null,
		message: Object.keys(message).length > 0 ? message : null,
		code: Object.keys(code).length > 0 ? code : null,
		placeholder: set.placeholder,
		palette: {
			bg,
			sidebar: frameBg,
			panel: surface ?? base.panel,
			element: element ?? base.element,
			text: fg,
			muted: mutedOnCanvas,
			accent: link ?? base.accent,
			primary: accent ?? base.primary,
			red: red ?? base.red,
			orange: orange ?? base.orange,
			yellow: yellow ?? base.yellow,
			green: green ?? base.green,
			blue: blue ?? base.blue,
			purple: purple ?? base.purple,
			cyan: cyan ?? base.cyan,
		},
	};
}

/**
 * The theme's swatch palette for one appearance. A theme with no colors for
 * `mode` shows the ones it has.
 */
export function customThemePalette(theme: ThemeSource, mode: ThemeAppearance): ThemePalette {
	try {
		const own = customThemeOwnAppearance(theme);
		const shown = customThemeHasAppearance(theme, mode) ? mode : own;
		return resolveCustomTheme(customThemeColors(theme, shown) ?? {}, shown).palette;
	} catch {
		// The picker draws a swatch for every saved theme; one it cannot
		// resolve must not take Settings down with it.
		return getThemeDefinition(DEFAULT_THEME_STYLE)[mode];
	}
}

/**
 * The frame's elements: the sidebar rail, the Windows title bar, and the bars
 * and button groups in the band above the center pane, where the top bar
 * floats on the frame (macOS). `data-theme-frame` marks any other control
 * while it sits on the frame (the titlebar buttons).
 */
const SIDEBAR = '[data-slot="sidebar-inner"]';
const FRAME_SCOPE =
	':is([data-slot="sidebar-inner"], [data-theme-frame], .window-titlebar, .center-panel-shell--floating-topbar :is(.center-panel-titlebar, .session-topbar-surface, .session-inspector__topbar, .session-topbar-actions, .panel-topbar-controls))';

function rule(selector: string, declarations: Declarations, first: string[] = []): string {
	const lines = [...first, ...Object.entries(declarations).map(([name, value]) => `${name}: ${value};`)];
	return `${selector} {\n${lines.map((line) => `\t${line}`).join("\n")}\n}`;
}

function appearanceCss(selector: string, theme: ThemeSource, mode: ThemeAppearance): string[] {
	const colors = customThemeColors(theme, mode);
	if (!colors) return [];
	const resolved = resolveCustomTheme(colors, mode);
	return [
		rule(selector, resolved.root, [`color-scheme: ${mode};`]),
		resolved.frame ? rule(`${selector} ${FRAME_SCOPE}`, resolved.frame) : "",
		// The selected tab in the band: light mode mixes its fill from the
		// canvas, which washes out on a frame of another color.
		resolved.frame
			? rule(`${selector} .center-panel-shell--floating-topbar [data-terminal-tab-frame][data-active]::before`, {
					"background-color": resolved.frame["--secondary"],
				})
			: "",
		// Set on the row itself, so a selected row keeps its fill while the
		// pointer is over the list around it.
		...(resolved.rows
			? [
					rule(`${selector} ${SIDEBAR} :hover`, { "--sidebar-accent": resolved.rows.hover }),
					rule(`${selector} ${SIDEBAR} :active`, { "--sidebar-accent": resolved.rows.active }),
					rule(`${selector} ${SIDEBAR} [data-active="true"]`, { "--sidebar-accent": resolved.rows.selected }),
				]
			: []),
		// A queued bubble is an outline, not a fill; it keeps its own look.
		resolved.message ? rule(`${selector} .cursor-chat-human-message.bg-raised`, resolved.message) : "",
		resolved.code ? rule(`${selector} .chat-code`, resolved.code) : "",
		resolved.placeholder ? rule(`${selector} ::placeholder`, { color: resolved.placeholder }) : "",
	];
}

/**
 * The stylesheet for one custom theme while it is on screen: its tokens per
 * appearance, then its CSS. Selectors match the built-in catalog's
 * (theme-tokens.ts), one step more specific for dark so its colors do not leak
 * into light.
 */
export function buildCustomThemeCss(theme: Pick<CustomTheme, "id" | "appearance" | "colors" | "variants" | "css">): string {
	try {
		return buildCss(theme);
	} catch {
		// A theme this build cannot resolve shows as the default look.
		return "";
	}
}

function buildCss(theme: Pick<CustomTheme, "id" | "appearance" | "colors" | "variants" | "css">): string {
	const root = `:root[data-style-theme="${customThemeStyle(theme.id)}"]`;
	const own = theme.css?.trim() ?? "";
	return [
		...appearanceCss(`${root}:not([data-theme="light"])`, theme, "dark"),
		...appearanceCss(`${root}[data-theme="light"]`, theme, "light"),
		// The daemon refuses CSS that loads a file, so this only meets CSS it
		// never checked. The theme then shows with its colors alone.
		themeCssLoadsFile(own) === null ? own : "",
	]
		.filter(Boolean)
		.join("\n");
}

/**
 * True when a declaration value (or a block's text) would load a file. Theme
 * CSS comes from shared files and agents and must not fetch: a url() under an
 * attribute selector could send what is on screen to another host. The daemon
 * refuses such CSS on import; the renderer checks the source again before
 * applying it and then every parsed rule, since it may be shown CSS the daemon
 * never saw (a saved copy, an older daemon).
 */
export function themeCssValueFetches(value: string): boolean {
	return themeCssLoadsFile(value) !== null;
}

/**
 * Shared theme CSS often scopes a rule to one appearance with `.dark` and
 * `:root:not(.dark)`. This app marks the appearance with `data-theme` on
 * :root, so those selectors are rewritten to match.
 */
export function themeAppearanceSelector(selector: string): string {
	return selector
		.replace(/:root:not\(\.dark\)/g, ':root[data-theme="light"]')
		.replace(/\.dark(?![\w-])/g, ':root:not([data-theme="light"])');
}

type RuleList = { cssRules: CSSRuleList; deleteRule(index: number): void };

/**
 * Removes every declaration in a block that would load a file, or that cannot
 * be read back to check. Exported for tests.
 *
 * This is the second check; themeCssLoadsFile reads the source first and is
 * the one that sees everything. A parsed block cannot: a shorthand holding a
 * var() stays unparsed until it is used, so `background: url(https://…)
 * var(--x)` reads back as empty longhands. While the shorthand is intact the
 * block's text still shows it. Once a later declaration overrides one of its
 * longhands, the browser serializes the rest as `background-image: ;` and the
 * url is in neither the values nor the text, though it still loads when the
 * rule applies. Those unreadable declarations are removed.
 */
export function stripThemeDeclarations(style: Pick<CSSStyleDeclaration, "length" | "item" | "getPropertyValue" | "removeProperty" | "cssText">): void {
	for (let item = style.length - 1; item >= 0; item--) {
		const property = style.item(item);
		const value = style.getPropertyValue(property);
		if (themeCssValueFetches(value) || (value === "" && hidesItsValue(style.cssText, property))) style.removeProperty(property);
	}
	if (themeCssValueFetches(style.cssText)) style.cssText = "";
}

/** True when the block's text lists `property` with nothing after the colon. */
function hidesItsValue(cssText: string, property: string): boolean {
	const at = cssText.indexOf(`${property}:`);
	if (at < 0 || (at > 0 && !/[\s;]/.test(cssText[at - 1]))) return false;
	return /^\s*(?:!important\s*)?(?:;|$)/.test(cssText.slice(at + property.length + 1));
}

/**
 * Keeps only rule kinds it can check, so a new at-rule that fetches is dropped
 * by default, and points appearance selectors at this app's marker.
 */
function stripFetches(parent: RuleList): void {
	for (let index = parent.cssRules.length - 1; index >= 0; index--) {
		const rule = parent.cssRules[index];
		if (!rule) continue;
		if (typeof CSSKeyframesRule !== "undefined" && rule instanceof CSSKeyframesRule) {
			for (const frame of Array.from(rule.cssRules)) stripThemeDeclarations((frame as CSSKeyframeRule).style);
			continue;
		}
		const styled = rule instanceof CSSStyleRule || (typeof CSSFontFaceRule !== "undefined" && rule instanceof CSSFontFaceRule);
		const grouping =
			rule instanceof CSSMediaRule ||
			(typeof CSSSupportsRule !== "undefined" && rule instanceof CSSSupportsRule) ||
			(typeof CSSContainerRule !== "undefined" && rule instanceof CSSContainerRule) ||
			(typeof CSSLayerBlockRule !== "undefined" && rule instanceof CSSLayerBlockRule);
		if (!styled && !grouping) {
			parent.deleteRule(index);
			continue;
		}
		if (styled) stripThemeDeclarations((rule as CSSStyleRule).style);
		if (rule instanceof CSSStyleRule) {
			const selector = themeAppearanceSelector(rule.selectorText);
			if (selector !== rule.selectorText) rule.selectorText = selector;
		}
		// Style rules can hold nested rules too.
		if ("cssRules" in rule && "deleteRule" in rule) stripFetches(rule as unknown as RuleList);
	}
}

let sheet: CSSStyleSheet | null = null;
let shownCss = "";

function canAdoptSheets(): boolean {
	return typeof document !== "undefined" && typeof CSSStyleSheet === "function" && "adoptedStyleSheets" in document && "replaceSync" in CSSStyleSheet.prototype;
}

/**
 * Puts `css` on the page (after every other sheet), or removes the sheet when
 * empty. The new sheet is parsed and checked while detached and only then
 * swapped in, so unchecked rules never reach the page; if the check fails,
 * the theme's CSS comes off the page instead.
 */
function writeSheet(css: string): void {
	if (!canAdoptSheets()) return;
	if (css === shownCss && (css === "" || (sheet && document.adoptedStyleSheets.includes(sheet)))) return;
	const previous = sheet;
	let next: CSSStyleSheet | null = null;
	if (css) {
		try {
			// A whole stylesheet that loads a file is not applied at all (the saved
			// copy from the last launch takes this path without the daemon).
			if (themeCssLoadsFile(css) === null) {
				next = new CSSStyleSheet();
				// replaceSync drops @import rules; stripFetches checks the rest.
				next.replaceSync(css);
				stripFetches(next);
			}
		} catch {
			next = null;
		}
	}
	const others = document.adoptedStyleSheets.filter((adopted) => adopted !== previous);
	document.adoptedStyleSheets = next ? [...others, next] : others;
	sheet = next;
	shownCss = next ? css : "";
}

function storage(): Storage | null {
	try {
		return typeof window === "undefined" ? null : window.localStorage;
	} catch {
		return null;
	}
}

let catalog = new Map<string, CustomTheme>();

/**
 * The daemon's custom themes, so the picker and switches can show them. An id
 * goes into a CSS selector, so one the daemon would not have issued is left
 * out rather than trusted.
 */
export function setCustomThemeCatalog(themes: readonly CustomTheme[]): void {
	catalog = new Map(themes.filter((theme) => isCustomThemeStyle(customThemeStyle(theme.id))).map((theme) => [theme.id, theme]));
}

/** Adds or replaces one theme, e.g. the one an import just returned. */
export function upsertCustomTheme(theme: CustomTheme): void {
	if (isCustomThemeStyle(customThemeStyle(theme.id))) catalog.set(theme.id, theme);
}

export function listCustomThemes(): CustomTheme[] {
	return [...catalog.values()];
}

export function getCustomTheme(style: string): CustomTheme | undefined {
	return isCustomThemeStyle(style) ? catalog.get(customThemeId(style)) : undefined;
}

/**
 * Shows the custom theme `style` names, or hides custom theme CSS for a
 * built-in one. Before the daemon answers, the saved copy
 * (rememberCustomThemeStyle) stands in. `data-theme-custom` on :root lets
 * styles.css keep a custom theme's center pane solid in a translucent window.
 */
export function showCustomThemeStyle(style: string): void {
	if (typeof document === "undefined") return;
	const root = document.documentElement;
	if (!isCustomThemeStyle(style)) {
		delete root.dataset.themeCustom;
		writeSheet("");
		return;
	}
	root.dataset.themeCustom = "true";
	const theme = getCustomTheme(style);
	if (theme) {
		writeSheet(buildCustomThemeCss(theme));
		return;
	}
	const cached = readCachedCss();
	if (cached?.style === style) writeSheet(cached.css);
}

/**
 * Saves the stylesheet of the theme the user keeps, so the next launch paints
 * it before the daemon answers. Call it for the saved theme only: a theme
 * previewed from the picker is shown without being remembered. It writes every
 * time (callers are a theme pick or a change to the themes, both rare), so
 * what is stored never depends on what this window believes it stored.
 */
export function rememberCustomThemeStyle(style: string): void {
	if (!isCustomThemeStyle(style)) {
		try {
			storage()?.removeItem(customThemeCssStorageKey);
		} catch {
			// Nothing saved to forget.
		}
		return;
	}
	const theme = getCustomTheme(style);
	if (theme) cacheCss(style, buildCustomThemeCss(theme));
}

/** True when the custom theme on screen would change if shown again from the catalog. */
export function customThemeCssChanged(style: string): boolean {
	const theme = getCustomTheme(style);
	return theme !== undefined && buildCustomThemeCss(theme) !== shownCss;
}

function cacheCss(style: CustomThemeStyle, css: string): void {
	try {
		if (css.length <= MAX_CACHED_CSS_CHARS) storage()?.setItem(customThemeCssStorageKey, JSON.stringify({ style, css }));
		else storage()?.removeItem(customThemeCssStorageKey);
	} catch {
		// Storage full or unavailable: the theme still shows for this session.
	}
}

function readCachedCss(): { style: string; css: string } | null {
	try {
		const parsed = JSON.parse(storage()?.getItem(customThemeCssStorageKey) ?? "null") as unknown;
		if (parsed && typeof parsed === "object" && "style" in parsed && "css" in parsed) {
			const { style, css } = parsed as { style: unknown; css: unknown };
			if (typeof style === "string" && typeof css === "string") return { style, css };
		}
	} catch {
		// A stale or foreign value: ignore it.
	}
	return null;
}

/**
 * The theme and light/dark switch an agent asked for, if this app has not
 * applied it yet. Marks it applied, so the user's later picks in Settings
 * stand until an agent asks again. `themeId` is a built-in id or a custom
 * theme's id.
 */
export function takeCustomThemeSwitch(request: CustomThemeSwitchRequest): { mode?: "light" | "dark" | "system"; themeId?: string } | null {
	if (!request.id) return null;
	const store = storage();
	try {
		if (store?.getItem(customThemeSwitchStorageKey) === request.id) return null;
		store?.setItem(customThemeSwitchStorageKey, request.id);
	} catch {
		// Without storage the request would apply on every load; skip it instead.
		return null;
	}
	const taken: { mode?: "light" | "dark" | "system"; themeId?: string } = {};
	if (request.mode === "light" || request.mode === "dark" || request.mode === "system") taken.mode = request.mode;
	if (request.themeId) taken.themeId = request.themeId;
	return taken;
}
