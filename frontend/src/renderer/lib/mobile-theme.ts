// The mobile app's palette for a desktop theme, so the onboarding's phone
// shows the theme the desktop has selected the way the phone app paints it.
//
// A mirror of packages/mobile/lib/theme.ts (themeFor, statusVisual) and packages/mobile/lib/themeDerive.ts
// (deriveMobileTheme). Those modules are pure, but they sit under the mobile
// package's Expo tsconfig, which the renderer's bundler cannot load without
// the mobile dependencies installed. The catalog and colour math are shared
// directly. Keep this in step with those two files.
import { contrastRatio, mixColor, withAlpha } from "./theme-color";
import { DEFAULT_THEME_STYLE, getThemeDefinition, type ThemePalette, type ThemeStyle } from "./theme-palettes";

export type MobileTheme = {
	bgBase: string;
	bgElevated: string;
	textPrimary: string;
	textSecondary: string;
	textTertiary: string;
	textFaint: string;
	borderSubtle: string;
	borderDefault: string;
	accent: string;
	orange: string;
	amber: string;
	red: string;
	green: string;
	purple: string;
	blue: string;
};

const TEXT_CONTRAST = 4.5;
const FAINT_CONTRAST = 3;

function ensureContrast(color: string, bg: string, min: number, toward: string): string {
	if (contrastRatio(color, bg) >= min) return color;
	for (let amount = 0.05; amount < 1; amount += 0.05) {
		const next = mixColor(color, toward, amount);
		if (contrastRatio(next, bg) >= min) return next;
	}
	return toward;
}

/** themeDerive.ts deriveMobileTheme, for the slots the phone preview draws. */
function deriveMobileTheme(p: ThemePalette, dark: boolean): MobileTheme {
	const extreme = dark ? "#ffffff" : "#000000";
	const bgBase = dark ? p.bg : mixColor(p.sidebar, p.text, 0.04);
	const readable = (color: string) => ensureContrast(color, bgBase, TEXT_CONTRAST, extreme);
	const orange = readable(p.orange);
	const amber = readable(p.yellow);
	const red = readable(p.red);
	const green = readable(p.green);
	return {
		bgBase,
		bgElevated: dark ? p.panel : mixColor(p.panel, "#ffffff", 0.6),
		textPrimary: p.text,
		textSecondary: readable(p.muted),
		textTertiary: readable(mixColor(p.muted, p.bg, 0.15)),
		textFaint: ensureContrast(mixColor(p.muted, p.bg, 0.35), bgBase, FAINT_CONTRAST, extreme),
		borderSubtle: withAlpha(p.text, 0.06),
		borderDefault: withAlpha(p.text, dark ? 0.1 : 0.12),
		accent: p.text,
		orange,
		blue: readable(p.blue),
		amber,
		red,
		green,
		purple: readable(p.purple),
	};
}

const derived = new Map<string, MobileTheme>();

/** theme.ts themeFor, but derived for every theme, Orchestrate included (the
 * phone app hand-tunes Orchestrate), so the phone sits beside the desktop
 * window in the same colours. */
export function themeFor(scheme: "light" | "dark", style: ThemeStyle = DEFAULT_THEME_STYLE): MobileTheme {
	const key = `${style}:${scheme}`;
	let theme = derived.get(key);
	if (!theme) {
		theme = deriveMobileTheme(getThemeDefinition(style)[scheme], scheme === "dark");
		derived.set(key, theme);
	}
	return theme;
}

/** theme.ts statusVisual's colours, for the statuses the preview shows. */
export function statusColor(theme: MobileTheme, status: "working" | "needs_input" | "mergeable" | "review_pending"): string {
	return status === "working" ? theme.orange : status === "needs_input" ? theme.amber : status === "mergeable" ? theme.green : theme.textSecondary;
}
