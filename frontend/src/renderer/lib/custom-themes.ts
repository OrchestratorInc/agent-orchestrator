import { isCustomThemeId, parseCustomTheme, type CustomTheme } from "../../shared/custom-themes";
import { getThemeDefinition, type ThemeDefinition, type ThemeStyle as BuiltInThemeStyle } from "./theme-palettes";
import { buildThemeStylesheet } from "./theme-tokens";

export type ThemeStyle = BuiltInThemeStyle | CustomTheme["id"];

// The last list main sent, kept so the chosen theme paints before main answers
// and so the artifact snapshot window, which has no theme bridge, can read it.
export const customThemesStorageKey = "ao.custom-themes";

const STYLE_ELEMENT_ID = "ao-custom-themes";

/** The cached themes, parsed again: the values end up in a stylesheet. */
function readStoredCustomThemes(): CustomTheme[] {
	try {
		const stored: unknown = JSON.parse(window.localStorage.getItem(customThemesStorageKey) ?? "[]");
		if (!Array.isArray(stored)) return [];
		return stored.flatMap((entry: { file?: unknown; label?: unknown; dark?: unknown; light?: unknown } | null) => {
			if (typeof entry?.file !== "string") return [];
			const theme = parseCustomTheme(entry.file, JSON.stringify({ name: entry.label, dark: entry.dark, light: entry.light }));
			return "problem" in theme ? [] : [theme];
		});
	} catch {
		return [];
	}
}

let customThemes: CustomTheme[] = typeof window === "undefined" ? [] : readStoredCustomThemes();

export function getCustomThemes(): CustomTheme[] {
	return customThemes;
}

export function findCustomTheme(id: unknown): CustomTheme | undefined {
	return isCustomThemeId(id) ? customThemes.find((theme) => theme.id === id) : undefined;
}

export function themeDefinition(style: ThemeStyle): ThemeDefinition {
	return isCustomThemeId(style) ? (findCustomTheme(style) ?? getThemeDefinition("orchestrate")) : getThemeDefinition(style);
}

/** Changes whenever the theme's colors do; keys what was rendered with them. */
export function customThemeRevision(theme: CustomTheme): string {
	let hash = 5381;
	for (const char of JSON.stringify([theme.dark, theme.light])) hash = (hash * 33 + char.charCodeAt(0)) >>> 0;
	return hash.toString(36);
}

export function installCustomThemeStylesheet(): void {
	if (typeof document === "undefined") return;
	let style = document.getElementById(STYLE_ELEMENT_ID);
	if (!style) {
		style = document.createElement("style");
		style.id = STYLE_ELEMENT_ID;
		document.head.appendChild(style);
	}
	const css = buildThemeStylesheet(customThemes);
	if (style.textContent !== css) style.textContent = css;
}

export function replaceCustomThemes(themes: CustomTheme[]): void {
	customThemes = themes;
	try {
		window.localStorage.setItem(customThemesStorageKey, JSON.stringify(themes));
	} catch {
		// ignore
	}
	installCustomThemeStylesheet();
}

export function reloadStoredCustomThemes(): void {
	customThemes = readStoredCustomThemes();
	installCustomThemeStylesheet();
}
