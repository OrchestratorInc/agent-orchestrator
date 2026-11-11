import { mixColor, relativeLuminance, withAlpha } from "./theme-color";
import { THEMES, type ThemeDefinition, type ThemePalette } from "./theme-palettes";

type Mode = "dark" | "light";

/** Match the selected 22% lighter label bias, also used by button.css. */
function textOn(fill: string): string {
	const automatic = Math.sqrt(0.05 * 1.05) - 0.05;
	const cutoff = automatic + (1 - automatic) * 0.22;
	return relativeLuminance(fill) < cutoff ? "#ffffff" : "#000000";
}

/**
 * Resolve a palette into its theme-dependent color tokens, as concrete
 * hex. Covers the shadcn base, status, links, brand mark, charts/passive text
 * and terminal ANSI slots. Destructive colors stay shared in tokens.css.
 */
export function deriveThemeTokens(p: ThemePalette, mode: Mode): Record<string, string> {
	const dark = mode === "dark";
	const primary = p.primary ?? p.accent;
	const onPrimary = textOn(primary);
	const onAccent = textOn(p.accent);
	// Lift popovers off the canvas: lighter than panels in dark, near-white in light.
	const popover = dark ? mixColor(p.panel, p.element, 0.6) : mixColor(p.panel, "#ffffff", 0.6);
	const fade = (amount: number) => mixColor(p.bg, p.text, amount);
	const bright = (color: string) => mixColor(color, dark ? "#ffffff" : p.text, 0.22);
	const inReview = mixColor(p.orange, p.yellow, 0.5);
	// Pink, as in Orchestrate: a red/purple mix stays apart from Building, Ready
	// and In review. Palettes have no pink role, and `p.cyan` would not do: it
	// equals blue in Dracula and Monokai and green in Rosé Pine.
	const validating = mixColor(p.red, p.purple, 0.5);

	return {
		"--background": p.bg,
		"--foreground": p.text,
		"--card": p.panel,
		"--card-foreground": p.text,
		"--popover": popover,
		"--popover-foreground": p.text,
		"--primary": primary,
		"--primary-foreground": onPrimary,
		"--secondary": p.element,
		"--secondary-foreground": p.text,
		"--muted": p.element,
		"--muted-foreground": p.muted,
		"--accent": p.element,
		"--accent-foreground": p.text,
		"--border": withAlpha(p.text, dark ? 0.09 : 0.11),
		"--input": withAlpha(p.text, dark ? 0.06 : 0.07),
		"--ring": p.accent,
		"--chart-1": fade(0.85),
		"--chart-2": fade(0.65),
		"--chart-3": fade(0.5),
		"--chart-4": fade(0.38),
		"--chart-5": fade(0.22),
		"--sidebar": p.sidebar,
		"--sidebar-foreground": p.text,
		"--sidebar-primary": primary,
		"--sidebar-primary-foreground": onPrimary,
		"--sidebar-accent": p.element,
		"--sidebar-accent-foreground": p.text,
		"--sidebar-ring": p.accent,

		"--color-text-passive": fade(0.5),
		"--color-brand-logo": p.accent,
		"--color-brand-logo-bright": bright(p.accent),
		"--color-brand-logo-foreground": onAccent,
		"--color-text-markdown-code": p.accent,
		"--color-text-markdown-link": p.accent,
		"--color-text-markdown-link-hover": bright(p.accent),

		"--color-status-working": p.blue,
		"--color-status-needs-you": p.orange,
		// tokens.css picks between these by the new sidebar design flag.
		"--color-status-validating-new": validating,
		"--color-status-validating-legacy": p.yellow,
		"--color-status-in-review": inReview,
		"--color-status-ready": p.green,
		"--color-status-merged": p.purple,
		"--color-project-accent-violet": p.purple,

		// ANSI black collapses into the plate in terminal-themes.ts; white is the
		// muted gray so light plates keep it visible.
		"--color-term-black": p.bg,
		"--color-term-red": p.red,
		"--color-term-green": p.green,
		"--color-term-yellow": p.yellow,
		"--color-term-blue": p.blue,
		"--color-term-magenta": p.purple,
		"--color-term-cyan": p.cyan,
		"--color-term-white": p.muted,
		"--color-term-bright-black": mixColor(p.muted, p.bg, 0.25),
		"--color-term-bright-red": bright(p.red),
		"--color-term-bright-green": bright(p.green),
		"--color-term-bright-yellow": bright(p.yellow),
		"--color-term-bright-blue": bright(p.blue),
		"--color-term-bright-magenta": bright(p.purple),
		"--color-term-bright-cyan": bright(p.cyan),
		"--color-term-bright-white": p.text,
		"--color-term-selection-dark": withAlpha(p.accent, 0.3),
		"--color-term-selection-light": withAlpha(p.accent, 0.25),
		"--color-term-selection-inactive": withAlpha(p.muted, 0.2),
		"--color-term-selection-inactive-light": withAlpha(p.muted, 0.15),
	};
}

function block(selector: string, tokens: Record<string, string>, colorScheme: Mode): string {
	const body = Object.entries(tokens)
		.map(([name, value]) => `\t${name}: ${value};`)
		.join("\n");
	return `${selector} {\n\tcolor-scheme: ${colorScheme};\n${body}\n}`;
}

/**
 * CSS for every named theme. Selectors match the old hand-written blocks:
 * dark `:root[data-style-theme=x]` (0,2,0) and light
 * `:root[data-style-theme=x][data-theme=light]` (0,3,0), so both beat the
 * base `:root` / `:root[data-theme=light]` tokens regardless of load order.
 */
export function buildThemeStylesheet(themes: readonly ThemeDefinition[] = THEMES): string {
	return themes
		.filter((theme) => theme.id !== "orchestrate")
		.flatMap((theme) => {
			const selector = `:root[data-style-theme="${theme.id}"]`;
			return [
				block(selector, deriveThemeTokens(theme.dark, "dark"), "dark"),
				block(`${selector}[data-theme="light"]`, deriveThemeTokens(theme.light, "light"), "light"),
			];
		})
		.join("\n");
}

const STYLE_ELEMENT_ID = "ao-theme-catalog";

/** Inject the named-theme stylesheet once; safe to call repeatedly. */
export function installThemeStylesheet(): void {
	if (typeof document === "undefined" || document.getElementById(STYLE_ELEMENT_ID)) return;
	const style = document.createElement("style");
	style.id = STYLE_ELEMENT_ID;
	style.textContent = buildThemeStylesheet();
	document.head.appendChild(style);
}
