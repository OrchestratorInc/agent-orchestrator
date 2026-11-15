import { DEFAULT_THEME_STYLE, isThemeStyle, type ThemeStyle } from "./theme-palettes";
import { installThemeStylesheet } from "./theme-tokens";

export type Theme = "light" | "dark";
export type ThemePreference = Theme | "system";
export type { ThemeStyle };

export const themeStorageKey = "ao.theme";
export const themeStyleStorageKey = "ao.theme-style";

function getLocalStorage() {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

export function systemTheme(): Theme {
	if (typeof window === "undefined") return "dark";
	return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

export function readStoredThemePreference(): ThemePreference {
	try {
		const stored = getLocalStorage()?.getItem(themeStorageKey);
		if (stored === "light" || stored === "dark" || stored === "system") return stored;
	} catch {
	}
	return "system";
}

export function resolveTheme(preference: ThemePreference = readStoredThemePreference()): Theme {
	if (preference === "system") return systemTheme();
	return preference;
}

export function readStoredThemeStyle(): ThemeStyle {
	try {
		const stored = getLocalStorage()?.getItem(themeStyleStorageKey);
		if (isThemeStyle(stored)) return stored;
	} catch {
	}
	return DEFAULT_THEME_STYLE;
}

export function applyDocumentTheme(theme: Theme): void {
	if (typeof document === "undefined") return;
	document.documentElement.dataset.theme = theme;
	document.documentElement.style.colorScheme = theme;
}

export function applyDocumentThemeStyle(style: ThemeStyle): void {
	if (typeof document === "undefined") return;
	if (style === DEFAULT_THEME_STYLE) {
		delete document.documentElement.dataset.styleTheme;
	} else {
		installThemeStylesheet();
		document.documentElement.dataset.styleTheme = style;
	}
}

export function isDocumentThemeStyle(style: ThemeStyle): boolean {
	if (typeof document === "undefined") return false;
	return (document.documentElement.dataset.styleTheme ?? DEFAULT_THEME_STYLE) === style;
}

let previewing = false;

export function previewDocumentThemeStyle(style: ThemeStyle | null, saved: ThemeStyle): void {
	if (typeof document === "undefined") return;
	const root = document.documentElement;
	if (style !== null) {
		previewing = true;
		root.dataset.themeTransition = "active";
		applyDocumentThemeStyle(style);
		return;
	}
	if (!previewing) return;
	previewing = false;
	applyDocumentThemeStyle(saved);
	requestAnimationFrame(() =>
		requestAnimationFrame(() => {
			if (!previewing) delete root.dataset.themeTransition;
		}),
	);
}

export function runThemeTransition(update: () => void, onReveal?: () => void): void {
	if (typeof document === "undefined") return;

	const root = document.documentElement;
	if (root.dataset.windowTranslucent === "true" && typeof document.startViewTransition === "function") {
		runTranslucentThemeTransition(update, onReveal);
		return;
	}
	const finish = () => {
		requestAnimationFrame(() => {
			delete root.dataset.themeTransition;
		});
	};
	const run = () => {
		root.dataset.themeTransition = "active";
		update();
		void root.offsetHeight;
	};

	if (typeof document.startViewTransition !== "function") {
		run();
		onReveal?.();
		finish();
		return;
	}

	const transition = document.startViewTransition(() => {
		run();
	});
	onReveal?.();
	void transition.finished.finally(finish);
}

function runTranslucentThemeTransition(update: () => void, onReveal?: () => void): void {
	const root = document.documentElement;
	const transition = document.startViewTransition(async () => {
		root.dataset.themeTransition = "active";
		update();
		await new Promise((resolve) => setTimeout(resolve, 0));
	});
	void transition.ready.then(onReveal, onReveal);
	void transition.finished.finally(() =>
		requestAnimationFrame(() =>
			requestAnimationFrame(() => {
				if (!previewing) delete root.dataset.themeTransition;
			}),
		),
	);
}
